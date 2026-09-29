# Task: Fix #440 — memory adapter: bounded SQLITE_BUSY retry-with-backoff

Repo: g8s @ main. Tracking: tamld/g8s#440 (P2; the operator has directed
this be paid now rather than waiting for a third flake occurrence).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- internal/memory/adapter.go
- internal/memory/busy_retry_test.go (new file)

Do NOT run git commit. Do NOT modify any other file. Two other workers are
operating in parallel on unrelated files (internal/runtime/, tools/) —
touching anything outside the list above corrupts their delivery.

## Context

`TestConcurrency_ReadWriteRace` (30 workers x 20 StoreWorkingContext ops)
failed twice with SQLITE_BUSY on windows-latest -race (PR #430 first run +
PR #431 run; both passed on rerun). The DSN already carries
`busy_timeout(30000)` (internal/memory/adapter.go:71), but under
race-induced slowdown that can still expire. WAL queueing handles
contention when the writer waits; the retry covers the residual window.

`StoreWorkingContext` is at internal/memory/adapter.go:184;
`LoadWorkingContext` is the symmetric read path.

## Required implementation

1. Add an unexported helper in adapter.go:

   ```go
   func withBusyRetry(op func() error) error
   ```

   Semantics: run op; if the error is a SQLITE_BUSY-class error
   (modernc.org/sqlite surfaces it as error code 5 / "database is locked"
   / "database table is locked" — match conservatively with a helper
   `isBusyErr(err)` that checks the string forms and, if available, the
   sqlite error code), retry up to 3 total attempts with jittered backoff
   50-200ms between attempts; on the final attempt surface the original
   error unchanged. Non-busy errors return immediately, unwrapped.

2. Testability seam (DoD requirement): package-level vars

   ```go
   var (
       busyRetryAttempts = 3
       busyBackoff       = func(attempt int) time.Duration { ... jittered 50-200ms ... }
   )
   ```

   Tests override them; production code reads them at call time (no
   capture at init).

3. Wrap the storage operations inside `StoreWorkingContext` and
   `LoadWorkingContext` with `withBusyRetry`. No semantics change beyond
   the retry (the upsert is idempotent already — issue-verified).

## Tests — busy_retry_test.go (red first)

1. `isBusyErr` classifies: an error whose string contains
   "database is locked" → true; "database table is locked" → true;
   "constraint failed" → false; nil → false.
2. Retry loop: op fails twice with a busy error then succeeds →
   withBusyRetry returns nil and op ran 3 times (attempts counter).
3. Exhaustion: op always busy → returns the last busy error (errors.Is /
   string match preserved) after exactly busyRetryAttempts calls.
4. Non-busy error: op fails once with a non-busy error → returned
   immediately, op ran exactly once, error text identical (no wrapping).
5. Backoff injection: set busyBackoff to a recorder func; assert it is
   called between attempts with the attempt index, and that a zero
   duration is honored (no sleep) in the test.

## Constraints

- stdlib + existing deps only (modernc.org/sqlite is already imported).
- No change to DSN/pragmas. No API signature changes to exported methods.
- Keep the existing concurrency stress test passing:
  `go test ./internal/memory/` locally (race flags if available locally:
  `go test -race ./internal/memory/`).
- Match surrounding code style; comments only for non-obvious constraints.
