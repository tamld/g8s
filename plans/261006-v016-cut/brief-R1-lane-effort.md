# Task: R1 — Red-cell verification of the effort lane classes + signal model (v0.16 gate G1)

Repo: g8s @ main. Hard verification gate (D-01) before the v0.16.0 tag.
Your job: build falsification tables that stress the effort class
registry and the pass-3 signal model, then report which expectations
the current code falsifies. You are the reason defects do not ship.

Read first (in order):

- internal/lane/effortclass.go — class registry (first match by
  priority wins, unregistered tasks fail open to medium)
- internal/lane/effortsignal.go — pass-3 signal model
- .g8s/effort-classes.yml — the live class config it loads
- internal/lane/effortclass_test.go + effortsignal_test.go +
  coverage_gap_test.go — existing coverage; do NOT duplicate it,
  target what it misses
- docs/decisions/0032-containment-levels.md (F-token vocabulary,
  context only)

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/lane/redtest_effort_lane_test.go (new file — the only
  file you may create or modify)

Do NOT modify any non-test file. Do NOT run git commit. If a
verification case fails against current code, that is a FINDING:
mark it `t.Skip("FINDING-R1-k: <one-line mismatch>")` with a
preceding comment stating expected vs actual, and keep the file
compiling and green. Never patch the implementation to make a case
pass.

## Required verification dimensions (table-driven)

1. Class registry ordering: two classes whose path globs both match
   one path — confirm the higher-priority (lower number) wins;
   equal priorities; priorities at 0 and negative-int boundary.
2. Fail-open floor: a path matching NO class resolves to medium;
   empty class list; nil/missing config file.
3. Glob semantics: patterns from effort-classes.yml against
   near-miss paths (`plans/261006-v016-cut/brief-R1-lane-effort.md`
   matches `brief-R[0-9]*`? `brief-RC1` vs `brief-R1`; nested dir
   depth; trailing slash). Note: the repo pins path-matching
   semantics deliberately — flag any use of filepath.Match that is
   case-insensitive on Windows and state which oracle covers it.
4. Effort level validation: class entries with unknown
   default_effort strings; empty string; a level not on the ladder.
5. Signal model (effortsignal.go): declared vs observed signal
   handling per the pass-3 model as documented in the file's own
   doc comments — zero-signal defaults, repeated signals,
   contradictory declared/observed pairs, boundary values of any
   numeric weight/rate fields (0, 1, max, negative where the type
   allows), and the realignment rule.
6. Schema handling: wrong schema_version string; forward version;
   missing schema_version.

## Constraints

- stdlib only; `go test ./internal/lane/` green; `go build ./...`
  green; gofumpt clean.
- Every table case carries a comment naming the rule it verifies.
- Coverage target: every exported function of both files touched by
  at least one falsification case.

## Report

- Number of cases per dimension, pass/skip counts.
- The FINDING list (R1-1, R1-2, ...): one line each — rule,
  expected, actual, severity guess (release-blocker / polish).
- If you find ZERO findings, say so explicitly and list the three
  strongest falsification cases you ran.
