#!/usr/bin/env python3
"""Dependency-light MCP stdio server for AGY dispatch.

This intentionally implements only the local JSON-RPC methods needed by Codex
tool discovery: initialize, tools/list, and tools/call. It keeps the policy in
agy_harness.py and agy_dispatch.py instead of creating a second permission layer.
"""

from __future__ import annotations

import json
import os
import platform
import shlex
import sys
import time
from argparse import Namespace
from pathlib import Path
from typing import Any

import agy_dispatch
from agy_control_plane import ControlPlane, TASK_STATES, default_db_path
from agy_harness import PERMISSIONS, ROLES, get_permission, get_role


SERVER_NAME = "agy_dispatch_mcp"
SERVER_VERSION = "0.4.0"
DEFAULT_MCP_PROTOCOL_VERSION = "2025-06-18"
SUPPORTED_MCP_PROTOCOL_VERSIONS = (
    "2025-06-18",
    "2025-03-26",
    "2024-11-05",
)
WORKSPACE_WRITE_BLOCK_REASON = (
    "workspace_write dispatch is intentionally disabled in the MCP minimum "
    "surface. Create a separate human-approved ADR with rollback and "
    "observability before enabling it."
)


def _workspace_write_enabled() -> bool:
    """Opt-in per project via env (ADR-001, tamld-llm-wiki
    docs/goals/tiered-admission-gate-design-20260726/adr-001-workspace-write-scribe.md).
    Default stays blocked for every consumer that does not set the flag."""
    return os.environ.get("AGY_MCP_ALLOW_WORKSPACE_WRITE") == "1"


def _json_text(payload: dict[str, Any]) -> str:
    return json.dumps(payload, ensure_ascii=False, indent=2)


def _tool_result(payload: dict[str, Any], *, is_error: bool = False) -> dict[str, Any]:
    return {
        "content": [{"type": "text", "text": _json_text(payload)}],
        "isError": is_error,
    }


def _roles_payload() -> dict[str, Any]:
    return {
        "ok": True,
        "roles": [
            {
                "name": role.name,
                "purpose": role.purpose,
                "output_focus": role.output_focus,
                "forbidden": list(role.forbidden),
            }
            for role in sorted(ROLES.values(), key=lambda item: item.name)
        ],
    }


def _permissions_payload() -> dict[str, Any]:
    return {
        "ok": True,
        "permissions": [
            {
                "name": permission.name,
                "description": permission.description,
                "mutation_allowed": permission.mutation_allowed,
                "skip_permissions_allowed": permission.skip_permissions_allowed,
                "max_prompt_chars": permission.max_prompt_chars,
                "mcp_enabled": permission.name != "workspace_write" or _workspace_write_enabled(),
            }
            for permission in sorted(PERMISSIONS.values(), key=lambda item: item.name)
        ],
        "disabled_reason": {
            "workspace_write": WORKSPACE_WRITE_BLOCK_REASON,
        },
    }


def _self_awareness_payload(arguments: dict[str, Any] | None = None) -> dict[str, Any]:
    agy_bin = agy_dispatch.resolve_agy_binary(None)
    return {
        "ok": True,
        "server": {
            "name": SERVER_NAME,
            "version": SERVER_VERSION,
            "transport": "stdio-jsonrpc",
            "minimum_surface": True,
        },
        "provider": {
            "name": "agy",
            "installed": agy_bin is not None,
            "binary": str(agy_bin) if agy_bin else None,
            "default_model": agy_dispatch.DEFAULT_MODEL,
            "capabilities": [
                "role-governed dispatch",
                "read-only artifact collection",
                "automation_read permission skipping behind harness",
                "post-run read-only contract detection",
            ],
            "setup_required": agy_bin is None,
            "setup_hint": (
                "Install AGY, add it to PATH, or set AGY_BIN."
                if agy_bin is None
                else None
            ),
        },
        "runtime": {
            "python": sys.version.split()[0],
            "platform": sys.platform,
            "os": platform.platform(),
            "cwd": os.getcwd(),
        },
        "policy": {
            "roles": sorted(ROLES),
            "permissions": sorted(PERMISSIONS),
            "workspace_write_enabled": _workspace_write_enabled(),
            "workspace_write_block_reason": WORKSPACE_WRITE_BLOCK_REASON,
        },
        "control_plane": {
            "enabled": True,
            "database": str(default_db_path()),
            "application_only": True,
            "workspace_write_enabled": _workspace_write_enabled(),
            "worker_command": "python3 scripts/agy_worker.py",
        },
    }


