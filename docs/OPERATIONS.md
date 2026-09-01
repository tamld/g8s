# Operations & Runbook Guide for g8s

> **The Operator's Handbook**: Comprehensive operational commands, local development workflows, service daemon management, and troubleshooting runbooks for `g8s`.

---

## 1. Core Command Matrix

| Operation | CLI Command | Description |
| :--- | :--- | :--- |
| **System Sanity Check** | `g8s doctor` / `g8s doctor --json` | Inspect database permissions, worker discovery, and providers. |
| **Task Submission** | `g8s submit --prompt "..." --role scout` | Enqueue a durable async task with security harness checks. |
| **Task Inspection** | `g8s get <task-id>` | Show current durable state, lease owner, and result hash. |
| **Queue Listing** | `g8s tasks --state QUEUED --limit 20` | Query tasks filtered by state with pagination. |
| **Lineage Tree** | `g8s lineage <task-id>` | Trace complete ancestry from root task to target child. |
| **Child Subtasks** | `g8s children <parent-id>` | List direct child subtasks submitted by an orchestrator. |
| **Issue Write Receipt**| `g8s receipt issue --path "src/*" --ttl 600` | Issue cryptographic single-use write delegation receipt. |
| **Blast Radius Analyzer**| `g8s analyze --file <path>` | Compute change risk score and suggested write receipt paths. |
| **Knowledge Vault Query**| `g8s vault query <search-term>` | Perform BM25 ranked full-text search over Tri-Anchor records. |
| **Service Install** | `g8s service install` *(macOS launchd; Linux systemd)* | Register background daemon. |
| **Service Start/Stop** | `g8s service start` / `g8s service stop` | Start or stop the background daemon. |
| **Service Status** | `g8s service status` | Check whether daemon is loaded and database is intact. |
| **MCP Surface** | `g8s mcp` | Launch Stdio JSON-RPC 2.0 MCP server for IDEs. |

---

## 2. Local Development & Testing Workflows

`g8s` enforces **Strict Pure-Go (Zero-CGO)** and **Dual-Pass Testing** across all supported platforms.

### A. Dual-Pass Docker Verification (Recommended)

Run the full dual-pass test suite in an isolated Linux container:

```sh
docker run --rm -v $(pwd):/app -w /app golang:1.25 bash -c "
  gofmt -w . && \
  CGO_ENABLED=0 go test ./... && \
  CGO_ENABLED=1 go test -race ./internal/... && \
  go build -v ./...
"
```

### B. Host Machine Commands

```sh
# Format code
gofmt -w .

# Run standard test suite (Zero-CGO)
CGO_ENABLED=0 go test -v ./...

# Run race detector (requires CGO=1 for Go race instrumentation)
CGO_ENABLED=1 go test -v -race ./internal/...

# Build release binary locally
CGO_ENABLED=0 go build -ldflags="-s -w" -o g8s ./cmd/g8s
```

---

## 3. Service Daemon Management & Troubleshooting

`g8s` runs as a user-level background daemon to process queued tasks and serve local orchestrators.

### macOS (LaunchAgent)

* **Unit Path**: `~/Library/LaunchAgents/com.tamld.g8s.plist`
* **Log Directory**: `~/.local/state/g8s/`
* **Manual Management**:
  ```sh
  # View loaded state
  launchctl print gui/$(id -u)/com.tamld.g8s

  # Force reload
  launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.tamld.g8s.plist
  launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.tamld.g8s.plist

  # View live logs
  tail -f ~/.local/state/g8s/service.stdout.log ~/.local/state/g8s/service.stderr.log
  ```

### Linux (Systemd User Unit)

* **Unit Path**: `~/.config/systemd/user/g8s.service`
* **Log Directory**: `~/.local/state/g8s/`
* **Manual Management**:
  ```sh
  # View status and journal
  systemctl --user status g8s
  journalctl --user -u g8s -f

  # Restart service
  systemctl --user restart g8s
  ```

---

## 4. Maintenance & Recovery Runbooks

### Runbook 1: Reclaiming Stale Task Leases
If a worker crashes or loses network connectivity while executing a task, the task remains in `LEASED` state until its heartbeat lease lapses ($60\text{s}$ default).

To manually inspect and reclaim stale leases:
```sh
# 1. List active leased tasks
g8s tasks --state LEASED

# 2. Check if worker PID is alive
ps aux | grep g8s

# 3. Resume task if stuck in NEEDS_INFO or BLOCKED
g8s resume <task-id> --reason "operator manual recovery"
```

### Runbook 2: Database Compaction (VACUUM)
SQLite in WAL mode periodically writes changes to `.db-wal`. To optimize and compact:
```sh
# Inspect database size
ls -lh ~/.local/state/g8s/g8s.db*

# Force SQLite WAL checkpoint
sqlite3 ~/.local/state/g8s/g8s.db "PRAGMA wal_checkpoint(TRUNCATE); VACUUM;"
```

### Runbook 3: Diagnosing Harness Validation Failures
If `g8s submit` returns `harness validation failed`:
1. Check if the prompt contains blocked shell patterns (`rm -rf`, `drop table`, `git push --force`).
2. Check if `--add-dir` points inside a denied path fragment (`.ssh`, `.aws`, `.env`, `.gnupg`).
3. If `--permission workspace_write` was requested, ensure a valid `--receipt-id` is provided.
