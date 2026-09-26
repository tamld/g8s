# 09 — Worker Supervisor Spec (DELTA-09)

**Status**: `APPLIED`
Port of `reference/python/scripts/agy_worker.py` to a Pure-Go package
`internal/worker`. The supervisor claims one durable task from the
`internal/controlplane` store, runs it as an isolated process group inside a
per-attempt run directory, and finishes the attempt with sealed evidence.

## ADDED Requirements

### Requirement: Duration parsing accepts only positive bounded expressions

`ParseDurationSeconds(value string) (float64, error)` SHALL accept Go-style
compound durations (`250ms`, `1m2s`, `2h`) and reject zero, negative, empty,
and malformed values, including wrong-case suffixes (`500MS`).

#### Scenario: millisecond and compound forms parse exactly
- `ParseDurationSeconds("250ms")` returns `0.25`.
- `ParseDurationSeconds("1m2s")` returns `62`.

#### Scenario: invalid values are rejected
- `"0s"`, `""`, `"abc"`, `"-5s"`, and `"500MS"` all return errors.
- A returned value is never `<= 0`.

### Requirement: One attempt runs in a private 0700 run directory

For every claimed attempt the supervisor SHALL create
`<runRoot>/<task_id>/attempt-<N>` (mode 0700) where `<N>` equals the task's
1-based attempt counter after `ClaimTask`. It SHALL write `prompt.txt`
(mode 0600) from the decoded request payload, redirect child stdout/stderr to
private files (mode 0600), and remove `prompt.txt` plus both output files when
the attempt ends — regardless of outcome. `result.json` produced by the worker
is preserved; raw `worker.stdout` / `worker.stderr` files are NEVER persisted.

#### Scenario: happy path leaves receipt but no prompt
- After a successful `RunOnce`, `attempt-1/receipt.json` exists,
  `prompt.txt` no longer exists, and the task state is `SUCCEEDED`.

#### Scenario: failed attempts still clean up
- When spawning or execution fails, `prompt.txt` is removed from the run
  directory before `RunOnce` returns.

#### Scenario: oversized and non-UTF-8 output cannot wedge the loop
- A child emitting >200KB of mixed binary output is captured within
  `captureMaxBytes`; collection completes and no stdout/stderr file survives.

### Requirement: Children run in killable process groups

On POSIX the supervisor SHALL spawn children with `Setpgid` and terminate via
`kill(-pgid, SIGTERM)` followed by `kill(-pgid, SIGKILL)` after a grace
window, ignoring ESRCH/EPERM races. Cancellation, timeout, and shutdown all
terminate through this path. The supervisor SHALL record `child.pid` in the
run directory for cross-process coordination.

#### Scenario: cancel while running kills the tree
- With `cancel_requested` observed during execution, the child process group
  receives SIGTERM, `FinishAttempt` records non-retryable failure with error
  "cancelled by orchestrator", and the task ends `CANCELLED`.

#### Scenario: timeout terminates the attempt
- When the injected clock passes the request timeout deadline, the tree is
  terminated and the attempt fails retryable with status `timeout`.

#### Scenario: SIGTERM exit contract
- `ExitCodeForSignal(15)` returns `143` (`128 + signal`) for the CLI loop;
  any signal code maps identically.

### Requirement: Lease lifecycle drives every terminal branch

The poll loop SHALL derive signals only from the control plane:
`GetTask` returning nil/foreign lease means lease lost; `CancelRequested`
means cancelled; the injectable clock passing the deadline means timeout;
`RenewHeartbeat` failure means lease lost. Heartbeats renew at
`clamp(leaseSeconds/3, 100ms, 5s)` intervals on the injected clock. Before any
work, `StartTask` must transition LEASED→RUNNING under the claimed token; a
spawned child whose start loses the race is terminated immediately and the
attempt reports `lease_lost` without finishing.

