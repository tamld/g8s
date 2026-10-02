# Task: #485 stage 2 (submit throttle) + #476 P2.7 (scratch-branch honesty)

Repo: g8s @ main. Tracking: tamld/g8s#485 stage 2 + #476 P2.7.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/controlplane/store.go (ONLY: the SubmitTask throttle — the TODO
  at ~line 1762 — no other edits in this file)
- internal/controlplane/throttle_test.go (new file)
- internal/settings/settings.go (+ internal/settings/settings_throttle_test.go new)
- internal/cleanup/cleanup.go (ONLY: the scratch-branch honesty fix)
- internal/cleanup/scratch_target_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
tools/release.sh and cmd/g8s/ in parallel.

## Required implementation

1. **Submit throttle** (store.go SubmitTask, the TODO site):
   - Bounded submit rate per actor: sliding-hour window enforced in-DB
     (count SubmitTask rows for the actor in the last 3600s from
     tasks/audit of created_at) — implementation: a small SELECT COUNT on
     the tasks table filtered by actor + created_at window, executed
     inside the existing submit transaction.
   - Limit source: settings key `submit_rate_limit_per_hour` (integer,
     default 0 = unlimited; values < 0 rejected by settings validation).
     Read the setting at call time via the settings manager the store
     already has access to — if no settings integration exists in Store,
     read it via a package-level var default with a setter (same
     injection pattern as internal/worker busyRetryAttempts).
   - Over-limit: SubmitTask returns a typed error
     `ErrSubmitRateLimited` naming the actor, the limit, and the window.
   - Attribution: the rejection is recorded (last_error-style log line on
     stderr is enough; no new table).
2. **Scratch-branch honesty** (internal/cleanup/cleanup.go ~878): when the
   operator selects target `scratch-branch` but `--scratch` is not
   enabled, the sweep silently no-ops. Fix: selecting the target enables
   it (keep `--scratch` working unchanged), so `--target scratch-branch`
   alone performs the sweep. Update the in-function comment.

## Tests — red first

- throttle_test.go: limit=3 → 4th submit in the hour fails with
  ErrSubmitRateLimited naming actor/limit/window; different actor
  unaffected; limit=0 (default) unlimited; window slides (inject clock:
  submits at T-3700s do not count).
- settings_throttle_test.go: set/get/unset round-trip; "-5" rejected.
- scratch_target_test.go: `--target scratch-branch` semantics via
  direct sweep invocation with the target set and scratch enabled-by-
  target → the stale scratch branch IS swept (existing --scratch
  behavior unchanged).

## Constraints

- stdlib + existing deps; no new tables (count on existing tasks rows);
  match style; comments only for non-obvious constraints.
- `go test ./internal/controlplane/ ./internal/settings/
  ./internal/cleanup/` and `go build ./...` locally green.
