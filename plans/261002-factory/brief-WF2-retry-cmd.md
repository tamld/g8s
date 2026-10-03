# Task: Wave F2 — ADR-0029 budgeted auto-retry, cmd layer

Repo: g8s @ main. Design: docs/decisions/0029-budgeted-auto-retry.md
(ACCEPTED). Tracking: plans/261002-factory/plan.md, #485.
DEPENDS ON F1 (controlplane primitives) — F1 runs in parallel; develop
against the FROZEN contract below and note anything that doesn't fit.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your
changes DIRECTLY to these receipt-scoped repository files:

- cmd/g8s/resubmit.go (new)
- cmd/g8s/resubmit_test.go (new)
- cmd/g8s/autopilot.go (add the `retry` tick job)
- cmd/g8s/main.go (register the resubmit command + usage line ONLY)
- docs/user-guide/autopilot.md (tick jobs + resubmit sections)

Do NOT run git commit. Do NOT modify internal/ (F1 and F3 own those
packages in parallel).

## Frozen contract (F1 provides)

```go
// internal/controlplane/retry.go
type RetryClass string // Retryable / NotRetryable
func ClassifyRetryable(resultJSON, lastError string) RetryClass
type ResubmitOpts struct {
    Enabled    bool
    MaxPerTask int
    MaxPerHour int
}
func (s *Store) ResubmitTask(ctx context.Context, origTaskID string,
    opts ResubmitOpts) (newTaskID string, err error)
```

Settings keys (F1 adds): `auto_retry_enabled` (bool, default false),
`auto_retry_max_per_task` (default 2), `auto_retry_max_per_hour`
(default 10). Settings read pattern: see existing settings consumers
in cmd (e.g. default_provider in submit.go:60-71).

## Required implementation

1. **`g8s resubmit --task <id> [--reason <text>]`** — the
   supervisor-driven path (ADR-0029 D3: covers EVERY class including
   workspace_write, but always with a FRESH receipt issued by the
   supervisor; this command never mints receipts — if the original
   was workspace_write, instruct the operator to issue a receipt and
   pass `--receipt-id <id>` to include it in the new request).
   Envelope kind "resubmit"; errors: task not found / not FAILED /
   not retryable → clean E_* envelopes.
2. **Tick job `retry`** in runAutopilotTick (autopilot.go:398-449):
   - Add "retry" to the allowed jobs list; `runTickRetry(ctx)`.
   - Behavior: scan FAILED tasks (store query — add a narrow
     `ListRetryableFailed(ctx, limit)` to your expectations of F1 ONLY
     IF trivially additive; otherwise filter in cmd from `g8s tasks
     --state FAILED`), for each: ClassifyRetryable RETRYABLE + auto-
     retry feature flag ON + task permission ≠ workspace_write (D3:
     the tick NEVER touches receipt-required work) → ResubmitTask.
   - Output: autopilot_tick envelope with per-task results
     (resubmitted/skipped+why). Idempotent: double-fire safe via
     derived keys (F1 guarantees).
3. **--help/usage**: resubmit line in printUsage (main.go) matching
   existing style.
4. **docs/user-guide/autopilot.md**: retry job semantics (budget,
   backoff, receipt boundary), resubmit command, the D-09 directive
   sentence: "a scheduled check that retries outside its budget is a
   bug".

## Tests — resubmit_test.go (red first)

- resubmit happy path on a real temp store (existing
  setupTaskWithState pattern in watch_test.go): FAILED transient task
  → new task id returned, envelope kind "resubmit".
- Refusals: not-FAILED, not-retryable, flag-off → error envelopes
  with correct E_* codes.
- Tick job: seeded FAILED tasks (mix of classes + workspace_write) →
  correct subset resubmitted; workspace_write NEVER resubmitted by
  the tick; flag-off → job reports disabled, no-op.
- Existing autopilot/worker tests stay green (regression guard).

## Constraints

- stdlib only; match watch.go/autopilot.go style; envelope via
  cli.NewEnvelope; exit codes follow main.go conventions.
- `go test ./cmd/g8s/` green; `go build ./...` green;
  `GOOS=windows go build ./cmd/g8s/` compiles.
