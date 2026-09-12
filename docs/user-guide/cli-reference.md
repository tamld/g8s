# CLI Reference

The `g8s` binary is the self-describing, single entry point for task submission, control-plane inspection, task lineage queries, write receipt delegation, supervisor orchestration, and Stdio MCP serving. All state lives in a Zero-CGO SQLite database (`modernc.org/sqlite`) running in WAL mode.

---

## Environment Variables

| Variable | Purpose | Default |
| :--- | :--- | :--- |
| `G8S_DB` | Path to shared SQLite control-plane & receipt database | `~/.local/state/g8s/g8s.db` |
| `AGY_BIN` | Explicit worker binary path (overrides PATH lookup) | Resolved from `PATH` |

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

### 4. `g8s lineage <task-id>`
Prints the full ancestry chain of a task up to the root parent, ordered chronologically (`Root -> Child -> Grandchild`).

```sh
g8s lineage grandchild-task-id-123
```

---

### 5. `g8s children <parent-task-id>`
Lists all direct child subtasks submitted under a specified parent task ID.

```sh
g8s children root-task-id-123
```

---

### 6. `g8s receipt` — Write Receipt Management

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
Revokes an active receipt, preventing further consumption.

---

## Subcommands — Supervisor Orchestration (DELTA-11 + DELTA-18)

### 7. `g8s orchestrate`
Runs the supervisor-driven fix loop (Concern A) or intent-based FanOut orchestration (DELTA-18).

```sh
# Self-test: deterministic escalation at 9 attempts
g8s orchestrate "Refactor auth middleware to use pure-Go context tokens" \
  --self-test \
  --max-attempts 3 \
  --max-approaches 3 \
  --actor "brain-supervisor"

# From free-text intent (comma/newline split into sub-tasks)
g8s orchestrate --from-intent "Scan for security issues, generate tests, update docs" \
  --model gemini-3.8-flash-high \
  --role collector \
  --permission read_only \
  --add-dir ./src \
  --json

# From intent file
g8s orchestrate --from-file ./INTENT.md --json

# Brief-driven orchestration
g8s orchestrate --brief-file ./BRIEF.md --json
```

#### Key Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--self-test` | `bool` | `false` | Run self-contained supervisor loop against real agy worker. |
| `--from-intent` | `string` | `""` | Free-text natural language intent (comma/newline split). |
| `--from-file` | `string` | `""` | Path to file containing natural language intent. |
| `--brief-file` | `string` | `""` | Path to brief markdown file to issue and dispatch. |
| `--brief` | `string` | `""` | Stored brief ID for dual-blind orchestration. |
| `--dispatch` | `string` | `""` | Stored brief ID to re-issue and dispatch. |
| `--blind-converge` | `int` | `0` | Run N dual-blind workers with isolated worktrees. |
| `--task` | `string` | `"scan ./src..."` | Task description handed to the supervisor. |
| `--max-attempts` | `int` | `3` | Attempts per approach (iteration cap). |
| `--max-approaches` | `int` | `3` | Approach budget before HITL escalation. |
| `--timeout` | `duration` | `5m` | Per-attempt execution window. |
| `--provider` | `string` | `"agy"` | Agent provider backend (`agy`, `codex`, `claude`, `ollama`). |
| `--model` | `string` | *(auto)* | Target worker model. |
| `--role` | `string` | `"collector"` | Worker role contract. |
| `--permission` | `string` | `"read_only"` | Permission profile. |
| `--add-dir` | `string` | `[cwd]` | Additional allowed directory (repeatable). |

---

### 8. `g8s orchestrate-aic` (DELTA-18)
Thin AIC integration wrapper for automated GitHub PR reviews. Extracts PR diff via `gh pr diff` and dispatches review intent to `g8s orchestrate --from-intent`.

```sh
# Requires gh CLI authenticated
g8s orchestrate-aic --pr 123 --intent "Review security changes for auth middleware" --json
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--pr` | `int` | *(Required)* | GitHub PR number (positive integer). |
| `--intent` | `string` | *(Required)* | Review intent or guidance text. |
| `--model` | `string` | *(auto)* | Target worker model. |
| `--add-dir` | `string` | `[cwd]` | Additional allowed directory (repeatable). |

