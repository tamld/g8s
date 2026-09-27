# Plan: S6 — Gate Lane Routing Implementation (ADR-0024)

**Session type**: T2 (execution)

```yaml
issue: 420
session_type: T2 (execution)
slice: S6 (post-v0.12.0 enforcement slice)
ratified_by: operator brainstorm 2026-09-27 (3-round: lanes → P0 define → lifecycle)
adr: docs/decisions/0024-gate-lane-routing.md
design: docs/designs/gate-lane-routing.md
```

## Objective

Turn ALDC's prompt-level enforcement into machine-checkable gates across three layers: deterministic lane router with per-lane gate bundles, Jev-assisted ambiguity resolution, P0 trust-boundary lifecycle (deny-all/allow-some/self-learning), and a derived deep-audit worker team.

## Slices

| Slice | Gates/Components | Layer | Notes |
|---|---|---|---|
| S6-1 | **G1** spec↔code sync (DELTA scenario → test mapping) + **G2** structure regen-diff (generator `tools/gen_structure.sh`, markers in README) | CI + tools | Highest campaign value: catches DELTA-22-style drift + README rot |
| S6-2 | **G3** link integrity (README + docs/) + **G4** coverage ratchet (≥ prev-main − 0.25pt) | CI | Needs a coverage artifact handoff between runs |
| S6-3 | **G5** PR contract (Closes #N / label) + **G6** ledger-by-size (>200-line feat/fix references a plan ledger) + Layer 1 router + lane-bundles config + Layer 2 Jev assist | CI + cmd | Policy sign-off: G6 threshold; Lane S confirm UX |
| S6-4 | Layer 3 P0 registry (`trust-boundaries.yml` seeded from ADR-0024) + blast-radius matcher + escalation telemetry + learning-loop review ritual | CI + cmd | Depends on S6-1..3 |
| S6-5 | Layer 4 audit fan-out runner (6-ish dimension workers via #394 drain, read_only verifier packets, supervisor join) + autopilot cadence | cmd + plans | Depends on S6-4 |

## Red-test contract

Each gate lands RED-first with a fixture that violates exactly that gate:
- G1: a delta scenario with no mapping test → FAIL.
- G2: README tree edited by hand vs generator output → FAIL (diff non-empty).
- G3: a dead markdown link → FAIL.
- G4: coverage 0.3pt below prev-main → FAIL; within ratchet → PASS.
- G5: >200-line feat PR without ledger reference → FAIL (warning <200).
- G6 fixture: router rules table → lane mapping table-driven (docs→D, feat→F, trust-path→S).

## Non-goals

- No LLM in the Layer 1 hot path (deterministic only).
- No gate for docs prose quality (independent review stays human/agent).
- No amendment to the constitution — P0 enforces existing axioms.

## Verification

Every gate cites ≥1 real campaign bug it would have caught (see ADR-0024). Falsification: zero attributable catches across one campaign → simplify or remove by amendment. Each slice runs the full ALDC cycle (RED-first, dual-pass CI, pre-push 12/12, review).
