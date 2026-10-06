# Task: R2 — Red-cell verification of the effort dispatch adapter (v0.16 gate G1)

Repo: g8s @ main. Hard verification gate (D-01) before the v0.16.0
tag. Your job: falsify the effort adapter's rule matrix and confirm
its fail-safe conventions hold at every boundary. You are the reason
defects do not ship.

Read first (in order):

- internal/routing/effort.go — the adapter (translation + fallback
  convention, hard-refuse rule)
- internal/routing/effort_test.go — existing coverage; do NOT
  duplicate it, target what it misses
- internal/config/catalog.go — catalog loader the adapter resolves
  entries from
- plans/261004-effort-optimization/design.md point 3 — the
  translation + fallback convention the adapter must honor
- internal/routing/router.go — Decision fields the adapter fills

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/routing/redtest_effort_test.go (new file — the only file
  you may create or modify)

Do NOT modify any non-test file. Do NOT run git commit. A case that
fails against current code is a FINDING: mark it
`t.Skip("FINDING-R2-k: <one-line mismatch>")` with a preceding
comment stating expected vs actual, keep the file compiling and
green. Never patch the implementation to make a case pass.

## Required verification dimensions (table-driven)

The adapter rule matrix (verify EACH row independently, then the
interactions):

1. Unknown model entry (nil) → pass-through: applied = requested,
   no mismatch, "" stays "" — EXCEPT baked-suffix model ids (row 4).
2. Requested "" → applied = DefaultEffort when it is a ladder
   level; adaptive/dynamic/empty default → "".
3. Named style: pass-through when requested is in SupportedEfforts;
   otherwise nearest-supported applied AND recorded — minimal index
   distance on the ladder, tie → LOWER level, mismatch = true,
   never an error.
4. Baked-name model ids (suffix -low/-medium/-high/-xhigh/-max —
   amended 2026-10-07 after live finding, issue #568): applied =
   the baked level ALWAYS; requested "" or == baked → no mismatch;
   requested != baked → mismatch = true, recorded, never an error;
   mandatory+none still hard-refuses; budget fields zero.
5. Toggle style: nearest-supported; mismatch exactly when requested
   is outside SupportedEfforts.
6. Budget style: exact key → tokens from EffortBudgetMap; requested
   level without a budget entry → nearest level that HAS one;
   sparse map fallback; mismatch on fallback; tokens surfaced;
   zero-token boundaries.
7. The ONLY hard-refuse: Mandatory == true AND requested == "none"
   → typed error naming provider and model; confirm no other
   combination refuses.
8. Distance/tie edges: requested at both ends of the ladder;
   supported list of length 1; requested equal to default.
9. Catalog merge fail-open: missing catalog, unknown provider,
   unknown model — routing must record and continue, never fail.
10. Decision field mapping: effort_requested / effort_applied /
    mismatch / budget-token fields filled exactly when the adapter
    ran; omitempty behavior on the zero values.

## Constraints

- stdlib only; `go test ./internal/routing/` green;
  `go build ./...` green; gofumpt clean.
- Every table case carries a comment naming the rule row it
  verifies.

## Report

- Number of cases per dimension, pass/skip counts.
- The FINDING list (R2-1, R2-2, ...): rule, expected, actual,
  severity guess (release-blocker / polish).
- If ZERO findings, say so explicitly and list the three strongest
  falsification cases you ran.
