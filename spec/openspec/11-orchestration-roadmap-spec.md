# 11 — Orchestration Roadmap Spec (DELTA-11)

**Status**: `ACCEPTED`
**Owner**: tamld
**Created**: 2026-08-28
**Depends on**: DELTA-09 worker-supervisor (APPLIED)
**Supersedes**: none

**Promotion**: Concern B (B.1-B.3) promoted from DRAFT → ACCEPTED on 2026-09-12 as part of T021. Implementation status: IMPLEMENTING.
**Promotion**: Concern C (C.1-C.2) promoted from DRAFT → ACCEPTED on 2026-09-12 as part of T022. Implementation status: IMPLEMENTING.

Port of the orchestration strategy sketched in
`docs/designs/supervisor-fix-loop.md` to the OpenSpec delta format. This
delta introduces a **supervisor-driven fix loop** that wraps the existing
`internal/orchestrator`, `internal/harness`, and `internal/receipt` with
planning, RCA, ADR, escalation, and meta-optimizer feedback.

## Three Concerns

This delta is split into three sub-requirements that map to three issues:

- **Concern A — Supervisor-driven fix loop**: the loop itself. Planner +
  reviewer + RCA + escalator.
- **Concern B — Receipt evolution for supervisor**: extends the receipt
  schema so the supervisor has the data it needs to make decisions
  (approach shift, RCA confidence, ADR linkage, attempt lineage).
- **Concern C — Meta-optimizer**: two feedback loops. One tunes how the
  supervisor thinks (envelope selection, RCA confidence thresholds). One
  tunes how the worker executes (prompt templates, role selection,
  skill loadout).

## Scope boundary

This delta does **not** redefine the process-level worker supervisor
(DELTA-09, `internal/worker`). It sits **above** `internal/orchestrator`
and below the CLI / HITL boundary. The Worker interface in
`internal/orchestrator` is unchanged.

## ADDED Requirements

### Requirement: A supervisor package owns the fix loop above internal/orchestrator

`internal/supervisor` SHALL expose a top-level entry point
`Run(ctx context.Context, req RunRequest) (RunResult, error)` that drives a
bounded fix loop. The supervisor SHALL NOT import AGY, codex, or any
worker binary directly. It SHALL call only the public surface of
`internal/orchestrator` (Worker, Receipt, FanOut) and
`internal/harness` (Role, Permission, ValidateRequest,
BuildContractPromptWithReceipt). It SHALL persist state to
`internal/controlplane` (SQLite WAL) via the existing store contract.

#### Scenario: supervisor dispatches one worker via FanOut
- A `RunRequest` with a single task description produces exactly one
  `FanOut` call with `len(plan) == 1` and `MaxParallel == 1`.
- The worker subprocess exits with code 0 and a receipt is recorded.

#### Scenario: supervisor never imports worker backends
- `go list -deps ./internal/supervisor | grep -E 'codex|agy|gemini|claude'`
  returns no matches.

**Implementation Status**: IMPLEMENTING (T020)

### Requirement: Each task envelope selects a measurable subset of SRS/PRD/DoR/DoD/DnD/Validateds/FSM

`RunRequest.EnvelopeHints` SHALL be an optional map from field name to
required bool. When `EnvelopeHints` is nil, the supervisor SHALL pick the
minimum set: `DoR`, `DoD`, `Validateds`. When `EnvelopeHints` is set,
the supervisor SHALL honor it for the listed fields and pick the rest
automatically. The chosen envelope SHALL be persisted as a JSON column
on the supervisor row so Concern C can score it later.

#### Scenario: minimal envelope for a single-file change
- `EnvelopeHints` is nil. Chosen envelope: `{DoR, DoD, Validateds}`.

#### Scenario: full envelope for a cross-package refactor
- `EnvelopeHints = {"SRS": true, "PRD": true, "FSM": true}`. Chosen
  envelope: `{DoR, DoD, Validateds, SRS, PRD, FSM}`.

**Implementation Status**: IMPLEMENTING (T020)

### Requirement: Iteration is bounded by 3 attempts × 3 approaches = 9 total, then HITL

