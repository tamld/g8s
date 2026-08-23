#!/usr/bin/env python3
"""Operator CLI for the durable AGY dispatch control plane."""

from __future__ import annotations

import argparse
import json
from typing import Any

import agy_dispatch
from agy_control_plane import ControlPlane, TASK_STATES, default_db_path
from agy_harness import permission_names, role_names


def _safe_task(task: dict[str, Any]) -> dict[str, Any]:
    payload = {key: value for key, value in task.items() if key != "request"}
    request = task["request"]
    payload["request"] = {
        "model": request["model"],
        "role": request["role"],
        "permission": request["permission"],
        "timeout": request["timeout"],
        "add_dirs": request["add_dirs"],
        "skip_permissions": request["skip_permissions"],
        "capability_grant": request["capability_grant"],
    }
    return payload


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Manage durable application-only AGY tasks.")
    parser.add_argument("--db", default=str(default_db_path()))
    subparsers = parser.add_subparsers(dest="command", required=True)

    submit = subparsers.add_parser("submit")
    submit.add_argument("--idempotency-key", required=True)
    submit.add_argument("--parent-task-id")
    submit.add_argument("--prompt", required=True)
    submit.add_argument("--model", default=agy_dispatch.DEFAULT_MODEL)
    submit.add_argument("--role", choices=role_names(), default="collector")
    submit.add_argument(
        "--permission",
        choices=[name for name in permission_names() if name != "workspace_write"],
        default="read_only",
    )
    submit.add_argument("--timeout", default="5m0s")
    submit.add_argument("--add-dir", action="append", default=[])
    submit.add_argument("--skip-permissions", action="store_true")
    submit.add_argument("--priority", type=int, default=0)
    submit.add_argument("--max-attempts", type=int, default=1)

    get = subparsers.add_parser("get")
    get.add_argument("task_id")

    list_parser = subparsers.add_parser("list")
    list_parser.add_argument("--state", choices=sorted(TASK_STATES))
    list_parser.add_argument("--limit", type=int, default=50)

    cancel = subparsers.add_parser("cancel")
    cancel.add_argument("task_id")
    cancel.add_argument("--reason", required=True)

    subparsers.add_parser("reconcile")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    control_plane = ControlPlane(args.db)
    if args.command == "submit":
        task = control_plane.submit_task(
            request={
                "prompt": args.prompt,
                "model": args.model,
                "role": args.role,
                "permission": args.permission,
                "timeout": args.timeout,
                "add_dirs": args.add_dir,
                "skip_permissions": args.skip_permissions,
                "no_sandbox": False,
            },
            idempotency_key=args.idempotency_key,
            parent_task_id=args.parent_task_id,
            priority=args.priority,
            max_attempts=args.max_attempts,
            actor="operator-cli",
        )
        payload: Any = _safe_task(task)
    elif args.command == "get":
        task = control_plane.get_task(args.task_id)
        if task is None:
            print(json.dumps({"ok": False, "status": "not_found"}, indent=2))
            return 1
        payload = _safe_task(task)
    elif args.command == "list":
        payload = [
            _safe_task(task)
            for task in control_plane.list_tasks(state=args.state, limit=args.limit)
        ]
    elif args.command == "cancel":
        task = control_plane.cancel_task(
            args.task_id,
            actor="operator-cli",
            reason=args.reason,
        )
        if task is None:
            print(json.dumps({"ok": False, "status": "not_found"}, indent=2))
            return 1
        payload = _safe_task(task)
    else:
        payload = {"reconciled": control_plane.reconcile_expired()}
    print(json.dumps(payload, indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
