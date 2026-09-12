# ADR-0003: Meta-optimizer read-only ingestion

> **Status**: Proposed (2026-09-12)
> **Date**: 2026-09-12
> **Deciders**: tamld (owner), g8s supervisor (advisor)
> **SSoT**: `docs/designs/supervisor-fix-loop.md` § Concern C
> **Implements**: DELTA-11 orchestration-roadmap §ADDED C.1-C.2
> **Supersedes**: none
> **Depends on**: ADR-0001 (Accepted 2026-08-28), ADR-0002 (Proposed 2026-09-12)

## Context

ADR-0001 established the code-level supervisor-driven fix loop. ADR-0002 extended the receipt schema with supervisor metadata (approach/attempt lineage, RCA confidence, ADR linkage). The supervisor now emits per-cycle metrics (envelope_score, first_attempt_success, attempts_to_success, approaches_to_success, rca_confidence_avg, cycle_duration_seconds, escalation_count, false_escalation_rate) as specified in `docs/designs/supervisor-fix-loop.md` §10.

The meta-optimizer (Concern C) needs to consume these metrics to:
1. **Score envelope selection** — learn which envelope field combinations yield higher success rates
2. **Tune iteration caps** — adjust 3×3=9 bounds based on observed success distributions
3. **Calibrate RCA confidence threshold** — the 0.6 safety valve may be too aggressive or too lenient
4. **Optimize autopilot priority queue** — severity × confidence × cost-inverse weights

Two paths were considered:

1. **Read-write meta-optimizer** — the optimizer writes tuned configs back to the supervisor, applies automatic adjustments, and runs as a background daemon. Full autonomy.
2. **Read-only meta-optimizer** — the optimizer exposes aggregate queries (CLI + Go API) that humans or automation can invoke. No writes, no automatic tuning. Pure observability.

## Decision

Adopt **read-only meta-optimizer ingestion** for this tranche. The supervisor exposes:

- **Go API**: `Aggregate(store, ctx, opts)` returning `AggregateMetrics` with 8 computed fields
- **Go API**: `StreamMetrics(store, ctx, opts, fn)` for incremental processing
- **CLI**: `g8s supervisor metrics --aggregate` for cross-run aggregation
- **CLI**: `g8s supervisor metrics --json-stream` for streaming output
- **Flag collision guard**: `--task-id` + `--aggregate` and `--task-id` + `--json-stream` rejected with usage error (exit code 2)

All queries are **strictly read-only** — no INSERT/UPDATE/DELETE on any table.

The six tables in the control plane are:
1. `supervisor_tasks`
2. `supervisor_decisions`
3. `write_receipts` (from Concern B)
4. `tasks` (orchestrator)
5. `task_events` (orchestrator)
6. `control_plane_maintenance`

## Rationale

1. **Safety first** — automatic tuning without human review is a risk. The 0.6 RCA threshold, 3×3 iteration cap, and envelope weights are control surfaces that can cause cascading failures if tuned poorly. Read-only lets us observe before acting.
2. **Separation of concerns** — the supervisor's job is *execution* (run loop, emit metrics). The meta-optimizer's job is *analysis* (query metrics, propose config). Keeping them separate avoids circular dependencies.
3. **Human-in-the-loop** — the priority queue weights, iteration caps, and confidence thresholds are policy decisions. A read-only API lets the owner review aggregates and decide before applying.
4. **Foundation for future** — the aggregate/streaming API is the data layer. Future tranches add write capability (e.g., `Optimizer.Propose()`) on top of this read-only foundation without breaking changes.
5. **CLI as contract** — `g8s supervisor metrics --aggregate` is the stable interface. CI/CD, dashboards, and scripts can depend on it without importing Go internals.

## Consequences

Positive:

- Zero risk of meta-optimizer corrupting supervisor state (read-only queries only)
- Clear separation: supervisor executes, meta-optimizer observes
- Aggregate formulas match §10 of design doc exactly — single source of truth
- Flag collision guard prevents ambiguous CLI invocations
- Streaming API handles large history without OOM

Negative:

- No automatic tuning yet — human must review aggregates and adjust config manually
- `false_escalation_rate` remains 0 until feedback mechanism is added (future tranche)

Neutral:

- `internal/orchestrator`, `internal/harness`, `internal/receipt` unchanged
- ADR-0001 and ADR-0002 remain valid; this ADR extends the metrics layer

## Alternatives considered

### A. Read-write meta-optimizer in this tranche

Pros: full autonomy, self-tuning supervisor.
Cons: higher blast radius, harder to debug, no human oversight on config changes. Rejected because the supervisor's control surfaces (iteration caps, RCA threshold, envelope weights) are not yet battle-tested in production.

### B. Separate metrics table / external TSDB

Pros: isolation, scaling, different query engine.
Cons: adds infrastructure complexity (InfluxDB, Prometheus, etc.) for what is fundamentally a small SQLite WAL dataset. Rejected because SQLite WAL handles the read load (thousands of runs, not millions) and keeps the system dependency-free.

### C. No meta-optimizer at all

Pros: simpler codebase.
Cons: supervisor cannot learn. Every task pays the 3×3=9 cost regardless of difficulty. Rejected because the design doc explicitly calls for Concern C.

## Follow-ups

- Future tranche: add `Optimizer.Propose(currentConfig, metrics) SupervisorConfig` write API
- Future tranche: `false_escalation_rate` feedback loop (user marks escalations as "should have worked")
- Future tranche: continuous autopilot tuning via scheduled aggregate queries

## Change log

- **v1** (2026-09-12): Initial proposal. Status: Proposed. Will be Accepted on T022 completion.