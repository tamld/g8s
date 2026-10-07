# Task: F4 — gauges math hardening (R3 findings R3-9/R3-10/R3-11)

Repo: g8s @ main. The ladder red-cell (task 885a9abd) proved three
metric-correctness holes in the gauges the v0.16 release notes cite.
Fix with red-first tests. Read first:

- internal/ladder/gauges.go — ComputeGauges
- internal/ladder/redtest_ladder_test.go — FINDING-R3-9/10/11 skips
  (expected vs actual documented in the rows)

## Required fixes

1. R3-9: ComputeGauges validates token counter monotonicity — events
   that would move a cumulative counter backwards are rejected (skip
   the event, keep the counter; do not panic, do not go negative).
2. R3-10: HITL Rate stays within [0,1] — multiple child rungs emitting
   HITL events for the same root task must count ONCE per root (dedup
   by root task id), not once per event.
3. R3-11: ComputeGauges with a class filter that matches nothing
   returns empty per-class sections and TotalRounds=0 — never leak
   unfiltered events into the totals.

Do NOT change: EvaluateNextRung, evidence packets, WriteToFile (a
sibling task owns those), or any cmd/ file.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/ladder/gauges.go
- internal/ladder/redtest_ladder_test.go (flip the three rows from
  skip to real assertions; add boundary rows: exactly-at-1.0 rate,
  empty input, single root multi-HITL)

Do NOT run git commit.

## Constraints

- stdlib; `go test ./internal/ladder/` green; `go build ./...` green;
  gofumpt clean; `golangci-lint run ./internal/ladder/...` clean.

## Report

- Per finding: the fix, the dedup/rejection rule, rows flipped.
- Full `go test ./internal/ladder/` output tail.