The supervisor SHALL enforce the iteration policy documented in
`docs/designs/supervisor-fix-loop.md` §6:

- Maximum 3 attempts per approach.
- Maximum 3 approaches per task.
- After 9 total attempts without success, the supervisor SHALL emit
  an `Escalation` event and exit non-zero.
- Escalation payload SHALL be JSON on stdout matching the schema in
  `docs/designs/supervisor-fix-loop.md` §9.

These numbers SHALL be configurable via `SupervisorConfig` but SHALL
default to the values above until Concern C tunes them.

#### Scenario: third approach shift triggers escalation
- Approach 0 fails on attempts 0, 1, 2. Approach 1 fails on attempts 0,
  1, 2. Approach 2 fails on attempt 0. Total attempts = 7, but the
  supervisor enters Approach 2 with `attempt_idx == 0`; it does not
  escalate yet. Only after Approach 2 fails on attempts 0, 1, 2 (total
  attempts = 9) does the supervisor escalate.

#### Scenario: configurable caps are honored
- `SupervisorConfig.MaxAttemptsPerApproach = 5`. Approach 0 runs 5
  attempts, then shifts. Escalation only after approach 2 also fails 5
  times.

**Implementation Status**: IMPLEMENTING (T020)

### Requirement: Every approach shift produces an RCA + ADR pair

When the supervisor decides to shift approach (i.e. attempts in the
current approach are exhausted and the supervisor chooses a new
approach over HITL escalation), it SHALL:

1. Persist an RCA record (`supervisor_decisions.kind = 'rca'`) with
   fields `failed_attempt_ids`, `symptom`, `root_cause`, `evidence`,
   `confidence`.
2. Write an ADR file under `docs/decisions/NNNN-slug.md` with fields
   `Status`, `Context`, `Decision`, `Consequences`, `Supersedes`.
3. Persist an ADR link (`supervisor_decisions.kind = 'adr'`) referencing
   the file path.
4. If `confidence < 0.6`, the supervisor SHALL pause and emit a
   `NEEDS_INFO` event instead of shifting (safety valve).

#### Scenario: RCA below confidence threshold pauses the loop
- Approach 0 fails 3 times. RCA returns `confidence = 0.4`. Supervisor
  emits `NEEDS_INFO` with the RCA payload. No ADR is written, no
  approach shift happens.

#### Scenario: RCA above threshold shifts approach and writes ADR
- Approach 0 fails 3 times. RCA returns `confidence = 0.85`. Supervisor
  writes `docs/decisions/0002-shift-to-rewrite.md` referencing the RCA,
  sets approach = 1, attempt = 0, and spawns the next worker.

**Implementation Status**: IMPLEMENTING (T020)

### Requirement: Receipt (Concern B) is the supervisor's primary evidence

The supervisor SHALL consider a worker receipt the **only** signal of
worker success. It SHALL NOT inspect worker stdout/stderr to decide
whether to accept or revise. The receipt's `OK` field, `CommitSHA`,
`FilesModified`, `ScopeViolations`, and `Validateds` results are the
authoritative inputs to the reviewer module.

#### Scenario: clean receipt with passing Validateds
- Worker emits `Receipt{OK: true, CommitSHA: "abc...", FilesModified: [...]}`. `Validateds` pass. Supervisor accepts.

#### Scenario: scope violation is always a fail
- `Receipt.ScopeViolations` is non-empty. Supervisor rejects the
  receipt regardless of `OK` field. RCA notes the scope violation as
  the symptom.

This requirement belongs to Concern B (receipt evolution). It is
documented here because the supervisor's correctness depends on it.

**Implementation Status**: IMPLEMENTING (T020)

### Requirement: Meta-optimizer (Concern C) consumes per-cycle metrics

The supervisor SHALL emit the metrics listed in
`docs/designs/supervisor-fix-loop.md` §10 on every cycle. Metrics SHALL
be persisted to `internal/controlplane` and SHALL be queryable via a
new CLI subcommand `g8s supervisor metrics` (read-only). Concern C
ingests these metrics via that subcommand.

