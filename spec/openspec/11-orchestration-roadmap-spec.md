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

### Requirement: AIC contract defines automated review surface via orchestrate-aic (§ADDED 18.1)

`g8s orchestrate-aic` SHALL accept `--pr <number>` (required, positive integer)
and `--intent <text>` (required, non-empty text), along with optional flags
`--model` (defaults to `gemini-3.7-flash-high`) and `--json` (defaults to `true`).
The wrapper SHALL fetch the PR diff via `gh pr diff <number>`, compose the diff
with user intent, and delegate execution to `g8s orchestrate --from-intent`.
The command SHALL emit the structured JSON envelope to stdout for automated
review ingestion.

#### Scenario: AIC dispatches PR review via orchestrate-aic
- Operator or CI runs `g8s orchestrate-aic --pr 100 --intent "review security changes" --json`.
- `gh pr diff 100` diff is captured and concatenated with intent prompt.
- `g8s orchestrate --from-intent` runs and emits JSON envelope containing `supervisor_task_id`, `outcome`, `sub_tasks`, and `receipt_summary`.

#### Scenario: missing PR or intent exits with usage error
- `g8s orchestrate-aic --pr 0` or missing `--intent` exits with code 2 and usage diagnostic.

**Implementation Status**: IMPLEMENTED (T022/DELTA-18)

### Requirement: Orchestration from intent maps free-text to FanOut sub-tasks (§ADDED 18.2)

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

## Change log

- **v0.1.0-draft** (2026-08-28): Initial delta from Concern A/B/C
  design doc.
- **v1.0.0-ACCEPTED** (2026-08-28): Promoted from v0.1.0-draft as part of T020 (g8s-orchestration-roadmap goal). Status DRAFT → ACCEPTED. §ADDED Requirements (six blocks: A supervisor package, B envelope selection, C iteration policy, D RCA+ADR pair, E receipt evidence, F meta-optimizer metrics) marked IMPLEMENTING. No behavioral or scenario changes; promotion reflects owner ratification to begin implementation.
- **v1.1.0-ACCEPTED** (2026-08-29): Added DELTA-18 requirements for AIC automated review integration (`g8s orchestrate-aic`) and `--from-intent` / `--from-file` sub-task FanOut orchestration (§ADDED 18.1, §ADDED 18.2). Marked IMPLEMENTED.

