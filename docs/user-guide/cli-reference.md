# CLI Reference

The `g8s` binary is the self-describing, single entry point for task submission, control-plane inspection, task lineage queries, write receipt delegation, supervisor orchestration, and Stdio MCP serving. All state lives in a Zero-CGO SQLite database (`modernc.org/sqlite`) running in WAL mode.

---

## Environment Variables

| Variable | Purpose | Default |
| :--- | :--- | :--- |
| `G8S_DB` | Path to SQLite control-plane database (receipts live in sibling receipts.db) | `~/.local/state/g8s/g8s.db` |
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
  --provider "agy" \
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
| `--permission` | `string` | `"read_only"` | Permission profile (`read_only`, `automation_read`, `workspace_write`, `supplied_text`). |
| `--add-dir` | `string` | `[cwd]` | Allowed filesystem directory (repeatable). Validated against forbidden paths. |
| `--receipt-id` | `string` | `""` | Write Receipt ID (mandatory when `--permission workspace_write`). |
| `--parent-task-id`| `string` | `""` | Parent task ID for subtask lineage tracking and tree queries. |
| `--skip-permissions`| `bool` | `false` | Bypass permission checks (allowed only if permission profile permits). |
| `--allow-worker-commit` | `bool` | `false` | Opt in to the ADR-0041 worker git-commit proxy (`allow_worker_commit` in task payload). Commit-only: `git fetch`/`git push` remain supervisor-owned. Requires `--permission workspace_write` (rejected for `read_only` and other profiles). |
| `--provider` | `string` | `""` | Target provider name (`agy`, `codex`, etc.). Stored durably in task payload for worker claim affinity. Falls back to `default_provider` setting if omitted. Precedence: CLI flag > settings default > absent (legacy). |
| `--model` | `string` | `"gemini-3.8-flash-high"` | Target worker model identifier. |
| `--priority` | `int` | `0` | Queue priority (`-100` to `100`). Higher priority tasks are claimed first. |
| `--max-attempts` | `int` | `1` | Retry budget (`1` to `10`). |
| `--effort` | `string` | `"medium"` | Target worker reasoning effort level (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`). See [effort.md](effort.md). |
| `--output-schema` | `string` | `""` | JSON Schema (Draft 7) string or file path validating worker result structure (#655). Supervised results failing schema validation trigger non-retryable `contract_violation` failures. |
| `--route` | `string` | `"manual"` | Routing mode (`manual` or `auto`). When `auto`, selects provider, model, and role using context-based rules and manifest configuration with optional Jev assistance. See [routing.md](routing.md). |

> See [providers.md](providers.md) for multi-provider setup, [routing.md](routing.md) for task routing, and [effort.md](effort.md) for reasoning effort configuration and the quality ladder.

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

#### Cancelling a running task (two phases)

While a worker holds a **RUNNING** lease, `g8s cancel` does **not** jump straight to terminal `CANCELLED`. It sets `cancel_requested` so the worker can stop cooperatively. The attempt then finishes in **WORKER_COMPLETED** with a cancelled result envelope. A **second** `g8s cancel` after the worker has exited (task no longer `RUNNING`) seals terminal `CANCELLED`. Same task id, same attempt count; request/result content hashes are unchanged across that second call.

| Phase | Task state (queue row) | `cancel_requested` | Result envelope (sanitized) | Notes |
| :--- | :--- | :---: | :--- | :--- |
| 1 — request stop while running | `RUNNING` → `WORKER_COMPLETED` | `true` | `ok`: **false**, `status`: **cancelled** | Worker observes cancel, terminates attempt; `FinishAttempt` records the verdict. |
| 2 — seal after worker exit | `WORKER_COMPLETED` → `CANCELLED` | `true` | (stored result unchanged) | Second cancel on the non-running task applies terminal `CANCELLED` without replaying the provider. |

**Rule of thumb:** cancel is idempotent-ish — the first call **requests** cooperative stop on a live attempt; a later call on the **idle** task **seals** terminal cancellation.

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

### 5a. `g8s deliver <task-id>`
Applies changes from an isolated worktree deliverable produced by a completed task to the current working checkout. Delivery enforces strict write receipt capability delegation and atomic per-file application.

```sh
# Apply deliverable from completed task to current checkout
g8s deliver 3d6f4520-21a4-4f4a-9cbb-9d7fb2389d31

# Validate changes and verify receipt scope without copying
g8s deliver 3d6f4520-21a4-4f4a-9cbb-9d7fb2389d31 --dry-run
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task-id` | `string` | `""` | Task ID to deliver (can be specified as a positional argument). |
| `--dry-run` | `bool` | `false` | Perform validation and list files without copying changes. |

#### Receipt Gate Semantics:
- **Capability Enforcement**: The task's request payload must contain a valid `receipt_id`. Delivery fails with `E_DENIED` if missing or invalid.
- **Path Scope Verification**: All candidate files in the deliverable worktree are validated against the receipt's `allowed_paths` glob list before applying any changes.
- **Zero-Partial Guarantee**: If even one modified path falls outside `allowed_paths`, delivery is completely refused and no files in the workspace are modified.

#### File Application & Skipped Behavior:
- **Atomic Apply**: Each file is written to a temporary file (`.g8s-deliver-*`) in the destination directory and atomically renamed (`os.Rename`) to its final destination. If a mid-loop failure occurs (e.g. read-only directory), all pending temporary files are removed and landed files are reported.
- **Symlink Refusal**: `os.Lstat` checks destination paths in the checkout. If a destination exists and is a symlink, it is skipped with a warning to prevent writing through symlinks.
- **Git Porcelain Status Handling**:
  - **Applied**: `??` (untracked file), `M` (modified), `A` (added).
  - **Skipped with Warning**: `R` (renamed), `D` (deleted), `C` (copied), `T` (typechange), `U` (unmerged conflict). Copy-apply in v1 does not model deletions, renames, copies, typechanges, or unresolved conflicts.

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
g8s orchestrate --from-intent "Run security benchmark suite" \
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

# Run worker with strict provider claim affinity
g8s worker --provider agy

# Run concurrent workers with provider and model claim affinity (#657)
g8s worker --concurrency 4 --provider agy --model "gemini-3.8-flash-high"
```

When `--provider` and/or `--model` are specified, the worker enforces strict claim affinity across serial (`--concurrency 1`, `--once`) and concurrent modes (`--concurrency > 1`, #657):
- Atomic candidate selection filters tasks directly in SQLite (`json_extract(request_json, '$.provider')` and `json_extract(request_json, '$.model')`).
- Out-of-affinity tasks remain untouched in `QUEUED` state with `attempts = 0` and no lease.
- Unfiltered workers (`--provider ""` and `--model ""`) claim all queued tasks in standard priority/FIFO order.
See [providers.md](providers.md) for multi-provider setup.

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--once` | `bool` | `true` | Claim and execute a single task, then exit. |
| `--provider` | `string` | `""` | Restrict claiming to tasks targeting this provider (strict claim affinity in serial and concurrent modes). |
| `--model` | `string` | `""` | Restrict claiming to tasks targeting this model (strict claim affinity in serial and concurrent modes). |
| `--concurrency` | `int` | `1` | Number of concurrent worker goroutines running parallel claim loops. |
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
  --payload-file ./BRIEF.md \
  --title "Security audit" \
  --dod "All tests pass, no scope violations" \
  --ttl 2h

# Consume an active brief
g8s brief-consume --id brief-abc123 --actor "worker-001"
```

---

### 14. `g8s autopilot`
Manages the background cron-based scheduler daemon for autonomous health sweeps, CI remediation, and issue processing.

```sh
# Start autopilot scheduler
g8s autopilot start --config autopilot.yaml

# Trigger immediate scan cycle
g8s autopilot trigger

# Inspect status
g8s autopilot status

# Stop daemon
g8s autopilot stop

# Show configuration
g8s autopilot config --show

# Run one maintenance tick (doctor, retention, hygiene) or reflection-receipt packets
g8s autopilot tick
g8s autopilot tick --reflection-receipt path/to/receipt.json --dry-run

# Submit tasks from scan sources to the queue
g8s autopilot submit --config autopilot.yaml --max-submissions 10
g8s autopilot submit --dry-run --max-submissions 5
```

#### Subcommands:
| Subcommand | Description |
| :--- | :--- |
| `start` | Start the in-process autopilot scheduler |
| `stop` | Stop the autopilot scheduler |
| `status` | Show autopilot scheduler status |
| `trigger` | Manually trigger a scan cycle |
| `config` | Show or update autopilot configuration |
| `tick` | Run one tick of background jobs (doctor, retention, hygiene) |
| `submit` | Submit tasks from scan sources to the queue |
| `install-schedule` | Install OS-native scheduler for periodic ticks |
| `uninstall-schedule` | Uninstall OS-native scheduler |

#### Flags (`g8s autopilot tick`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--jobs` | `string` | `doctor,retention,hygiene` | Comma-separated list of maintenance jobs to run (ignored when `--reflection-receipt` is set). |
| `--reflection-receipt` | `string` | `""` | Path to an S2 reflection receipt; submit bounded add-list packets instead of maintenance jobs. |
| `--dry-run` | `bool` | `false` | Print reflection packet specs without opening the queue. |
| `--provider` | `string` | `""` | Target reflection worker provider (queue default when empty). |
| `--model` | `string` | `gemini-3.8-flash-high` | Target reflection worker model. |
| `--effort` | `string` | `""` | Reflection worker effort (queue default when empty). |
| `--json` / `--jsonl` | `bool` | `false` | Emit machine-readable envelope. |

#### Flags (`g8s autopilot submit`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--config` | `string` | `""` | Path to autopilot config YAML file |
| `--max-submissions` | `int` | `0` | Maximum number of tasks to submit (0 = unlimited) |
| `--dry-run` | `bool` | `false` | Show tasks that would be submitted without submitting |
| `--provider` | `string` | `"9router"` | Target worker provider for submitted read-only scout tasks |
| `--model` | `string` | `"fast-agent"` | Target worker model for submitted read-only scout tasks |
| `--json` / `--jsonl` | `bool` | `false` | Emit machine-readable envelope |

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
  --title "Receipt delegation engine" \
  --package "receipt" \
  --file "internal/receipt/receipt.go" \
  --problem "Unrestricted write mutation" \
  --root-cause "Missing capability checks"

# Query vault using BM25 full-text search
g8s vault query "write receipts CAS" --limit 10

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
g8s cleanup-worktrees --older-than 1h
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
Migrate legacy cwd-relative g8s data (state DB, receipts, heartbeats) to canonical paths.

```sh
g8s migrate --from ./ --to ~/.local/state/g8s --dry-run
g8s migrate --from ./ --to ~/.local/state/g8s --force
```

---

### 22. `g8s converge`
Dual-blind convergence synthesis for multiple worker proposal files on the same brief.

```sh
g8s converge ./proposal-a.md ./proposal-b.md --out ./converged.md
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

> **Security Requirement (#659)**: Unauthenticated binds (`--token ""` / empty `ApiToken`) are strictly permitted ONLY for literal IPv4 and IPv6 loopback addresses (`127.0.0.0/8`, `::1`). Non-loopback addresses (e.g. `192.168.1.100:8080`), hostnames, and wildcard addresses (`0.0.0.0:8080`, `[::]:8080`, or `:8080`) MUST be configured with a valid, non-whitespace API token (`--token` or `ApiToken` in config). `g8s serve` validates configuration pre-flight and rejects unauthenticated non-loopback binds immediately at startup without opening any network listener.

```sh
# Start HTTP API server on loopback interface (token optional)
g8s serve --address "127.0.0.1:8080"

# Start HTTP API server on wildcard or non-loopback interface (token required)
g8s serve --address "0.0.0.0:8080" --token "secret-token"

# Run as background daemon blocking until SIGINT/SIGTERM
g8s serve --address "127.0.0.1:8080" --daemon
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--address` | `string` | `"127.0.0.1:8080"` | Host and port to listen on. Non-loopback and wildcard binds require `--token`. |
| `--token` | `string` | `""` | Bearer API token for authentication (mandatory for non-loopback binds). |
| `--daemon` | `bool` | `false` | Run as long-lived daemon (blocks until signal received). |
| `--config` | `string` | `""` | Path to server configuration YAML file. |

---

### 24a. `g8s receipt-export`
Performs operator-invokable retry and repair for evidence receipt exports (DELTA-31, #658). Operates directly on the stored task record without claiming tasks, acquiring leases, or re-invoking models.

```sh
# Retry receipt export for a completed task
g8s receipt-export --task "task-123"

# Retry export specifying explicit run and evidence directories
g8s receipt-export --task "task-123" --run-dir ".g8s/runs/attempt-1" --evidence-dir "~/.local/state/g8s/evidence"

# Output structured JSON result envelope
g8s receipt-export --task "task-123" --json
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task`, `--task-id` | `string` | `""` | Task ID to export/retry receipt for (can also be provided as positional argument). |
| `--run-dir`, `--attempt-dir` | `string` | `""` | Path to attempt run directory (optional). |
| `--evidence-dir` | `string` | `""` | Path to evidence lake directory (defaults to `G8S_EVIDENCE_DIR`). |
| `--require-local` | `bool` | `true` | Require attempt-local export to succeed for overall acceptance. |
| `--require-central` | `bool` | `false` | Require centralized Evidence Lake export to succeed for overall acceptance. |
| `--json` / `--jsonl` | `bool` | `true` / `false` | Output structured JSON result envelope (`kind: receipt`, `verb: export`). |

> JSON `data` includes export `local`/`central` status, digests, and optional `claim_fence` (verdict, `task_id`, `claim_task_id`, `receipt_id`, `code`) when harvest fencing was recorded on the task.

---

### 24b. `g8s reflex triage`
Runs the System-1 reflex gate (Jev / TypeSafe AI sensor + deterministic supervisor policy, ADR-0020) on a *planned* mutation and prints the verdict: `grant_receipt`, `escalate_hitl`, or `instant_kill`. Use it as an enforce-code pre-mutation gate in scripts and CI.

```sh
g8s reflex triage \
  --task "slice-42" \
  --summary "raise success-path test deadline 5s to 30s" \
  --files "internal/runtime/verify_test.go" \
  --allowed "internal/runtime/*"
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task` | `string` | `cli-reflex-triage` | Mutation task id for the audit trail. |
| `--summary` | `string` | *(required)* | One-line diff summary of the planned mutation. |
| `--files` | `string` | `""` | Comma-separated files the mutation will touch. |
| `--allowed` | `string` | `""` | Comma-separated allowed path globs. |
| `--json` / `--jsonl` | `bool` | `true`/`false` | Emit machine-readable envelope (`kind: reflex_verdict`). |

> Requires `TYPESAFE_API_KEY` (or a `.env` with it). Without a key the gate degrades to the deterministic classifier and reports `fallback: true`.

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

**23 tools registered** (22 active + `g8s_run` reserved post-MVP stub): `g8s_run`, `g8s_submit`, `g8s_get`, `g8s_receipt_issue`, `g8s_self_awareness`, `g8s_blast_radius`, `g8s_dispatch`, `g8s_list_tasks`, `g8s_cancel_task`, `g8s_list_roles`, `g8s_list_permissions`, `g8s_search`, `g8s_read`, `g8s_edit`, `g8s_select`, `g8s_llm_call`, `g8s_charter_issue`, `g8s_charter_verify`, `g8s_portfolio_status`, `g8s_skills_list`, `g8s_skills_read`, `g8s_context_pack`, `g8s_coordinator_mesh`.

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

### 29a. `g8s eval` — Adversarial Probe Evaluation Harness
Executes the adversarial probe evaluation harness (#254) to evaluate provider compliance, test defense invariants, or run release self-audit probes per ADR-0026. Computes and emits the Provider Reliability Index (PRI).

```sh
# Enumerate available evaluation probes
g8s eval list

# Filter probe suite by category
g8s eval list --category self-audit

# Run evaluation suite against mock compliant provider
g8s eval run --provider mock-compliant

# Run self-audit probes against live agy provider per ADR-0026 release gate
g8s eval run --category self-audit --provider agy --model gemini-3.8-flash-high
```

#### Subcommands:
- `g8s eval list`: Enumerates registered probes with category and description.
- `g8s eval run`: Executes selected probe suite against a mock or live worker provider.

#### Flags (`g8s eval list`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--category` | `string` | `""` | Filter probe suite by category (e.g. `self-audit`). |

#### Flags (`g8s eval run`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--provider` | `string` | `"mock-compliant"` | Target provider: `mock-compliant`, `mock-defiant`, `agy`, `claude`. |
| `--model` | `string` | `""` | Target model for live providers (defaults to `gemini-3.8-flash-high` for `agy`, `claude-sonnet-4-5` for `claude`). |
| `--category` | `string` | `""` | Filter probe suite by category. |
| `--probes` | `string` | `""` | Comma-separated list of probe IDs to execute. |
| `--timeout` | `duration` | `30s` | Per-probe execution timeout. |

#### Self-Audit Category (ADR-0026):
The `self-audit` probe category verifies internal g8s harness integrity before release (ADR-0026 §4):
- **Refusal-Echo (#443)**: Verifies that security boundary refusals correctly echo and redact without leaking confidential context.
- **Worktree-Discard (#443-f2/#450)**: Verifies that rejected/failed attempts discard worktree debris without polluting the shared checkout.
- **Sanitizer-Fidelity (#434/#445)**: Asserts prompt/payload escape pairs remain atomic and unaltered through transport.
- **Receipt-Bypass**: Proves worker processes without valid Write Receipts cannot mutate workspace files.

---

### 30. `g8s init` / `g8s config` / `g8s completion` / `g8s service`
Runtime initialization, persistent key-value configuration, shell completion generation, and OS service management.

```sh
# Runtime initialization
g8s init

# Configuration management
g8s config list
g8s config get default_provider
g8s config set default_provider agy
g8s config unset default_provider

# Shell completion
g8s completion zsh > ~/.zsh/completions/_g8s

# OS service management
g8s service install
```

#### Persistent Configuration Keys (`g8s config`):
| Key | Description |
| :--- | :--- |
| `default_provider` | Default target provider for dispatch executions and queue submissions. |
| `default_model` | Default target model identifier for dispatch executions. |
| `default_role` | Default worker role profile for submitted tasks. |
| `default_timeout` | Default maximum execution duration for tasks (e.g. `60s`, `5m`). |
| `data_dir` | Directory for g8s database and persistent storage. |
| `scope` | Installation and execution scope (`user` or `system`). |
| `evidence_dir` | Centralized directory for exported task execution receipts and logs. |
| `evidence_retention_days` | Evidence directory retention period in days (empty = unlimited). |
| `log_level` | Verbosity level for daemon and CLI operations (`debug`, `info`, `warn`, `error`). |
| `submit_rate_limit_per_hour` | Maximum tasks submitted per hour per actor (0 = unlimited). |
| `auto_retry_enabled` | Enable automatic resubmission of transiently failed tasks (default false). |
| `auto_retry_max_per_task` | Maximum automatic retries per original task (0-10, default 2). |
| `auto_retry_max_per_hour` | Maximum automatic retries per hour per state directory (0-1000, default 10). |
| `autonomy_level` | Autonomy posture level (0 = manual/no auto-merge, 1 = docs-lane auto-merge). |

---

### 31. `g8s ladder` — Quality Ladder Escalation & Telemetry
Inspects failure lineage, evaluates automated quality-ladder escalation plans, advances tasks through multi-rung remediation, and reports empirical pass/escalation gauges. See [effort.md](effort.md) for detailed ladder policies.

```sh
# Display ladder lineage, cumulative tokens used, and next rung plan
g8s ladder status <task-id>

# Advance to the next ladder rung (escalate effort, model alternative, or emit HITL packet)
g8s ladder advance <task-id> --prompt "Refined instructions for retry"

# Advance and write HITL evidence packet to file if human review is required
g8s ladder advance <task-id> --out ./hitl-packet.json

# Report empirical pass-rates, escalation-rates, and HITL metrics
g8s ladder gauges
g8s ladder gauges docs
```

#### Subcommands:
- `g8s ladder status <task-id>`: Traverses task lineage to root, aggregates cumulative token consumption against class budgets, inspects latest failure shape, and plans next action.
- `g8s ladder advance <task-id>`: Executes the planned next rung. Dispatches diagnosis at Rung 0, escalates effort along supported subsets at Rungs 1..3, tries alternative models at Rungs 4..5, or refuses to HITL.
- `g8s ladder gauges [class]`: Computes pass rates per `(class, effort)`, escalation rates per class, and overall HITL rates from telemetry events and task history.

#### Flags (`g8s ladder status`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task` / `--task-id` | `string` | `""` | Task ID to inspect (can be passed as positional argument). |
| `--db` | `string` | `""` | Path to control-plane database (`g8s.db`). |
| `--telemetry-db` | `string` | `""` | Path to telemetry database (`telemetry.db`). |

#### Flags (`g8s ladder advance`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task` / `--task-id` | `string` | `""` | Task ID to advance (can be passed as positional argument). |
| `--prompt` | `string` | `""` | Task prompt for escalation re-dos (required for Rungs 1..5). |
| `--prompt-file` | `string` | `""` | Path to file containing task prompt for escalation re-dos. |
| `--receipt-id` | `string` | `""` | Fresh write receipt ID (required if advancing a `workspace_write` task). |
| `--out` | `string` | `""` | Optional file path to write HITL evidence packet when human review is required. |
| `--db` | `string` | `""` | Path to control-plane database (`g8s.db`). |
| `--telemetry-db` | `string` | `""` | Path to telemetry database (`telemetry.db`). |

#### Flags (`g8s ladder gauges`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--class` | `string` | `""` | Filter gauges for a specific task class (can be passed as positional argument). |
| `--db` | `string` | `""` | Path to control-plane database (`g8s.db`). |
| `--telemetry-db` | `string` | `""` | Path to telemetry database (`telemetry.db`). |

---

### 32. `g8s search` / `g8s read` / `g8s edit` / `g8s select` — Native Cross-Platform Agent I/O (`DELTA-23`)
Pure-Go, cross-platform replacements for `rg`/`grep`/`find`, `cat`/`head`/`tail`/`sed -n`, `sed -i`/`awk`, and `jq` that enforce `G8S_SCOPE_ROOT`, `WriteReceipt.AllowedPaths` (plus `.git/**` & `.g8s/**` protection), and automatic DLP credential redaction in-process.

```sh
# Search literals or regexes inside scope root
g8s search "VerifyWriteScope" internal/ --json

# Bounded line-range file read with SHA-256 precondition output
g8s read internal/fsops/fsops.go --lines 320:360 --json

# Atomic CAS file edit with optional SHA-256 optimistic concurrency check
g8s edit internal/fsops/fsops.go --old "oldText" --new "newText" --receipt-id <rc-id> --json

# Pure-Go JSON dot-path extraction (or pass --select <dot.path> to any g8s command)
g8s doctor --json --select .data.overall_status
```

---

### 33. `g8s llm-call` & `g8s providers discover` — Universal LLM Consumer & Bounded Agentic Loop (`DELTA-25..32`, `#661..#664`)
Invokes any configured `api_call` provider (`9router`, `cliproxyapi`, `openai`, `anthropic`) synchronously or in a bounded multi-turn verify-and-repair loop with Pre-Turn-1 toolchain/Vault grounding, Non-Markovian cycle breaking (`A->B->A`), atomic per-turn file snapshot rollback, and `--verify-cmd` secret environment scrubbing.

```sh
# Live model discovery and combo liveness / sunset-model (HTTP 410 EOL) audit
g8s providers discover --provider 9router --json
g8s providers eval-combos --provider 9router --timeout 30s --json
g8s providers evaluate --provider 9router --audit-catalog --max-tokens 512 --json

# Bounded multi-turn verify-and-repair loop
g8s llm-call \
  --provider 9router \
  --model fast-agent \
  --prompt "Implement feature X" \
  --context-files "internal/pkg/x.go" \
  --apply-files \
  --max-turns 3 \
  --verify-cmd "go test ./internal/pkg/..." \
  --json
```

---

### 33a. `g8s blind-verify` — Double-Blind Counterexample & Falsification Review (`DELTA-34`, `g8s-blind-verifier`)
Executes the g8s-blind-verifier protocol: strips worker self-report and model identity, presents ONLY SpecContract + Invariants + RawGitDiff + MechanicalTestOutput to a fresh verifier model, and returns structured PASS/FAIL with counterexamples.

```sh
g8s blind-verify \
  --spec SPEC.md \
  --invariants INVARIANTS.md \
  --diff changes.diff \
  --test-output test-results.txt \
  --model deep-coding \
  --provider 9router \
  --timeout 120s \
  --json
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--base` | `string` | `"origin/main"` | Git base ref when `--diff` is omitted (captures full untruncated `git diff`) |
| `--head` | `string` | `""` | Optional git head ref when `--diff` is omitted |
| `--diff` | `string` | `""` | Optional path to git diff file (or `-` for stdin) |
| `--spec` | `string` | `""` | Optional path to SpecContract markdown file |
| `--spec-text` | `string` | `""` | Inline spec contract summary when `--spec` is omitted |
| `--invariants` | `string` | `"spec/constitution.md"` | Optional path to Invariants file |
| `--test-output` | `string` | `"PASS"` | Optional path to mechanical test output file (or `-` for stdin) |
| `--model` | `string` | `"fast-agent"` | Verifier model identifier |
| `--effort` | `string` | `"low"` | Reasoning effort level for verifier |
| `--provider` | `string` | `"9router"` | Provider for verifier model |
| `--max-tokens` | `int` | `4096` | Max output tokens for verifier response |
| `--timeout` | `duration` | `"180s"` | Verification timeout |

#### Blind Verification Protocol (S3-A):
1. **Input**: BlindReviewPacket (SpecContract, Invariants, RawGitDiff, MechanicalTestOutput)
2. **Stripped**: Worker self-report, PR description, model identity, narrative context
3. **Output**: `{ "passed": boolean, "findings": [...], "counterexamples": [...] }`
4. **Contract**: If `passed=false`, at least one counterexample MUST be provided

---

### 34. `g8s fleet` & `g8s receipt-export` — Cross-Repo Fleet Hygiene & Receipt Recovery (`DELTA-31..32`)
Inspects dirty working trees, ahead/behind branch divergence, and orphaned worktrees across sibling repositories, or recovers unexported task receipts idempotently.

```sh
# Check fleet hygiene across workspace repositories (exits non-zero if dirty under --require-clean)
g8s fleet status --dir /workspace/repos --require-clean --json

# Recover or re-export a completed task's execution receipt
g8s receipt-export --task <task-id> --json
```

---

### 35. `g8s feature` — Declarative Feature Map, Blast Radius DAG & Live Conformance Audit (`DELTA-23`, `#649`, `#665`, `#686`)
Validates `.g8s/feature-map.yml` (schema 2.0, cycle detection, bidirectional `depends_on` $\leftrightarrow$ `direct_consumers` edge symmetry, zero-orphan `internal/` package coverage), computes transitive blast radius and allowed write scopes, and runs 2-layer AST + live test conformance audits (`STATIC_ONLY`, `VERIFIED_LIVE`, `DRIFTED_CONTRACT`, `BROKEN_RUNTIME`).

```sh
# Validate feature map against repository structure
g8s feature verify --file .g8s/feature-map.yml --repo . --json

# List all feature nodes or inspect transitive blast radius & derived allowed_paths
g8s feature list --json
g8s feature inspect FEAT-FSOPS-01 --json

# Run 2-layer AST contract & live behavioral test audit across feature nodes (#686)
g8s feature audit --json
g8s feature audit --feature FEAT-FEATUREMAP-01 --live --json
```

---

### 36. `g8s verify` / `g8s watch` / `g8s reflex` — Information Firewall, Event Wakeup & System-1 Triage (`DELTA-19..24`)
Executes worktree-targeted non-vacuous verification producing `RoundReceiptSchema` (`g8s verify`), blocks on task/PR milestones without polling loops (`g8s watch`), and runs System-1 `Jev` pre-mutation triage (`g8s reflex`).

```sh
# Verify an isolated task worktree and emit RoundReceiptSchema
g8s verify --task <task-id> --json

# Event-driven wakeup on task completion or PR checks
g8s watch --task <task-id> --milestone worker-complete --json

# System-1 pre-mutation triage
g8s reflex triage --summary "Update feature map validation" --files "internal/featuremap/featuremap.go" --json
```

---

### 37. `g8s charter` — Orthogonal Stratum Federation & Epoch-Fenced Charter Engine (`DELTA-33`, `ADR-0035`)
Manages epoch-fenced `S1 -> S2` domain charters stored in `$G8S_STATE_DIR/charters.db` and projects read-only `O-Plane` portfolio telemetry.

```sh
# Issue an S1 -> S2 Charter for a repository
g8s charter issue --repo tamld/g8s --mandate "Ship DELTA-33" --path "internal/**" --ttl 2h --json

# Verify an active Charter's epoch and fencing token
g8s charter verify --id <charter-id> --epoch 1 --fence <fence-token> --json

# Revoke and reclaim a Charter (increments epoch and rotates fencing token)
g8s charter revoke --id <charter-id> --reason "Reclaimed by S1 coordinator" --json

# Project read-only O-Plane portfolio status
g8s charter portfolio --json
```

---

### 38. `g8s coordinator` & `g8s memory autopsy|outcome|compound` — Coordinator Experiential Mastery Pipeline (`#688`, `#689`, `#690`)
Drives the closed-loop Coordinator expertise lifecycle (`Intake -> Autopsy -> Outcome Feedback -> Compounding -> Domain Mastery Scorecard`):
- `g8s coordinator intake`: Synthesizes feature orientation, upstream research gate, 3-round Root-Core dialectical questions (`wiki-deep-why -> wiki-socratic -> ck:predict`), and deterministic skill/tool/worker routing.
- `g8s memory autopsy`: Extracts sanitized failure-to-fix lessons (`Học -> Sai -> Sửa -> Rút kinh nghiệm`) with `Causality`, `Spatial`, and `Forensic` anchors.
- `g8s memory outcome` & `g8s memory compound`: Tracks first-pass verification corroborations vs. counter-example failures (`Ứng dụng -> Giỏi hơn -> Trở thành chuyên gia`) and promotes `READY_FOR_DOCTRINE` memories into `vault.db`.
- `g8s coordinator mastery`: Computes per-domain mastery tiers (`NOVICE`, `PRACTITIONER`, `EXPERT`, `MASTER`) across `.g8s/feature-map.yml`.

```sh
# 1. Cognitive Intake & 3-Round Dialectical Deconstruction (#688)
g8s coordinator intake --goal "Add experiential autopsy loop" --files "internal/memory/autopsy.go" --json

# 2. Extract & Propose Experiential Autopsy on Failure-to-Fix (#689)
g8s memory autopsy --task <task-id> --failed-check "go_test" --diagnostic "..." --fix-summary "..." --file "internal/memory/autopsy.go" --auto-propose --json

# 3. Record Outcome Feedback & Compound Corroborated Entries (#690)
g8s memory outcome --ids <entry-id> --session <sess-id> --passed=true --json
g8s memory compound --file "internal/memory/autopsy.go" --test-file "internal/memory/experiential_pipeline_test.go" --json

# 4. Inspect Per-Domain Mastery Scorecard (#690)
g8s coordinator mastery --json
```

---

### 39. `g8s resubmit`
Resubmits a `FAILED` task with a supervisor receipt boundary (new attempt; does not bypass harness checks).

```sh
g8s resubmit --task <task-id>
g8s resubmit --task <task-id> --receipt-id <write-receipt-id> --reason "operator retry"
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--task`, `--task-id` | `string` | `""` | Task ID to resubmit. |
| `--receipt-id` | `string` | `""` | Fresh write receipt ID (required if the original task used `workspace_write`). |
| `--reason` | `string` | `""` | Reason for resubmission. |
| `--db` | `string` | `""` | Path to control-plane database. |

---

### 40. `g8s update` / `g8s uninstall`
Self-update from GitHub releases with SHA-256 verification, and controlled removal of the binary, MCP entries, and optional state purge.

```sh
g8s update --check
g8s update --version v0.21.0
g8s uninstall --dry-run
g8s uninstall --yes --purge-state
```

#### `g8s update` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--check` | `bool` | `false` | Only report whether an update is available; make no changes. |
| `--force` | `bool` | `false` | Reinstall even when the target version is already running. |
| `--version` | `string` | `""` | Install this release tag (e.g. `v0.21.0`) instead of the latest. |
| `--timeout` | `duration` | `2m` | Overall network timeout for release resolution and download. |

#### `g8s uninstall` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--dry-run` | `bool` | `true` | List what would be removed without removing anything (default unless `--yes`). |
| `--yes` | `bool` | `false` | Apply the uninstall plan. |
| `--purge-state` | `bool` | `false` | Also remove g8s state, config, cache and log directories. |

---

### 41. `g8s offer`
Scaffold and audit repository offer profiles (embedded `knowledge`, `security`, `infra`, `utility` bundles).

```sh
g8s offer init --profile knowledge --dir ./sibling-repo
g8s offer check --dir ./sibling-repo --gates spec,structure,links
g8s offer version
```

#### Subcommands:
| Subcommand | Description |
| :--- | :--- |
| `init` | Scaffold `.g8s/` plus gate scripts from the embedded profile (`--profile`, `--dir`, `--force`, `--gates-only`, `--gates-dir`). |
| `check` | Run copied gates in the target tree (`--gates`, `--gates-dir`). |
| `version` | Print the offer bundle version. |

#### `g8s offer init` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--profile` | `string` | `""` | Profile: `knowledge` \| `security` \| `infra` \| `utility` (required unless `--gates-only`). |
| `--dir` | `string` | `"."` | Target project directory. |
| `--force` | `bool` | `false` | Overwrite an existing scaffold. |
| `--gates-only` | `bool` | `false` | Install/refresh `tools/ci_*.sh` gate scripts without modifying `.g8s/` registries. |
| `--gates-dir` | `string` | `"tools"` | Relative directory to install gate scripts. |

#### `g8s offer check` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--dir` | `string` | `"."` | Target project directory. |
| `--gates` | `string` | `"spec,structure,links"` | Comma list: `spec`, `structure`, `links`. |
| `--gates-dir` | `string` | `""` | Relative directory containing gate scripts. |

---

### 42. `g8s reach`
Read-only platform data acquisition with ordered fallback (`read` is GET-only).

```sh
g8s reach read https://example.com/doc --json
g8s reach github read --url https://api.github.com/repos/tamld/g8s --json
```

**Channels (S1):** `web`, `github`. **Actions:** `read` (GET-only, read-only).

---

### 43. `g8s lesson`
Record and verify operational lessons in the lessons ledger (`docs/lessons/ledger.jsonl` by default).

```sh
g8s lesson create --file lesson-draft.json
g8s lesson verify --file lesson-draft.json
g8s lesson list --status ratified
```

#### Subcommands:
| Subcommand | Description |
| :--- | :--- |
| `create` | Verify and append a lesson draft to the ledger (`--file` required). |
| `verify` | Verify a lesson draft without appending (dry-run). |
| `list` | List lessons in the ledger (`--status`, `--ledger`, `--passthrough`). |

#### Shared Flags (`create` / `verify`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--file` | `string` | `""` | Path to lesson draft JSON file (required). |
| `--ledger` | `string` | `docs/lessons/ledger.jsonl` | Path to lessons ledger file. |
| `--round-cap` | `int` | `3` | Round lesson budget cap. |
| `--db` | `string` | `""` | Path to state-dir telemetry database. |

#### `g8s lesson list` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--status` | `string` | `""` | Filter by lesson status (`proposed`, `ratified`, `rejected`). |
| `--ledger` | `string` | `docs/lessons/ledger.jsonl` | Path to lessons ledger file. |
| `--passthrough` | `bool` | `false` | Output raw JSONL lines without envelope. |

---

### 44. `g8s feedback`
Record supervisor feedback, HITL ground truth, overrides, and governance hypothesis reports.

```sh
g8s feedback record --rule reflex.triage --verdict tp --note "confirmed false positive"
g8s feedback override --rule reflex.triage --task <task-id> --reason "supervisor waiver"
g8s feedback report --json
```

#### Subcommands:
| Subcommand | Description |
| :--- | :--- |
| `record` | Record a Sovereign HITL Ground Truth verdict (`tp`, `fp`, `veto`) for a rule (`--rule`, `--verdict` required). |
| `override` | Record an F0 Supervisor OverrideReceipt for an advisory signal. |
| `report` | Evaluate falsifiable governance hypotheses (H1, H2, H3) and rule weights. |

#### `g8s feedback record` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--rule` | `string` | `""` | Rule ID to calibrate (required). |
| `--verdict` | `string` | `""` | HITL verdict: `tp`, `fp`, or `veto` (required). |
| `--note` | `string` | `""` | Optional human operator rationale. |
| `--package` | `string` | `""` | Optional package name. |
| `--task` | `string` | `""` | Optional task ID. |
| `--db` | `string` | `""` | Optional telemetry database path override. |

---

### 45. `g8s context-pack`
Build bounded pre-mutation context packets (token-budget knapsack over workspace paths).

```sh
g8s context-pack --paths internal/harness,spec/constitution.md --focus "write receipt" --out pack.md --json
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--root` | `string` | `"."` | Workspace root directory to scan and compress. |
| `--paths` | `string` | `""` | Comma-separated file or directory paths. |
| `--allowed-paths` | `string` | `""` | Optional comma-separated pinned `allowed_paths` patterns. |
| `--focus` | `string` | `""` | Focus query for BM25F and Personalized PageRank scoring. |
| `--budget` | `int` | `8000` | Token budget for Multiple-Choice Knapsack solver. |
| `--out` | `string` | `""` | Optional file path to write rendered Markdown bundle. |

---

### 46. `g8s qa-report`
Generate mechanical QA verification reports from a BA/QA specification.

```sh
g8s qa-report --spec spec/openspec/delta-45.md --holdout-paths internal/foo_test.go --json
```

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--spec` | `string` | `""` | Path to BA/QA specification markdown file. |
| `--task` | `string` | `""` | Optional task ID. |
| `--holdout-paths` | `string` | `""` | Comma-separated holdout test paths. |
| `--allowed-paths` | `string` | `""` | Comma-separated worker allowed write paths. |
| `--ac-bindings` | `string` | `""` | Comma-separated `AC=TestName` pairs (e.g. `AC-01=TestFoo,AC-02=TestBar`). |
| `--checks-passed` | `bool` | `true` | Whether mechanical test execution passed. |

---

### 47. `g8s claim`
Verify documentation claims against executable anchors (deterministic repo probes; optional LLM decomposition).

```sh
g8s claim check --text "All CLI commands are documented in cli-reference.md" --repo . --json
g8s claim check --file brief.md --run-tests --llm --provider 9router --model reasoning-free
```

Classifies every atomic claim as `MECHANICAL_VERIFIED`, `OBSERVATIONAL`, `UNVERIFIED`, or `CONTRADICTED`. Exit code `1` (`E_PREMISE_CONTRADICTED`) when any claim is refuted by a deterministic repo probe.

#### `g8s claim check` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--text` | `string` | `""` | Operator request or brief to check. |
| `--file` | `string` | `""` | Read the request or brief from a file. |
| `--repo` | `string` | `"."` | Repository root used for deterministic probes. |
| `--run-tests` | `string` | `""` | Execute `go test -run ^Name$` for test pass/fail claims. |
| `--test-timeout` | `duration` | `2m` | Per-test execution timeout for `--run-tests`. |
| `--llm` | `bool` | `false` | Add candidate claims from an LLM decomposer (statuses stay deterministic). |
| `--provider` | `string` | `"9router"` | API-call provider for `--llm`. |
| `--model` | `string` | `"reasoning-free"` | Model/combo for `--llm`. |
| `--llm-timeout` | `duration` | `1m` | Timeout for the `--llm` decomposition call. |

---

### 48. `g8s f0`
F0 supervisor commitment-point gate and repository status checks.

```sh
g8s f0 gate check --repo . --action merge --pr 787 --json
g8s f0 status --repo . --json
```

#### Subcommands:
| Subcommand | Description |
| :--- | :--- |
| `gate check` | Evaluate commitment-point gates before merge, close, or publish (`--action`, `--repo`, `--pr`, `--base`, `--head-sha`). |
| `status` | Print F0 gate status for a repository (`--repo`). |

#### `g8s f0 gate check` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--action` | `string` | `"merge"` | Commitment action (`merge`, `close`, `publish`). |
| `--repo` | `string` | `"."` | Target repository path. |
| `--base` | `string` | `"main"` | Base branch. |
| `--pr` | `int` | `0` | Optional PR number. |
| `--head-sha` | `string` | `""` | Optional head commit SHA. |

---

### 49. `g8s scorecard`
DELTA-45 five-axis cross-evaluation and Kaizen engine over rolling model windows.

```sh
g8s scorecard status --window 20 --json
g8s scorecard audit --provider 9router --json
g8s scorecard kaizen --json
```

#### Subcommands:
| Subcommand | Description |
| :--- | :--- |
| `status` | Print current scorecard axes and gauges. |
| `audit` | Run scorecard audit pass. |
| `kaizen` | Emit Kaizen improvement recommendations. |

#### Shared Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--provider` | `string` | `""` | Filter model axis by provider name. |
| `--window` | `int` | `20` | Rolling observation window per model. |

---

### 50. `g8s gates`
Ingest and list CI workflow run history for the reflex scorecard (`gate_ci_run_events.jsonl`).

```sh
cat runs.json | g8s gates record --json
g8s gates record --file runs.json
g8s gates list --json
```

#### Subcommands:
| Subcommand | Description |
| :--- | :--- |
| `record` | Append workflow run rows from JSON (stdin or `--file`) to the ledger. |
| `list` | Print all recorded CI gate run rows from the ledger. |

#### Shared Flags (`record` / `list`):
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--state-dir` | `string` | `""` | State directory root (default: `G8S_STATE_DIR` or platform default). |
| `--file` | `string` | `""` | (`record` only) Read JSON from file instead of stdin. |

---

### 51. `g8s audit`
Query transactional enterprise access-audit log (#441).

```sh
g8s audit list --actor operator --limit 50 --json
g8s audit list --action task.submit --since 2026-01-01T00:00:00Z
```

#### `g8s audit list` Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--actor` | `string` | `""` | Filter by actor identity. |
| `--action` | `string` | `""` | Filter by audit action (e.g. `task.submit`, `task.claim`, `receipt.issue`). |
| `--subject` | `string` | `""` | Filter by subject ID (`task_id` or `receipt_id`). |
| `--since` | `string` | `""` | Filter by RFC3339 timestamp lower bound. |
| `--limit` | `int` | `50` | Maximum audit records to return (`1..500`). |

---

### 52. `g8s help`
Prints the root command list and short descriptions (same surface as `g8s --help`).

```sh
g8s help
g8s help submit
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
| `--select` | `string` | Pure-Go dot-path selector projecting a specific field from JSON output (replaces `jq`). |
| `--actor` | `string` | Actor identity for traceability. |
| `--trace-id` | `string` | Distributed trace ID (auto-generated if omitted). |
| `--help` | `bool` | Show command-specific usage. |