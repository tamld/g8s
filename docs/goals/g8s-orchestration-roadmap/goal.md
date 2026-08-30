# g8s Orchestration Roadmap — Phase 6: Supervisor-Driven Fix Loop

## Objective

Land the three-concern orchestration roadmap that wraps the existing
`internal/orchestrator`, `internal/harness`, and `internal/receipt` with a
**supervisor-driven fix loop**. The supervisor is the Brain; AGY is one worker
backend, never the decision maker. Three deliverables in three slices:

- **Concern A** — `internal/supervisor` package + `g8s orchestrate` CLI
  subcommand: planner, enforcer, reviewer, RCA, escalator, HITL contract.
- **Concern B** — Receipt evolution: extend the receipt schema so the
  supervisor has approach-shift / RCA-confidence / ADR-linkage / attempt-lineage
  data on every worker write.
- **Concern C** — Meta-optimizer: two feedback loops (L1 tunes supervisor
  thinking; L2 tunes worker execution) wired off `g8s supervisor metrics`.

## Original Request

Owner (2026-08-28, Vietnamese, intent preserved): "Tiếp tục hoàn thiện g8s.
Brainstorm + Plan high-level design cho 7 orchestration requirements: (1) AGY
opens issues, (2) AGY fixes code, (3) user reviews code/behavior/results,
(4) judgment board continuous evaluation, (5) supervisor strategy + worker
behavior via receipts, (6) review optimizer for supervisor thinking,
(7) review optimizer for worker execution. AGY chỉ là worker, supervisor là
g8s orchestrate. High-level design doc, không code."

## Intake Summary

- Input shape: `existing_plan` (decomposed from 7 raw requirements into 3
  concerns; mapping preserved in this charter §North Stars).
- Audience: repo owner (tamld), reviews at phase boundaries only.
- Authority: `approved` (full delegated authority inside CWD; publish step
  stays owner-gated).
- Proof type: `metric` (test count + race detector + supervisor-specific
  oracle).
- Completion proof: every concern lands with (1) its OpenSpec delta in
  `APPLIED` state, (2) ≥ 1 dedicated Go test file passing under
  `CGO_ENABLED=0 go test ./...` and `CGO_ENABLED=1 go test -race ./...`, and
  (3) a final audit mapping receipts to the three concerns.
- Goal oracle: see below.
- Likely misfire: dumping 7 requirements into one mega-package; treating
  AGY as the supervisor; binding `internal/orchestrator` Worker interface
  (which must stay unchanged); gold-plating Concern C before Concern A ships.
- Blind spots considered:
  - Existing `internal/orchestrator` Worker interface is frozen — supervisor
    must speak the existing types, not extend them.
  - Receipt schema today (`internal/receipt`) only carries the worker-side
    proof; Concern B needs supervisor-side fields without breaking existing
    receipt verification.
  - Concern C cannot be evaluated without first producing Concern A metrics;
    sequential ordering is enforced by this charter.
  - RCA confidence is heuristic until Concern C tunes it; the `< 0.6`
    safety valve must be honored even with noisy output.
- Existing plan facts:
  - `internal/orchestrator/` ships `Worker` interface, `AgyWorker`,
    `FanOut`, scope-violation detection.
  - `internal/harness/` ships `Role`, `Permission`, `ValidateRequest`,
    `BuildContractPromptWithReceipt`.
  - `internal/receipt/` ships `IssueReceipt`, `ValidateAndConsume`,
    `RevokeReceipt`, `ListActiveReceipts` + `write_receipts` SQLite WAL.
  - `cmd/g8s/main.go` exists; `g8s orchestrate` subcommand is a stub and
    must be wired to `internal/supervisor.Run`.

## North Stars (from owner)

1. **Disciplined worker dispatch** with receipt + harness, not prompt
   injection. The gate is written in Go and tested with `-race`.
2. **g8s as the substrate** — Role, Permission, Receipt, Worktree, FSM are
   typed Go values importable by other tools.
3. **Code is the truth** — every gate is a function with a test; every
   receipt is a SQLite WAL row; every ADR is a versioned file under
   `docs/decisions/`.

## Goal Oracle

The oracle for this goal is:

```
In Docker golang:1.25:
  CGO_ENABLED=0 go vet ./... && go test ./... is fully green
AND
  CGO_ENABLED=1 go test -race ./... is green with zero reports
AND
  Concern A: internal/supervisor/ exists; ./bin/g8s orchestrate "self-test"
             runs the loop end-to-end on a synthetic failing task and emits
             exactly the iteration / RCA / escalation events described in
             docs/designs/supervisor-fix-loop.md §6 + §9.
AND
  Concern B: receipt.IssueReceipt now records approach_idx, attempt_idx,
             rca_confidence, adr_path fields; receipts issued before this
             delta still verify (backward compatibility tested).
AND
  Concern C: ./bin/g8s supervisor metrics --task-id sup-X returns the eight
             metrics listed in docs/designs/supervisor-fix-loop.md §10.
```

The PM must keep comparing task receipts to this oracle. Planning, a
passing Concern A alone, or a clean-looking board is not enough. The goal
finishes only when a final Judge/PM audit maps receipts and verification
back to this oracle and records `full_outcome_complete: true`.

## Concern-to-Deliverable Map

