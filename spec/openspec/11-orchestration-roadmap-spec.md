# 11 — Orchestration Roadmap Spec (DELTA-11)

**Status**: `ACCEPTED`
**Owner**: tamld
**Created**: 2026-08-28
**Depends on**: DELTA-09 worker-supervisor (APPLIED)
**Supersedes**: none

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

**Implementation Status**: ACCEPTED + IMPLEMENTING (T020, T021)

### Requirement: B.1 Additive receipt columns for supervisor provenance (SupervisorMeta)

`internal/receipt` SHALL define an exported struct `SupervisorMeta` carrying
four decision-provenance fields:
- `ApproachIdx int`: the 0-indexed approach attempt (0..2), identifying the high-level strategy pursued.
- `AttemptIdx int`: the 0-indexed attempt within the current approach (0..2), tracking retry count.
- `RCAConfidence float64`: diagnostic confidence score (0.0..1.0) produced by Root Cause Analysis prior to an approach shift.
- `ADRPath string`: relative repository path to the architecture decision record documenting the approach shift rationale (e.g. `docs/decisions/0002-shift-to-rewrite.md`).

`WriteReceipt` SHALL include an optional `SupervisorMeta *SupervisorMeta` field.
`IssueReceipt` SHALL accept functional options `opts ...IssueOption`, including
`WithSupervisorMeta(*SupervisorMeta)`. If no metadata is passed or the pointer
is nil, `SupervisorMeta` SHALL be omitted (nil).

#### Scenario: receipt issued with supervisor metadata preserves all four fields
- Caller invokes `IssueReceipt(..., WithSupervisorMeta(&SupervisorMeta{ApproachIdx: 1, AttemptIdx: 2, RCAConfidence: 0.85, ADRPath: "docs/decisions/0002-rewrite.md"}))`.
- Persisted receipt row contains all four values; read back struct matches input.

#### Scenario: receipt issued without options preserves nil SupervisorMeta
- Caller invokes `IssueReceipt(issuer, paths, ttl)` without options.
- Persisted receipt row contains SQL NULL for all four columns; read back struct has `SupervisorMeta == nil`.

**Implementation Status**: ACCEPTED + IMPLEMENTING (T021)

### Requirement: B.2 Idempotent receipt schema migration via table_info scan

`internal/receipt` SHALL maintain a schema version gate (`PRAGMA user_version = 2`).
On startup, `NewReceiptManager` SHALL inspect existing columns in `write_receipts`
via `PRAGMA table_info(write_receipts)` and execute idempotent `ALTER TABLE write_receipts ADD COLUMN`
statements only for columns that are absent:
- `approach_idx INTEGER`
- `attempt_idx INTEGER`
- `rca_confidence REAL`
- `adr_path TEXT`

The migration SHALL NOT fail if columns already exist, and multiple consecutive
migration runs SHALL be idempotent no-ops. Unsupported schema versions (`version < 0`
or `version > 2`) SHALL be rejected with a typed error.

#### Scenario: migrating pre-v2 database upgrades schema without data loss
- An existing database on v1 schema (7 columns) is opened with `NewReceiptManager`.
- Migration adds the four new columns and bumps `user_version` to 2. Existing rows remain intact.

#### Scenario: idempotent re-migration is a no-op
- Migration is invoked on an already migrated database (v2).
- Zero ALTER TABLE statements are executed; initialization succeeds without error.

**Implementation Status**: ACCEPTED + IMPLEMENTING (T021)

### Requirement: B.3 NULL tolerance contract for backward-compatible verification

Pre-migration receipts (already on disk from v0.2.0 production) MUST continue to
verify. All new columns SHALL be nullable with no DEFAULT value, representing the
factual absence of supervisor metadata on pre-migration records.

`VerifyReceipt(receiptID string) (*WriteReceipt, error)` and read paths
(`ValidateAndConsume`, `ListActiveReceipts`) SHALL treat NULL column values as absent
metadata (`SupervisorMeta == nil`). Missing supervisor metadata SHALL NOT cause
verification errors, zero-value struct pollution, or false rejections.

#### Scenario: v1 receipt verifies cleanly on v2 manager
- A receipt created under v1 schema is verified via `VerifyReceipt`.
- Verification succeeds (returns OK), with `SupervisorMeta == nil`.

#### Scenario: explicit NULL columns deserialize as absent metadata
- A receipt with explicitly NULL supervisor columns is read via `VerifyReceipt` or `ValidateAndConsume`.
- Returns `SupervisorMeta == nil`, preserving Go struct zero values only where intended.

**Implementation Status**: ACCEPTED + IMPLEMENTING (T021)

### Requirement: C.1 Read-only query layer contract for meta-optimizer insights

`internal/supervisor` SHALL expose a read-only query and aggregation layer over
`supervisor_tasks` and `supervisor_metrics`:
- `Aggregate(store *controlplane.Store, ctx context.Context, opts AggregateOptions) (AggregateMetrics, error)`
- `StreamMetrics(store *controlplane.Store, ctx context.Context, opts AggregateOptions, fn func(item TaskMetricsItem) error) error`

The query layer SHALL NOT perform any write, update, delete, or schema mutations on
`supervisor_*` or `tasks` tables. It SHALL NOT alter supervisor configurations or perform
automated hyperparameter auto-tuning in this phase (concern C read-only contract).

