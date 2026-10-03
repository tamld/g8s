# Task: Wave G2 — gate-lane router in code, ALDC Layer 1 (issue #514, ADR-0024 S6-3, SCORECARD S-6)

Repo: g8s @ main. Designs (read both first):
docs/decisions/0024-gate-lane-routing.md (ratified) +
docs/designs/gate-lane-routing.md (full spec: lane table at :32-37,
Jev-assist at :41-43). Long-planned, never coded — you are the first
implementation. Composability note: lanes constrain WHERE (paths,
trust); the context router (internal/routing, not yours) decides WHO.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/lane/lane.go (new package)
- internal/lane/lane_test.go (new)
- internal/telemetry/ — ONLY IF the Jev-suggestion telemetry event
  needs an event-type constant (smallest possible diff; justify)
- .g8s/lane-bundles.yml — ONLY if the design requires a field the
  current file lacks (prefer consuming as-is; justify any edit)

Do NOT run git commit. Do NOT touch cmd/, internal/routing/,
internal/controlplane/, docs/.

## Required implementation

1. **Lane enum + router**: Lanes 0 (hotfix), D (docs), R (refactor),
   F (feature, default), S (security). Input: a planned change =
   {paths []string, summary string}. Rules from lane-bundles.yml
   (consume the existing tracked file's structure — read it first):
   docs-paths → D; trust-boundary paths (the P0 registry in
   .g8s/trust-boundaries.yml) → S; explicit hotfix marker → 0;
   everything else → F (or R when the summary/paths declare a pure
   refactor — document the rule you implement).
2. **Deterministic hot path**: NO network, NO LLM. Table-driven red-
   first tests (the plan.md:36 contract): docs→D, feat→F,
   trust-path→S, mixed docs+code→F, unknown→F, empty→F.
3. **Jev-assisted ambiguity hook** (Layer 2 per the design): an
   OPTIONAL suggestion input — `Suggest(lane.Input, suggestion Lane,
   source string) (Lane, bool)` — validates the suggestion against
   the same bundles; **Lane S from a suggestion is ALWAYS refused**
   (design :43); the caller records (suggestion, source, decision)
   via a telemetry event if the telemetry package exposes a suitable
   generic event — otherwise return the decision and let the CALLER
   record (prefer this; smallest diff).
4. **Deny-by-default**: unparseable bundles / missing file ⇒ all
   lanes fall back to F EXCEPT trust-paths still route S (the trust
   registry is the safety floor — it must work even when bundles are
   broken; test it).

## Constraints

- stdlib; match internal/ package style; no cmd changes; no schema.
- `go test ./internal/lane/` green; build green.
- Report: the rule table implemented, the bundles fields consumed,
  the Lane-S-invariant tests, and anything in the 2026-09-27 design
  you had to interpret (with reasoning).
