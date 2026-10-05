# Brainstorm: effort-optimization design decisions (issue #550, v0.16)

**Status**: RATIFIED by the operator 2026-10-05 (3/3 decisions via the
brainstorm session). **Session type**: T1 (strategy)

Companion to design.md (the verified cross-platform survey + manifest
schema). This document fixes the four OPEN design questions.

## Q1 — Catalog collection, selection, freshness (3-layer, ratified)

**Selection criteria** (what enters the catalog): a model enters when it
meets ≥1 of — (a) flagship/current-gen of a registered provider, (b) a
DISTINCT effort contract (different supported levels), (c) operator pin.
Exits when the vendor EOLs it (grace period for in-flight tasks). Add/remove
is an operator-ratified diff (law 5).

**Freshness — three layers (stale has nowhere to hide)**:
1. **Discovery-first** (where the industry serves it): OpenRouter
   `GET /models` exposes per-model `supported_efforts` + `default_effort`
   (machine-readable); Ollama `/api/show` per local model. g8s QUERIES
   live at dispatch — the catalog is a cache + fallback, not the source
   of truth.
2. **Research clock** (D-08 pattern): catalog carries `verified_at`;
   a 30-day clock (or pre-release) triggers the 3-agent research pass as
   a task; the diff is operator-ratified.
3. **Failure-driven sensor**: a dispatch error of the class "model/param
   rejected (400)" means the catalog lied — negative-pattern telemetry
   fires a catalog-staleness signal (the sensor class already exists).

## Q2 — Effectiveness measurement, the quality ladder, and HITL (6 rungs, ratified)

**Measurement**: the verifier IS the gauge (S-7) — a task passing its
class checks at the assigned effort = sufficient. The A/B (2026-10-05)
proved quality equivalence on transcription-class; other classes measure
via check-outcomes over rounds.

**The quality ladder** (distinct from ADR-0029 transport retry — two
ladders, two budgets, one lineage):

```
Rung 0: effort per class mapping
  ↓ class checks FAIL
Rung 1..3: effort +1 step (per the model's supported_efforts subset)
  ↓ FAIL
Rung 4..5: model alternative (a different provider serving the same class)
  ↓ FAIL
Rung 6: task resplit (the brief may be the defect — decompose)
  ↓ FAIL
HITL — mandatory, with the full evidence packet:
  ladder history, final verdict, the exact failing checks,
  tokens per rung (the cost receipt of the whole ladder)
```

- Each rung is a NEW task via the existing resubmit mechanics
  (parent_task_id lineage); the quality-ladder cap is **6 rungs/task**,
  separate from the ADR-0029 transport caps (2/task, 10/hour).
- **Early termination**: checks PASS → done. And the failure-shape rule:
  when the verifier reports a NON-effort-shaped failure (brief
  contradiction, missing input, environment) → **HITL immediately** —
  burning rungs on a mis-shaped problem wastes quota.
- **HITL efficiency principle**: the factory exhausts every cheap option
  first; the operator sees only the residue, with evidence, never a raw
  task.

## Q3 — Architectural compatibility (effort is orthogonal, nothing breaks)

| Existing | Interaction | Verdict |
|---|---|---|
| ADR-0024 lanes | lane = WHERE/trust; effort-class = intensity — a THIRD registry in the same pattern (lane-bundles, verifier-classes, effort-classes) | no conflict |
| ADR-0030 router | router picks provider/model/role; effort slots into model selection via the adapter. **Jev does not touch effort in v1** (YAGNI; the "Jev never raises" law is unnecessary if Jev is out of the path) | no conflict |
| ADR-0029 retry | two ladders, two budgets, one lineage: transport-retry (honest-FAILED, same effort) vs quality-ladder (checks-fail, escalating effort) | clean separation |
| Receipts #448 | effort is a request parameter — the write boundary untouched | no conflict |
| ADR-0032 containment | effort is per-attempt, not per-containment-level | no conflict |
| Backward compat | manifest without effort metadata → default medium + recorded; no migration | safe |

## Q4 — Per-purpose effort and the earning mechanism (ratified: earned + operator diff)

**Default hypotheses** (confounded by the all-high campaign history —
treated as hypotheses, not facts):

| Purpose-class | Default | Basis |
|---|---|---|
| research/discovery | high | unknown-unknowns |
| code/feature | high | design + edge cases |
| test-authoring | medium | checklist-shaped |
| validate/verify | medium | evidence matching |
| docs/transcription | low | **A/B-proven: 3.7× faster, equal quality** |

**The earning mechanism — the ladder IS the instrument**: a class whose
tasks repeatedly fail low and pass medium accrues pass-rate telemetry →
the class EARNS a raised default; a class passing consistently low EARNS
a lowered one. Defaults are earned-by-pass-rate, never decreed; every
adjustment is an operator-ratified diff (law 5 consistent). Proven so far:
1 class (transcription). The wave builds the gauge; the numbers accrete.

**Multi-model selection for one class** (v1): the deterministic router
places provider/model as today; the effort knob is the new dimension.
Price-per-token comparison: YAGNI until telemetry is dense enough.

## DoD updates for #550 (superseding the original list)

1. config.File loader (effort metadata) — with the discovery-first layer
   (OpenRouter/Ollama live query) as the freshness source
2. Dispatch adapter (effort_requested → effort_applied, recorded;
   hard-refuse only mandatory+none)
3. `.g8s/effort-classes.yml` + `submit --effort` knob (default medium)
4. Quality-ladder rung machinery (≤6 rungs, lineage-linked, early
   termination on non-effort-shaped failures, HITL evidence packet)
5. Telemetry (class, effort_requested/applied, tokens) + SCORECARD
   cost-per-class row
6. Staleness sensor (400-rejection → catalog signal)
7. A/B transcription-class recorded (done 2026-10-05); per-class pass-rate
   gauge live