---

### 9. `g8s supervisor-metrics` (Concern C)
Queries supervisor metrics persisted in the control plane. Read-only ingestion for meta-optimizer.

```sh
# Aggregate metrics across all runs (8 metrics)
g8s supervisor-metrics --aggregate --json

# Streaming per-task metrics (one JSON object per task)
g8s supervisor-metrics --json-stream

# Single run metrics
g8s supervisor-metrics --task-id sup-abc123 --json

# Filtered aggregation
g8s supervisor-metrics --aggregate --time-range 24h --worker-name agy --json
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task-id` | `string` | `""` | Supervisor task ID (single-run mode). |
| `--aggregate` | `bool` | `false` | Aggregate metrics across all runs. |
| `--json-stream` | `bool` | `false` | Emit one JSON object per task (streaming). |
| `--time-range` | `duration` | `0` | Filter by time window (e.g. `1h`, `24h`). |
| `--worker-name` | `string` | `""` | Filter by worker name (`agy`, `codex`, `claude`, etc.). |

> **Flag collision guard**: `--task-id` cannot be combined with `--aggregate` or `--json-stream` (exits with usage error code 2).

---

### 10. `g8s brief` — Decoupled Brief Dispatch

#### `g8s brief-issue`
Issues a brief (contract-driven task specification) with DoD and optional title.

```sh
g8s brief-issue \
  --file ./BRIEF.md \
  --title "Security audit" \
  --dod "All tests pass, no scope violations" \
  --ttl 2h \
  --issued-by "brain-supervisor"
```

#### `g8s brief-consume`
Atomically consumes a brief, preventing double-execution.

```sh
g8s brief-consume --brief-id <brief-id> --actor "worker-001"
```

---

## Subcommands — Operations & Maintenance

### 11. `g8s cleanup`
Inspects and purges ghost worker processes, orphan worktrees, and stale artifacts.

```sh
g8s cleanup --dry-run   # Inspect only
g8s cleanup --force     # Execute cleanup
```

### 12. `g8s cleanup-worktrees`
Cleans up orphaned git worktrees created during dual-blind or FanOut runs.

```sh
g8s cleanup-worktrees --dry-run
g8s cleanup-worktrees --force
```

### 13. `g8s status`
Real-time worker heartbeat and process status introspection.

```sh
g8s status --worker --json
```

### 14. `g8s state`
Control plane state inspection and maintenance commands.

```sh
g8s state active-count
g8s state reconcile
```

### 15. `g8s migrate`
Database schema migrations and version management.

```sh
g8s migrate status
g8s migrate up
```

### 15. `g8s converge`
Dual-blind convergence synthesis for multiple worker runs on the same brief.

```sh
g8s converge --brief-id <brief-id> --threshold 0.7
```

### 16. `g8s sleep` / `g8s wake`
Pause and resume control plane task processing.

```sh
g8s sleep --reason "maintenance window"
g8s wake
```

### 17. `g8s providers`
Lists registered worker providers and their available models.

```sh
g8s providers --json
```

### 18. `g8s mcp`
Serves the standard Stdio JSON-RPC 2.0 Model Context Protocol (MCP) server on `stdin`/`stdout`.

```sh
g8s mcp
```

**11 tools exposed**: `g8s_dispatch`, `g8s_get_task`, `g8s_list_tasks`, `g8s_cancel_task`, `g8s_submit`, `g8s_blast_radius`, `g8s_run`, `g8s_self_awareness`, `g8s_receipt_issue`, `g8s_list_roles`, `g8s_list_permissions`.

---

## Subcommands — Security & Introspection

### 19. `g8s roles` & `g8s permissions`
Inspect built-in security profiles directly in your terminal:

```sh
g8s roles
g8s permissions
```

### 20. `g8s version`
Prints binary version banner, Go runtime, and Zero-CGO pure Go status.

```sh
g8s version
```

### 21. `g8s doctor`
Runs health checks on the binary, control plane, and worker provider availability.

```sh
g8s doctor --json
```

### 22. `g8s init` / `g8s config` / `g8s completion` / `g8s service`
Runtime initialization, configuration management, shell completion, and service management (macOS LaunchAgent).

```sh
g8s init --force
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