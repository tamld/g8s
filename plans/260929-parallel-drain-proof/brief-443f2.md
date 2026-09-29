# Task: Fix #443 facet 2 / #448 — Pool.Release must not destroy uncommitted worktree deliverables

Repo: g8s. Tracking: tamld/g8s#443 (facet 2) + tamld/g8s#448 (acceptance item 2).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (absolute paths):

- /Users/tamld/Documents/github/g8s/internal/orchestrator/worktree.go
- /Users/tamld/Documents/github/g8s/internal/orchestrator/worktree_edge_test.go

Do NOT run git commit. Do NOT modify any other file. Another worker is
operating on unrelated files in parallel — touching anything outside the
list above corrupts their delivery.

## Problem

`Pool.Release` in `internal/orchestrator/worktree.go` (line ~123) removes
the worktree DIRECTORY unconditionally via `gitRemoveWorktree`; the
`keep` flag only controls branch deletion. The drain log promises
"isolated in worktree ... (kept for inspection)" but uncommitted
deliverables inside the worktree are destroyed at release. Field
evidence: two completed worker runs (tasks 4771793a, 853b0b02) had their
edits destroyed this way.

## Required repair (implement exactly this)

In `Pool.Release` (keep=true path only): before removing, inspect the
worktree for uncommitted state. If it is dirty (uncommitted tracked
changes OR untracked files — `git -C <wt> status --porcelain` non-empty),
PRESERVE the directory: skip `gitRemoveWorktree`, and log to stderr:

    [info] orchestrator: worktree <path> preserved (uncommitted deliverables, task <id-or-unknown>)

The lease bookkeeping (allocated map delete) is unchanged. keep=false
keeps today's behavior exactly (remove + delete branch). A clean worktree
with keep=true is still removed (avoids unbounded accumulation; the
existing `g8s cleanup-worktrees --older-than` reaper handles preserved
dirs by age).

Implementation shape:

    if keep {
        if dirty, derr := worktreeDirty(wt.Path); derr == nil && dirty {
            // preserve: skip removal, keep branch implicitly (it exists)
            fmt.Fprintf(os.Stderr, "[info] orchestrator: worktree %s preserved (uncommitted deliverables)\n", wt.Path)
            return nil
        }
    }
    // existing removal + branch handling unchanged

Add helper `worktreeDirty(path string) (bool, error)` running
`git -C <path> status --porcelain` and treating any output as dirty.
On inspection error (derr != nil), fail safe to CURRENT behavior
(remove) — never wedge the release path.

## Tests — extend `worktree_edge_test.go` (table-driven, real temp git repos like the existing tests there)

1. `TestReleasePreservesDirtyWorktree`: worktree with an uncommitted
   modification → after `Release(keep=true)`: directory still exists with
   the modification; branch still exists.
2. `TestReleasePreservesUntrackedWorktree`: worktree with only an
   untracked new file → preserved on keep=true.
3. `TestReleaseCleanWorktreeRemoved`: clean worktree → keep=true removes
   directory as today.
4. `TestReleaseKeepFalseUnchanged`: dirty worktree → keep=false removes
   directory and deletes branch (existing behavior must not regress).

Follow the existing test setup helpers in that file for creating real
worktrees in t.TempDir().

## Constraints

- Scope limited to the two files listed above.
- `go test ./internal/orchestrator/` must pass in full.
- gofmt clean; `go build ./...` clean.
- No API changes to Pool/Acquire signatures.

## Deliverables (report back)

1. List of changed files.
2. Tail of `go test ./internal/orchestrator/ -run 'TestRelease' -v`.
3. Tail of `go test ./internal/orchestrator/...` (full package).
4. One sentence stating the new keep=true semantics.
