# Task: F3 — ladder integrity hardening (R3 findings R3-1/R3-3/R3-6/R3-7)

Repo: g8s @ main. The ladder red-cell (task 885a9abd, suite
internal/ladder/redtest_ladder_test.go) proved four integrity holes.
Fix them with red-first tests. Read first:

- internal/ladder/policy.go — EvaluateNextRung (the lineage gate)
- internal/ladder/evidence.go — BuildEvidencePacket + WriteToFile
- internal/ladder/redtest_ladder_test.go — the failing rows
  (FINDING-R3-1/3/6/7 skips describe expected vs actual)

## Required fixes

1. R3-1: EvaluateNextRung refuses a history containing duplicate rung
   indices (double-advance) — return a plan with Action == "" and a
   RefusalError naming the duplicated index (or the package's
   existing refusal convention — follow the neighbors).
2. R3-3: EvaluateNextRung validates sequence continuity — rung indices
   must advance without gaps from the first entry; a skipped index is
   refused the same way.
3. R3-6: BuildEvidencePacket validates required fields (RootTaskID,
   Class, FinalVerdict per the finding) — typed error, not a
   silently-partial packet.
4. R3-7: WriteToFile validates the destination path — refuse escapes
   outside the packet's declared root/scope and refused fragments
   (follow the harness scope convention; the CLI --out flag is
   operator-supplied but the function must still refuse traversal).

Do NOT change: the rung ordering policy itself, HITL routing, gauge
math (a sibling task owns R3-9/10/11), or any cmd/ file.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/ladder/policy.go
- internal/ladder/evidence.go
- internal/ladder/redtest_ladder_test.go (un-skip the four rows by
  replacing the skip with real assertions; add any missing rows)

Do NOT run git commit.

## Constraints

- stdlib; `go test ./internal/ladder/` green; `go build ./...` green;
  gofumpt clean; `golangci-lint run ./internal/ladder/...` clean.
- Red-first: the four rows must fail against the unpatched logic when
  you start (verify by reading the current behavior, do not run git
  history games).

## Report

- Per finding: the guard added, the error type/message, test rows
  flipped from skip to pass.
- Full `go test ./internal/ladder/` output tail.
