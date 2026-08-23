#!/usr/bin/env python3
"""Claim and execute durable AGY tasks from the local control-plane queue."""

from __future__ import annotations

import argparse
import json
import os
import re
import signal
import socket
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any

from agy_control_plane import ControlPlane, default_db_path
from agy_dispatch import read_bounded_file, sanitize_output


PLUGIN_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_DISPATCH_SCRIPT = PLUGIN_ROOT / "scripts" / "agy_dispatch.py"
DURATION_PATTERN = re.compile(
    r"^(?:(?P<hours>\d+(?:\.\d+)?)h)?"
    r"(?:(?P<minutes>\d+(?:\.\d+)?)m)?"
    r"(?:(?P<seconds>\d+(?:\.\d+)?)s)?$"
)
_ACTIVE_PROCESS: subprocess.Popen[str] | None = None
_ACTIVE_PROCESS_LOCK = threading.Lock()


def parse_duration_seconds(value: str) -> float:
    if value.endswith("ms"):
        try:
            milliseconds = float(value[:-2])
        except ValueError as exc:
            raise ValueError(f"invalid duration: {value}") from exc
        if milliseconds <= 0:
            raise ValueError("duration must be positive")
        return milliseconds / 1000.0
    match = DURATION_PATTERN.fullmatch(value)
    if not match or not any(match.groupdict().values()):
        raise ValueError(f"invalid duration: {value}")
    seconds = (
        float(match.group("hours") or 0) * 3600
        + float(match.group("minutes") or 0) * 60
        + float(match.group("seconds") or 0)
    )
    if seconds <= 0:
        raise ValueError("duration must be positive")
    return seconds


def _write_private_text(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(value, encoding="utf-8")
    path.chmod(0o600)


def build_dispatch_command(
    request: dict[str, Any],
    *,
    prompt_path: Path,
    result_path: Path,
    dispatch_script: Path = DEFAULT_DISPATCH_SCRIPT,
) -> list[str]:
    command = [
        sys.executable,
        str(dispatch_script),
        "--prompt-file",
        str(prompt_path),
        "--model",
        request["model"],
        "--role",
        request["role"],
        "--permission",
        request["permission"],
        "--timeout",
        request["timeout"],
        "--out",
        str(result_path),
    ]
    if request.get("agy_bin"):
        command.extend(["--agy-bin", request["agy_bin"]])
    for root in request.get("add_dirs", []):
        command.extend(["--add-dir", root])
    if request.get("skip_permissions"):
        command.append("--skip-permissions")
    return command


def _spawn(
    command: list[str],
    *,
    stdout_path: Path,
    stderr_path: Path,
    cwd: Path,
) -> subprocess.Popen[str]:
    stdout_path.parent.mkdir(parents=True, exist_ok=True)
    stdout_handle = stdout_path.open("w", encoding="utf-8", errors="replace")
    stderr_handle = stderr_path.open("w", encoding="utf-8", errors="replace")
    stdout_path.chmod(0o600)
    stderr_path.chmod(0o600)
    kwargs: dict[str, Any] = {
        "text": True,
        "stdout": stdout_handle,
        "stderr": stderr_handle,
        "cwd": str(cwd),
    }
    if os.name == "nt":
        kwargs["creationflags"] = subprocess.CREATE_NEW_PROCESS_GROUP
    else:
        kwargs["start_new_session"] = True
    try:
        return subprocess.Popen(command, **kwargs)
    finally:
        stdout_handle.close()
        stderr_handle.close()


def _collect_process_output(process: subprocess.Popen[str], stdout_path: Path, stderr_path: Path) -> tuple[str, str]:
    if os.name != "nt" and process.returncode is None:
        terminate_process_tree(process)
    process.wait()
    try:
        stdout = read_bounded_file(stdout_path)
        stderr = read_bounded_file(stderr_path)
        return stdout, stderr
    finally:
        stdout_path.unlink(missing_ok=True)
        stderr_path.unlink(missing_ok=True)


def _set_active_process(process: subprocess.Popen[str] | None) -> None:
    global _ACTIVE_PROCESS
    with _ACTIVE_PROCESS_LOCK:
        _ACTIVE_PROCESS = process


def terminate_active_process() -> None:
    with _ACTIVE_PROCESS_LOCK:
        process = _ACTIVE_PROCESS
    if process is not None:
        terminate_process_tree(process)


def _supports_waitid_wnowait() -> bool:
    return os.name != "nt" and all(
        hasattr(os, name) for name in ("waitid", "P_PID", "WEXITED", "WNOHANG", "WNOWAIT")
    )


def _process_is_running(process: subprocess.Popen[str]) -> bool:
    if not _supports_waitid_wnowait():
        return process.poll() is None
    if process.returncode is not None:
        return False
    try:
        status = os.waitid(
            os.P_PID,
            process.pid,
            os.WEXITED | os.WNOHANG | os.WNOWAIT,
        )
    except ChildProcessError:
        return False
    return status is None


def terminate_process_tree(process: subprocess.Popen[str], *, grace_seconds: float = 2.0) -> None:
    """Terminate only the live process represented by this local Popen handle."""
    if os.name == "nt":
        if process.poll() is not None:
            return
        subprocess.run(
            ["taskkill", "/F", "/T", "/PID", str(process.pid)],
            capture_output=True,
            text=True,
            check=False,
        )
        return
    if process.returncode is not None:
        return
    process_group = process.pid
    try:
        os.killpg(process_group, signal.SIGTERM)
    except (PermissionError, ProcessLookupError):
        process.wait()
        return
    deadline = time.monotonic() + grace_seconds
    if _supports_waitid_wnowait():
        while _process_is_running(process) and time.monotonic() < deadline:
            time.sleep(0.02)
        try:
            os.killpg(process_group, signal.SIGKILL)
        except (PermissionError, ProcessLookupError):
            pass
        process.wait()
        return
    try:
        process.wait(timeout=grace_seconds)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process_group, signal.SIGKILL)
        except (PermissionError, ProcessLookupError):
            pass
        process.wait()


