# CLI Reference

The `g8s` binary is the single entry point for task submission, inspection, receipts,
and MCP serving. All state lives in a single SQLite database (Zero-CGO, WAL mode).

## Environment

| Variable | Purpose | Default |
| --- | --- | --- |
| `G8S_DB` | Control-plane database path | `~/.local/state/g8s/g8s.db` |
| `AGY_BIN` | Explicit worker binary path (overrides PATH lookup) | resolved from PATH |

## Subcommands

### `version`

Prints the build banner.

```sh
g8s version
# g8s v0.1.0 (The Gatekeepers - Zero-CGO, Pure Go)
```

### `roles`

Lists all six role profiles with purpose and forbidden actions.

```sh
g8s roles
```

Roles: `collector`, `mcp-mapper`, `scout`, `summarizer`, `test-runner`, `verifier`.

### `permissions`

Lists permission profiles including whether each is enabled on the MCP surface.

```sh
g8s permissions
```

Profiles: `read_only`, `automation_read`, `workspace_write` (`workspace_write`
is never enabled over MCP — it requires a delegated receipt).

### `submit`

Enqueues a task for workers to claim.

```sh
g8s submit \
  --idempotency-key inventory-1 \
  --payload '{"prompt": "inventory the module", "timeout": "30s"}' \
  --model gemini-3.7-flash-high \
  --role collector \
  --permission read_only \
  --add-dir . \
  --timeout 30s
```

| Flag | Meaning |
| --- | --- |
| `--idempotency-key` | Unique key; resubmitting returns the same task (`deduplicated: true`) |
| `--payload` | JSON payload decoded by the worker (must include worker-facing fields such as `prompt`) |
| `--model` | Worker model identifier (required) |
| `--role` | Role profile name (default `collector`) |
| `--permission` | Permission profile name (default `read_only`) |
| `--add-dir` | Explicit filesystem scope root (repeatable, required) |
| `--timeout` | Execution window (required, e.g. `30s`) |

Exit codes: `0` submitted or deduplicated; non-zero on validation failure
(missing model/add_dirs, bad role or permission, oversized key).

### `get <task-id>`

Returns a sanitized task view (the raw prompt payload is never echoed back).

```sh
g8s get a1b2c3d4-0000-0000-0000-000000000000
```

States you will observe: `QUEUED`, `LEASED`, `RUNNING`, `NEEDS_INFO`, `BLOCKED`,
`SUCCEEDED`, `FAILED`, `CANCELLED`.

### `receipt-issue`

Issues a single-use delegated write receipt.

```sh
g8s receipt-issue -issuer brain -path './src/*' -ttl 300
```

| Flag | Meaning |
| --- | --- |
| `-issuer` | Identity of the issuing orchestrator (required) |
| `-path` | Allowed path pattern (repeatable) |
| `-ttl` | Seconds until expiry, bounded 1..3600 |

Output is a JSON envelope containing `receipt_id`, `allowed_paths`,
`expires_at`. Pass these fields to the worker so it can consume the receipt
exactly once during its run.

### `mcp`

Serves the eleven-tool stdio MCP server on stdin/stdout. See the
[MCP tools guide](mcp-tools.md).
