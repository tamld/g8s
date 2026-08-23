# AGY Dispatch

Local Codex plugin for dispatching bounded AGY worker jobs behind role and permission contracts.

## Purpose

Codex stays the orchestrator for planning, architecture, verification, and wiki promotion. AGY Flash workers handle mechanical collection, scouting, summarization, and verification through this plugin's harness.

## Source Layout

- `.codex-plugin/plugin.json` - Codex plugin manifest.
- `.mcp.json` - MCP server registration for the local dispatcher.
- `scripts/agy_harness.py` - role and permission profiles.
- `scripts/agy_dispatch.py` - single bounded AGY job runner.
- `scripts/agy_collect.py` - scoped collection wrapper for artifact-heavy directories.
- `scripts/agy_control_plane.py` - SQLite WAL task registry, leases, events, and receipts.
- `scripts/agy_control.py` - operator CLI for submit/get/list/cancel/reconcile.
- `scripts/agy_worker.py` - cancellable application-only queue worker.
- `scripts/agy_service.py` - macOS user LaunchAgent lifecycle manager.
- `scripts/agy_mcp_server.py` - dependency-light stdio MCP server for tool discovery and guarded dispatch.
- `schemas/*.schema.json` - task, result, and receipt interchange contracts.
- `scripts/test_agy_dispatch.py` - focused dispatch wrapper tests.
- `scripts/test_agy_mcp_server.py` - focused MCP server tests.
- `scripts/test_agy_control_plane.py` - queue, lease, lineage, receipt, and migration tests.
- `scripts/test_agy_worker.py` - process cancellation, timeout, pause, and cleanup tests.
- `scripts/test_agy_service.py` - LaunchAgent contract, rollback, and lifecycle tests.
- `scripts/test_receipt_delegation.py` - receipt lifecycle: CRUD, security, edge, abuse.
- `scripts/test_safety_coordination.py` - prompt injection, revocation, multi-agent coordination.
- `docs/DESIGN-receipt-delegation.md` - system design: architecture, boundary, security gates.
- `skills/agy-dispatch/SKILL.md` - user-facing skill instructions.

## Current Guardrails

- `read_only` cannot use permission skipping.
- `automation_read` may use `--dangerously-skip-permissions`, but the dispatcher keeps AGY sandboxing enabled unless `--no-sandbox` is explicit.
- Read-only worker output is scanned for mutation side effects and converted into a harness failure when detected.
- Added directories are resolved before denied-path checks, including symlinks and `..` traversal.
- Durable tasks require at least one explicit scope root and reject roots that contain known credential stores.
- Durable tasks use idempotency keys, atomic leases, heartbeat CAS, retry ceilings, cancellation, and hashed receipts.
- Task state, event, and receipt hash are committed atomically; database migrations use an exclusive transaction.
- The worker launches each dispatch in its own process group, drains output through private files, and polls cancellation, lease ownership, and execution deadline.
- The MCP surface requires explicit scope roots and never lets a caller select the AGY executable.
- MCP dispatch blocks `workspace_write` and `no_sandbox` until a separate human-approved plan adds rollback and observability.
- `workspace_write` at the harness level requires a valid write receipt issued by a Brain-tier orchestrator via `ControlPlane.issue_write_receipt()`. Receipts are one-time use, time-limited (max 3600s), and scope-restricted by `allowed_paths` glob patterns. Workers receive the scope restriction in their contract prompt.
- Read-only workers receive a wiki engine policy in their contract prompt: `wiki.py query/search/read/classify` ALLOWED, `wiki.py write/reflect/orient/claim/bypass` FORBIDDEN.
- AGY executable lookup checks explicit path, `AGY_BIN`, `PATH`, and conservative home fallbacks for macOS and Windows.

## MCP Tools

The plugin registers one local stdio MCP server named `agy-dispatch` through `.mcp.json`.

