# MCP Tools

`g8s mcp` serves an eleven-tool stdio JSON-RPC server (protocol version
negotiated per client, default `2025-06-18`).

## Tool surface

| Tool | Purpose | Guard notes |
| --- | --- | --- |
| `g8s_run` | Execute a control-plane helper run | typed pending-dependency error by design |
| `g8s_blast_radius` | Blast-radius analysis | typed pending-dependency error by design |
| `g8s_submit` | Enqueue a durable task | rejects `workspace_write` (`blocked_by_policy`) and `no_sandbox` (`blocked_by_sandbox_policy`) |
| `g8s_get` | Sanitized task view | request payloads never echo the raw prompt |
| `g8s_receipt_issue` | Issue single-use write receipt | TTL bounded server-side |
| `g8s_self_awareness` | Provider/policy metadata | read-only |
| `g8s_dispatch` | Synchronous guarded dispatch | guard chain: policy → harness → sandbox → add_dirs → binary probe → contract violation |
| `g8s_list_tasks` | Filtered task listing | sanitized views |
| `g8s_cancel_task` | Request cancellation | requires task id + reason |
| `g8s_list_roles` | Role profiles | read-only |
| `g8s_list_permissions` | Permission profiles incl MCP availability | `workspace_write` reported disabled |

## Client configuration

### Claude Desktop / any stdio client

```json
{
  "mcpServers": {
    "g8s": {
      "command": "/usr/local/bin/g8s",
      "args": ["mcp"]
    }
  }
}
```

### Cursor

```json
{
  "mcpServers": {
    "g8s": {
      "command": "g8s",
      "args": ["mcp"]
    }
  }
}
```

## Guard chain for `g8s_dispatch`

Requests are evaluated in order and rejected with structured errors:

1. `workspace_write` → `blocked_by_policy` (receipts cannot cross MCP).
2. `skip_permissions` on a read-only profile → `blocked_by_harness`.
3. `no_sandbox` → `blocked_by_sandbox_policy`.
4. Empty `add_dirs` → `blocked_missing_add_dirs`.
5. Worker binary unresolvable → `setup_required` with a setup hint.
6. Read-only contract violation in worker output → `contract_violation` envelope.
