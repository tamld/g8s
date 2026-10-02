# ADR-0030: Context-Based Task Router — Deterministic First, Jev Optional

- **Status**: Accepted (2026-10-02) — operator directive (factory
  program kickoff, plans/261002-factory/plan.md). Two operator
  constraints are binding: (1) the distributor role — orchestrator
  thinks, workers do, **the router distributes** (stream-splitting,
  routing, tool selection by context); (2) **Jev is OPTIONAL** — it
  supports, is measured and benchmarked, never mandatory, because not
  every installation has it.
- **Issue**: factory program M3/M5 metrics (SCORECARD S-3/S-4/S-5).
- **Supersedes**: none. **Depends on**: ADR-0024 (Jev advises,
  deterministic policy decides; no LLM in the hot path), #457/#458
  (providers manifest + claim affinity), #396 (Context Broker
  fail-open assembly), ADR-0021 (typed-advice posture).

## Context

Today every task goes to one default model (`gemini-3.8-flash-high`)
unless the submitter hand-picks. The providers manifest (#457) already
supports many providers/models with claim affinity; the analyzer
already computes blast radius and suggested write scopes; the Context
Broker already assembles enriched, fail-open context packets. What is
missing is the DECISION LAYER that turns context into routing.

The pattern to follow already exists twice in the repo: the reflex
triage gate (internal/reflex/jev.go) always falls back to a
deterministic classifier (`Source: "jev" | "deterministic"`,
`IsFallback`, never errors), and the L3 quality gate is advisory by
default (#411). The router is the third instance of this shape — and
the first whose quality is BENCHMARKED against its own fallback.

## Decision

1. **Two-layer router** (`internal/routing`):
   - **Layer 1 — deterministic (ALWAYS available, zero network)**:
     rules over the enriched request — payload class (docs paths,
     trust paths), blast radius via `analyzer.AnalyzeFileImpact`
     (HIGH/CRITICAL → bigger-context model), size/timeout hints,
     provider availability from the manifest. Produces the decision
     even when nothing else exists. This is the shipping default for
     every installation.
   - **Layer 2 — Jev-assisted (OPTIONAL, benchmark-gated)**: when
     configured (`TYPESAFE_API_KEYS` + router mode), Jev SUGGESTS
     provider/model/role via the typed-question contract; deterministic
     policy VALIDATES the suggestion (unknown provider/model/tool ⇒
     reject the suggestion, keep Layer 1); any failure ⇒ Layer 1 with
     `IsFallback: true`. Every decision records `Source`,
     `IsFallback`, `Reason`, `Confidence` — no silent behavior.
2. **Benchmark as acceptance (the operator's "đo, kiểm chứng, có
   benchmark")**: eval category `task_routing` — the same fixture set
   routed by Layer 1 alone vs Layer 2, scored by the existing PRI
   pipeline with mock providers (CI-runnable). Jev's place in the
   factory is EARNED by this delta (SCORECARD S-4) and re-earned by
   the falsification clocks; it is never assumed.
3. **Injection point**: submit-time `payloadMap` provider/model/role
   (claim affinity then does the rest — #458). Flag `--route
   auto|manual` with default `manual` = byte-identical to today
   (integration lands in Wave G after this ADR's library wave F3).
4. **Scope guards (inherited, restated)**: Jev never assigns Lane S,
   never mints receipts, never edits spec/ or directives.md, and the
   router never invents providers/models outside the manifest.

## Consequences

- Every installation gets correct-if-boring routing for free; Jev
  installations get measured upside, not a dependency.
- The router becomes the factory's distributor seam: Wave G wires it
  into submit/orchestrate; later waves feed it decomposition output
  (the orchestrator's plans) and it answers with concrete routes.
- Falsification: RT-F (fixture routes deterministically, no network,
  fallback parity) gates Wave F; the M3 delta gates Jev's continued
  role; an escape (wrong route causing a real failure) cites the class
  and earns a rule or kills the suggestion path.

## Related

- plans/261002-factory/plan.md + SCORECARD.md (metrics, ladder, waves)
- ADR-0024 (lanes; Jev advise-only), ADR-0021 (typed advice), ADR-0029
  (budgeted retry — the factory's safety floor), #457/#458 (manifest +
  affinity), #396 (Context Broker), internal/reflex/jev.go (the
  fallback pattern this router follows).
