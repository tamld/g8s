#!/usr/bin/env python3
"""Role and permission harness for AGY worker dispatch."""

from __future__ import annotations

import re
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class RoleProfile:
    name: str
    purpose: str
    output_focus: str
    forbidden: tuple[str, ...]


@dataclass(frozen=True)
class PermissionProfile:
    name: str
    description: str
    mutation_allowed: bool
    skip_permissions_allowed: bool
    max_prompt_chars: int


ROLES: dict[str, RoleProfile] = {
    "collector": RoleProfile(
        name="collector",
        purpose="Collect a bounded inventory of paths, headings, metadata, and reusable procedures.",
        output_focus="evidence paths, compact findings, skipped-sensitive list, uncertainty",
        forbidden=("editing files", "running installs", "copying raw confidential payloads"),
    ),
    "scout": RoleProfile(
        name="scout",
        purpose="Find candidate modules, skills, MCP servers, configs, harnesses, loops, and project artifacts.",
        output_focus="candidate list grouped by value and adoption risk",
        forbidden=("changing state", "promoting claims as proven", "reading credential material"),
    ),
    "mcp-mapper": RoleProfile(
        name="mcp-mapper",
        purpose="Map MCP server tools, provider registries, permissions, and runtime boundaries.",
        output_focus="tool surface, provider model, permission gates, adoption/avoid recommendations",
        forbidden=("launching servers", "using real credentials", "calling external systems"),
    ),
    "summarizer": RoleProfile(
        name="summarizer",
        purpose="Summarize existing artifacts without adding new claims beyond the inspected evidence.",
        output_focus="short synthesis, file evidence, open questions",
        forbidden=("inventing missing context", "copying long proprietary text", "making final decisions"),
    ),
    "verifier": RoleProfile(
        name="verifier",
        purpose="Check whether a bounded claim is supported by files, command output, or structured artifacts.",
        output_focus="claim status, supporting paths, contradicting evidence, residual uncertainty",
        forbidden=("fixing the issue", "rewriting evidence", "treating absence as proof"),
    ),
    "test-runner": RoleProfile(
        name="test-runner",
        purpose="Run explicitly provided safe verification commands and summarize results.",
        output_focus="commands run, exit codes, key failures, next diagnostic step",
        forbidden=("destructive commands", "dependency installs unless explicitly permitted", "unbounded retries"),
    ),
}


PERMISSIONS: dict[str, PermissionProfile] = {
    "read_only": PermissionProfile(
        name="read_only",
        description="Default profile. Read/list/summarize only. Uses AGY sandbox by default.",
        mutation_allowed=False,
        skip_permissions_allowed=False,
        max_prompt_chars=30_000,
    ),
    "automation_read": PermissionProfile(
        name="automation_read",
        description="Read-only automation profile. Allows AGY permission skipping only behind this harness.",
        mutation_allowed=False,
        skip_permissions_allowed=True,
        max_prompt_chars=30_000,
    ),
    "workspace_write": PermissionProfile(
        name="workspace_write",
        description="Workspace mutation profile for future MCP use. Requires explicit caller selection.",
        mutation_allowed=True,
        skip_permissions_allowed=True,
        max_prompt_chars=20_000,
    ),
}


BLOCKED_TASK_PATTERNS = tuple(
    re.compile(pattern, re.IGNORECASE)
    for pattern in (
        r"\brm\s+-rf\b",
        r"\bdel\s+/[sS]\b",
        r"\brmdir\s+/[sS]\b",
        r"\bfdisk\b",
        r"\bmkfs\b",
        r"\bdd\s+if=",
        r"\bdrop\s+database\b",
        r"\bdrop\s+table\b",
        r"\btruncate\s+table\b",
        r"\bshutdown\b",
        r"\breboot\b",
        r"\bhalt\b",
        r"\binit\s+0\b",
        r"\bcopy\s+private\s+key\b",
        r"\bexfiltrate\s+private\s+key\b",
        r"\bcat\s+\.env\b",
        r"\bopen\s+\.env\b",
        r"\bcopy\s+token\s+store\b",
    )
)


DENIED_PATH_FRAGMENTS = (
    "/.env",
    "/.ssh",
    "/.gnupg",
    "/.aws",
    "/.config/gh",
    "/.npmrc",
    "/.pypirc",
    "master.key",
    "id_rsa",
    "id_ed25519",
)


def _contains_sensitive_root(root: Path) -> bool:
    home = Path.home().resolve(strict=False)
    sensitive_paths = (
        home / ".ssh",
        home / ".gnupg",
        home / ".aws",
        home / ".config" / "gh",
        home / ".npmrc",
        home / ".pypirc",
    )
    return any(path == root or path.is_relative_to(root) for path in sensitive_paths)


def role_names() -> list[str]:
    return sorted(ROLES)


def permission_names() -> list[str]:
    return sorted(PERMISSIONS)


def get_role(name: str) -> RoleProfile:
    try:
        return ROLES[name]
    except KeyError as exc:
        raise ValueError(f"unknown role '{name}'. Available: {', '.join(role_names())}") from exc


