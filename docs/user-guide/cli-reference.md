# CLI Reference

The `g8s` binary is the self-describing, single entry point for task submission, control-plane inspection, task lineage queries, write receipt delegation, supervisor orchestration, and Stdio MCP serving. All state lives in a Zero-CGO SQLite database (`modernc.org/sqlite`) running in WAL mode.

---

## Environment Variables

| Variable | Purpose | Default |
| :--- | :--- | :--- |
| `G8S_DB` | Path to shared SQLite control-plane & receipt database | `~/.local/state/g8s/g8s.db` |
| `AGY_BIN` | Explicit worker binary path (overrides PATH lookup) | Resolved from `PATH` |
| `G8S_PROVIDERS` | Path to custom provider configuration JSON file | `~/.config/g8s/providers.json` |

---

## Subcommands — Task Submission & Control Plane

### 1. `g8s submit`
Queues an asynchronous durable task into the SQLite WAL control plane after validating it against the security harness.

```sh
g8s submit \
  --idempotency-key "task-refactor-001" \
  --prompt "Scan internal/harness for security bypasses" \
  --role "collector" \
  --permission "read_only" \
  --add-dir "." \
  --model "gemini-3.8-flash-high" \
  --priority 10 \
  --max-attempts 3
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--idempotency-key` | `string` | *(Required)* | Unique idempotency key. Resubmissions deduplicate atomically. |
| `--prompt` | `string` | *(Required)* | Task prompt handed to the worker LLM. |
| `--role` | `string` | `"collector"` | Worker role contract (`collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`). |
| `--permission` | `string` | `"read_only"` | Permission profile (`read_only`, `automation_read`, `workspace_write`). |
| `--add-dir` | `string` | `[cwd]` | Allowed filesystem directory (repeatable). Validated against forbidden paths. |
| `--receipt-id` | `string` | `""` | Write Receipt ID (mandatory when `--permission workspace_write`). |
| `--parent-task-id`| `string` | `""` | Parent task ID for subtask lineage tracking and tree queries. |
| `--skip-permissions`| `bool` | `false` | Bypass permission checks (allowed only if permission profile permits). |
| `--model` | `string` | `"gemini-3.8-flash-high"` | Target worker model identifier. |
| `--priority` | `int` | `0` | Queue priority (`-100` to `100`). Higher priority tasks are claimed first. |
| `--max-attempts` | `int` | `1` | Retry budget (`1` to `10`). |

---

### 2. `g8s get <task-id>`
Prints the current durable JSON representation of a task from the control plane.

```sh
g8s get 3d6f4520-21a4-4f4a-9cbb-9d7fb2389d31
```

---

### 3. `g8s tasks`
Lists durable tasks in the control-plane queue with optional state filtering and pagination limits.

```sh
# List all tasks
g8s tasks

# Filter by state with custom limit
g8s tasks --state QUEUED --limit 20
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--state` | `string` | `""` | Filter by state: `QUEUED`, `LEASED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `CANCELLED`, `NEEDS_INFO`, `BLOCKED`. |
| `--limit` | `int` | `50` | Maximum number of tasks to return (`1..200`). |

---

### 4. `g8s cancel <task-id>`
Cancels an active, leased, or queued task in the SQLite control plane.

```sh
# Cancel by positional ID
g8s cancel 3d6f4520-21a4-4f4a-9cbb-9d7fb2389d31

# Cancel with explicit reason
g8s cancel --task-id 3d6f4520-21a4-4f4a-9cbb-9d7fb2389d31 --reason "User requested abort"
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task-id` | `string` | `""` | Task ID to cancel (can be provided as positional argument). |
| `--reason` | `string` | `"cancelled via CLI"` | Reason recorded in the task event audit log. |

---

### 5. `g8s resume <task-id>`
Resumes a task halted in `NEEDS_INFO` or `BLOCKED` state, optionally providing clarifying answers or refined instructions.

```sh
# Resume task
g8s resume 3d6f4520-21a4-4f4a-9cbb-9d7fb2389d31

