# Design: Gate Lane Routing & Trust-Boundary Lifecycle (S6)

**Status**: Proposed (brainstorm consensus 2026-09-27)
**Related**: ADR-0024, #415 (process lifecycle), #396 (Context Broker), #395 (memory gate), plans/260927-s6-gate-lanes/plan.md

---

## Problem

Two failure classes observed across the v0.12.0 campaign:

1. **Claims rot**: README's Project Structure and counts drifted (claimed 38 packages / 820 tests; reality 45 / 988). Documentation-by-prose decays every PR.
2. **ALDC enforcement is prompt-level**: the process (RED-first, spec-code sync, ledger discipline) lives in agents' heads and skill prose. Evidence of gaps: DELTA-22 spec promised a flag the code never implemented; a spec scenario was unimplementable against the architecture; coverage regressed 79.51% under the 80% floor's nose; two divergent CHANGELOGs split the release history.

Uniform maximum enforcement is not the answer — every PR at effort MAX is overthinking, and review fatigue becomes the next gap. The ratified direction: **lanes** (gate bundles scaled by change class), **routing** (deterministic-first, Jev-assisted on ambiguity, supervisor-owned P0), and a **periodic deep-audit worker team** whose size derives from a registry, not assumption.

## Evaluated Approaches

| Approach | Verdict | Why |
|---|---|---|
| A. Uniform gates on every PR | Rejected | Overthinking inverted — docs-only PRs pay feature-price; review fatigue normalizes bypass. |
| B. Author-declared lanes + CI spot-check | Rejected (primary) | Trusting the author to pick their own gate intensity is the exact hole the gates exist to close. |
| C. **Deterministic router + Jev-assisted ambiguity + supervisor-owned P0 exceptions + derived audit team** | **Chosen** | Deterministic where machines are right; Jev where context matters; humans where trust boundaries move. |

## Final Design

### Layer 1 — Lane router (every PR, deterministic)

A rules table (`paths` + `size` + `labels` → lane) evaluates every PR. No LLM in the hot path.

| Lane | Trigger (default) | Gate bundle |
|---|---|---|
| **0 · hotfix** | author request + supervisor approval | build + targeted tests; **retro-gate mandatory**: full Lane F bundle re-runs post-hoc (telemetry-tagged — the escape hatch has a trail) |
| **D · docs** | paths under `docs/`, `*.md` only | G3 link integrity, G2 structure regen, doc-contract gate |
| **R · refactor** | no behavior change declared, mechanical | G2, G4 ratchet, G5 PR contract |
| **F · feature** | default for `feat`/`fix` | G1 spec↔code sync, G2, G4, G5, G6 ledger-by-size |
| **S · security** | change touches a declared trust boundary | **all gates** + security-redaction scan + independent review mandatory |

Bundles live in one machine-readable config (`.g8s/lane-bundles.yml`) — adding a gate to a lane is a one-line, reviewable change.

### Layer 2 — Ambiguity resolution (Jev-assisted)

When router rules cannot classify (mixed paths, unclear behavior intent): Jev classifies with context (ContextPacket from #396 + memory labels from #395) → **suggests** a lane → telemetry records the suggestion → the supervisor confirms with one keystroke. Per ADR-0021: Jev advises, deterministic policy decides. Lane S is never Jev-assigned.

### Layer 3 — P0 lifecycle (deny-all, allow-some, self-learning)

P0 is not a judgment call — it is a **machine match against declared trust boundaries** (`.g8s/trust-boundaries.yml`), computed by the blast-radius analyzer (DELTA-20) over the paths/symbols of the constitution's axioms: receipts (Axiom 2), containment (Axiom 4), self-description (Axiom 5), auth/authz, poison surfaces (memory promotion gate, dispatch sanitizer).

```
PR ──► machine match ──► MATCH ──► P0 (supervisor lane, all gates, manual review)
                 └──► NO MATCH ──► P1/P2 (Jev-routed lanes)
Jev/supervisor may ESCALATE a non-match to P0 (recorded: who, why, paths).
Machine-matched P0 is never downgraded by a single actor (deny-all).
Escalation history is reviewed at campaign close; a repeated pattern
(≥2 escalations of the same shape) is promoted — via PR + gates — into
trust-boundaries.yml. The machine learns from the supervisor; neither
bypasses the other.
```

Falsification (ADR-0023 style): if a full campaign produces no promoted rules AND no catches attributable to P0, the lifecycle is ceremony — simplify it away by amendment.

### Layer 4 — Deep-audit worker team (periodic, derived size)

At campaign close and on autopilot cadence: a fan-out of read-only verifier workers, one packet per audit dimension, fresh context each, binary oracles, supervisor joins (per `skills/g8s-supervisor/references/multi-worker-fanout.md`). **Team size = |blast radius of the period's changes ∩ audit dimension registry|** — dimensions are declared (docs-freshness, spec↔code sync, test-health, security-redaction, lifecycle, evidence); each declares its oracle and scope. The number is derived, not assumed.

## Implementation Considerations & Risks

- **Misroute risk**: the router is deterministic; a wrong rule skips gates silently. Mitigation: Layer 3 audits sample PRs against their lane (an auditor checks "was Lane R actually a refactor?"); misroute patterns promote router-rule fixes.
- **Bureaucracy risk**: G6's size threshold (~200 changed lines) keeps small fixes light; hotfix lane keeps emergencies fast; every gate has the falsification clause.
- **Cost risk**: Layer 3 runs per-campaign, not per-PR; auditor packets are bounded (files ≤ N, minutes ≤ M).
- **Dependency**: Layer 2 needs #396 ContextPacket + #395 memory labels (both merged). Layer 3 P0 matching needs the blast-radius analyzer (DELTA-20, merged).

## Success Metrics & Validation

1. Every gate must cite ≥1 campaign bug it would have caught (G1: DELTA-22 flag gap; G2: README counts; G3: README link audit; G4: coverage 79.51%; G5: #398/#410 issue-close split; G6: multi-slice PRs like #405).
2. Falsification: any gate with zero catches after one campaign → simplify or remove by amendment.
3. Supervisor time on P1/P2 PRs must trend down (lane bundles shrink review surface); P0 handling time is bounded and tracked.

## Next Steps

S6-1: G1 spec↔code sync gate + G2 structure regen gate (includes the generator). S6-2: G3 link integrity + G4 coverage ratchet. S6-3: G5 PR contract + G6 ledger-by-size (policy sign-off). Tracked in the S6 issue; each slice runs the full ALDC cycle.

---

## Addendum — Distribution model: pull-based bundle (2026-09-27, ratified)

The offer bundle ships in-repo (`offer/`): per-type profiles (knowledge /
security / infra / utility) + a self-service onboarding guide. Distribution
is **pull-based**: each sibling project's main agent adopts at its own
pace — nothing is pushed, no cross-project control plane exists (single-
tenant non-goal holds).

The learning loop closes via the existing Mode-3 contribution flow
(sanitized packets → PR), not telemetry harvesting: projects contribute
operational evidence (gate catches, PRI scores, audit findings) as
reviewable packets. Cross-project pattern promotion follows the same
falsification clause: two onboarding cycles without a cross-project
pattern improving g8s → the bundle simplifies to profiles-only.
