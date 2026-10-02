# ADR-0029: Budgeted Auto-Retry of Failed Tasks (#485 Stage 4)

- **Status**: Proposed (2026-10-02) — three decision points (D1–D3) await
  operator ratification before any code lands.
- **Issue**: #485 (autopilot staged path, stage 4). Prerequisites met:
  submit throttle ✓ (#488), queue perf baseline ✓ (#487), stage-3 ticks
  ✓ (#492), event-driven signal surface — in flight (#481).
- **Supersedes**: none. **Depends on**: #492 tick posture ("ticks are
  hints; the queue is the memory"), #481 signal file, #491 per-state-dir
  instance identity, ADR-0024 P0 trust-boundary lifecycle.

## Context

Stage 4 is the last and riskiest stage of the ratified autopilot path:
auto-resubmit of FAILED tasks. Demand is proven — this campaign ran
THREE manual external automations around g8s (post-quota retry, hygiene
sweep, deadline checks), and the wave-B overnight incident (2026-10-01)
left 3 tasks honestly FAILED in the durable queue that a human
resubmitted by hand the next morning. That hand labor is exactly what
stage 4 automates.

In-band retry already exists and is NOT this ADR's subject: every task
carries `attempts/max_attempts` (1..10) consumed during execution
(`reconcileExpiredTx`, store.go:1805). Stage 4 is OUT-OF-BAND: a new
task spawned after the original reached terminal FAILED.

Why it is risky: perf baseline (docs/user-guide/performance.md §4) shows
sub-millisecond queue ops — an unconstrained resubmit loop executes
thousands of transitions per second; and a blind retry of the wrong
failure class re-runs destructive or already-delivered work.

## Decision points (awaiting operator)

### D1 — Which FAILED tasks are retryable (recommendation: deny by default)

Retryable classes (transient/infrastructure): provider transport errors,
worker spawn failure, task timeout, `interrupted`, lease-death recovery.
NOT retryable (deterministic or trust-violating): receipt/sanitizer
violations, refusal verdicts, `E_USAGE`, NEEDS_INFO, CANCELLED, and any
task that already produced a delivered artifact.

Falsification citations: #451 (FAILED-verdict-WITH-delivery — blind
retry double-delivers); the sanitizer/refusal classes (#434/#443);
unclassified `last_error` defaults to NOT retryable (same
deny-all/allow-some posture as ADR-0024 P0).

### D2 — Budget numbers (recommendation)

- Per original task: **max 2 auto-resubmits** (total 3 executions
  including the original).
- Per state dir: **max 10 auto-resubmits/hour** — independent of, and
  in addition to, `submit_rate_limit_per_hour` (which bounds humans and
  scripts; this bounds the machine's own retry loop).
- Backoff from terminal time: **5m → 20m → 60m** (exponential).
- No auto-retry while a task with an overlapping receipt scope is in
  flight (parallel-drain disjointness invariant, ADR-0026 §3.2).
- Feature flag `auto_retry_enabled` ships **default OFF** in v0.14;
  flipped ON only after one observed soak period.

Falsification citation: perf baseline §4 (sub-ms ops make an
unthrottled loop pathological); the hourly cap mirrors the throttle
class #488 introduced for runaway dispatchers.

### D3 — Who fires it, and the receipt boundary (recommendation: split by trust)

- **Tick job `retry`** (stateless, idempotent, joins the existing
  doctor/retention/hygiene job set): auto-resubmits ONLY tasks whose
  permission profile needs no receipt (read_only /
  automation_read). Safe to leave unattended.
- **Supervisor-driven `g8s resubmit --task <id> [--receipt-id ...]`**:
  covers every class including workspace_write, but requires a FRESH
  receipt issued by the supervisor session — which the #481 signal file
  wakes to do exactly this. The durable queue holds write-tasks
  honestly overnight; the morning wake + one command replaces today's
  hand labor.
- The tick NEVER mints receipts. Automating receipt issuance would be a
  self-delegation loop — the exact class ADR-0024 P0 denies.

Falsification citations: #448 (workspace_write delivery contract —
receipt is the real enforcement); #491 (retries stay inside the owning
state dir; no cross-instance resurrection).

## Mechanics (implementation-level, both mechanisms)

- Resubmission is a NEW task with derived idempotency key
  `<orig-task-id>#r<N>` (the UNIQUE constraint then makes double-fire
  idempotent — a tick and a supervisor racing produce one retry, the
  second insert is refused) and `parent_task_id = <orig-task-id>` (free
  lineage + budget accounting: count children).
- Request metadata records `retry_of` and `retry_attempt` for audit.

## Consequences

- Stage 4 lands as 2 slices per DEBT-34: controlplane (classification,
  budget accounting, resubmit primitive) → cmd/g8s (tick job + resubmit
  command + settings keys `auto_retry_enabled`,
  `auto_retry_max_per_task`, `auto_retry_max_per_hour`).
- directives.md gains D-08 ("a scheduled check that retries outside its
  budget is a bug") alongside the D-07 event-driven directive from #481.
- Non-goals (revisit triggers recorded): no daemon, no webhook, no
  cross-host retry, no retry of CANCELLED/NEEDS_INFO. Multi-host
  (ADR-0027 Trigger A) reopens the design.

## Related

- #485 (staged path), #481 (signal file — the wake edge), #488
  (throttle), #487 (perf baseline), #491 (instance identity), #492
  (ticks), ADR-0024 (gate lanes), ADR-0025 (no-daemon posture),
  docs/user-guide/performance.md §4.