# Resume with updated prompt and reason
g8s resume 3d6f4520-21a4-4f4a-9cbb-9d7fb2389d31 \
  --prompt "Proceed with approach B using standard flag.FlagSet" \
  --reason "Clarification provided by operator"
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task-id` | `string` | `""` | Task ID to resume (can be provided as positional argument). |
| `--prompt` | `string` | `""` | Updated prompt or clarifying instructions. |
| `--reason` | `string` | `"resumed via CLI"` | Reason recorded in the task event audit log. |

---

### 6. `g8s lineage <task-id>`
Prints the full ancestry chain of a task up to the root parent, ordered chronologically (`Root -> Child -> Grandchild`).

```sh
g8s lineage grandchild-task-id-123
```

---

### 7. `g8s children <parent-task-id>`
Lists all direct child subtasks submitted under a specified parent task ID.

```sh
g8s children root-task-id-123
```

---

### 8. `g8s receipt` — Write Receipt Management

#### `g8s receipt issue`
Issues a cryptographic, single-use, TTL-bounded, path-scoped Write Receipt on behalf of the Brain orchestrator.

```sh
g8s receipt issue \
  --issuer "brain-orchestrator" \
  --path "./internal/receipt/*" \
  --allow "./spec/openspec/*" \
  --ttl 600
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--issuer` | `string` | `"operator"` | Identity of the issuing agent/orchestrator recorded on the receipt. |
| `--path`, `--allow` | `string` | *(Required)* | Allowed file path glob pattern (repeatable). |
| `--ttl` | `int` | `600` | Time-to-live in seconds (`1` to `3600`). |

#### `g8s receipt show|get <receipt-id>`
Prints the receipt envelope with all metadata including supervisor columns (`approach_idx`, `attempt_idx`, `rca_confidence`, `adr_path`) and provenance context.

#### `g8s receipt verify <receipt-id>`
Validates a receipt against the control plane (expiry, revocation, single-use consumption).

#### `g8s receipt revoke <receipt-id>`
Revokes an unconsumed write receipt immediately.

#### `g8s receipt list`
Lists all active, consumed, or expired receipts with status filtering.

---

## Subcommands — Orchestration & Supervision

### 9. `g8s orchestrate`
Runs the supervisor self-test loop against the worker engine with automated Root Cause Analysis (RCA) and bounded fix cycles.

```sh
g8s orchestrate "Run security benchmark suite" \
  --max-attempts 5 \
  --model "gemini-3.8-flash-high"
```

---

### 10. `g8s orchestrate-aic` (DELTA-18)
Automated PR review and remediation orchestrator. Analyzes GitHub PR diffs, distills noise, generates reviewer contracts, and drives verifier subtasks.

```sh
g8s orchestrate-aic --pr 42 --intent "Verify zero-alloc buffer pool changes"
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--pr` | `int` | *(Required)* | GitHub PR number (positive integer). |
| `--intent` | `string` | *(Required)* | Review intent or guidance text. |
| `--model` | `string` | *(auto)* | Target worker model. |
| `--add-dir` | `string` | `[cwd]` | Additional allowed directory (repeatable). |

---

### 11. `g8s worker`
Runs the local background supervisor loop, claiming tasks from the SQLite queue, executing them with configured provider templates, and reporting status.

```sh
# Claim and execute a single task, then exit (ideal for CI)
g8s worker --once

# Run continuous worker for a specific model with 120s lease
g8s worker --once=false --model "gemini-3.8-flash-high" --lease 120
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--once` | `bool` | `true` | Claim and execute a single task, then exit. |
| `--model` | `string` | `""` | Restrict claiming to tasks targeting this model. |
| `--lease` | `int` | `60` | Lease duration in seconds. |

---

### 12. `g8s supervisor-metrics` & `g8s supervisor-metrics-update-false`
Queries supervisor telemetry persisted in the control plane (`supervisor_metrics` table) and records operator feedback.

```sh
# Aggregate metrics across all runs (8 core metrics)
g8s supervisor-metrics --aggregate --json

