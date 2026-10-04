# Queue Performance Baseline

This document records the empirical queue operation latency baseline for the g8s SQLite control plane.

> **Disclaimer**: This is not a SLA — baseline for the throttle design, see `internal/controlplane/store.go:1329` (`ErrSubmitRateLimited`).

---

## 1. Test Environment

| Parameter | Specification |
| --- | --- |
| **Date** | 2026-10-02 |
| **CPU Count** | 10 logical cores |
| **Architecture / OS** | darwin/arm64 (macOS) |
| **Go Version** | `go1.27.1 darwin/arm64` |
| **SQLite Engine** | `modernc.org/sqlite` (pure Go, Zero-CGO) |
| **Pragmas** | `WAL` journal mode, `synchronous=FULL`, `busy_timeout=30000`, `txlock=immediate` |
| **Connection Pool** | `MaxOpenConns=10`, `MaxIdleConns=5` |

---

## 2. Benchmark Configuration

The benchmark is defined in [`internal/controlplane/perf_smoke_test.go`](../../internal/controlplane/perf_smoke_test.go) (`TestPerfSmokeQueueOps`):

- **Tasks**: 200 tasks pre-seeded into an isolated hermetic SQLite database.
- **Workers**: $N=10$ concurrent goroutine workers.
- **Workflow**: Each worker concurrently executes `ClaimTask` $\rightarrow$ `StartTask` $\rightarrow$ `Heartbeat` $\rightarrow$ `FinishAttempt` until all 200 tasks are completed.
- **Sample Size**: $\ge 200$ samples per operation.
- **Guard**: Skipped in `-short` mode (`testing.Short()`). Fails if any operation experiences pathological lock contention ($p95 > 10\text{s}$).

---

## 3. Recorded PERF Output

```text
PERF submit=p50=0.18ms p95=0.26ms claim=p50=0.16ms p95=0.25ms finish=p50=0.17ms p95=0.27ms
PERF submit=p50=0.18ms p95=0.26ms
PERF claim=p50=0.16ms p95=0.25ms
PERF heartbeat=p50=0.04ms p95=0.04ms
PERF finish=p50=0.17ms p95=0.27ms
```

### Metrics Summary

| Operation | Samples | $p50$ Latency | $p95$ Latency |
| --- | --- | --- | --- |
| **SubmitTask** | 200 | 0.18 ms | 0.26 ms |
| **ClaimTask** | 200 | 0.16 ms | 0.25 ms |
| **Heartbeat** | 200 | 0.04 ms | 0.04 ms |
| **FinishAttempt** | 200 | 0.17 ms | 0.27 ms |

---

## 4. Observations & Throttle Design Context

1. **Sub-millisecond Queue Operations**: All queue operations complete with $p95 < 1\text{ms}$ under 10 concurrent workers. Lock contention under SQLite WAL mode with immediate transactions remains negligible at this concurrency.
2. **Throttle Sizing**: Because database round-trips take $< 0.3\text{ms}$, an unconstrained loop could execute thousands of task transitions per second per worker. This confirms the necessity of the hourly rate limit throttle referenced in `store.go:1762` (`Check hourly rate limit`) to guard against runaway dispatchers.

---

## 5. How to Reproduce

Run the queue smoke benchmark without `-short`:

```bash
go test -v -count=1 -run TestPerfSmokeQueueOps ./internal/controlplane
```

To skip the benchmark during rapid test cycles:

```bash
go test -short ./...
```