def _worker_pause_state(result: dict[str, Any]) -> tuple[str, str] | None:
    candidates: list[dict[str, Any]] = [result]
    stdout = result.get("stdout")
    if isinstance(stdout, str):
        text = stdout.strip()
        if text.startswith("```") and text.endswith("```"):
            lines = text.splitlines()
            text = "\n".join(lines[1:-1]).strip()
        try:
            decoded = json.loads(text)
        except (TypeError, json.JSONDecodeError):
            decoded = None
        if isinstance(decoded, dict):
            candidates.append(decoded)
    for candidate in candidates:
        state = candidate.get("status")
        if state in {"NEEDS_INFO", "BLOCKED"}:
            reason = candidate.get("reason") or candidate.get("summary") or state.lower()
            return state, str(reason)
    return None


def _execute_task(
    control_plane: ControlPlane,
    task: dict[str, Any],
    *,
    worker_id: str,
    lease_seconds: float,
    poll_seconds: float = 0.2,
    dispatch_script: Path = DEFAULT_DISPATCH_SCRIPT,
) -> dict[str, Any]:
    task_id = task["task_id"]
    lease_token = task["lease_token"]
    request = task["request"]
    attempt = task["attempts"]
    run_dir = control_plane.db_path.parent / "runs" / task_id / f"attempt-{attempt}"
    prompt_path = run_dir / "prompt.txt"
    result_path = run_dir / "result.json"
    stdout_path = run_dir / "worker.stdout"
    stderr_path = run_dir / "worker.stderr"
    _write_private_text(prompt_path, request["prompt"])
    command = build_dispatch_command(
        request,
        prompt_path=prompt_path,
        result_path=result_path,
        dispatch_script=dispatch_script,
    )
    try:
        process = _spawn(
            command,
            stdout_path=stdout_path,
            stderr_path=stderr_path,
            cwd=Path(request["add_dirs"][0]),
        )
        _set_active_process(process)
    except OSError as exc:
        if not control_plane.start_task(task_id, worker_id, lease_token):
            return {"ok": False, "status": "lease_lost", "reason": str(exc)}
        result = {"ok": False, "status": "execution_error", "reason": sanitize_output(str(exc))}
        updated = control_plane.finish_attempt(
            task_id,
            worker_id,
            lease_token,
            result=result,
            success=False,
            retryable=True,
            error=result["reason"],
        )
        prompt_path.unlink(missing_ok=True)
        control_plane.export_task(task_id, run_dir)
        return updated
    if not control_plane.start_task(task_id, worker_id, lease_token):
        terminate_process_tree(process)
        prompt_path.unlink(missing_ok=True)
        return {"ok": False, "status": "lease_lost"}

    deadline = time.monotonic() + parse_duration_seconds(request["timeout"])
    heartbeat_interval = max(0.1, min(5.0, lease_seconds / 3.0))
    next_heartbeat = time.monotonic() + heartbeat_interval
    terminal_reason: str | None = None
    lease_lost = False
    while _process_is_running(process):
        now = time.monotonic()
        execution_signal = control_plane.execution_signal(task_id, worker_id, lease_token)
        if execution_signal == "cancel_requested":
            terminal_reason = "cancelled"
            terminate_process_tree(process)
            break
        if execution_signal == "lease_lost":
            lease_lost = True
            terminate_process_tree(process)
            break
        if now >= deadline:
            terminal_reason = "timeout"
            terminate_process_tree(process)
            break
        if now >= next_heartbeat:
            if not control_plane.heartbeat(
                task_id,
                worker_id,
                lease_token,
                lease_seconds=lease_seconds,
            ):
                lease_lost = True
                terminate_process_tree(process)
                break
            next_heartbeat = now + heartbeat_interval
        time.sleep(poll_seconds)

    stdout, stderr = _collect_process_output(process, stdout_path, stderr_path)
    if lease_lost:
        prompt_path.unlink(missing_ok=True)
        return {"ok": False, "status": "lease_lost"}
    if terminal_reason == "cancelled":
        result = {
            "ok": False,
            "status": "cancelled",
            "stdout": sanitize_output(stdout),
            "stderr": sanitize_output(stderr),
        }
        updated = control_plane.finish_attempt(
            task_id,
            worker_id,
            lease_token,
            result=result,
            success=False,
            retryable=False,
            error="cancelled by orchestrator",
        )
    elif terminal_reason == "timeout":
        result = {
            "ok": False,
            "status": "timeout",
            "stdout": sanitize_output(stdout),
            "stderr": sanitize_output(stderr),
        }
        updated = control_plane.finish_attempt(
            task_id,
            worker_id,
            lease_token,
            result=result,
            success=False,
            retryable=True,
            error="execution deadline exceeded",
        )
    else:
        try:
            result = json.loads(result_path.read_text(encoding="utf-8"))
            if not isinstance(result, dict):
                raise ValueError("result must be an object")
        except (OSError, ValueError, json.JSONDecodeError) as exc:
            result = {
                "ok": False,
                "status": "invalid_result",
                "reason": sanitize_output(str(exc)),
                "worker_stdout": sanitize_output(stdout),
                "worker_stderr": sanitize_output(stderr),
            }
        success = process.returncode == 0 and result.get("ok") is True
        retryable = not bool(result.get("contract_violation"))
        pause = _worker_pause_state(result)
        if pause is not None:
            updated = control_plane.pause_task(
                task_id,
                worker_id,
                lease_token,
                state=pause[0],
                result=result,
                reason=pause[1],
            )
        else:
            updated = control_plane.finish_attempt(
                task_id,
                worker_id,
                lease_token,
                result=result,
                success=success,
                retryable=retryable,
                error=None
                if success
                else str(result.get("reason") or result.get("status") or "failed"),
            )
    prompt_path.unlink(missing_ok=True)
    control_plane.export_task(task_id, run_dir)
    return updated


