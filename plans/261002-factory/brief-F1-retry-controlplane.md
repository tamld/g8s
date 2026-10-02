# Task: Wave F1 — ADR-0029 budgeted auto-retry, controlplane layer

Repo: g8s @ main. Design: docs/decisions/0029-budgeted-auto-retry.md
(ACCEPTED — D1 deny-by-default classes; D2 budget 2/task + 10/hour +
5/20/60m backoff + feature flag; D3 tick retries receipt-free only —
NOT your concern, that is F2). Tracking context: factory program
plans/261002-factory/plan.md, #485.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your
changes DIRECTLY to these receipt-scoped repository files (paths
relative to $G8S_REPO_ROOT):

- internal/controlplane/retry.go (new)
- internal/controlplane/retry_test.go (new)
- internal/controlplane/store.go (minimal: export hooks only if needed)
- internal/settings/settings.go (add 3 keys + validators)

Do NOT run git commit. Do NOT modify cmd/ (F2 owns it),
internal/routing/ (F3 owns it), or docs/. Two other workers run in
parallel with disjoint file scopes.

## Context (grep-verified at main)

- Task lifecycle: internal/controlplane/lifecycle.go (FinishAttempt,
  CancelTask, RequestEdit, PauseTask), store.go (reconcileExpiredTx,
  SubmitTask with UNIQUE idempotency_key, tasks table has
  parent_task_id, attempts, max_attempts).
- Throttle precedent: store.go:1268-1291 ErrSubmitRateLimited +
  in-memory per-actor hourly counter — mirror this shape for the
  retry budget.
- Settings: internal/settings/settings.go — closed-world
  AllowedConfigKeys map (add key + description), per-key validators
  (settings.go:156-205 pattern), Config struct optional fields.
- Result envelope: task.result_json with $.ok / $.error.code
  (E_* codes); last_error column.

## Required implementation

1. **Classification (D1, deny-by-default)** in retry.go:
   `ClassifyRetryable(resultJSON string, lastError string) RetryClass`
   — RETRYABLE only for: timeout, interrupted, spawn-failure,
   provider-transport (malformed function call, 5xx-ish markers).
   NOT_RETRYABLE: receipt/sanitizer violation markers, refusal markers,
   E_USAGE, empty/unknown → NOT_RETRYABLE. Export the class constants.
2. **Budget accounting**: count children via parent_task_id +
   request metadata `retry_of`/`retry_attempt`; per-state-dir hourly
   counter of auto-resubmits (in-memory, mirroring the throttle).
3. **`Store.ResubmitTask(ctx, origTaskID string, opts ResubmitOpts)
   (newTaskID string, err error)`** — the single primitive F2 calls:
   - Load original; require state FAILED (terminal-failed only);
     ClassifyRetryable must say RETRYABLE; feature flag
     (opts.Enabled, from settings) must be on; budget caps enforced
     (≤ opts.MaxPerTask retries per original; ≤ opts.MaxPerHour per
     store); backoff: new task NOT eligible for claim until
     completed_at + backoff (5m→20m→60m by attempt count) — implement
     via submit with a future `not_before` respected by claim, or the
     simplest honest mechanism you find in the existing schema
     (document the choice).
   - New task: derived idempotency key `<orig>#r<N>` (UNIQUE
     constraint = idempotent double-fire), parent_task_id = orig,
     request copied EXCEPT receipt_id dropped (trust boundary — a
     workspace_write retry requires a FRESH receipt; document that the
     tick must never pass one), `retry_of`/`retry_attempt` metadata
     added, permission preserved.
4. **Settings keys**: `auto_retry_enabled` (bool, default false),
   `auto_retry_max_per_task` (int 0..10, default 2),
   `auto_retry_max_per_hour` (int 0..1000, default 10). Validators in
   the settings.go pattern.

## Tests — retry_test.go (red first)

- Classification table: each retryable class, each non-retryable
  class, unknown → NOT_RETRYABLE.
- ResubmitTask happy path: FAILED transient task → new QUEUED task
  with derived key, parent linkage, retry metadata, no receipt.
- Budget: 3rd resubmit of same original refused; 11th in an hour
  refused (inject clock).
- Backoff: resubmitted task not claimable before backoff elapses
  (fake clock via newTestStoreWithClock).
- Flag off → refused with a clear error.
- Non-FAILED state, NOT_RETRYABLE class, workspace_write WITH
  receipt_id requested → refused.
- Double-fire idempotency: same resubmit twice → same new task id
  (UNIQUE key), not an error.
- Settings validators: negative/out-of-range rejected.

## Constraints

- stdlib + existing deps; no schema migration (use existing columns +
  request_json metadata); match store.go style (tx via BeginTx,
  busy-retry helper if needed).
- `go test ./internal/controlplane/ ./internal/settings/` green;
  `go build ./...` green. Other packages must not break.
