# Task: Wave F4 — controlplane coverage lift (D-06 ratchet)

Repo: g8s @ branch feat/factory-retry-cp. Goal: raise package
internal/controlplane coverage from 78.1% to ≥ 85% so the CI aggregate
(per-package mean, CGO_ENABLED=0, cmd/g8s excluded) clears the 80.75%
ratchet floor with margin.

## Delivery protocol

Scratch worktree. Write ONLY to this receipt-scoped file set:

- internal/controlplane/coverage_gap_test.go (extend the existing file)
- internal/controlplane/coverage_gap2_test.go (new, if cleaner)

Do NOT touch any non-test file. Do NOT run git commit. Another worker
owns internal/routing tests in parallel.

## Context (measure first — evidence over guess)

Baseline (supervisor-measured, `go tool cover -func`): under-covered
functions and their current rates:

- CheckpointTask 43.3% (lifecycle.go:726) — needs a RUNNING task:
  submit → ClaimTask → drive to RUNNING (find how existing lifecycle
  tests reach RUNNING — grep lifecycle_test.go / controlplane_test.go
  for StartTask or state transitions), then checkpoint with a real
  CheckpointData (see model.go:268), assert persisted checkpoint_data
  column, then a second checkpoint refused/behaves per code.
- ResumeFromCheckpoint 40.6% (lifecycle.go:779) — happy path after a
  real checkpoint: assert state + new lease; error paths for wrong
  lease token and wrong state.
- RequeueResult 36.4% (lifecycle.go:1118) — happy path: get a task to
  WORKER_COMPLETED (drive FinishAttempt via the store API like other
  tests do, or seed state via raw SQL like watch_test.go does), then
  requeue → assert QUEUED + supervisor feedback recorded.
- extractActorFromReq 41.7% (store.go:1327) — table test over request
  JSON variants (actor present, absent, malformed, empty).
- ListSupervisorDecisions 0% — find it in supervisor_store.go; create
  a supervisor task, AppendDecision ×2, list, assert order/fields.
- GetSupervisorTaskWorker 0% — find it; exercise success + unknown-id.
- extractWorkerNameFromJSON 0% — table test, same style as
  extractActorFromReq.

## Required

1. Table-driven, stdlib testing only, match controlplane_test.go style
   (newTestStore helper, issue-tag comments).
2. Each test red-then-green in YOUR worktree: run
   `go test -count=1 ./internal/controlplane/` green at the end.
3. Report the final `go test -cover` number for the package and the
   per-function rates for the seven functions above.
4. Any test that exposes a REAL bug: do not fix production code —
   record it as a skipped regression test with a `// BUG(...)` comment
   and flag it in your report.

## Constraints

- No new deps. No production-code edits. Tests must stay hermetic
  (t.TempDir stores via newTestStore; fake clocks where timing
  matters). Deterministic — no sleeps.
