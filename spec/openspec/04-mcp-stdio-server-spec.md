# OpenSpec DELTA-04: Stdio JSON-RPC 2.0 Model Context Protocol (MCP) Server

**Status**: `PROPOSED` (Amendment A applied)  
**Milestone**: M2 (Capabilities)  
**Package**: `internal/mcp`  
**Amendment**: A — T015 MCP surface expansion (2026-08-24)

---

## 1. Goal & Context
Expose `g8s` capabilities as a standard Model Context Protocol (MCP) server over Unix Stdio (JSON-RPC 2.0). This allows Claude Desktop, Cursor, Codex, and Windsurf to seamlessly dispatch worker tasks, query resource pools, and issue write receipts.

---

## 2. Supported MCP Tools (Amendment A: six -> eleven)

1. **`g8s_run`**: Synchronously execute an isolated task with progress notifications.
2. **`g8s_submit`**: Asynchronously queue a durable background task.
3. **`g8s_get`**: Fetch task status and sanitized execution output.
4. **`g8s_receipt_issue`**: Issue a path-scoped, time-limited single-use Write Receipt.
5. **`g8s_self_awareness`**: Query active providers, model availability, and concurrency slots.
6. **`g8s_blast_radius`**: Query LSP call hierarchy and AST impact score for a target symbol.
7. **`g8s_dispatch`** *(A)*: Synchronously execute one bounded read-only dispatch through the `internal/dispatch` wrapper.
8. **`g8s_list_tasks`** *(A)*: List durable tasks filtered by state (`TaskFilter{State,Limit}`).
9. **`g8s_cancel_task`** *(A)*: Request cooperative cancellation of a durable task with an audit reason.
10. **`g8s_list_roles`** *(A)*: Enumerate registered role profiles from `internal/harness`.
11. **`g8s_list_permissions`** *(A)*: Enumerate permission profiles plus their MCP enablement metadata.

Naming deviation from the Python baseline (`agy_*`) is intentional per judge decision T005-D1: this repository's own spec names win; concepts map 1:1.

---

## 3. Go Interface Definition

```go
package mcp

import "context"

type MCPServer interface {
    ServeStdio(ctx context.Context) error
    RegisterTools() error
}
```

The `ControlPlaneAPI` seam grows to the full DELTA-03 public contract subset used by tools:
`SubmitTask`, `GetTask`, `ListTasks`, `CancelTask`. The concrete `*controlplane.Store`
already satisfies it.

---

## 4. Amendment A Normative Rules

### 4.1 Protocol version negotiation
`initialize` echoes back the client-supplied `params.protocolVersion` verbatim when
non-empty; otherwise it answers with the default `"2025-06-18"`. `serverInfo.name` is
`"g8s"` (deviation from baseline `agy_dispatch_mcp`, same T005-D1 rationale).

### 4.2 Sanitized request views
Any tool response that embeds a durable task's request payload (`g8s_get`,
`g8s_list_tasks`) MUST omit the `prompt` member of the stored request object.
Non-object payloads render as JSON `null`.

### 4.3 Permission enablement metadata
`g8s_list_permissions` returns each profile with `mcp_enabled` and, when disabled,
`disabled_reason`. `workspace_write` is disabled on the MCP surface because write
receipts cannot be carried through MCP tool arguments: its `disabled_reason` must
contain the literal string `workspace_write`.

### 4.4 Guard chain (evaluated in order; failing guard short-circuits)
Applies to `g8s_dispatch` (all guards) and `g8s_submit` (guards 1 and 2). A blocked
call is an `isError` JSON-RPC error whose `data.status` is machine-readable and whose
executor/dispatcher is never invoked:

| # | Condition | `data.status` | Message requirement |
|---|---|---|---|
| 1 | `permission == "workspace_write"` | `blocked_by_policy` | mentions receipt requirement |
| 2 | profile forbids skip AND `skip_permissions == true` (e.g. `read_only`) | `blocked_by_harness` | message contains `skip-permissions` |
| 3 | `no_sandbox == true` | `blocked_by_sandbox_policy` | message contains `no_sandbox` |
| 4 | `g8s_dispatch` only: `add_dirs` empty | `blocked_missing_add_dirs` | message contains `explicit add_dirs` |
| 5 | worker binary unresolvable | `setup_required` | carries `setup_hint` |

### 4.5 Contract-violation surfacing
When a dispatched read-only run exits 0 but the detector finds side effects,
`internal/dispatch.Run` reports `harness_returncode = 3` (`ReadOnlyContractExit`)
with a populated `contract_violation` report. The tool surfaces this as `isError`
with `data.status = "contract_violation"` carrying `harness_returncode` and the full
violation list (`type`, first-hit evidence) so Brain-tier clients can audit the breach.

### 4.6 Successful dispatch envelope
On success `g8s_dispatch` returns `{ok, returncode, harness_returncode,
duration_seconds, command_preview, permission, stdout, stderr}` where `stdout`/`stderr`
are credential-sanitized by `dispatch.SanitizeOutput` and `command_preview` never
contains prompt text.
