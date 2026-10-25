# MCP Tools

`g8s mcp` serves a twenty-two-tool stdio JSON-RPC server (protocol version
negotiated per client, default `2025-06-18`).

## Tool surface

| Tool | Purpose | Guard notes |
| --- | --- | --- |
| `g8s_run` | Execute a control-plane helper run | typed pending-dependency error by design |
| `g8s_blast_radius` | Blast-radius analysis | typed pending-dependency error by design |
| `g8s_submit` | Enqueue a durable task | supports `model`, `provider`, `effort`, `scope_root`, `role`, `permission`, `priority`, `max_attempts`; rejects `workspace_write` (`blocked_by_policy`) and `no_sandbox` (`blocked_by_sandbox_policy`) |
| `g8s_get` | Sanitized task view | request payloads never echo the raw prompt |
| `g8s_receipt_issue` | Issue single-use write receipt | TTL bounded server-side |
| `g8s_self_awareness` | Provider/policy metadata | read-only |
| `g8s_dispatch` | Synchronous guarded dispatch | guard chain: policy → harness → sandbox → add_dirs → binary probe → contract violation |
| `g8s_list_tasks` | Filtered task listing | sanitized views |
| `g8s_cancel_task` | Request cancellation | requires task id + reason |
| `g8s_list_roles` | Role profiles | read-only (10 canonical roles) |
| `g8s_list_permissions` | Permission profiles incl MCP availability | `workspace_write` reported disabled |
| `g8s_search` | Native repository search | pure-Go regex/literal/files-only search respecting gitignore and receipt boundaries (DELTA-22/23) |
| `g8s_read` | Bounded file reading | line-range slicing, SHA-256 fingerprint, binary guard, and receipt-scope validation (DELTA-23) |
| `g8s_edit` | Atomic CAS file editing | exact-match replacement with precondition SHA-256 and mandatory write-receipt validation (DELTA-23) |
| `g8s_select` | Native JSON dot-path selector | pure-Go field extraction and array indexing replacing `jq` (DELTA-23) |
| `g8s_llm_call` | Direct synchronous LLM invocation | calls any configured `api_call` HTTP provider gateway (`openai`, `anthropic`, `9router`) (DELTA-25) |
| `g8s_charter_issue` | Issue epoch-fenced Stratum Charter | mints an `S0/S1 -> S2` domain charter with canonical SHA-256 digest and fencing token (DELTA-33) |
| `g8s_charter_verify` | Verify Stratum Charter | validates active charter state, epoch, and fencing token (DELTA-33) |
| `g8s_portfolio_status` | O-Plane portfolio projection | read-only projection of active, completed, and revoked charters across repositories (DELTA-33) |
| `g8s_skills_list` | Digest-pinned skill listing | lists `skill://g8s/<tier>/<name>` manifests filtered by F0/F1/F2 containment tier and optional role (DELTA-34) |
| `g8s_skills_read` | Digest-verified skill hydration | reads `SKILL.md` with optional `expected_digest` SHA-256 TOCTOU check and F0/F1/F2 tier guard (DELTA-34) |
| `g8s_context_pack` | Pure-Go AST & Knapsack context compressor | 4-tier AST/lexical elision (`FULL`/`SKEL`/`SIGS`/`TREE`) + Personalized PageRank + BM25F + Knapsack budget optimizer (DELTA-36) |

## Tool details & extensions

### `g8s_submit`

Queues a background task in the SQLite-backed control plane.

```json
{
  "name": "g8s_submit",
  "arguments": {
    "idempotency_key": "task-uuid",
    "payload": {"prompt": "Run security audit"},
    "role": "verifier",
    "permission": "read_only",
    "provider": "9router",
    "model": "fast-agent",
    "effort": "low",
    "scope_root": "/path/to/project",
    "add_dirs": ["/path/to/project"]
  }
}
```

- `provider`: optional target provider name from `providers.json` (e.g. `9router`, `openai`).
- `effort`: optional reasoning effort level (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`).
- `scope_root`: optional base directory extending the worker confinement jail.

### `g8s_select`

Native pure-Go selector querying JSON payloads via dot notation (`.data.receipt_id` or `items.0.name`) or array indexing (`items[0].name`).

```json
{
  "name": "g8s_select",
  "arguments": {
    "input": "{\"data\":{\"receipt_id\":\"rc-42\"}}",
    "selector": ".data.receipt_id"
  }
}
```

Returns:
```json
{
  "selector": ".data.receipt_id",
  "value": "rc-42"
}
```

### `g8s_llm_call`

Synchronous direct LLM invocation against any configured `api_call` provider from `providers.json`.

```json
{
  "name": "g8s_llm_call",
  "arguments": {
    "provider": "9router",
    "model": "fast-agent",
    "prompt": "Explain Go concurrency primitives",
    "system": "You are a concise Go tutor",
    "max_tokens": 1024,
    "temperature": 0.2,
    "timeout": "45s"
  }
}
```

Returns structured metrics: `provider`, `protocol`, `requested_model`, `resolved_model`, `output`, `prompt_tokens`, `completion_tokens`, `total_tokens`, and `duration_ms`.

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
