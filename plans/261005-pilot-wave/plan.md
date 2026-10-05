# Pilot-first effort wave — the v0.16 execution plan (RATIFIED 2026-10-05)

**Status**: APPROVED by the operator ("duyệt, em làm đi"). **Session type**: T2 (execution)
**Design parents**: plans/261004-effort-optimization/{design.md,brainstorm-rails.md}
(RATIFIED incl. pass-2 amendments — workload-first sequencing, #550) + #442
(offer rollout, falsification metrics M1–M4).

## The one-line mission

Onboard ONE sibling project (first-in-line per data: tamld-llm-wiki,
bounded slice), run REAL dispatches through g8s, and let the effort
machinery measure itself on that workload — cut v0.16.0 when the
cost-per-class row carries real-workload numbers.

## Phases and completion gates (each gate is an artifact, never prose)

### Phase 1 — Pilot bootstrap (workload exists)
- 1.1 `g8s offer init` into the sibling (knowledge profile, pull path)
- 1.2 Sibling AGENTS.md points at the offer onboarding
- **GATE 1.3**: offer files exist in the sibling + offer/SCORECARD row
  scaffold = first M1 (adoption) evidence

### Phase 2 — First real dispatch (M2 starts accruing)
- 2.1 Brief a bounded docs-freshness audit (ONE content directory — never
  the whole 21k-file surface)
- 2.2 Dispatch through g8s: submit → worker → verify (read_only)
- **GATE 2.3**: audit report delivered = M2 evidence (a real catch, or an
  honest no-catch) → second SCORECARD row

### Phase 3 — Effort machinery rides (parallel, non-blocking)
- 3.1 L1: config.File loader (consumes agent-models.v1) → PR
- 3.2 C1: dispatch adapter + `submit --effort` → PR
- 3.3 W2: `.g8s/effort-classes.yml` + token telemetry → PR
- **GATE 3.4**: one REAL sibling dispatch runs E2E with `--effort low` —
  machinery proven on workload, not in-garage

### Phase 4 — Ladder + gauges (the new organ goes live)
- 4.1 Diagnosis rung (failure-shape classification → routes the ladder)
- 4.2 Quality-ladder machinery (≤6 rungs hard ceiling, TOKEN budget)
- 4.3 Gauges live on SCORECARD: pass-rate, escalation-rate, HITL-rate
- **GATE 4.4**: one real failure traverses the ladder per design
  (diagnosis → route → rung → verdict) — the ladder has RUN, not just built

### Phase 5 — Cut v0.16.0
- **GATE 5.1**: cost-per-class row carries real-workload numbers + 8-gate
  release flow + #442 scorecard M1/M2 filled → `release.sh 0.16.0`

## Falsification metrics (from #442, running throughout)

- M1 adoption <2 onboarded in 1 month → simplify onboarding
- M2: 3 first-audits with zero real catches → gates are g8s-specific →
  scope to g8s only
- M3 = 0 contribution packets after 2 months → one-way bundle
- M4: gates removed from any project → investigate before expanding
- Effort-wave specific: audit-sampling diff gauge (P1), escalation-rate
  gauge (P4), HITL-rate ≤1/N rounds (P6) — from brainstorm-rails.md

## Known risks + standing mitigations

| Risk | Mitigation |
|---|---|
| Delivery-loss regression | #553 fix merged + red-tested; pilot = first external consumer |
| Sibling has no CI | that IS the M2 value: gates new = catches new |
| 21k-file surface too wide | bounded: one directory per dispatch; widen only on M2 catch |
| Ladder never ran on real failure | GATE 4.4 requires one real traversal |
| Supervisor worktree-removal class (551 incident 3) | no reap while tasks RUNNING — recorded, red-tested |

## Effort discipline (live from this wave)

Default medium; high only with a recorded justification in the dispatch
prompt/PR body. The pilot's dispatches are the first consumers of the
agent-models.v1 catalog.