| Concern | Deliverable | New spec | New code (under `internal/`) | New CLI surface | New ADR chain |
| ------- | ----------- | -------- | --------------------------- | --------------- | ------------- |
| **A**   | Supervisor fix loop | DELTA-11 §ADDED A.1–A.5 | `internal/supervisor/` (planner, enforcer, reviewer, rca, escalator, supervisor_metrics) | `g8s orchestrate`, `g8s supervisor metrics` | ADR-0001 (proposed → accepted during T020) |
| **B**   | Receipt evolution | DELTA-11 §ADDED B.1–B.3 (additive, backward-compat) | `internal/receipt/` extension (new columns + migration) | none (existing receipt CLI works unchanged) | ADR-0002 (proposed at T021) |
| **C**   | Meta-optimizer | DELTA-11 §ADDED C.1–C.2 (read-only ingestion) | `internal/supervisor/optimizer.go` (read-only metrics consumer) | `g8s supervisor metrics` (Concern A surfaces the data; Concern C consumes) | ADR-0003 (proposed at T022) |

## Goal Kind

`existing_plan`

## Current Tranche

Sequential execution: land Concern A first (so the supervisor exists and can
emit metrics), then Concern B (so receipts carry the data the supervisor
needs), then Concern C (so the optimizer has something to read). Each
concern produces one bounded vertical slice with its own OpenSpec delta,
its own ADR, and its own green dual-pass gate. No parallel concerns —
Concern C cannot be evaluated without A's metrics; Concern B's new fields
are pointless until A writes them.

## Non-Negotiable Constraints

- **Constitution invariants** carry over from `g8s-mvp-oss` goal (Zero-CGO,
  injectable clock, receipt single-use TTL ≤ 3600s path-scoped, containment
  0600/0700, process groups).
- **Spec-first**: no code before the corresponding OpenSpec delta is
  ACCEPTED. DELTA-11 currently DRAFT; each concern produces a delta section
  that is ACCEPTED → IMPLEMENTING → APPLIED.
- **Frozen Worker interface**: `internal/orchestrator.Worker` MUST NOT change.
  The supervisor speaks the existing surface only.
- **Receipt backward compatibility**: Concern B MUST preserve verification of
  receipts issued before the migration. A migration test is mandatory.
- **All gates run in Docker `golang:1.25`** with named cache volumes
  (`g8s-gomod`, `g8s-gocache`); host stays toolchain-free.
- **100% English** in code, commits, comments, error messages, ADRs.
- **ADR per shift**: every approach shift (3 attempts in same approach
  exhausted) writes a new ADR under `docs/decisions/NNNN-slug.md` referencing
  the prior ADR + the RCA that triggered the shift.
- **HITL escalation contract**: when iteration caps exhaust, supervisor
  emits the JSON digest described in `docs/designs/supervisor-fix-loop.md`
  §9 on stdout and exits non-zero. No silent escalation.
- **No push / no repo visibility changes** without explicit owner approval.

## Stop Rule

Stop only when a final audit proves the full original outcome is complete
(all three concerns APPLIED, dual-pass gate green, receipts map to concerns)
— or when an owner-gated step remains, recorded as a blocked terminal card
with `waiting_for_user_approval: true`.

## Slice Sizing

Safe means bounded, explicit, verified, reversible. It does not mean tiny.
Concern A lands as one vertical package (`internal/supervisor` + one CLI
subcommand + tests) — not split across multiple half-states. Concern B
lands as a backward-compatible receipt schema migration (additive only;
old receipts must still verify). Concern C is a thin read-only consumer of
Concern A metrics; no new persistence layer.

If an exact human approval phrase is the only remaining blocker and no safe
local work remains, ask once and stop. Preserve the exact phrase in the
blocked receipt as `required_reply`, set `waiting_for_user_approval: true`,
set `goal.status: blocked`, and set `active_task: null`.

## Board Health

The PM owns board health:

```bash
node ~/.claude/skills/goal-prep/scripts/check-goal-state.mjs docs/goals/g8s-orchestration-roadmap
```

## Canonical Board

Machine truth lives at `docs/goals/g8s-orchestration-roadmap/state.yaml`. If
this charter and `state.yaml` disagree, `state.yaml` wins.

## Run Command

```text
/goal Follow docs/goals/g8s-orchestration-roadmap/goal.md.
```

## PM Loop

On every `/goal` continuation:

1. Read this charter, and follow the GoalBuddy execution contract
   (`references/goal-execution.md` in the goal-prep skill) when available.
2. Read `state.yaml`.
3. Re-check intake facts and the oracle.
4. Work only on the active board task (one of T020 / T021 / T022).
5. Assign Scout, Judge, Worker, or PM according to the task (in this
   harness the PM executes roles directly, optionally delegating heavy
   exploration to subagents).
6. Write a compact task receipt; update the board.
7. Continue to the next concern unless blocked; review at concern
   boundaries only.
8. Finish only with an audit receipt recording `full_outcome_complete: true`.

## Cross-references

- `spec/openspec/11-orchestration-roadmap-spec.md` — DELTA-11 (DRAFT, this
  goal ACCEPTs it).
- `docs/designs/supervisor-fix-loop.md` — design doc, source of truth for
  the supervisor's behavior (v0.1.0-draft).
- `docs/decisions/0001-supervisor-driven-fix-loop.md` — ADR-0001
  (Proposed → Accepted at T020).
- `internal/orchestrator/`, `internal/harness/`, `internal/receipt/`,
  `internal/controlplane/` — frozen substrates the supervisor builds on.