# Streaming per-task metrics (one JSON object per task)
g8s supervisor-metrics --json-stream

# Single run metrics
g8s supervisor-metrics --task-id sup-abc123 --json

# Filtered aggregation
g8s supervisor-metrics --aggregate --time-range 24h --worker-name agy --json

# Feedback loop: mark escalation as false positive
g8s supervisor-metrics-update-false --task-id sup-abc123 --false
```

#### `g8s supervisor-metrics` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task-id` | `string` | `""` | Supervisor task ID (single-run mode). |
| `--aggregate` | `bool` | `false` | Aggregate metrics across all runs. |
| `--json-stream` | `bool` | `false` | Emit one JSON object per task (streaming). |
| `--time-range` | `duration` | `0` | Filter by time window (e.g. `1h`, `24h`). |
| `--worker-name` | `string` | `""` | Filter by worker name (`agy`, `codex`, `claude`, etc.). |

#### `g8s supervisor-metrics-update-false` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task-id` | `string` | *(Required)* | Supervisor task ID to update. |
| `--false` | `bool` | `false` | Mark escalation as false positive (task should have succeeded). |

---

### 13. `g8s brief-issue` & `g8s brief-consume`
Decoupled contract-driven brief dispatch system with Definition of Done (DoD) and TTL expiry.

```sh
# Issue a structured task brief
g8s brief-issue \
  --file ./BRIEF.md \
  --title "Security audit" \
  --dod "All tests pass, no scope violations" \
  --ttl 2h

# Consume an active brief
g8s brief-consume --brief-id brief-abc123 --actor "worker-001"
```

---

### 14. `g8s autopilot`
Manages the background cron-based scheduler daemon for autonomous health sweeps, CI remediation, and issue processing.

```sh
# Start autopilot scheduler
g8s autopilot start --cron "*/15 * * * *" --repo .

# Trigger immediate scan cycle
g8s autopilot trigger --type issues

# Inspect status
g8s autopilot status

# Stop daemon
g8s autopilot stop

# Show configuration
g8s autopilot config --show
```

---

## Subcommands — Code Intelligence & Knowledge

### 15. `g8s analyze`
Quantifies code blast radius, symbol references, and downstream dependency impact using native Go AST parsing to recommend precise write scopes.

```sh
# Analyze file blast radius
g8s analyze --file ./internal/receipt/receipt.go

# Analyze impact of a specific symbol
g8s analyze --file ./internal/receipt/receipt.go --symbol "IssueReceipt" --root .
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--file` | `string` | *(Required)* | Target file path to analyze (can be passed as positional argument). |
| `--symbol` | `string` | `""` | Target symbol identifier (function, struct, method). |
| `--root` | `string` | `[cwd]` | Codebase root directory for dependency analysis. |

---

### 16. `g8s vault`
Pure-Go Decoupled Tri-Anchor Knowledge Vault with SQLite FTS5 full-text indexing and BM25 relevance ranking (DELTA-11).

```sh
# Store a distillation record
g8s vault store \
  --id "DELTA-02-A" \
  --delta-id "DELTA-02" \
  --spec-anchor "spec/openspec/02-receipt-delegation-spec.md" \
  --plan-anchor "plans/receipt-sprint.md" \
  --confidence 0.95 \
  --summary "Receipt delegation engine implementation patterns"

# Query vault using BM25 full-text search
g8s vault query --q "write receipts CAS" --limit 10

# List stored vault records
g8s vault list --limit 25

# Get record by ID
g8s vault get DELTA-02-A

# Delete record by ID
g8s vault delete DELTA-02-A
```

---

## Subcommands — Operations & Daemon

### 17. `g8s cleanup`
Inspects and purges ghost worker processes, orphan worktrees, branches, tags, and stale receipts.

