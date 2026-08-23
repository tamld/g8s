#!/usr/bin/env python3
"""Create scoped AGY collection jobs for artifact-heavy directories."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from pathlib import Path

from agy_harness import permission_names, role_names


PLUGIN_ROOT = Path(__file__).resolve().parents[1]
DISPATCH = PLUGIN_ROOT / "scripts" / "agy_dispatch.py"


MODES = {
    "inventory": "Create a concise file inventory. Group by domain, extension, and likely value. Do not read secrets.",
    "frontmatter": "Extract markdown frontmatter keys, titles, summaries, and epistemic status. Do not copy long bodies.",
    "candidates": "Find candidate skills, MCP config, harness, loop, Gorai, and project knowledge artifacts.",
    "logs": "Summarize logs or command outputs into errors, warnings, root-cause hints, and next actions.",
    "mcp-map": "Map MCP servers, provider registries, tool surfaces, permission gates, and runtime adoption risks.",
    "skill-map": "Map skills, roles, operating procedures, reusable prompts, and adoption boundaries.",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Dispatch a scoped AGY artifact collection job.")
    parser.add_argument("--root", required=True, help="Root path for AGY to inspect.")
    parser.add_argument("--mode", choices=sorted(MODES), default="inventory")
    parser.add_argument("--focus", default="", help="Extra focus instruction.")
    parser.add_argument("--model", default="Gemini 3.5 Flash (Low)")
    parser.add_argument("--role", choices=role_names(), default="collector")
    parser.add_argument("--permission", choices=permission_names(), default="read_only")
    parser.add_argument("--timeout", default="5m0s")
    parser.add_argument("--out-dir", default=str(Path.cwd() / "agy-reports"))
    parser.add_argument("--name", default="", help="Report basename. Defaults to mode timestamp.")
    parser.add_argument("--skip-permissions", action="store_true", help="Allow AGY permission skipping when the permission profile allows it.")
    parser.add_argument("--print-stdout", action="store_true")
    return parser.parse_args()


def build_prompt(root: Path, mode: str, focus: str, role: str, permission: str) -> str:
    return f"""You are a fast read-only artifact collection worker for Codex.

Scope:
- Inspect only this workspace scope: {root}
- Do not mutate files.
- Do not read .env, private keys, token stores, password files, identity documents, or raw confidential payloads.
- If a path looks sensitive, list it under sensitive_flags and skip content.
- Role requested by Codex: {role}
- Permission profile requested by Codex: {permission}

Task:
{MODES[mode]}

Extra focus:
{focus or "None"}

Return a compact JSON object with these keys:
- scope
- methods
- files_considered
- findings
- sensitive_flags
- uncertainty
- recommended_next_actions
"""


def main() -> int:
    args = parse_args()
    root = Path(args.root).expanduser().resolve()
    if not root.exists():
        raise SystemExit(f"Root does not exist: {root}")

    out_dir = Path(args.out_dir).expanduser()
    out_dir.mkdir(parents=True, exist_ok=True)
    stamp = time.strftime("%Y%m%d-%H%M%S")
    basename = args.name or f"agy-{args.mode}-{stamp}"
    prompt_path = out_dir / f"{basename}.prompt.txt"
    result_path = out_dir / f"{basename}.result.json"
    prompt_path.write_text(
        build_prompt(root, args.mode, args.focus, args.role, args.permission),
        encoding="utf-8",
    )

    cmd = [
        sys.executable,
        str(DISPATCH),
        "--prompt-file",
        str(prompt_path),
        "--role",
        args.role,
        "--permission",
        args.permission,
        "--add-dir",
        str(root),
        "--model",
        args.model,
        "--timeout",
        args.timeout,
        "--out",
        str(result_path),
    ]
    if args.skip_permissions:
        cmd.append("--skip-permissions")
    if args.print_stdout:
        cmd.append("--print-stdout")

    proc = subprocess.run(cmd, text=True, check=False)
    summary = {
        "ok": proc.returncode == 0,
        "returncode": proc.returncode,
        "prompt": str(prompt_path),
        "result": str(result_path),
        "root": str(root),
        "mode": args.mode,
        "model": args.model,
        "role": args.role,
        "permission": args.permission,
        "skip_permissions": args.skip_permissions,
    }
    print(json.dumps(summary, indent=2, ensure_ascii=False))
    return proc.returncode


if __name__ == "__main__":
    raise SystemExit(main())