def _optional_str(arguments: dict[str, Any], key: str, default: str | None = None) -> str | None:
    value = arguments.get(key, default)
    if value is None:
        return None
    if not isinstance(value, str):
        raise ValueError(f"{key} must be a string")
    return value


def _required_str(arguments: dict[str, Any], key: str) -> str:
    value = _optional_str(arguments, key)
    if value is None or not value.strip():
        raise ValueError(f"{key} is required")
    return value


def _optional_bool(arguments: dict[str, Any], key: str, default: bool = False) -> bool:
    value = arguments.get(key, default)
    if not isinstance(value, bool):
        raise ValueError(f"{key} must be a boolean")
    return value


def _optional_int(arguments: dict[str, Any], key: str, default: int) -> int:
    value = arguments.get(key, default)
    if not isinstance(value, int) or isinstance(value, bool):
        raise ValueError(f"{key} must be an integer")
    return value


def _optional_list_str(arguments: dict[str, Any], key: str) -> list[str]:
    value = arguments.get(key, [])
    if value is None:
        return []
    if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
        raise ValueError(f"{key} must be a list of strings")
    return value


def _validate_known_profiles(role: str, permission: str) -> None:
    get_role(role)
    get_permission(permission)


def _control_plane() -> ControlPlane:
    return ControlPlane(default_db_path())


def _task_payload(task: dict[str, Any]) -> dict[str, Any]:
    request = task["request"]
    payload = {
        "task_id": task["task_id"],
        "parent_task_id": task["parent_task_id"],
        "idempotency_key": task["idempotency_key"],
        "schema_version": task["schema_version"],
        "state": task["state"],
        "priority": task["priority"],
        "request_hash": task["request_hash"],
        "result": task["result"],
        "result_hash": task["result_hash"],
        "receipt_hash": task["receipt_hash"],
        "attempts": task["attempts"],
        "max_attempts": task["max_attempts"],
        "lease_owner": task["lease_owner"],
        "lease_expires_at": task["lease_expires_at"],
        "cancel_requested": task["cancel_requested"],
        "created_at": task["created_at"],
        "updated_at": task["updated_at"],
        "completed_at": task["completed_at"],
        "last_error": task["last_error"],
        "request": {
            "model": request["model"],
            "role": request["role"],
            "permission": request["permission"],
            "timeout": request["timeout"],
            "add_dirs": request["add_dirs"],
            "skip_permissions": request["skip_permissions"],
            "capability_grant": request["capability_grant"],
        },
    }
    if "deduplicated" in task:
        payload["deduplicated"] = task["deduplicated"]
    return payload


def submit_task(arguments: dict[str, Any] | None = None) -> dict[str, Any]:
    arguments = arguments or {}
    request = {
        "prompt": _required_str(arguments, "prompt"),
        "model": _optional_str(arguments, "model", agy_dispatch.DEFAULT_MODEL)
        or agy_dispatch.DEFAULT_MODEL,
        "role": _optional_str(arguments, "role", "collector") or "collector",
        "permission": _optional_str(arguments, "permission", "read_only") or "read_only",
        "timeout": _optional_str(arguments, "timeout", "5m0s") or "5m0s",
        "add_dirs": _optional_list_str(arguments, "add_dirs"),
        "skip_permissions": _optional_bool(arguments, "skip_permissions", False),
        "no_sandbox": _optional_bool(arguments, "no_sandbox", False),
    }
    task = _control_plane().submit_task(
        request=request,
        idempotency_key=_required_str(arguments, "idempotency_key"),
        parent_task_id=_optional_str(arguments, "parent_task_id"),
        priority=_optional_int(arguments, "priority", 0),
        max_attempts=_optional_int(arguments, "max_attempts", 1),
        actor="codex-mcp",
    )
    return {"ok": True, "status": task["state"].lower(), "task": _task_payload(task)}


def get_task_status(arguments: dict[str, Any] | None = None) -> dict[str, Any]:
    arguments = arguments or {}
    task = _control_plane().get_task(_required_str(arguments, "task_id"))
    if task is None:
        return {"ok": False, "status": "not_found"}
    return {"ok": True, "status": "ok", "task": _task_payload(task)}


