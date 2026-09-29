# Task: Fix #459 — TestRunWithTimeout 1s spawn budget flakes under CI load

Repo: g8s @ main. Tracking: tamld/g8s#459 (P1 reliability, small).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to this receipt-scoped repository file (path relative to
$G8S_REPO_ROOT):

- internal/runtime/verify_test.go

Do NOT run git commit. Do NOT modify any other file. Two other workers are
operating in parallel on unrelated files (internal/memory/, tools/) —
touching anything outside the list above corrupts their delivery.

## Context

`TestRunWithTimeout` (internal/runtime/verify_test.go:145) spawns
`cmd /c echo hello` on Windows with a 1-second `RunWithTimeout` budget.
On loaded 2-core CI runners the spawn alone can exceed 1s, so the test
reports `command execution timeout` — it measures runner latency, not
product behavior (evidence: failed twice on PR #458 run 36559716865 while
the same suite passed in a parallel job of the same run; main is green 6/6).

## Required implementation

1. Raise the happy-path budget in `TestRunWithTimeout` from
   `1*time.Second` to `5*time.Second`.
2. `TestRunWithTimeout_Timeout` (verify_test.go:169) exercises the timeout
   path — leave its semantics intact: if it uses a short budget to force a
   timeout, keep that budget but make sure it still triggers a genuine
   timeout (e.g. sleeping command duration must stay well above the
   budget).
3. No production-code changes. No expected-output changes (the
   `hello\r\n` windows expectation stays).

## Tests

- Run `go test ./internal/runtime/ -run 'TestRunWithTimeout' -v` locally —
  both subtests must pass.
- The point of the change: the happy path must tolerate slow spawns while
  the timeout-path test still proves timeout enforcement works.

## Constraints

- Match surrounding test style. No new dependencies. Do not touch
  internal/runtime production files.