#### Scenario: metrics are queryable
- After a cycle, `./bin/g8s supervisor metrics --task-id sup-X` returns
  JSON with all eight fields listed in §10.

#### Scenario: metrics persist across supervisor restarts
- A cycle runs, supervisor exits. Restart supervisor. Metrics for the
  prior cycle are still queryable.

**Implementation Status**: IMPLEMENTING (T020)

### Requirement: Receipt schema extends with supervisor metadata (Concern B.1)

The receipt schema SHALL add four additive columns to the `write_receipts` table:
- `approach_idx` (INTEGER) — supervisor approach index (0-based)
- `attempt_idx` (INTEGER) — supervisor attempt index within the approach (0-based)
- `rca_confidence` (REAL) — RCA confidence score [0, 1] that drove the approach shift
- `adr_path` (TEXT) — filesystem path to the ADR written for this approach shift

These columns SHALL be nullable so that receipts issued before the migration remain readable. The `SupervisorMeta` struct SHALL be added to `WriteReceipt` as an optional field.

#### Scenario: receipt issued with supervisor metadata
- Supervisor calls `IssueReceipt` with `WithSupervisorMeta(&SupervisorMeta{ApproachIdx: 0, AttemptIdx: 1, RCAConfidence: 0.85, ADRPath: "docs/decisions/0002-shift.md"})`.
- The returned `WriteReceipt` has `SupervisorMeta` populated and all four columns written to the row.

**Implementation Status**: IMPLEMENTING (T021)

### Requirement: Backward-compatible migration via idempotent ALTER TABLE (Concern B.2)

The migration from schema version 1 to version 2 SHALL be idempotent:
- It SHALL inspect `write_receipts` columns via `PRAGMA table_info`.
- For each missing column in `{approach_idx, attempt_idx, rca_confidence, adr_path}`, it SHALL execute `ALTER TABLE write_receipts ADD COLUMN <col> <type>`.
- Re-running the migration on an already-migrated database SHALL be a no-op (no errors, no duplicate columns).
- The migration SHALL run inside the exclusive transaction in `Manager.initialize()`.

#### Scenario: fresh database
- `NewReceiptManager` creates the table with all columns present. Migration is a no-op.

#### Scenario: legacy v1 database
- Opening a v1 database (user_version = 1) triggers migration. All four columns are added. `user_version` is bumped to 2.

**Implementation Status**: IMPLEMENTING (T021)

### Requirement: IssueReceipt signature extension with nil-safe SupervisorMeta (Concern B.3)

`IssueReceipt` SHALL accept an optional `SupervisorMeta` via `WithSupervisorMeta` option. The option SHALL be nil-safe: callers that do not pass it (or pass `nil`) MUST still succeed. Legacy callers using the old signature (no `SupervisorMeta`) MUST continue to work without modification.

`VerifyReceipt`, `ValidateAndConsume`, and `ListActiveReceipts` SHALL read the four new columns when present and populate `SupervisorMeta` if any column is non-NULL. If all four are NULL, `SupervisorMeta` SHALL be `nil`.

#### Scenario: legacy caller
- Old code calls `m.IssueReceipt("brain", []string{"src/**"}, time.Hour)` without `WithSupervisorMeta`.
- Returns a valid receipt with `SupervisorMeta == nil`.

#### Scenario: new caller with metadata
- New code calls `m.IssueReceipt("brain", []string{"src/**"}, time.Hour, WithSupervisorMeta(meta))`.
- Returns a valid receipt with `SupervisorMeta` populated.

#### Scenario: explicit NULL columns
- A row has `approach_idx = NULL, attempt_idx = NULL, rca_confidence = NULL, adr_path = NULL`.
- `VerifyReceipt` returns `SupervisorMeta == nil` (not zero values, not error).

**Implementation Status**: IMPLEMENTING (T021)

### Requirement: Meta-optimizer read-only aggregate API (Concern C.1)

