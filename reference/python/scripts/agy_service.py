#!/usr/bin/env python3
"""Install and operate the AGY durable worker as a macOS user LaunchAgent."""

from __future__ import annotations

import argparse
import json
import os
import plistlib
import re
import sqlite3
import stat
import subprocess
import sys
import tempfile
import uuid
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Iterator, Sequence

from agy_control_plane import ControlPlane, default_db_path
from agy_dispatch import resolve_agy_binary, sanitize_output


DEFAULT_LABEL = "com.tamld.agy-dispatch-worker"
PLUGIN_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_WORKER_SCRIPT = PLUGIN_ROOT / "scripts" / "agy_worker.py"
LABEL_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9.-]*$")
COMMAND_TIMEOUT_SECONDS = 30
MAINTENANCE_TTL_SECONDS = 900
CommandRunner = Callable[..., subprocess.CompletedProcess[str]]


class ServiceError(RuntimeError):
    """Raised when a service lifecycle operation fails closed."""


@dataclass(frozen=True)
class ServiceConfig:
    label: str
    python_path: Path
    worker_script: Path
    agy_path: Path | None
    db_path: Path
    plist_path: Path
    working_directory: Path
    stdout_path: Path
    stderr_path: Path
    uid: int

    @property
    def domain(self) -> str:
        return f"gui/{self.uid}"

    @property
    def target(self) -> str:
        return f"{self.domain}/{self.label}"


def default_python_path() -> Path:
    return Path(sys.executable).expanduser().resolve(strict=True)


def build_config(
    *,
    label: str = DEFAULT_LABEL,
    python_path: str | Path | None = None,
    worker_script: str | Path = DEFAULT_WORKER_SCRIPT,
    agy_path: str | Path | None = None,
    db_path: str | Path | None = None,
    plist_path: str | Path | None = None,
    uid: int | None = None,
) -> ServiceConfig:
    database = Path(db_path or default_db_path()).expanduser().resolve(strict=False)
    state_dir = database.parent / "service"
    discovered_agy = Path(agy_path).expanduser() if agy_path else resolve_agy_binary(None)
    return ServiceConfig(
        label=label,
        python_path=Path(python_path or default_python_path()).expanduser().resolve(strict=False),
        worker_script=Path(worker_script).expanduser().resolve(strict=False),
        agy_path=discovered_agy.resolve(strict=False) if discovered_agy else None,
        db_path=database,
        plist_path=Path(
            plist_path or Path.home() / "Library" / "LaunchAgents" / f"{label}.plist"
        ).expanduser(),
        working_directory=Path(worker_script).expanduser().resolve(strict=False).parents[1],
        stdout_path=state_dir / "worker.stdout.log",
        stderr_path=state_dir / "worker.stderr.log",
        uid=os.getuid() if uid is None else uid,
    )


def validate_config(config: ServiceConfig, *, platform_name: str = sys.platform) -> None:
    if platform_name != "darwin":
        raise ServiceError("LaunchAgent service management is supported only on macOS")
    if not LABEL_PATTERN.fullmatch(config.label):
        raise ServiceError(f"invalid LaunchAgent label: {config.label}")
    if config.agy_path is None:
        raise ServiceError("installed AGY application binary was not found")
    for name, path in (
        ("python", config.python_path),
        ("worker", config.worker_script),
        ("AGY", config.agy_path),
    ):
        if not path.is_absolute():
            raise ServiceError(f"{name} path must be absolute")
        if not path.is_file():
            raise ServiceError(f"{name} file is unavailable: {path}")
        metadata = path.stat()
        if metadata.st_uid not in {0, config.uid}:
            raise ServiceError(f"{name} file has an untrusted owner: {path}")
        if metadata.st_mode & 0o022:
            raise ServiceError(f"{name} file is group/world-writable: {path}")
    for name, path in (("python", config.python_path), ("AGY", config.agy_path)):
        if not os.access(path, os.X_OK):
            raise ServiceError(f"{name} executable is unavailable: {path}")
    if not config.working_directory.is_dir():
        raise ServiceError(f"working directory is unavailable: {config.working_directory}")