```sh
g8s cleanup --dry-run   # Inspect only
g8s cleanup --force     # Execute cleanup
```

---

### 18. `g8s cleanup-worktrees`
Cleans up orphaned git worktrees created during dual-blind or FanOut runs.

```sh
g8s cleanup-worktrees --dry-run
g8s cleanup-worktrees --force
```

---

### 19. `g8s status`
Real-time worker heartbeat, active leases, and process status introspection.

```sh
g8s status --worker --json
```

---

### 20. `g8s state`
Control plane state inspection and event log replay.

```sh
g8s state show <task-id>
g8s state replay <task-id>
```

---

### 21. `g8s migrate`
Database schema migrations and version management.

```sh
g8s migrate status
g8s migrate up
```

---

### 22. `g8s converge`
Dual-blind convergence synthesis for multiple worker runs on the same brief.

```sh
g8s converge --brief-id <brief-id> --threshold 0.7
```

---

### 23. `g8s sleep` / `g8s wake`
Marks operator away to defer non-critical notifications, or ends sleep cycle emitting voice/json summary.

```sh
g8s sleep --until "2h"
g8s wake --format json
```

---

### 24. `g8s serve`
Runs long-lived daemon mode exposing RESTful HTTP API server for remote task submission, receipt validation, and Prometheus metrics.

```sh
# Start HTTP API server on default port
g8s serve --address ":8080"

# Run as background daemon blocking until SIGINT/SIGTERM
g8s serve --address "127.0.0.1:8080" --daemon
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--address` | `string` | `":8080"` | Host and port to listen on. |
| `--daemon` | `bool` | `false` | Run as long-lived daemon (blocks until signal received). |
| `--config` | `string` | `""` | Path to server configuration YAML file. |

---

## Subcommands — Protocols & Security Introspection

### 25. `g8s providers`
Lists detected AI agent CLI providers and their availability status.

```sh
g8s providers --json
```

---

### 26. `g8s mcp`
Serves the standard Stdio JSON-RPC 2.0 Model Context Protocol (MCP) server on `stdin`/`stdout`.

```sh
g8s mcp
```

**11 tools exposed**: `g8s_dispatch`, `g8s_get_task`, `g8s_list_tasks`, `g8s_cancel_task`, `g8s_submit`, `g8s_blast_radius`, `g8s_run`, `g8s_self_awareness`, `g8s_receipt_issue`, `g8s_list_roles`, `g8s_list_permissions`.

---

### 27. `g8s roles` & `g8s permissions`
Inspects built-in security profiles and mutation permissions:

```sh
g8s roles
g8s permissions
```

---

### 28. `g8s version`
Prints binary version banner, Go runtime, and Zero-CGO pure Go status.

```sh
g8s version
```

---

### 29. `g8s doctor`
Runs health checks on binary integrity, SQLite database connectivity, and worker provider availability.

```sh
g8s doctor --json
g8s doctor --fix
```

---

### 30. `g8s init` / `g8s config` / `g8s completion` / `g8s service`
Runtime initialization, persistent key-value configuration, shell completion generation, and OS service management.

```sh
g8s init
g8s config get G8S_DB
g8s completion zsh > ~/.zsh/completions/_g8s
g8s service install --user
```

---

## Standalone 1-Liner Installation

Install or upgrade `g8s` directly via curl:

```sh
curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
```

Custom installation directory:
```sh
G8S_INSTALL_DIR="/usr/local/bin" curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
```

---

## Global Flags (All Commands)

| Flag | Type | Description |
| :--- | :--- | :--- |
| `--json` | `bool` | Output structured JSON envelope (v1). |
| `--jsonl` | `bool` | Output JSON Lines (one envelope per line). |
| `--actor` | `string` | Actor identity for traceability. |
| `--trace-id` | `string` | Distributed trace ID (auto-generated if omitted). |
| `--help` | `bool` | Show command-specific usage. |