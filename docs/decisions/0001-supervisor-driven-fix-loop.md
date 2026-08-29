# ADR-0001: Supervisor-driven fix loop with code-level enforcement

> **Status**: Accepted (2026-08-28, ratified by repo owner as part of T020 — see docs/goals/g8s-orchestration-roadmap/)
> **Date**: 2026-08-28
> **Deciders**: tamld (owner), g8s supervisor (advisor)
> **SSoT**: `docs/designs/supervisor-fix-loop.md`
> **Implements**: DELTA-11 orchestration-roadmap
> **Supersedes**: none

## Context

g8s ships a working process-level worker supervisor (`internal/orchestrator`, DELTA-09) that fans out worker subprocesses and harvests receipts. It does not decide **what** to fix, **when** to fix it, or **whether** to retry. Every fix loop today is hand-driven by a human via CLI or issues.

Two paths were considered for the next layer:

1. **Prompt-injected orchestration** — the supervisor writes natural-language rules into the worker's prompt and trusts the model to follow them. Common in "skill dispatch / orchestrator" tools. No code-level gate.
2. **Code-level orchestration** — the supervisor calls typed Go contracts (`internal/harness`, `internal/receipt`, `internal/orchestrator`) and treats worker receipts as the only authoritative signal. The model has no path around the gate.

## Decision

Adopt **code-level orchestration**. The supervisor sits above `internal/orchestrator` and uses the existing harness, receipt, and diff-based scope enforcement. It does not add prompt injection; it adds planning, RCA, ADR, and escalation.

The supervisor:

- Selects a measurable task envelope (`DoR`, `DoD`, `DnD`, `Validateds`, plus optional `SRS`, `PRD`, `FSM`) based on task class. The selection is itself measurable (envelope_score).
- Bounds iterations to 3 attempts × 3 approaches = 9 total, then escalates to HITL.
- Produces a Root Cause Analysis (RCA) on every approach shift. If RCA confidence < 0.6, it pauses for `NEEDS_INFO` instead of shifting.
- Writes an Architecture Decision Record (ADR) under `docs/decisions/` for every approach shift.
- Trusts only the receipt (`internal/receipt` rows, `internal/orchestrator.FanOut` diff scope, `internal/harness.ValidateRequest`) — never the worker's prose.

## Rationale

Three north stars drove this:

1. **Disciplined worker dispatch** — receipts and harness replace prompt rules. The model cannot bypass Go code or SQLite WAL.
2. **g8s as the substrate** — Role, Permission, Receipt, Worktree, FSM are typed Go values importable by other tools. One contract, one source of truth.
3. **Code is the truth** — every gate is a function with a `-race` test. `go test -race ./...` is the contract. The diff between main and the running system is the spec.

A prompt-injected orchestrator (option 1) cannot meet any of these. A model update, jailbreak, or misaligned prompt bypasses prompt rules. They survive only as long as the model cooperates. Code-level gates survive model updates because they are written in Go and tested with `-race`.

The bounded iteration policy (3×3=9) was chosen as the minimum that lets the supervisor demonstrate self-correction (3 attempts per approach for tweaking, 3 approaches for "refactor vs patch vs rewrite") without burning unbounded compute. Concern C (meta-optimizer) tunes these bounds later based on observed success rates.

The `confidence < 0.6` safety valve prevents blind approach shifts when RCA is uncertain. The supervisor pauses for `NEEDS_INFO` rather than committing to a new approach on a guess.

## Consequences

Positive:

- A model update cannot weaken the gates.
- Other tools can import g8s's typed contracts as the substrate.
- Every approach shift produces a versioned ADR — audits are easy.
- Metrics emitted per cycle let the meta-optimizer learn.

Negative:

- More code to write and maintain (`internal/supervisor` package).
- RCA quality depends on the supervisor's structured prompt; until Concern C tunes it, RCA confidence may be noisy.
- 9-attempt cap may be too low for genuinely hard tasks (refactor of large subsystem). HITL must be willing to engage.

Neutral:

- The Worker interface in `internal/orchestrator` is unchanged. Existing `AgyWorker` keeps working.
- The CLI gains new subcommands (`g8s orchestrate`, `g8s supervisor metrics`) but the existing CLI surface is preserved.

## Alternatives considered

### A. Prompt-injected orchestration

Pros: cheap to implement, fast to ship.
Cons: gates are advisory, not enforced. A model update can break the gate. Cannot meet north stars 1–3.

Rejected because the project explicitly chose "code is the truth" as a north star.

### B. External orchestrator (e.g. Temporal, Airflow)

Pros: battle-tested durability.
Cons: g8s would become a worker registry, not a substrate. Adds a heavy external dependency for what is fundamentally a CLI tool with SQLite WAL. Defeats the "code is the truth" north star — Temporal's state is opaque to g8s.

Rejected as overkill for the current scope.

### C. Single-attempt fix with no loop

Pros: simplest possible design.
Cons: every hard task escalates to HITL on first failure. No self-correction. Supervisor never learns.

Rejected because the bounded loop is the minimum that lets the supervisor prove self-correction before HITL.

## Supersedes

None.

## Follow-ups

- DELTA-11 implementation: `internal/supervisor` package + CLI subcommands.
- Concern B (receipt evolution) extends the receipt schema for supervisor-side fields.
- Concern C (meta-optimizer) consumes per-cycle metrics and tunes envelope selection, iteration caps, and RCA confidence thresholds.

## Change log

- **v1** (2026-08-28): Initial proposal. Status: Proposed.

## Ratification

Accepted on 2026-08-28 by the repository owner (tamld) as part of T020 of the g8s-orchestration-roadmap goal. This ratification moves ADR-0001 from Proposed to Accepted and authorizes the implementation work tracked under DELTA-11 (orchestration roadmap spec, also promoted from DRAFT to ACCEPTED on the same date). The decision itself — code-level orchestration over prompt-injected dispatch — is unchanged; only the lifecycle status moves. Subsequent revisions, if any, will be recorded in a new ADR that supersedes this one.