def service_path() -> str:
    return ":".join(
        [
            "/opt/homebrew/bin",
            "/usr/local/bin",
            "/usr/bin",
            "/bin",
            "/usr/sbin",
            "/sbin",
        ]
    )


def build_plist(config: ServiceConfig) -> dict[str, Any]:
    return {
        "Label": config.label,
        "ProgramArguments": [
            str(config.python_path),
            str(config.worker_script),
            "--db",
            str(config.db_path),
            "--worker-id",
            config.label,
            "--lease-seconds",
            "30",
            "--poll-seconds",
            "0.5",
            "--quiet",
        ],
        "WorkingDirectory": str(config.working_directory),
        "KeepAlive": True,
        "ProcessType": "Background",
        "ThrottleInterval": 10,
        "Umask": 0o077,
        "StandardOutPath": str(config.stdout_path),
        "StandardErrorPath": str(config.stderr_path),
        "EnvironmentVariables": {
            "PATH": service_path(),
            "AGY_BIN": str(config.agy_path),
            "PYTHONUNBUFFERED": "1",
        },
    }


def _run(
    command: Sequence[str],
    *,
    runner: CommandRunner = subprocess.run,
) -> subprocess.CompletedProcess[str]:
    try:
        return runner(
            list(command),
            text=True,
            errors="replace",
            capture_output=True,
            check=False,
            timeout=COMMAND_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired:
        return subprocess.CompletedProcess(
            list(command),
            124,
            "",
            f"command timed out after {COMMAND_TIMEOUT_SECONDS} seconds",
        )


def _command_error(command: Sequence[str], completed: subprocess.CompletedProcess[str]) -> str:
    detail = sanitize_output(completed.stderr.strip() or completed.stdout.strip() or "no detail")
    return f"{' '.join(command)} failed with {completed.returncode}: {detail}"


def _write_private_file(path: Path, data: bytes, mode: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(
        dir=path.parent,
        prefix=f".{path.name}.",
        suffix=".tmp",
    )
    temporary = Path(temporary_name)
    try:
        os.fchmod(descriptor, mode)
        with os.fdopen(descriptor, "wb") as handle:
            descriptor = -1
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        if descriptor >= 0:
            os.close(descriptor)
        temporary.unlink(missing_ok=True)


def _validate_owned_directory(path: Path, uid: int) -> None:
    metadata = path.lstat()
    if not stat.S_ISDIR(metadata.st_mode) or path.is_symlink():
        raise ServiceError(f"service directory is not a real directory: {path}")
    if metadata.st_uid != uid:
        raise ServiceError(f"service directory has an untrusted owner: {path}")
    if metadata.st_mode & 0o022:
        raise ServiceError(f"service directory is group/world-writable: {path}")


def _prepare_regular_file(path: Path, uid: int) -> None:
    flags = os.O_WRONLY | os.O_CREAT | os.O_APPEND | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(path, flags, 0o600)
    try:
        metadata = os.fstat(descriptor)
        if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != uid:
            raise ServiceError(f"service log is not an owned regular file: {path}")
        os.fchmod(descriptor, 0o600)
    finally:
        os.close(descriptor)


def _read_existing_plist(path: Path, uid: int) -> bytes | None:
    if path.is_symlink():
        raise ServiceError(f"refusing symlinked LaunchAgent plist: {path}")
    if not path.exists():
        return None
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != uid:
        raise ServiceError(f"LaunchAgent plist is not an owned regular file: {path}")
    return path.read_bytes()


def _prepare_paths(config: ServiceConfig) -> None:
    config.plist_path.parent.mkdir(parents=True, exist_ok=True)
    config.stdout_path.parent.mkdir(parents=True, exist_ok=True)
    _validate_owned_directory(config.plist_path.parent, config.uid)
    _validate_owned_directory(config.stdout_path.parent, config.uid)
    config.stdout_path.parent.chmod(0o700)
    for path in (config.stdout_path, config.stderr_path):
        try:
            _prepare_regular_file(path, config.uid)
        except OSError as exc:
            raise ServiceError(f"cannot prepare private service log: {sanitize_output(str(exc))}") from exc


def _is_loaded(config: ServiceConfig, *, runner: CommandRunner = subprocess.run) -> bool:
    return _run(["launchctl", "print", config.target], runner=runner).returncode == 0


def _active_task_count(config: ServiceConfig) -> int:
    if not config.db_path.exists():
        return 0
    try:
        control_plane = ControlPlane(config.db_path)
        return control_plane.active_task_count()
    except sqlite3.Error as exc:
        raise ServiceError(f"cannot inspect control-plane state: {sanitize_output(str(exc))}") from exc


@contextmanager
def _maintenance_guard(config: ServiceConfig, *, force: bool) -> Iterator[int]:
    owner = f"{config.label}:{os.getpid()}:{uuid.uuid4()}"
    try:
        control_plane = ControlPlane(config.db_path)
        active = control_plane.begin_maintenance(owner, ttl_seconds=MAINTENANCE_TTL_SECONDS)
    except (RuntimeError, sqlite3.Error) as exc:
        raise ServiceError(f"cannot enter control-plane maintenance: {sanitize_output(str(exc))}") from exc
    try:
        if active and not force:
            raise ServiceError(
                f"refusing lifecycle change while {active} task(s) are leased or running; use --force"
            )
        yield active
    finally:
        try:
            control_plane.end_maintenance(owner)
        except sqlite3.Error as exc:
            raise ServiceError(
                f"cannot leave control-plane maintenance: {sanitize_output(str(exc))}"
            ) from exc


def service_status(
    config: ServiceConfig,
    *,
    runner: CommandRunner = subprocess.run,
    platform_name: str = sys.platform,
) -> dict[str, Any]:
    validate_config(config, platform_name=platform_name)
    loaded = _is_loaded(config, runner=runner)
    return {
        "ok": True,
        "label": config.label,
        "domain": config.domain,
        "target": config.target,
        "loaded": loaded,
        "plist_exists": config.plist_path.is_file(),
        "plist_path": str(config.plist_path),
        "database_exists": config.db_path.is_file(),
        "database": str(config.db_path),
        "active_tasks": _active_task_count(config),
        "stdout_log": str(config.stdout_path),
        "stderr_log": str(config.stderr_path),
    }


def install_service(
    config: ServiceConfig,
    *,
    force: bool = False,
    runner: CommandRunner = subprocess.run,
    platform_name: str = sys.platform,
) -> dict[str, Any]:
    validate_config(config, platform_name=platform_name)
    with _maintenance_guard(config, force=force) as active:
        _prepare_paths(config)
        previous = _read_existing_plist(config.plist_path, config.uid)
        was_loaded = _is_loaded(config, runner=runner)
        payload = plistlib.dumps(build_plist(config), fmt=plistlib.FMT_XML, sort_keys=True)
        _write_private_file(config.plist_path, payload, 0o644)

        lint = _run(["plutil", "-lint", str(config.plist_path)], runner=runner)
        if lint.returncode != 0:
            if previous is None:
                config.plist_path.unlink(missing_ok=True)
            else:
                _write_private_file(config.plist_path, previous, 0o644)
            raise ServiceError(_command_error(["plutil", "-lint", str(config.plist_path)], lint))

        try:
            if was_loaded:
                bootout = _run(["launchctl", "bootout", config.target], runner=runner)
                if bootout.returncode != 0:
                    raise ServiceError(
                        _command_error(["launchctl", "bootout", config.target], bootout)
                    )
            bootstrap = _run(
                ["launchctl", "bootstrap", config.domain, str(config.plist_path)], runner=runner
            )
            if bootstrap.returncode != 0:
                raise ServiceError(
                    _command_error(
                        ["launchctl", "bootstrap", config.domain, str(config.plist_path)],
                        bootstrap,
                    )
                )
            kickstart = _run(["launchctl", "kickstart", "-p", config.target], runner=runner)
            if kickstart.returncode != 0:
                raise ServiceError(
                    _command_error(["launchctl", "kickstart", "-p", config.target], kickstart)
                )
        except Exception:
            _run(["launchctl", "bootout", config.target], runner=runner)
            if previous is None:
                config.plist_path.unlink(missing_ok=True)
            else:
                _write_private_file(config.plist_path, previous, 0o644)
                if was_loaded:
                    _run(
                        ["launchctl", "bootstrap", config.domain, str(config.plist_path)],
                        runner=runner,
                    )
            raise

        status = service_status(config, runner=runner, platform_name=platform_name)
        status.update(
            {"operation": "install", "replaced": previous is not None, "forced_active": active}
        )
        return status


def restart_service(
    config: ServiceConfig,
    *,
    force: bool = False,
    runner: CommandRunner = subprocess.run,
    platform_name: str = sys.platform,
) -> dict[str, Any]:
    validate_config(config, platform_name=platform_name)
    with _maintenance_guard(config, force=force) as active:
        if not config.plist_path.is_file() or not _is_loaded(config, runner=runner):
            raise ServiceError("service is not installed and loaded")
        completed = _run(["launchctl", "kickstart", "-k", "-p", config.target], runner=runner)
        if completed.returncode != 0:
            raise ServiceError(
                _command_error(["launchctl", "kickstart", "-k", "-p", config.target], completed)
            )
        status = service_status(config, runner=runner, platform_name=platform_name)
        status.update({"operation": "restart", "forced_active": active})
        return status


def uninstall_service(
    config: ServiceConfig,
    *,
    force: bool = False,
    runner: CommandRunner = subprocess.run,
    platform_name: str = sys.platform,
) -> dict[str, Any]:
    validate_config(config, platform_name=platform_name)
    with _maintenance_guard(config, force=force) as active:
        was_loaded = _is_loaded(config, runner=runner)
        if was_loaded:
            completed = _run(["launchctl", "bootout", config.target], runner=runner)
            if completed.returncode != 0:
                raise ServiceError(
                    _command_error(["launchctl", "bootout", config.target], completed)
                )
        removed = config.plist_path.is_file()
        config.plist_path.unlink(missing_ok=True)
        return {
            "ok": True,
            "operation": "uninstall",
            "label": config.label,
            "loaded": False,
            "plist_removed": removed,
            "state_preserved": True,
            "database": str(config.db_path),
            "forced_active": active,
        }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Manage the AGY worker macOS LaunchAgent.")
    parser.add_argument("--label", default=DEFAULT_LABEL)
    subparsers = parser.add_subparsers(dest="command", required=True)
    install = subparsers.add_parser("install")
    install.add_argument("--force", action="store_true")
    subparsers.add_parser("status")
    restart = subparsers.add_parser("restart")
    restart.add_argument("--force", action="store_true")
    uninstall = subparsers.add_parser("uninstall")
    uninstall.add_argument("--force", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    config = build_config(label=args.label)
    try:
        if args.command == "install":
            payload = install_service(config, force=args.force)
        elif args.command == "restart":
            payload = restart_service(config, force=args.force)
        elif args.command == "uninstall":
            payload = uninstall_service(config, force=args.force)
        else:
            payload = service_status(config)
    except (OSError, ServiceError) as exc:
        print(
            json.dumps(
                {"ok": False, "operation": args.command, "error": sanitize_output(str(exc))},
                indent=2,
            )
        )
        return 1
    print(json.dumps(payload, indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
