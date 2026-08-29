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

## Change log

- **v0.1.0-draft** (2026-08-28): Initial delta from Concern A/B/C
  design doc.
- **v1.0.0-ACCEPTED** (2026-08-28): Promoted from v0.1.0-draft as part of T020 (g8s-orchestration-roadmap goal). Status DRAFT → ACCEPTED. §ADDED Requirements (six blocks: A supervisor package, B envelope selection, C iteration policy, D RCA+ADR pair, E receipt evidence, F meta-optimizer metrics) marked IMPLEMENTING. No behavioral or scenario changes; promotion reflects owner ratification to begin implementation.
