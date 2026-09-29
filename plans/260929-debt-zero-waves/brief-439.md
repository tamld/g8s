# Task: Fix #439 — POSIX setsid grandchild escape hardening (process-tree accounting layer 2)

Repo: g8s @ main. Tracking: tamld/g8s#439 (P2, containment redundancy layer 2).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (absolute paths):

- $G8S_REPO_ROOT/internal/worker/worker.go
- $G8S_REPO_ROOT/internal/worker/orphan_sweep_test.go

Do NOT run git commit. Do NOT modify any other file. Another worker is
operating on an unrelated file (cmd/g8s/watch.go) in parallel — touching
anything outside the list above corrupts their delivery.

## Context

The #415 PR-1 post-run group sweep (`sweepAttemptGroup`, worker.go ~line
704) kills same-group survivors with `kill(-pgid)`. Windows is fully
contained (Job Objects, #429). POSIX gap: a worker child that calls
`setsid()` (double-fork) leaves the process group entirely — the sweep
cannot reach it, and no second mechanism exists.

## Required implementation (layer 2 accounting)

1. **Run-root marker injection**: at spawn, add an environment variable
   `G8S_RUN_MARKER=<attempt-unique-value>` to the child environment (the
   value: the task ID + attempt index or the run dir name — must be unique
   per attempt and must NOT contain secrets). Inject where the spawn env
   is assembled in worker.go (POSIX only; harmless on Windows).
2. **Escape sweep at attempt end**: extend the terminal sweep path (keep
   `sweepAttemptGroup` for the group kill) with a marker-based walk:
   - macOS: `pgrep -P <childPID>` iterated to a bounded depth (descendants
     of the child), then for each candidate PID read its environment
     marker via `ps -E` (macOS) and kill only processes whose marker
     matches `G8S_RUN_MARKER` exactly.
   - Linux: read `/proc/<pid>/environ` for the marker match.
   - Match ONLY on the exact marker value — never kill by age, name, or
     parent heuristics alone (no accidental kills of unrelated processes;
     document this invariant in a comment).
   - Kill = SIGKILL best-effort; emit the same `orphan_killed` trace
     telemetry event as #417 with a `layer=2` detail.
3. **Bounded work**: cap the walk (e.g. 512 PIDs, 2s budget) — sweep must
   never hang the attempt terminal path.

## Tests — extend `orphan_sweep_test.go` (follow its existing fixture style)

1. **Escape fixture**: spawn a shell that double-forks (`setsid`) a
   grandchild carrying the marker env; run the layer-2 sweep; assert the
   grandchild is killed (poll until gone, bounded). POSIX test; skip on
   Windows via runtime.GOOS guard like existing tests there.
2. **No-false-kill fixture**: a second unrelated process (no marker or a
   different marker value) survives the sweep.
3. **Bounded budget**: unit-test the walk cap (feed a synthetic list larger
   than the cap; assert it stops).

## Constraints

- Scope limited to the two files above.
- `go test ./internal/worker/ -run 'Orphan|Sweep|Escape' -v` green;
  `go test ./internal/worker/` green in full.
- gofmt clean; `go build ./...` clean.
- Windows behavior unchanged (no new Windows paths; Job Objects remain
  the Windows containment).

## Deliverables (report back)

1. List of changed files.
2. Tail of the sweep/escape test run.
3. Tail of the full worker package test run.
4. One sentence stating the marker-matching invariant.
