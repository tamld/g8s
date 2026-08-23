#!/usr/bin/env python3
"""Run one bounded AGY CLI job and save a structured result."""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import BinaryIO

from agy_harness import (
    build_contract_prompt,
    permission_names,
    role_names,
    validate_dispatch,
)


DEFAULT_MODEL = os.environ.get("AGY_MODEL", "Gemini 3.7 Flash (High)")
READ_ONLY_CONTRACT_EXIT = 3
MAX_CAPTURE_BYTES = 2 * 1024 * 1024
WINDOWS_EXECUTABLE_SUFFIXES = (".exe", ".cmd", ".bat")

SENSITIVE_PATTERNS = [
    (re.compile(r"postgresql://[^\s\"`]+"), "postgresql://<REDACTED>"),
    (re.compile(r"://[^\s\"`/:]+:[^\s\"`/@]+@"), "://<REDACTED>:<REDACTED>@"),
    (re.compile(r"specifically `[^`]+`"), "specifically `<REDACTED>`"),
    (re.compile(r"(?i)(password|credential|secret)[^.\n]{0,160}"), r"\1 <REDACTED>"),
]

READ_ONLY_VIOLATION_PATTERNS = [
    (
        re.compile(
            r"(?im)^\s*(?:[$>]\s*)?(?:uv\s+run\s+python\s+)?wiki\.py\s+"
            r"(?:reflect|write|rename|ingest|promote|claim|bypass)\b"
        ),
        "wiki_mutation_command",
    ),
    (
        re.compile(
            r"(?i)\b(?:i\s+)?(?:ran|executed|used)\s+`?"
            r"(?:uv\s+run\s+python\s+)?wiki\.py\s+"
            r"(?:reflect|write|rename|ingest|promote|claim|bypass)\b"
        ),
        "wiki_mutation_report",
    ),
    (re.compile(r"(?i)\bsession logged to log\.md\b"), "wiki_reflect_side_effect"),
    (re.compile(r"(?i)\bnote written:\b"), "wiki_write_side_effect"),
    (
        re.compile(
            r"(?im)^\s*(?:[$>]\s*)?git\s+"
            r"(?:add|commit|checkout|reset|merge|rebase|push|rm|mv)\b"
        ),
        "git_mutation_command",
    ),
    (
        re.compile(r"(?m)^\[[^\]\n]+ [0-9a-f]{7,}\] .+"),
        "git_commit_side_effect",
    ),
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Dispatch a read-only prompt to AGY.")
    parser.add_argument("--prompt", help="Prompt text to send to AGY.")
    parser.add_argument("--prompt-file", help="Read prompt text from a file.")
    parser.add_argument("--model", default=DEFAULT_MODEL, help="AGY model name.")
    parser.add_argument("--role", choices=role_names(), default="collector", help="Worker role contract.")
    parser.add_argument("--permission", choices=permission_names(), default="read_only", help="Harness permission profile.")
    parser.add_argument(
        "--agy-bin",
        help="Path or command name for the agy executable. Defaults to AGY_BIN, PATH, then safe home fallbacks.",
    )
    parser.add_argument("--add-dir", action="append", default=[], help="Directory to add to AGY workspace. Repeatable.")
    parser.add_argument("--timeout", default="5m0s", help="AGY --print-timeout value.")
    parser.add_argument("--out", help="Write JSON result to this file.")
    parser.add_argument("--no-sandbox", action="store_true", help="Do not pass --sandbox to AGY.")
    parser.add_argument(
        "--skip-permissions",
        action="store_true",
        help="Pass AGY --dangerously-skip-permissions. Allowed only by selected permission profile.",
    )
    parser.add_argument("--print-stdout", action="store_true", help="Also print AGY response text to stdout.")
    parser.add_argument(
        "--receipt-id",
        help="Write receipt ID from ControlPlane. Required when --permission=workspace_write.",
    )
    return parser.parse_args()


def read_prompt(args: argparse.Namespace) -> str:
    if args.prompt and args.prompt_file:
        raise SystemExit("Use either --prompt or --prompt-file, not both.")
    if args.prompt_file:
        return Path(args.prompt_file).read_text(encoding="utf-8")
    if args.prompt:
        return args.prompt
    if not sys.stdin.isatty():
        return sys.stdin.read()
    raise SystemExit("Provide --prompt, --prompt-file, or stdin.")


def sanitize_output(value: str) -> str:
    sanitized = value
    for pattern, replacement in SENSITIVE_PATTERNS:
        sanitized = pattern.sub(replacement, sanitized)
    return sanitized


def _read_bounded_stream(stream: BinaryIO, max_bytes: int = MAX_CAPTURE_BYTES) -> str:
    stream.seek(0, os.SEEK_END)
    size = stream.tell()
    stream.seek(0)
    if size <= max_bytes:
        payload = stream.read()
    else:
        half = max_bytes // 2
        head = stream.read(half)
        stream.seek(-half, os.SEEK_END)
        tail = stream.read(half)
        payload = head + b"\n<OUTPUT_TRUNCATED>\n" + tail
    return payload.decode("utf-8", errors="replace")


def read_bounded_file(path: Path, max_bytes: int = MAX_CAPTURE_BYTES) -> str:
    with path.open("rb") as stream:
        return _read_bounded_stream(stream, max_bytes=max_bytes)


def run_agy_command(
    command: list[str], *, cwd: str | Path | None = None
) -> subprocess.CompletedProcess[str]:
    with tempfile.TemporaryFile() as stdout_stream, tempfile.TemporaryFile() as stderr_stream:
        completed = subprocess.run(
            command,
            stdout=stdout_stream,
            stderr=stderr_stream,
            check=False,
            cwd=str(cwd) if cwd is not None else None,
        )
        return subprocess.CompletedProcess(
            args=command,
            returncode=completed.returncode,
            stdout=_read_bounded_stream(stdout_stream),
            stderr=_read_bounded_stream(stderr_stream),
        )


def _windows_suffix_candidates(path: Path, platform: str) -> list[Path]:
    if platform != "win32" or path.suffix:
        return [path]
    return [path, *(Path(f"{path}{suffix}") for suffix in WINDOWS_EXECUTABLE_SUFFIXES)]


def _first_existing(candidates: list[Path], exists: object) -> Path | None:
    for candidate in candidates:
        if exists(candidate):
            return candidate
    return None


def _resolve_reference(
    reference: str,
    *,
    platform: str,
    which: object,
    exists: object,
) -> Path | None:
    path = Path(reference).expanduser()
    direct = _first_existing(_windows_suffix_candidates(path, platform), exists)
    if direct is not None:
        return direct

    which_match = which(reference)
    if which_match:
        return Path(which_match)
    return None


def _home_fallbacks(home: Path) -> list[Path]:
    return [
        home / ".local" / "bin" / "agy",
        home / "AppData" / "Local" / "Programs" / "agy" / "agy",
        home / "AppData" / "Roaming" / "npm" / "agy",
    ]


def resolve_agy_binary(
    explicit: str | None,
    *,
    env: dict[str, str] | os._Environ[str] = os.environ,
    platform: str = sys.platform,
    home: Path | None = None,
    which: object = shutil.which,
    exists: object = Path.exists,
) -> Path | None:
    """Resolve the AGY executable without assuming one host layout."""
    for reference in (explicit, env.get("AGY_BIN")):
        if reference:
            resolved = _resolve_reference(
                reference,
                platform=platform,
                which=which,
                exists=exists,
            )
            if resolved is not None:
                return resolved

    path_match = which("agy")
    if path_match:
        return Path(path_match)

    resolved_home = home or Path.home()
    for fallback in _home_fallbacks(resolved_home):
        direct = _first_existing(_windows_suffix_candidates(fallback, platform), exists)
        if direct is not None:
            return direct
    return None


def _match_snippet(text: str, match: re.Match[str], radius: int = 96) -> str:
    start = max(0, match.start() - radius)
    end = min(len(text), match.end() + radius)
    snippet = text[start:end].replace("\n", "\\n")
    return sanitize_output(snippet)


def detect_read_only_contract_violations(stdout: str, stderr: str) -> list[dict[str, str]]:
    """Detect likely side effects from a worker that was supposed to stay read-only."""
    combined = "\n".join(part for part in (stdout, stderr) if part)
    violations: list[dict[str, str]] = []
    for pattern, violation_type in READ_ONLY_VIOLATION_PATTERNS:
        match = pattern.search(combined)
        if match:
            violations.append(
                {
                    "type": violation_type,
                    "snippet": _match_snippet(combined, match),
                }
            )
    return violations


def build_agy_command(args: argparse.Namespace, agy_bin: Path, effective_prompt: str) -> list[str]:
    cmd = [
        str(agy_bin),
        "--prompt",
        effective_prompt,
        "--model",
        args.model,
        "--print-timeout",
        args.timeout,
    ]
    if args.skip_permissions:
        cmd.append("--dangerously-skip-permissions")
    if not args.no_sandbox:
        cmd.append("--sandbox")
    for scope in args.add_dir:
        cmd.extend(["--add-dir", str(Path(scope).expanduser())])
    return cmd


def main() -> int:
    args = parse_args()
    prompt = read_prompt(args)
    agy_bin = resolve_agy_binary(args.agy_bin)
    if agy_bin is None:
        raise SystemExit("AGY binary not found. Set --agy-bin, set AGY_BIN, or add agy to PATH.")

    try:
        gate = validate_dispatch(
            prompt=prompt,
            role_name=args.role,
            permission_name=args.permission,
            add_dirs=args.add_dir,
            skip_permissions=args.skip_permissions,
            receipt_id=getattr(args, "receipt_id", None),
        )
    except ValueError as exc:
        raise SystemExit(f"AGY harness blocked dispatch: {exc}") from exc

    effective_prompt = build_contract_prompt(
        prompt, args.role, args.permission,
        receipt_info=gate.get("receipt"),
    )
    cmd = build_agy_command(args, agy_bin, effective_prompt)
    started = time.time()
    proc = run_agy_command(cmd)
    duration = round(time.time() - started, 3)
    contract_violations = (
        detect_read_only_contract_violations(proc.stdout, proc.stderr)
        if not gate["mutation_allowed"]
        else []
    )
    harness_returncode = (
        READ_ONLY_CONTRACT_EXIT
        if proc.returncode == 0 and contract_violations
        else proc.returncode
    )

    result = {
        "ok": harness_returncode == 0,
        "returncode": proc.returncode,
        "harness_returncode": harness_returncode,
        "duration_seconds": duration,
        "model": args.model,
        "role": args.role,
        "permission": args.permission,
        "harness_gate": gate,
        "agy_bin": str(agy_bin),
        "add_dirs": args.add_dir,
        "command_preview": f"{shlex.quote(str(agy_bin))} --prompt <prompt> --model {shlex.quote(args.model)} --print-timeout {shlex.quote(args.timeout)}",
        "stdout": sanitize_output(proc.stdout),
        "stderr": sanitize_output(proc.stderr),
    }
    if gate.get("receipt"):
        result["receipt"] = gate["receipt"]
    if contract_violations:
        result["contract_violation"] = {
            "policy": "read_only",
            "exit_code": READ_ONLY_CONTRACT_EXIT,
            "violations": contract_violations,
        }

    if args.out:
        out_path = Path(args.out).expanduser()
        out_path.parent.mkdir(parents=True, exist_ok=True)
        out_path.write_text(json.dumps(result, indent=2, ensure_ascii=False), encoding="utf-8")

    if args.print_stdout:
        print(sanitize_output(proc.stdout))
    elif not args.out:
        print(json.dumps(result, indent=2, ensure_ascii=False))

    return harness_returncode


if __name__ == "__main__":
    raise SystemExit(main())