def get_permission(name: str) -> PermissionProfile:
    try:
        return PERMISSIONS[name]
    except KeyError as exc:
        raise ValueError(
            f"unknown permission '{name}'. Available: {', '.join(permission_names())}"
        ) from exc


def validate_dispatch(
    *,
    prompt: str,
    role_name: str,
    permission_name: str,
    add_dirs: list[str],
    skip_permissions: bool,
    receipt_id: str | None = None,
) -> dict:
    role = get_role(role_name)
    permission = get_permission(permission_name)

    if len(prompt) > permission.max_prompt_chars:
        raise ValueError(
            f"prompt too long for permission={permission.name}: "
            f"{len(prompt)} > {permission.max_prompt_chars}"
        )

    if skip_permissions and not permission.skip_permissions_allowed:
        raise ValueError(
            f"--skip-permissions is not allowed for permission={permission.name}"
        )

    # ── Receipt gate: workspace_write requires Brain-issued receipt ────────
    receipt_info: dict | None = None
    if permission.mutation_allowed:
        if not receipt_id:
            raise ValueError(
                f"permission={permission.name} requires --receipt-id. "
                f"Brain must issue a write receipt first via ControlPlane.issue_write_receipt()"
            )
        from agy_control_plane import ControlPlane
        cp = ControlPlane()
        try:
            receipt_info = cp.validate_write_receipt(receipt_id)
        except ValueError as exc:
            raise ValueError(f"write receipt rejected: {exc}") from exc

    blocked = [pattern.pattern for pattern in BLOCKED_TASK_PATTERNS if pattern.search(prompt)]
    if blocked:
        raise ValueError(f"blocked task pattern(s): {', '.join(blocked)}")

    denied_dirs: list[str] = []
    resolved_dirs: list[str] = []
    for raw_dir in add_dirs:
        resolved = Path(raw_dir).expanduser().resolve(strict=False)
        normalized = "/" + str(resolved).replace("\\", "/").strip("/")
        if _contains_sensitive_root(resolved):
            denied_dirs.append(raw_dir)
            resolved_dirs.append(str(resolved))
            continue
        for fragment in DENIED_PATH_FRAGMENTS:
            if fragment.lower() in normalized.lower():
                denied_dirs.append(raw_dir)
                break
        resolved_dirs.append(str(resolved))
    if denied_dirs:
        raise ValueError(f"denied add-dir path(s): {', '.join(denied_dirs)}")

    result = {
        "role": role.name,
        "permission": permission.name,
        "mutation_allowed": permission.mutation_allowed,
        "skip_permissions": skip_permissions,
        "role_purpose": role.purpose,
        "allowed_roots": resolved_dirs,
    }
    if receipt_info:
        result["receipt"] = receipt_info
    return result


def build_contract_prompt(
    prompt: str,
    role_name: str,
    permission_name: str,
    receipt_info: dict | None = None,
) -> str:
    role = get_role(role_name)
    permission = get_permission(permission_name)
    forbidden = "\n".join(f"- {item}" for item in role.forbidden)

    if permission.mutation_allowed and receipt_info:
        paths_list = "\n".join(f"  - {p}" for p in receipt_info.get("allowed_paths", []))
        mutation_line = (
            "This task has DELEGATED WRITE permission via receipt.\n"
            "You may ONLY write to files matching these path patterns:\n"
            f"{paths_list}\n"
            "Writing to ANY path outside this scope is a policy violation.\n"
            f"Receipt ID: {receipt_info.get('receipt_id', 'unknown')}\n"
            f"Issuer: {receipt_info.get('issuer', 'unknown')}"
        )
    elif permission.mutation_allowed:
        mutation_line = "This task may mutate files only inside the explicit scope."
    else:
        mutation_line = "This task is read-only: do not edit, delete, move, install, commit, or write files."

    # Wiki engine restrictions for workers
    wiki_line = ""
    if not permission.mutation_allowed:
        wiki_line = (
            "\n\nWiki engine policy (MANDATORY):\n"
            "- ALLOWED: wiki.py query, wiki.py search, wiki.py read, wiki.py classify\n"
            "- FORBIDDEN: wiki.py write, wiki.py reflect, wiki.py orient, wiki.py claim, wiki.py bypass\n"
            "  These commands mutate shared session state and are reserved for the Brain orchestrator."
        )

    return f"""You are an AGY worker running behind the Codex dispatch harness.

Role: {role.name}
Purpose: {role.purpose}
Permission profile: {permission.name} — {permission.description}
Mutation policy: {mutation_line}
{wiki_line}
Forbidden for this role:
{forbidden}

Output contract:
- Return compact JSON or Markdown with exact paths inspected.
- If required information is missing, return JSON with status NEEDS_INFO and required_inputs.
- If policy or environment prevents safe work, return JSON with status BLOCKED and a reason.
- Separate findings from uncertainty.
- Put skipped sensitive paths under sensitive_flags.
- Do not copy secrets, credentials, private keys, identity documents, or raw confidential payloads.
- Do not claim completion beyond the evidence you inspected.

Original task:
{prompt}
"""