def list_task_status(arguments: dict[str, Any] | None = None) -> dict[str, Any]:
    arguments = arguments or {}
    state = _optional_str(arguments, "state")
    tasks = _control_plane().list_tasks(
        state=state,
        limit=_optional_int(arguments, "limit", 50),
    )
    return {"ok": True, "status": "ok", "tasks": [_task_payload(task) for task in tasks]}


def cancel_queued_task(arguments: dict[str, Any] | None = None) -> dict[str, Any]:
    arguments = arguments or {}
    task = _control_plane().cancel_task(
        _required_str(arguments, "task_id"),
        actor="codex-mcp",
        reason=_required_str(arguments, "reason"),
    )
    if task is None:
        return {"ok": False, "status": "not_found"}
    return {"ok": True, "status": "cancel_requested", "task": _task_payload(task)}


def dispatch_task(arguments: dict[str, Any] | None = None) -> dict[str, Any]:
    arguments = arguments or {}
    prompt = _required_str(arguments, "prompt")
    role = _optional_str(arguments, "role", "collector") or "collector"
    permission = _optional_str(arguments, "permission", "read_only") or "read_only"
    model = _optional_str(arguments, "model", agy_dispatch.DEFAULT_MODEL) or agy_dispatch.DEFAULT_MODEL
    timeout = _optional_str(arguments, "timeout", "5m0s") or "5m0s"
    add_dirs = _optional_list_str(arguments, "add_dirs")
    skip_permissions = _optional_bool(arguments, "skip_permissions", False)
    no_sandbox = _optional_bool(arguments, "no_sandbox", False)

    _validate_known_profiles(role, permission)
    if permission == "workspace_write" and not _workspace_write_enabled():
        return {
            "ok": False,
            "status": "blocked_by_policy",
            "reason": WORKSPACE_WRITE_BLOCK_REASON,
            "role": role,
            "permission": permission,
        }
    if no_sandbox:
        return {
            "ok": False,
            "status": "blocked_by_policy",
            "reason": "no_sandbox is disabled in the MCP surface",
            "role": role,
            "permission": permission,
        }
    if not add_dirs:
        return {
            "ok": False,
            "status": "blocked_by_policy",
            "reason": "at least one explicit add_dirs scope root is required",
            "role": role,
            "permission": permission,
        }

    try:
        gate = agy_dispatch.validate_dispatch(
            prompt=prompt,
            role_name=role,
            permission_name=permission,
            add_dirs=add_dirs,
            skip_permissions=skip_permissions,
        )
    except ValueError as exc:
        return {
            "ok": False,
            "status": "blocked_by_harness",
            "reason": str(exc),
            "role": role,
            "permission": permission,
        }

    agy_bin = agy_dispatch.resolve_agy_binary(None)
    if agy_bin is None:
        return {
            "ok": False,
            "status": "setup_required",
            "reason": "AGY binary not found.",
            "setup_hint": "Install AGY, add it to PATH, or set AGY_BIN.",
            "role": role,
            "permission": permission,
            "harness_gate": gate,
        }

    effective_prompt = agy_dispatch.build_contract_prompt(prompt, role, permission)
    args = Namespace(
        model=model,
        timeout=timeout,
        skip_permissions=skip_permissions,
        no_sandbox=no_sandbox,
        add_dir=add_dirs,
    )
    command = agy_dispatch.build_agy_command(args, Path(agy_bin), effective_prompt)
    started = time.time()
    try:
        proc = agy_dispatch.run_agy_command(command, cwd=gate["allowed_roots"][0])
    except OSError as exc:
        return {
            "ok": False,
            "status": "execution_error",
            "reason": str(exc),
            "role": role,
            "permission": permission,
            "harness_gate": gate,
        }

    duration = round(time.time() - started, 3)
    contract_violations = (
        agy_dispatch.detect_read_only_contract_violations(proc.stdout, proc.stderr)
        if not gate["mutation_allowed"]
        else []
    )
    harness_returncode = (
        agy_dispatch.READ_ONLY_CONTRACT_EXIT
        if proc.returncode == 0 and contract_violations
        else proc.returncode
    )
    result: dict[str, Any] = {
        "ok": harness_returncode == 0,
        "status": "ok" if harness_returncode == 0 else "failed",
        "returncode": proc.returncode,
        "harness_returncode": harness_returncode,
        "duration_seconds": duration,
        "model": model,
        "role": role,
        "permission": permission,
        "harness_gate": gate,
        "agy_bin": str(agy_bin),
        "add_dirs": add_dirs,
        "command_preview": (
            f"{shlex.quote(str(agy_bin))} --prompt <prompt> --model "
            f"{shlex.quote(model)} --print-timeout {shlex.quote(timeout)}"
        ),
        "stdout": agy_dispatch.sanitize_output(proc.stdout),
        "stderr": agy_dispatch.sanitize_output(proc.stderr),
    }
    if contract_violations:
        result["contract_violation"] = {
            "policy": "read_only",
            "exit_code": agy_dispatch.READ_ONLY_CONTRACT_EXIT,
            "violations": contract_violations,
        }
    return result


