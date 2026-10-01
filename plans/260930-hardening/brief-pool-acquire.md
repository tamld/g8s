# Task: worktree pool must never destroy a live worktree + evaporation discriminating test

Repo: g8s @ main. Tracking: #465 P1 hardening / delivery-loss campaign
follow-up (the "preserved-then-evaporated" class).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/orchestrator/worktree.go
- internal/orchestrator/worktree_acquire_test.go (new file)
- internal/harness/probe/selfaudit.go (ONLY: extend sa-002's Runner body —
  no other edits)
- internal/harness/probe/selfaudit_test.go (add one case)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
tools/release.sh — touching anything outside the list corrupts their
delivery.

## Context (verified at main)

`Pool.Acquire` (internal/orchestrator/worktree.go:90) computes
`id = "wt-" + shortID()` (8 hex chars from crypto/rand), joins
`wtPath = root/id`, then calls **`_ = os.RemoveAll(wtPath)` UNCONDITIONALLY**
before `gitAddWorktree`. If a pre-existing directory sits at that path, it
is destroyed without any check. A live worktree is recognizable by its
`.git` FILE (worktree marker pointing at the admin dir). This session lost
3 preserved worktrees ("preserved-then-evaporated"); the single worker-side
release site was verified clean (`defer releaseWorktree(true)` only), so
Acquire's unconditional RemoveAll is the last untested in-repo destructor
of that class.

## Required implementation

1. **Refuse to destroy live worktrees in Acquire** (worktree.go): before
   `os.RemoveAll(wtPath)`, if the path exists AND contains a `.git` entry
   (file or dir) — i.e. it is a live worktree or repo — do NOT remove it:
   retry with a fresh shortID instead (loop up to 5 attempts; after that
   return an error "worktree path collision with a live worktree after 5
   attempts: <path>"). Only unregistered leftover debris (no .git marker)
   may be removed, preserving today's cleanup-on-collision behavior.
2. **Discriminating test for the evaporation class**
   (worktree_acquire_test.go): build a temp git repo + Pool; create a
   directory under the pool root named exactly like a future shortID
   collision containing a `.git` file + a deliverable file; pre-seed
   shortID determinism if the code allows injection, otherwise loop
   Acquire up to 200 times asserting EVERY acquired path never equals a
   live-worktree path (the refusal loop guarantees this) and that a
   planted live worktree still exists with its deliverable after all
   acquires. Also assert: a DEBRIS directory (no .git) at a target path IS
   removed (cleanup-on-collision preserved).
3. **Extend sa-002 (worktree-discard probe)** (selfaudit.go Runner body
   only): after the existing Release(keep=true) survival assertion, run the
   full hostile lifecycle — `git worktree prune`, a second Pool with 10
   Acquire/Release cycles, and (if reachable without credentials) a cleanup
   sweep — then re-assert the preserved worktree dir + its deliverable file
   still exist. Keep the probe hermetic (temp dirs, local git only). If any
   step is environment-dependent, skip that step cleanly (t.Skip-equivalent
   behavior in the probe's outcome text).
4. Add the matching selfaudit_test.go case asserting the extended outcome.

## Tests — red first

- worktree_acquire_test.go: live-worktree collision → NOT removed, error
  after retries, deliverable survives; debris collision → removed (old
  behavior for genuine garbage).
- selfaudit_test.go: sa-002 extended run passes on this host (stock bash
  not required — pure Go + git binary).

## Constraints

- stdlib + existing deps; match style; comments only for non-obvious
  constraints.
- `go test ./internal/orchestrator/ ./internal/harness/probe/` and
  `go build ./...` locally green.