- `agy_list_roles`
- `agy_list_permissions`
- `agy_self_awareness`
- `agy_dispatch_task`
- `agy_submit_task`
- `agy_get_task`
- `agy_list_tasks`
- `agy_cancel_task`

`agy_dispatch_task` remains the backward-compatible synchronous path. The durable path queues through `agy_submit_task`; a separate `agy_worker.py` process performs execution so status and cancellation remain available while AGY runs.

## Durable Control Plane

The default database is `~/.local/state/agy-dispatch/control-plane.sqlite3`. Override it with `AGY_DISPATCH_STATE_DB` or `--db`.

```bash
python3 scripts/agy_control.py submit \
  --idempotency-key inventory-20260711-01 \
  --prompt "Collect a read-only inventory." \
  --model "Gemini 3.5 Flash (Low)" \
  --add-dir /path/to/scope

python3 scripts/agy_worker.py --once
python3 scripts/agy_control.py list
python3 scripts/agy_control.py cancel <task-id> --reason "No longer needed"
```

### macOS Worker Service

Install the durable worker as a user LaunchAgent so MCP submissions run without a manually started terminal process:

```bash
python3 scripts/agy_service.py install
python3 scripts/agy_service.py status
python3 scripts/agy_service.py restart
python3 scripts/agy_service.py uninstall
```

The service uses `gui/<uid>`, canonical executable paths, an explicitly pinned `AGY_BIN`, `KeepAlive`, background process classification, private logs, and the default SQLite state directory. User-writable lookup directories are excluded from its `PATH`, and lifecycle files reject symlink redirection. It runs the worker with `--quiet`, so task results and prompt hashes are not copied into service logs. Install, restart, and uninstall acquire a leased maintenance gate that blocks new worker claims, then fail closed while a task is already leased or running unless the local operator explicitly supplies `--force`. System lifecycle commands have a hard timeout, while the maintenance lease exceeds the bounded rollback window. Uninstall preserves the database, receipts, and logs.

Run `install` again after replacing the local Python or AGY application binary so the pinned canonical paths are refreshed.

Lifecycle:

```text
QUEUED -> LEASED -> RUNNING -> SUCCEEDED | FAILED | CANCELLED
                      |  |
                      |  +-> NEEDS_INFO | BLOCKED
                      +----> QUEUED when a retryable attempt remains
```

Expired leases are requeued until `max_attempts` is exhausted. A paused task is immutable; after clarification, the orchestrator submits a new task with `parent_task_id` instead of rewriting the old spec. Exported task metadata, event logs, and receipts live beneath the database state directory with mode `0600`; the raw prompt is replaced by its hash and temporary prompt/output files are deleted after each attempt. SQLite keeps the prompt only while a task can still run or retry, then replaces it with `prompt_hash`.

### v0.1 Boundaries

- Application-only: workers invoke the installed AGY CLI; no vendor API path exists.
- Custom AGY executable paths are rejected; the harness resolves the installed application binary.
- `workspace_write` and `--no-sandbox` are blocked for durable tasks.
- Receipts are hashed but intentionally marked `signed: false`.
- On macOS, the optional user LaunchAgent is installed explicitly through `agy_service.py`; other platforms still start `agy_worker.py` explicitly.
- Worker `SIGINT`/`SIGTERM` handling terminates the active local process group before exit.
- The current AGY application does not expose per-tool-call argument interception, so OS sandboxing plus post-run contract detection remain part of the boundary.

## Verification

```bash
python3 -m py_compile scripts/agy_harness.py scripts/agy_dispatch.py scripts/agy_collect.py scripts/agy_control_plane.py scripts/agy_control.py scripts/agy_worker.py scripts/agy_mcp_server.py scripts/test_*.py
PYTHONPATH=scripts python3 -m unittest discover -s scripts -p 'test_*.py' -v
```

Windows support is covered by resolver unit tests on macOS. A live Windows smoke test is still required after AGY is installed on the target workstation.
