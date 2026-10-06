# Task: R3 — Red-cell verification of the ladder machinery (v0.16 gate G1)

Repo: g8s @ main. Hard verification gate (D-01) before the v0.16.0
tag. Your job: falsify the ladder rung state machine, evidence
handling, gauges math, and policy thresholds. You are the reason
defects do not ship.

Read first (in order):

- internal/ladder/ladder.go — rung lineage and next-rung planning
- internal/ladder/classifier.go — rung classification
- internal/ladder/evidence.go — HITL evidence packets
- internal/ladder/gauges.go — pass-rate / escalation-rate / HITL
  metrics
- internal/ladder/policy.go — policy thresholds and HITL routing
- the four existing *_test.go files — existing coverage; do NOT
  duplicate it, target what it misses

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/ladder/redtest_ladder_test.go (new file — the only file
  you may create or modify)

Do NOT modify any non-test file. Do NOT run git commit. A case that
fails against current code is a FINDING: mark it
`t.Skip("FINDING-R3-k: <one-line mismatch>")` with a preceding
comment stating expected vs actual, keep the file compiling and
green. Never patch the implementation to make a case pass.

## Required verification dimensions (table-driven)

1. Rung state machine: legal advance sequences; double-advance of
   the same rung; advancing a completed task; lineage integrity
   after multiple rungs (parent chain cannot fork or skip).
2. Next-rung plan: empty lineage; single rung; the documented
   ordering guarantee; rung equal to the top of the ladder.
3. Classifier: boundary inputs per the classifier's own doc
   comments; unknown/empty task shapes; class collision (two
   classes matching) resolution.
4. Evidence packets: packet with missing required fields; empty
   evidence; out-file write path validation (reject path escapes);
   duplicate evidence ids.
5. Gauges math: empty sets (division-by-zero guard); single
   element; all-pass and all-fail; counter going backwards
   (monotonicity guard); rate values stay within [0,1]; class
   filter with unknown class.
6. Policy thresholds: values exactly at a threshold (inclusive vs
   exclusive per the documented rule); thresholds crossed from both
   sides; HITL routing fires exactly when the policy says so and
   never silently when disabled.
7. Interaction: gauge-driven policy decision feeding the next-rung
   plan — confirm the composition matches the documented flow in
   docs/user-guide/routing.md or the package's own README/comments
   (cite which).

## Constraints

- stdlib only; `go test ./internal/ladder/` green;
  `go build ./...` green; gofumpt clean.
- Every table case carries a comment naming the rule it verifies.

## Report

- Number of cases per dimension, pass/skip counts.
- The FINDING list (R3-1, R3-2, ...): rule, expected, actual,
  severity guess (release-blocker / polish).
- If ZERO findings, say so explicitly and list the three strongest
  falsification cases you ran.
