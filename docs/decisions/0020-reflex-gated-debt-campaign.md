# ADR-0020: Reflex-Gated Technical-Debt Campaign

**Status**: Accepted
**Date**: 2026-09-25
**Deciders**: Brain supervisor (main agent), operator-directed
**Related**: Issue #319, #320, #321; `internal/reflex` (Jev sensor); docs/decisions/0019 (unified memory facade, open PR #325)

---

## Context

The 2026-09-25 debt campaign runs g8s against itself (dogfood) while closing the
open `debt`-labeled issues. Two governance risks emerged immediately:

1. Agent-driven mutations (even small test/CI fixes) had no uniform risk gate,
   so every change relied on ad-hoc judgment.
2. Deep-diving individual tasks threatened to fragment the campaign and lose
   the roadmap-level goal (v0.3.0 sprint readiness).

`internal/reflex` already implements a System-1 risk sensor (Jev via
`api.typesafe.ai/v1/systemone`) with a deterministic supervisor policy engine,
but the package had no callers: it was live code with zero integration.

## Decision

1. **Every mutation in this campaign is triaged through the ReflexGate
   *before* it is applied.** The planned file set, a one-line diff summary, and
   allowed-path globs are submitted to `ReflexGate.TriageMutation` and the
   verdict is recorded in the campaign ledger (`plans/260925-debt-campaign/plan.md`).
2. **Verdict semantics for this campaign**:
   - `instant_kill` (breach ≥ 0.70) — mutation is abandoned; recorded verbatim.
   - `escalate_hitl` — the supervisor (main agent) adjudicates: proceed only
     when risk < 1.0 and breach < 0.20, pairing the mutation with an enhanced
     verification step (focused tests + dual-pass gates). Adjudication and
     rationale are recorded in the ledger.
   - `grant_receipt` — proceed directly.
3. **The observed Jev calibration gap is issue-tracked, not worked around**:
   live Jev responses currently carry `confidence = 0`, which makes the
   `grant_receipt` fast-path (Rule 2 requires confidence ≥ 0.80) unreachable;
   every live verdict lands on `escalate_hitl`. Rule 1 (kill) remains fully
   effective because it does not consult confidence. See campaign issue for
   the calibration fix proposal.

## Consequences

- The campaign produces a per-mutation, auditable risk trail (sensor verdict +
  adjudication + verification evidence) at negligible latency (~0.7–0.8 s per
  triage, measured live).
- `internal/reflex` gains its first production caller pattern, validating the
  decoupled sensor/policy design from `plans/260923-2255-reflex-sensor-decoupling`.
- The fast-path remains dead until the confidence calibration issue is fixed;
  until then the campaign runs on "escalate → adjudicate → enhanced verify".
- Rejected alternative: wiring the gate into the CLI dispatch path now. That
  requires an OpenSpec delta and runtime coupling; it is deferred until the
  calibration gap is closed.

## Re-audit note

Issues #319/#320 were audited at commit `7bf11ca`; re-verification against
`origin/main` (`b503108`) showed partial resolution already merged. The
campaign ledger records the per-issue residual deltas; issues are closed or
kept open strictly against the re-audit, not the original audit.