The supervisor SHALL expose a read-only aggregate API over `supervisor_tasks` and `supervisor_metrics` tables. The API SHALL compute the following aggregate metrics across all persisted runs (optionally filtered by time range, worker name, or task state):

| Metric | Description |
|--------|-------------|
| `total_runs` | Total supervisor runs in the filter window |
| `first_attempt_success_rate` | Fraction of runs where `FirstAttemptSuccess = true` |
| `avg_attempts_to_success` | Mean `AttemptsToSuccess` across runs |
| `avg_approaches_to_success` | Mean `ApproachesToSuccess` across runs |
| `rca_confidence_avg` | Mean `RCAConfidenceAvg` across runs |
| `escalation_rate` | Fraction of runs with `EscalationCount > 0` |
| `avg_cycle_duration_seconds` | Mean `CycleDurationSeconds` across runs |
| `false_escalation_rate` | Fraction of escalations where user feedback indicates should have succeeded (future Concern C tuning) |

The aggregate SHALL be queryable via `g8s supervisor metrics --aggregate` (CLI) and `supervisor.Aggregate()` (Go API). The query SHALL be strictly read-only — no writes to any table.

#### Scenario: aggregate over empty store
- `Aggregate()` on an empty database returns zero-value `AggregateMetrics` with `TotalRuns = 0`, no error.

#### Scenario: aggregate over N tasks
- `Aggregate()` computes all eight metrics correctly, matching the formulas in §10 of `docs/designs/supervisor-fix-loop.md`.

#### Scenario: filter by time_range
- `AggregateOptions{TimeRange: 1*time.Hour}` restricts to tasks created in the last hour.

#### Scenario: filter by worker_name
- `AggregateOptions{WorkerName: "agy"}` restricts to tasks attributed to the "agy" worker (via envelope JSON, task row, or decision row).

**Implementation Status**: IMPLEMENTING (T022)

### Requirement: Meta-optimizer read-only streaming + flag collision guard (Concern C.2)

The supervisor SHALL expose a streaming API `StreamMetrics()` that emits one `TaskMetricsItem` per task matching the filter, allowing incremental processing without loading all rows into memory. The CLI SHALL surface this via `g8s supervisor metrics --json-stream`.

The CLI SHALL reject the combination `--task-id` + `--aggregate` (flag collision) with a typed usage error (exit code 2), because `--task-id` requests single-run mode while `--aggregate` requests cross-run aggregation. `--task-id` + `--json-stream` SHALL also be rejected.

#### Scenario: streaming emits per-task items
- `StreamMetrics()` calls the callback once per matching task with `TaskMetricsItem` populated from the stored metrics.

#### Scenario: flag collision --task-id + --aggregate
- `g8s supervisor metrics --task-id sup-1 --aggregate` exits with code 2 and prints usage error "cannot combine --task-id with --aggregate".

#### Scenario: flag collision --task-id + --json-stream
- `g8s supervisor metrics --task-id sup-1 --json-stream` exits with code 2 and prints usage error "cannot combine --task-id with --json-stream".

**Implementation Status**: IMPLEMENTING (T022)

`g8s orchestrate` SHALL accept `--from-intent <text>` or `--from-file <path>` as
alternative entry points to `--self-test`. The intent text SHALL be split into
sub-tasks by comma (`,`) and newline (`\n`) delimiters without requiring LLM
model inference. Each parsed sub-task SHALL be mapped to an `orchestrator.TaskSpec`
with `role=collector` and `permission=read_only`. The supervisor fix loop and
`orchestrator.FanOut` SHALL execute the plan and output a JSON envelope
containing:
- `supervisor_task_id`: unique identifier for the orchestration run
- `outcome`: terminal outcome status (`SUCCEEDED`, `FAILED`, `ESCALATED`)
- `verdict`: reviewer verdict string
- `sub_tasks`: array of `{task_id, task, status, commit_sha, files_modified, duration_seconds}`
- `receipt_summary`: summary object with `{total_runs, succeeded, failed, total_duration_seconds, files_modified}`

