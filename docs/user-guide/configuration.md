# Configuration

g8s is intentionally configuration-light: behavior is driven by two environment
variables plus built-in profiles reviewed through the CLI.

## Database

| Setting | Value |
| --- | --- |
| Env var | `G8S_DB` |
| Default | `~/.local/state/g8s/g8s.db` (created with mode 0700) |
| Engine | SQLite via modernc.org/sqlite (pure Go, Zero-CGO), WAL journal |

The parent directory is created automatically on first use.

## Receipt bounds

Receipt TTLs are clamped server-side:

- minimum: **1 second**
- maximum: **3600 seconds** (1 hour)

Issuing with an empty path pattern list is rejected.

## Permission profiles

| Profile | Mutation allowed | Skip-permissions allowed | Available over MCP |
| --- | --- | --- | --- |
| `read_only` | no | no | yes |
| `automation_read` | no | yes | yes |
| `workspace_write` | yes (receipt-gated) | no | no — requires an explicit receipt |

## Role profiles

Six roles ship out of the box: `collector`, `mcp-mapper`, `scout`, `summarizer`,
`test-runner`, `verifier`. Each carries a purpose statement and forbidden-action
list rendered by `g8s roles`.
