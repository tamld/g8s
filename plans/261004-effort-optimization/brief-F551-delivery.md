# Task: FIX-551 — receipt-scoped writes must survive worktree release (issue #551)

Repo: g8s @ main. Root cause DIAGNOSED (see below) — your job is the fix
+ the red-test that pins it. This is the transport's delivery contract:
a completed task's writes must survive until `g8s deliver` or an explicit
reap, regardless of git tracking status.

## Root cause (diagnosed 2026-10-04, issue #551)

`Pool.Release` (internal/orchestrator/worktree.go:145) preserves a
keep=true worktree ONLY when `worktreeDirty()` (git status --porcelain
non-empty). `.g8s/` is GITIGNORED, so a worktree whose ONLY change is a
receipt-scoped gitignored file (e.g. .g8s/agent-models.yml, written by
tasks 5835836d and 55f99a62) reports CLEAN → removed + branch deleted,
even though the deliverable registration names it. Two consecutive
delivery losses (issue #551).

## Required fix

1. **Preservation must consider receipt-scoped existence, not just git
   status**: extend the release path so a worktree is preserved when
   git-dirty OR when ANY entry of the task's receipt AllowedPaths exists
   on disk under the worktree path (os.Stat per entry — glob entries
   like `./internal/verifier/*` count as protected if the literal prefix
   directory exists; document the rule you implement). Thread the
   allowed paths from the task request (receipt_id → receipts.db lookup
   already exists in the worker path — reuse it; do NOT re-implement
   ledger access).
2. **Call sites**: internal/orchestrator/fanout.go:54 and every other
   Pool.Release caller (grep them) — thread the protected paths through;
   keep the signature change minimal and documented.
3. **Semantics unchanged otherwise**: keep=false removes regardless;
   #477 collision-refusal untouched; forceCleanup (error paths)
   untouched.
4. **Red-test (red-first)**: a worktree whose only change is a
   gitignored receipt-scoped file ⇒ Release(keep=true, protected=[that
   path]) PRESERVES it (before the fix this test fails — the worktree is
   removed); plus: keep=false still removes; git-dirty still preserves;
   protected-path-absent + git-clean still removes.

## Delivery protocol

Scratch worktree. Write ONLY to:

- internal/orchestrator/worktree.go + a new
  internal/orchestrator/worktree_test.go additions (or the existing test
  file if one exists — follow the package convention)
- the call-site files you must thread paths through (orchestrator
  fanout/attempt paths; worker release site if applicable) — smallest
  possible diffs, each justified in the report

Do NOT run git commit. Do NOT touch internal/lessons, internal/routing,
cmd/, docs/.

## Constraints

- stdlib; `go test ./internal/orchestrator/ ./internal/worker/` green
  (plus the new red-tests); `go build ./...`; gofumpt + golangci-lint
  clean; `bash tools/ci_doc_contract_check.sh` green.
- Report: the call sites threaded, the protected-path existence rule you
  implemented (glob handling), and the red-test list with before/after
  verdicts.
