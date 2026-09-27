# ADR-0024: Gate Lane Routing — Deterministic Lanes, Jev-Assisted Ambiguity, Supervisor-Owned Trust Exceptions

**Status**: Proposed
**Date**: 2026-09-27
**Deciders**: TamLD (brainstorm 2026-09-27, 3-round consensus)
**Related**: #415 (process lifecycle audit), #418 (L3 enrichment), ADR-0021 (SOM — Jev advises, policy decides), ADR-0023 (falsification pattern), #396 (Context Broker), #395 (memory gate), DELTA-20 (blast-radius analyzer), `docs/designs/gate-lane-routing.md` (full design)

---

## Context

ALDC's enforcement is prompt-level: the process lives in skill prose and agent memory. Campaign evidence (v0.12.0) shows the gaps are real, not hypothetical: a DELTA-22 spec promised a flag the code never implemented; a spec scenario was unimplementable against the architecture; aggregate coverage regressed to 79.51% under an 80% floor; README structure and counts drifted within one campaign; two divergent CHANGELOGs split the release history.

Uniform maximum enforcement fails differently: docs-only PRs paying feature-price normalizes gate fatigue, and gate fatigue becomes the next gap.

## Decision

Three-layer enforcement, ratified by brainstorm consensus:

1. **Deterministic lane router (every PR)** — a rules table maps paths/size/labels to lanes; each lane carries a gate bundle (Lane 0 hotfix → Lane D docs → Lane R refactor → Lane F feature → Lane S security). No LLM in the hot path. Bundles live in `.g8s/lane-bundles.yml`.
2. **Jev-assisted ambiguity (on demand)** — when rules cannot classify, Jev (with ContextPacket + memory labels from #395/#396) *suggests* a lane; the suggestion is telemetry-recorded; the supervisor confirms. Lane S is never Jev-assigned. Per ADR-0021: Jev advises, deterministic policy decides.
3. **P0 trust-boundary lifecycle (deny-all, allow-some, self-learning)** — P0 is a machine match against declared trust boundaries (`.g8s/trust-boundaries.yml`: receipt surfaces, containment, self-description, auth/authz, poison surfaces — the constitution's axioms as paths/symbols, computed via the blast-radius analyzer). Machine-matched P0 is never downgraded by a single actor. The supervisor may *escalate* a non-match to P0; every escalation is telemetry-recorded, and a repeated pattern (≥2 of the same shape) is promoted into `trust-boundaries.yml` via PR + gates. **The machine learns from the supervisor; the supervisor never bypasses the gate.**
4. **Derived deep-audit team (periodic)** — at campaign close and autopilot cadence, a fan-out of read-only verifier workers audits N declared dimensions (docs-freshness, spec↔code sync, test-health, security-redaction, lifecycle, evidence). Team size = |blast radius ∩ dimension registry| — derived, not assumed. One packet per worker; workers never review each other; the supervisor joins.

## Consequences

- Gate intensity scales with change class — small fixes stay light, trust-boundary changes get everything.
- ALDC's soft process becomes machine-checkable; "the supervisor must remember everything" is replaced by "the checklist rides in the packet".
- New moving parts: lane-bundles config, trust-boundaries registry, router, four new CI checks (spec↔code sync, structure regen-diff, link integrity, coverage ratchet), a PR-contract check, and the audit fan-out runner.
- Costs: router maintenance (rules change with the tree — but rules live next to the code they gate), auditor token spend at campaign cadence, and the discipline to keep dimension oracles binary.

## Falsification

Every gate must cite at least one real campaign bug it would have caught (they all do today — see the design doc). After one full campaign with zero attributable catches, a gate is simplified or removed by amendment. The learning loop with zero promoted rules across a campaign downgrades to quarterly review.

## Status of Components

| Component | State |
|---|---|
| Layer 1 router + bundles | To build (S6-1) |
| Layer 2 Jev assist | To build (S6-2) — depends on #396/#395 (merged) |
| Layer 3 P0 lifecycle + registry | Design ratified; registry seeded from this ADR's trust-boundary set |
| Layer 4 audit team | To build (S6-3) — runner reuses the #394 concurrent drain + #417 sweep machinery |