def execute_task(
    control_plane: ControlPlane,
    task: dict[str, Any],
    *,
    worker_id: str,
    lease_seconds: float,
    poll_seconds: float = 0.2,
    dispatch_script: Path = DEFAULT_DISPATCH_SCRIPT,
) -> dict[str, Any]:
    task_id = task["task_id"]
    attempt = task["attempts"]
    run_dir = control_plane.db_path.parent / "runs" / task_id / f"attempt-{attempt}"
    prompt_path = run_dir / "prompt.txt"
    try:
        return _execute_task(
            control_plane,
            task,
            worker_id=worker_id,
            lease_seconds=lease_seconds,
            poll_seconds=poll_seconds,
            dispatch_script=dispatch_script,
        )
    finally:
        terminate_active_process()
        _set_active_process(None)
        prompt_path.unlink(missing_ok=True)
        (run_dir / "worker.stdout").unlink(missing_ok=True)
        (run_dir / "worker.stderr").unlink(missing_ok=True)


def run_once(
    control_plane: ControlPlane,
    *,
    worker_id: str,
    lease_seconds: float,
    poll_seconds: float = 0.2,
    dispatch_script: Path = DEFAULT_DISPATCH_SCRIPT,
) -> dict[str, Any] | None:
    task = control_plane.claim_next(worker_id, lease_seconds=lease_seconds)
    if task is None:
        return None
    return execute_task(
        control_plane,
        task,
        worker_id=worker_id,
        lease_seconds=lease_seconds,
        poll_seconds=poll_seconds,
        dispatch_script=dispatch_script,
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run durable application-only AGY tasks.")
    parser.add_argument("--db", default=str(default_db_path()))
    parser.add_argument("--worker-id", default=f"{socket.gethostname()}:{os.getpid()}")
    parser.add_argument("--lease-seconds", type=float, default=30.0)
    parser.add_argument("--poll-seconds", type=float, default=0.5)
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--quiet", action="store_true")
    parser.add_argument("--dispatch-script", default=str(DEFAULT_DISPATCH_SCRIPT))
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    control_plane = ControlPlane(args.db)
    dispatch_script = Path(args.dispatch_script).expanduser().resolve()
    shutdown_signal: int | None = None

    def request_shutdown(signum: int, _frame: Any) -> None:
        nonlocal shutdown_signal
        shutdown_signal = signum
        terminate_active_process()

    signal.signal(signal.SIGINT, request_shutdown)
    signal.signal(signal.SIGTERM, request_shutdown)
    try:
        while shutdown_signal is None:
            result = run_once(
                control_plane,
                worker_id=args.worker_id,
                lease_seconds=args.lease_seconds,
                poll_seconds=args.poll_seconds,
                dispatch_script=dispatch_script,
            )
            if result is None:
                if args.once:
                    return 0
                time.sleep(args.poll_seconds)
                continue
            if not args.quiet:
                print(json.dumps(result, indent=2, ensure_ascii=False))
            if shutdown_signal is not None:
                return 128 + shutdown_signal
            if args.once:
                return 0 if result.get("state") in {"SUCCEEDED", "CANCELLED", "QUEUED"} else 1
        return 128 + shutdown_signal
    finally:
        terminate_active_process()


if __name__ == "__main__":
    raise SystemExit(main())
