# Plan: S2 — Concurrent Dispatch (#394, DEBT-34 layer split W≠C → two PRs)

**Session type**: T2 (execution)

```yaml
issue: 394
session_type: T2 (execution)
slice: S2 (follows S1/#393, closed)
ratified_by: issue #394 ratified design + operator roadmap-wide delegation (2026-09-26)
layer_split: DEBT-34 W≠C — PR-A internal/worker only, PR-B cmd only (ci_layer_check forbids W+C)
precondition: "#393 merged (session gate, registry, worktree Pool proven)"
```

## Objective

`g8s worker` can drain the queue with N concurrent supervised attempts
(`--concurrency N`, default 1 = today's behavior byte-identical), hard-gated on
worktree isolation: N>1 without an isolatable git checkout is a usage error,
never a silent footgun.

## Design decisions (from issue #394 ratified design)

- **PR-A (internal/worker)**:
  - `LoopOptions.Concurrency int` — `<=1` or `Once` → existing serial `for{}`
    path verbatim (zero behavioral diff at N=1).
  - Concurrent path: N goroutines, each running full claim+run `RunOnce`
    cycles; a goroutine whose claim returns empty parks; the loop is drained
    when **all** workers are parked simultaneously; ctx cancel → exit 143
    after in-flight runs unwind. Rationale: `RunOnce` fuses claim+run, so a
    finishing worker re-claims retryable re-queued work itself; parked peers
    never need to un-park for sibling completion.
  - Telemetry lifecycle rework: per-run `defer closeTelemetry()` sets
    `telEngine=nil` while `telOnce` has already fired → **latent bug: serial
    multi-task runs silently drop telemetry from run 2 onward**, and N>1 makes
    it a data race. Fix: `telMu`-guarded lazy init (`telInited bool`,
    resettable), refcounted enter/exit per RunOnce — engine closes when the
    last in-flight run exits, fresh init for later runs.
  - `RunOptions.Dir string` — per-run child working-directory override
    (child Dir = firstNonEmpty(opts.Dir, firstOf(req.AddDirs), s.runRoot)).
  - `LoopOptions.Isolation` hook — `Acquire(ctx, workerID) (dir, release, err)`
    called once per concurrent worker; dir feeds every RunOnce of that worker;
    acquire failure fails the loop (exit 1). Interface lives in worker,
    adapter (Pool-based) lives in cmd — no import cycle, no W+C.
  - `LoopOptions.OnTask func(*controlplane.Task)` — progress hook invoked from
    worker goroutines (cmd serializes writes).
  - `-race` tests at N=4: drain, bounded in-flight ≤ N, cancel → 143,
    per-worker isolation acquire/release, telemetry refcount.

- **PR-B (cmd)**:
  - `g8s worker --concurrency N` (default 1). `--once` keeps its exact current
    single-attempt path; concurrency only engages the drain loop (`--once=false`).
  - N>1 requires per-worker worktree isolation via `orchestrator.Pool`:
    non-git checkout (pool creation impossible) → **hard usage error**
    "concurrency>1 requires worktree isolation" (exit usage path, never warn).
  - N>1 registers the session through the session gate (registry provenance +
    heartbeat + cleanup integration from #393/#400).
  - Concurrent drain prints per-task envelopes via OnTask (mutex-serialized).

## Boundaries / deferred

- Isolation is at child-spawn CWD level; task `AddDirs` with absolute paths
  into the original checkout bypass it (documented; per-task path rewriting is
  a polish-slice candidate).
- `--concurrency` on `orchestrate` (fix loop) is not in #394 scope.
- Known deferred items from S1 carry over (session_promoted telemetry event,
  gate-coverage hoisting, status --worker session surfacing).

## Verification contract (per PR)

RED-first tests (command + exit code quoted in ledger), dual-pass CI
(`CGO_ENABLED=0` vet+test; `CGO_ENABLED=1 -race`), pre-push 12/12 gates,
independent adversarial review, layer check W or C alone.
