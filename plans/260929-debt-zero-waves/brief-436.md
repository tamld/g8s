# Task: Fix #436 — watch: explicit worker-complete milestone for supervisor wake-up

Repo: g8s @ main. Tracking: tamld/g8s#436 (P2, consumer request, small).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (absolute paths):

- $G8S_REPO_ROOT/cmd/g8s/watch.go
- $G8S_REPO_ROOT/cmd/g8s/watch_test.go

Create watch_test.go if it does not exist. Do NOT run git commit. Do NOT
modify any other file. Another worker is operating on unrelated files
(internal/worker/) in parallel — touching anything outside the list above
corrupts their delivery.

## Context

`g8s watch --task <id>` exits when the task reaches terminal states only.
`checkTask` (watch.go ~line 146) maps SUCCEEDED → resolved, FAILED /
CANCELLED → failed. A task sitting at `WORKER_COMPLETED` (awaiting
supervisor acceptance in the control-plane model) never terminates watch:
the field report shows 5 polls then `exit 2, state: timeout` — so
`worker --once` supervisors cannot use watch as their wake-up signal.

## Required implementation

1. **Milestone flag**: add `--milestone <accept|worker-complete>` to the
   watch command (default `accept` = today's behavior, byte-for-byte
   backward compatible).
   - `accept`: current mapping unchanged (SUCCEEDED/FAILED/CANCELLED/
     TIMED_OUT/ACTION_REQUIRED terminal).
   - `worker-complete`: additionally treats `WORKER_COMPLETED` as a
     resolved milestone — watch exits 0 with
     `state: worker-complete, detail: worker finished; awaiting supervisor acceptance`.
   - FAILED/CANCELLED/TIMED_OUT remain failures (exit nonzero) under both
     milestones.
2. **JSON surface**: the milestone exit carries the same JSON envelope
   shape as today (state/detail fields), so consumers can switch on
   `state == "worker-complete"`.
3. **Docs**: update the `watch` line in the usage text (main.go prints it,
   but do NOT touch main.go — the usage string lives in watch.go if
   present; if the usage text for watch lives only in main.go, note that
   in your report instead of editing main.go).

## Tests — `watch_test.go` (table-driven)

1. `--milestone accept` (default, flag absent): task at WORKER_COMPLETED →
   still polling (no resolution) — assert the mapping, not a sleep.
2. `--milestone worker-complete`: WORKER_COMPLETED → resolved with the
   exact state string `worker-complete`.
3. `--milestone accept`: SUCCEEDED → resolved as today; FAILED → failure
   as today (regression guard).
4. `--milestone worker-complete`: FAILED → still failure (exit nonzero
   path).
Use the existing control-plane store fixture style used by sibling tests
in the package (check how other cmd tests build a Store; if none exists,
construct the Store in a temp dir as internal tests do).

## Constraints

- Scope limited to the two files above.
- `go test ./cmd/g8s/ -run 'Watch' -v` green; `go build ./...` clean;
  gofmt clean.
- Default behavior must be byte-for-byte unchanged (backward compatibility
  is a requirement, not a nicety).

## Deliverables (report back)

1. List of changed files.
2. Tail of the watch test run.
3. One sentence stating the default-flag behavior guarantee.