`AggregateOptions` SHALL accept:
- `TimeRange time.Duration`: relative duration filter from clock/now (e.g. 1h, 24h).
- `Since time.Time` / `Until time.Time`: absolute timestamp bounds.
- `WorkerName string`: worker identifier filter.
- `Clock func() time.Time`: injectable clock for deterministic time calculations.

#### Scenario: read-only query execution preserves database integrity
- Query layer executes `Aggregate` and `StreamMetrics` across existing supervisor tables.
- Database remains untouched; zero writes, updates, or deletions are executed.

#### Scenario: deterministic filtering by time range and worker name
- Caller supplies `TimeRange = 1 * time.Hour` and `WorkerName = "agy"`.
- Aggregation includes only supervisor tasks matching both filters.

**Implementation Status**: ACCEPTED + IMPLEMENTING (T021)

### Requirement: C.2 Aggregate metrics computation and filtering

The supervisor query layer SHALL compute aggregate telemetry across the eight per-cycle
metrics defined in §10:
1. `envelope_score`: average planner envelope quality score.
2. `first_attempt_success`: rate of cycles succeeding on attempt 1 (`FirstAttemptSuccessRate`).
3. `attempts_to_success`: average attempts required for successful cycles (`AvgAttemptsToSuccess`).
4. `approaches_to_success`: average approaches required for successful cycles (`AvgApproachesToSuccess`).
5. `rca_confidence_avg`: average RCA confidence score across failure shifts.
6. `cycle_duration_seconds`: average wall-clock duration of cycles (`AvgCycleSeconds`).
7. `escalation_count`: rate of cycles terminating in HITL escalation (`EscalationRate`).
8. `false_escalation_rate`: fraction of escalations classified as false alarms.

When `TotalRuns == 0` (empty store or no matching records), `Aggregate` SHALL return a
zero-value `AggregateMetrics` struct and `nil` error without dividing by zero.

The CLI surface (`g8s supervisor-metrics`) SHALL support:
- `--task-id <id>`: single task metrics.
- `--aggregate`: aggregate metrics across tasks.
- `--json-stream`: NDJSON / JSON-stream output emitting one JSON object per supervisor task.
- `--time-range <duration>`: time window filter.
- `--worker-name <name>`: worker name filter.

#### Scenario: aggregate over empty database returns zero values without error
- Caller runs `Aggregate` against a database with 0 supervisor tasks.
- Returns `AggregateMetrics{TotalRuns: 0, ...}` and `nil` error.

#### Scenario: streaming output emits one valid JSON record per task
- Caller runs `g8s supervisor-metrics --json-stream`.
- Each matching supervisor task emits a complete, newline-delimited JSON object to stdout.

**Implementation Status**: ACCEPTED + IMPLEMENTING (T021)

## MODIFIED Requirements

None. This delta adds new code without modifying existing specs.

## REMOVED Requirements

None.

## Cross-references

- `docs/designs/supervisor-fix-loop.md` — design doc, source of truth
  for the supervisor's behavior.
- `docs/decisions/0001-supervisor-driven-fix-loop.md` — ADR-0001, the
  decision to adopt this architecture over prompt-injection dispatch.
- `docs/decisions/0002-receipt-evolution-for-supervisor.md` — ADR-0002,
  receipt evolution for supervisor provenance and backward-compatible migration.
- `docs/goals/g8s-mvp-oss/goal.md` — Phase 6 milestone.

## Out of scope

- Process-level worker supervisor (DELTA-09).
- Provider / resource pool (DELTA-05).
- OS daemon service (DELTA-06).
- Provider classes (DELTA-10).

## Transition log

- 2026-08-28: Concern A promoted DRAFT → ACCEPTED (T020). §ADDED A.1-A.5 (the five Requirement: blocks) marked IMPLEMENTING.
- 2026-08-29: Concern B promoted DRAFT → ACCEPTED (T021). §ADDED B.1-B.3 (SupervisorMeta, idempotent migration, NULL tolerance) marked IMPLEMENTING.
- 2026-08-29: Concern C promoted DRAFT → ACCEPTED (T021). §ADDED C.1-C.2 (read-only query contract, aggregate metrics list) marked IMPLEMENTING.

## Change log

- **v0.1.0-draft** (2026-08-28): Initial delta from Concern A/B/C
  design doc.
- **v1.0.0-ACCEPTED** (2026-08-28): Promoted from v0.1.0-draft as part of T020 (g8s-orchestration-roadmap goal). Status DRAFT → ACCEPTED. §ADDED Requirements (six blocks: A supervisor package, B envelope selection, C iteration policy, D RCA+ADR pair, E receipt evidence, F meta-optimizer metrics) marked IMPLEMENTING. No behavioral or scenario changes; promotion reflects owner ratification to begin implementation.
- **v1.1.0-ACCEPTED** (2026-08-29): Added §ADDED B.1-B.3 for Concern B receipt evolution (SupervisorMeta, idempotent schema migration, NULL tolerance contract). Status ACCEPTED + IMPLEMENTING.
- **v1.2.0-ACCEPTED** (2026-08-29): Added §ADDED C.1-C.2 for Concern C read-only meta-optimizer query layer and CLI streaming surface. Status ACCEPTED + IMPLEMENTING.