TOOLS: dict[str, dict[str, Any]] = {
    "agy_list_roles": {
        "description": "List AGY dispatch role profiles and role-specific forbidden actions.",
        "annotations": {
            "title": "List AGY Roles",
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
        "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False},
        "handler": lambda _arguments: _roles_payload(),
    },
    "agy_list_permissions": {
        "description": "List AGY dispatch permission profiles and whether each profile is enabled in the MCP minimum surface.",
        "annotations": {
            "title": "List AGY Permissions",
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
        "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False},
        "handler": lambda _arguments: _permissions_payload(),
    },
    "agy_self_awareness": {
        "description": "Report AGY provider availability, runtime metadata, and MCP policy boundaries without exposing secrets.",
        "annotations": {
            "title": "AGY Self Awareness",
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": True,
        },
        "inputSchema": {
            "type": "object",
            "properties": {},
            "additionalProperties": False,
        },
        "handler": _self_awareness_payload,
    },
    "agy_dispatch_task": {
        "description": "Dispatch one bounded AGY worker task through existing role and permission guardrails. Workspace-write is disabled.",
        "annotations": {
            "title": "Dispatch AGY Task",
            "readOnlyHint": False,
            "destructiveHint": False,
            "idempotentHint": False,
            "openWorldHint": True,
        },
        "inputSchema": {
            "type": "object",
            "required": ["prompt", "add_dirs"],
            "properties": {
                "prompt": {
                    "type": "string",
                    "description": "Worker task. Must not request secrets, destructive commands, or raw company payload copying.",
                },
                "role": {"type": "string", "default": "collector", "enum": sorted(ROLES)},
                "permission": {
                    "type": "string",
                    "default": "read_only",
                    "enum": sorted(PERMISSIONS),
                },
                "model": {"type": "string", "default": agy_dispatch.DEFAULT_MODEL},
                "timeout": {"type": "string", "default": "5m0s"},
                "add_dirs": {
                    "type": "array",
                    "items": {"type": "string"},
                    "minItems": 1,
                },
                "skip_permissions": {"type": "boolean", "default": False},
                "no_sandbox": {"const": False},
            },
            "additionalProperties": False,
        },
        "handler": dispatch_task,
    },
    "agy_submit_task": {
        "description": "Queue one idempotent application-only AGY task for a separate durable worker.",
        "annotations": {
            "title": "Submit AGY Task",
            "readOnlyHint": False,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
        "inputSchema": {
            "type": "object",
            "required": ["prompt", "idempotency_key", "add_dirs"],
            "properties": {
                "prompt": {"type": "string"},
                "idempotency_key": {"type": "string", "minLength": 1, "maxLength": 200},
                "parent_task_id": {"type": "string", "minLength": 1},
                "priority": {"type": "integer", "minimum": -100, "maximum": 100, "default": 0},
                "max_attempts": {"type": "integer", "minimum": 1, "maximum": 10, "default": 1},
                "role": {"type": "string", "default": "collector", "enum": sorted(ROLES)},
                "permission": {
                    "type": "string",
                    "default": "read_only",
                    "enum": ["read_only", "automation_read"],
                },
                "model": {"type": "string", "default": agy_dispatch.DEFAULT_MODEL},
                "timeout": {"type": "string", "default": "5m0s"},
                "add_dirs": {
                    "type": "array",
                    "items": {"type": "string"},
                    "minItems": 1,
                },
                "skip_permissions": {"type": "boolean", "default": False},
                "no_sandbox": {"const": False},
            },
            "additionalProperties": False,
        },
        "handler": submit_task,
    },
    "agy_get_task": {
        "description": "Get sanitized durable task state without returning the raw prompt.",
        "annotations": {
            "title": "Get AGY Task",
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
        "inputSchema": {
            "type": "object",
            "required": ["task_id"],
            "properties": {"task_id": {"type": "string"}},
            "additionalProperties": False,
        },
        "handler": get_task_status,
    },
    "agy_list_tasks": {
        "description": "List sanitized durable AGY task states.",
        "annotations": {
            "title": "List AGY Tasks",
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
        "inputSchema": {
            "type": "object",
            "properties": {
                "state": {"type": "string", "enum": sorted(TASK_STATES)},
                "limit": {"type": "integer", "minimum": 1, "maximum": 200, "default": 50},
            },
            "additionalProperties": False,
        },
        "handler": list_task_status,
    },
    "agy_cancel_task": {
        "description": "Request cancellation of a queued or running durable AGY task.",
        "annotations": {
            "title": "Cancel AGY Task",
            "readOnlyHint": False,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
        "inputSchema": {
            "type": "object",
            "required": ["task_id", "reason"],
            "properties": {
                "task_id": {"type": "string"},
                "reason": {"type": "string", "minLength": 1},
            },
            "additionalProperties": False,
        },
        "handler": cancel_queued_task,
    },
}


def tool_list() -> dict[str, Any]:
    return {
        "tools": [
            {
                "name": name,
                "description": spec["description"],
                "inputSchema": spec["inputSchema"],
                "annotations": spec["annotations"],
            }
            for name, spec in TOOLS.items()
        ]
    }


def call_tool(params: dict[str, Any]) -> dict[str, Any]:
    name = params.get("name")
    arguments = params.get("arguments", {})
    if not isinstance(name, str):
        return _tool_result({"ok": False, "status": "invalid_request", "reason": "Tool name is required."}, is_error=True)
    if not isinstance(arguments, dict):
        return _tool_result({"ok": False, "status": "invalid_request", "reason": "arguments must be an object."}, is_error=True)
    if name not in TOOLS:
        return _tool_result({"ok": False, "status": "unknown_tool", "reason": f"Unknown tool: {name}"}, is_error=True)
    try:
        payload = TOOLS[name]["handler"](arguments)
    except ValueError as exc:
        return _tool_result({"ok": False, "status": "invalid_arguments", "reason": str(exc)}, is_error=True)
    return _tool_result(payload, is_error=not payload.get("ok", True))


def handle_request(request: dict[str, Any]) -> dict[str, Any] | None:
    request_id = request.get("id")
    method = request.get("method")
    params = request.get("params", {})
    if method == "notifications/initialized":
        return None
    if method == "initialize":
        client_version = params.get("protocolVersion") if isinstance(params, dict) else None
        protocol_version = (
            client_version
            if isinstance(client_version, str) and client_version in SUPPORTED_MCP_PROTOCOL_VERSIONS
            else DEFAULT_MCP_PROTOCOL_VERSION
        )
        result = {
            "protocolVersion": protocol_version,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {
                "name": SERVER_NAME,
                "title": "AGY Dispatch",
                "version": SERVER_VERSION,
            },
        }
    elif method == "tools/list":
        result = tool_list()
    elif method == "tools/call":
        if not isinstance(params, dict):
            return {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32602, "message": "params must be an object"}}
        result = call_tool(params)
    else:
        return {
            "jsonrpc": "2.0",
            "id": request_id,
            "error": {"code": -32601, "message": f"Method not found: {method}"},
        }
    return {"jsonrpc": "2.0", "id": request_id, "result": result}


def run_stdio() -> int:
    for line in sys.stdin:
        if not line.strip():
            continue
        try:
            request = json.loads(line)
            if not isinstance(request, dict):
                raise ValueError("request must be a JSON object")
            response = handle_request(request)
        except Exception as exc:  # noqa: BLE001 - server must return JSON-RPC errors.
            response = {
                "jsonrpc": "2.0",
                "id": None,
                "error": {"code": -32700, "message": str(exc)},
            }
        if response is not None:
            print(json.dumps(response, ensure_ascii=False), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(run_stdio())