#### Scenario: NEEDS_INFO pauses and releases the lease
- A worker result with `status: "NEEDS_INFO"` routes through `PauseTask`;
  the task state becomes `NEEDS_INFO` and `lease_owner` is NULL afterwards.

#### Scenario: stale completion is refused
- Finishing after lease loss surfaces the control plane's stale-lease error
  instead of corrupting another owner's attempt.

### Requirement: Attempt evidence is exported as sealed JSON

When an attempt ends the supervisor SHALL export `receipt.json` into the run
directory containing a redacted `GetTask` snapshot (task id, final/paused
state, attempt counters, timestamps). Export happens on success, failure,
pause, cancellation, and timeout alike.

#### Scenario: receipt parses with identity fields
- `receipt.json` decodes as JSON containing the task id and a state field.

#### Scenario: retries get separate directories
- A retryable failure followed by re-claim produces `attempt-1` and
  `attempt-2` directories, each holding its own `receipt.json`.

### Requirement: The runner seam keeps tests free of real binaries

Process creation sits behind a `Runner` interface (`Spawn` → `Child` with
`Done`, `ExitCode`, `Terminate`), and time flows through an injectable
`clock func() time.Time`. Production wiring uses `exec.Command` with POSIX
process groups; tests use fakes that complete, hang, or fail on demand.

#### Scenario: orphan reap is best effort
- `RunLoop` sweeps stale `child.pid` files at startup and kills leftover
  process groups best-effort without failing the run.

#### Scenario: once-mode exit codes
- Once-mode with no claimable task exits 0; a finished task exits 0 for
  `SUCCEEDED`/`CANCELLED`/`QUEUED` outcomes and 1 otherwise.

### Requirement: RunLoop drains through a bounded concurrent pool (#394)

`LoopOptions.Concurrency` selects the drain shape. Values `<= 1` (and any
`Once` request) take the existing serial `for{}` path unchanged — default
behavior is byte-identical. `Concurrency N > 1` claims and executes through at
most N simultaneous `RunOnce` cycles. A worker whose claim returns empty parks;
the loop is drained only when every worker is parked at the same moment, so a
retryable re-queue surfaced by a finishing worker is re-claimed by that worker.
Context cancellation unwinds in-flight attempts and exits `128+SIGTERM`. Each
concurrent worker may obtain a private working directory through the
`Isolation` hook (acquire → dir feeds every attempt of that worker → release
at worker exit); an isolation acquire failure stops the loop with exit 1 —
concurrency without isolation must fail loudly, never degrade to sharing.

#### Scenario: N=4 drains the queue under the race detector
- Four tasks submitted; `RunLoop` with `Concurrency: 4` runs all four to a
  terminal state and returns 0; `go test -race` reports zero warnings.

#### Scenario: concurrency is bounded
- With blocking children and more tasks than workers, the number of
  simultaneously spawned children never exceeds N.

#### Scenario: cancellation exits 143
- Cancelling the context while attempts are in flight terminates the children
  and `RunLoop` returns 143.

#### Scenario: isolation acquire failure fails the loop
- `Isolation.Acquire` returning an error stops the loop with exit 1 and the
  hook's `release` is never invoked for the failed acquire.

### Requirement: Telemetry engine lifecycle is refcounted per run

The closed-loop ingestion engine (#253) is opened lazily once and closed when
the last in-flight run exits — not at the end of every `RunOnce`. Closing
resets the lazy-init state so a later run in the same process re-opens a fresh
engine instead of silently dropping events. All engine access is
mutex-guarded, so concurrent runs may ingest simultaneously without data races.

#### Scenario: repeated serial runs all emit telemetry
- Two consecutive `RunOnce` calls in one process with `G8S_TELEMETRY=1` both
  persist trace events; the second run is not silenced by the first run's
  close.

#### Scenario: concurrent runs close the engine exactly once
- With N runs in flight, the engine closes only after the last run exits, and
  every run's events reach the ledger.
