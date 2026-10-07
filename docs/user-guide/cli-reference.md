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
| `--permission` | `string` | `"read_only"` | Permission profile (`read_only`, `automation_read`, `workspace_write`). |
| `--add-dir` | `string` | `[cwd]` | Allowed filesystem directory (repeatable). Validated against forbidden paths. |
| `--receipt-id` | `string` | `""` | Write Receipt ID (mandatory when `--permission workspace_write`). |
| `--parent-task-id`| `string` | `""` | Parent task ID for subtask lineage tracking and tree queries. |
| `--skip-permissions`| `bool` | `false` | Bypass permission checks (allowed only if permission profile permits). |
| `--provider` | `string` | `""` | Target provider name (`agy`, `codex`, etc.). Stored durably in task payload for worker claim affinity. Falls back to `default_provider` setting if omitted. Precedence: CLI flag > settings default > absent (legacy). |
| `--model` | `string` | `"gemini-3.8-flash-high"` | Target worker model identifier. |
| `--priority` | `int` | `0` | Queue priority (`-100` to `100`). Higher priority tasks are claimed first. |
| `--max-attempts` | `int` | `1` | Retry budget (`1` to `10`). |
| `--effort` | `string` | `"medium"` | Target worker reasoning effort level (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`). See [effort.md](effort.md). |
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
```

When `--provider` is specified, the worker claims only tasks whose payload `provider` matches the flag value (strict claim affinity; provider-filtered workers never claim legacy provider-less tasks, while unfiltered workers claim all queued tasks). See [providers.md](providers.md) for multi-provider setup.

#### Flags:
| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--once` | `bool` | `true` | Claim and execute a single task, then exit. |
| `--provider` | `string` | `""` | Restrict claiming to tasks targeting this provider (strict claim affinity). |
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

### 24a. `g8s reflex triage`
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

**11 tools exposed**: `g8s_dispatch`, `g8s_get`, `g8s_list_tasks`, `g8s_cancel_task`, `g8s_submit`, `g8s_blast_radius`, `g8s_run`, `g8s_self_awareness`, `g8s_receipt_issue`, `g8s_list_roles`, `g8s_list_permissions`.

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