#### Scenario: multi-line intent splits into sub-tasks
- Given an intent with 3 lines or comma-separated tasks, `g8s orchestrate --from-intent` constructs 3 `TaskSpec`s and runs them through the orchestrator.
- Output JSON envelope contains `sub_tasks` with length 3 and matching receipt summary.

#### Scenario: intent read from file
- Given `--from-file <path>`, `g8s orchestrate` reads intent from `<path>` and executes the orchestration loop.

#### Scenario: empty intent rejected
- Empty string for `--from-intent` or empty file for `--from-file` exits with status 2 and usage error.

**Implementation Status**: IMPLEMENTED (T022/DELTA-18)

## MODIFIED Requirements

None. This delta adds new code without modifying existing specs.

## REMOVED Requirements

None.

## Cross-references

- `docs/designs/supervisor-fix-loop.md` — design doc, source of truth
  for the supervisor's behavior.
- `docs/decisions/0001-supervisor-driven-fix-loop.md` — ADR-0001, the
  decision to adopt this architecture over prompt-injection dispatch.
- `docs/goals/g8s-mvp-oss/goal.md` — Phase 6 milestone.

## Out of scope

- Process-level worker supervisor (DELTA-09).
- Provider / resource pool (DELTA-05).
- OS daemon service (DELTA-06).
- Provider classes (DELTA-10).

## Transition log

- 2026-08-28: Concern A promoted DRAFT → ACCEPTED (T020). §ADDED A.1-A.5 (the five Requirement: blocks) marked IMPLEMENTING.
- 2026-08-29: DELTA-18 AIC integration and from-intent orchestration added (§ADDED 18.1, 18.2). Marked IMPLEMENTED (T022).
- 2026-09-12: Concern B promoted DRAFT → ACCEPTED (T021). §ADDED B.1-B.3 (receipt schema extension, idempotent migration, nil-safe IssueReceipt signature) marked IMPLEMENTING.
- 2026-09-12: Concern C promoted DRAFT → ACCEPTED (T022). §ADDED C.1-C.2 (read-only aggregate API, streaming + flag collision guard) marked IMPLEMENTING.

## Change log

- **v0.1.0-draft** (2026-08-28): Initial delta from Concern A/B/C
  design doc.
- **v1.0.0-ACCEPTED** (2026-08-28): Promoted from v0.1.0-draft as part of T020 (g8s-orchestration-roadmap goal). Status DRAFT → ACCEPTED. §ADDED Requirements (six blocks: A supervisor package, B envelope selection, C iteration policy, D RCA+ADR pair, E receipt evidence, F meta-optimizer metrics) marked IMPLEMENTING. No behavioral or scenario changes; promotion reflects owner ratification to begin implementation.
- **v1.1.0-ACCEPTED** (2026-08-29): Added DELTA-18 requirements for AIC automated review integration (`g8s orchestrate-aic`) and `--from-intent` / `--from-file` sub-task FanOut orchestration (§ADDED 18.1, §ADDED 18.2). Marked IMPLEMENTED.
- **v1.2.0-ACCEPTED** (2026-09-12): Promoted Concern B from DRAFT → ACCEPTED as part of T021. Added §ADDED B.1-B.3 (receipt schema extension with supervisor metadata, idempotent ALTER TABLE migration, nil-safe IssueReceipt signature extension). Implementation already exists in `internal/receipt` (SchemaVersion 3, SupervisorMeta, migrateSupervisorSchema, migrateConcernBSchema). No behavioral changes; promotion reflects owner ratification to begin Concern C.
- **v1.3.0-ACCEPTED** (2026-09-12): Promoted Concern C from DRAFT → ACCEPTED as part of T022. Added §ADDED C.1-C.2 (meta-optimizer read-only aggregate API with 8 metrics, streaming API, flag collision guard for --task-id + --aggregate/--json-stream). Implementation already exists in `internal/supervisor/optimizer.go` (Aggregate, StreamMetrics, AggregateOptions) and `cmd/g8s/supervisor_metrics.go` (CLI flags). Test coverage in `optimizer_test.go` (12 tests). No behavioral changes; promotion reflects owner ratification for read-only meta-optimizer ingestion.

