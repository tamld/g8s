---
title: "Plan: Supervisor Targeted Coverage & Invariant Hardening"
status: "completed"
created: "2026-09-24"
author: "Antigravity Assistant"
priority: "P1"
tags: ["testing", "supervisor", "coverage", "openmetrics", "pure-go"]
blockedBy: []
blocks: []
---

# Plan: Supervisor Targeted Coverage & Invariant Hardening

## 1. Executive Summary & Socratic Rationale
- **Context**: PR #317 achieved 82.72% aggregate coverage, unblocking CI Quality Gate.
- **Socratic Rationale**: Rather than chasing arbitrary percentage points across superficial getter/setter functions, we target the **single lowest package in the repository**: `internal/supervisor` (44.6%).
- **Empirical Evidence**: Telemetry aggregation (`optimizer.go:Aggregate`), OpenMetrics reporting (`system_metrics.go:ExportPrometheus`), and metric serialization (`metrics.go:EncodeMetrics`) currently have **0.0% coverage**.
- **Objective**: Lift `internal/supervisor` from **44.6%** to **$\ge 82.0\%$**, boosting repository aggregate coverage to $\sim \mathbf{83.9\%}$ without adding brittle mock code.

---

## 2. Invariants & Architecture Constraints
| Rule | Constraint |
| :--- | :--- |
| **Axiom 3: Pure-Go (Zero-CGO)** | All tests execute with `CGO_ENABLED=0` and native Go concurrency. |
| **Zero-Leak Windows Isolation** | Every SQLite test database must register `t.Cleanup(func() { _ = store.Close() })` to prevent Windows file-lock deletion failures. |
| **Deterministic Timing** | Goroutine tests must synchronize using `sync.WaitGroup` and channels; no arbitrary `time.Sleep`. |
| **Layer Ownership (DEBT-34)** | No imports of higher layers (`internal/server`, `internal/cli`, `cmd/...`). |

---

## 3. Work Breakdown & Implementation Phases

### Phase 1: System Metrics & Prometheus Telemetry (`internal/supervisor/system_metrics_test.go`)
- Create `internal/supervisor/system_metrics_test.go`.
- Target functions with 0% coverage:
  - `NewSystemMetricsCollector`
  - `Collect`
  - `CollectWithOptions`
  - `ExportPrometheus`
- Test Scenarios:
  1. `TestSystemMetricsCollector_MultiStateLifecycle`: Populate store with mix of tasks (`QUEUED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `CANCELLED`). Assert `TaskSuccessRate`, `TaskFailureRate`, queue latency, and execution durations.
  2. `TestSystemMetricsCollector_ContextCancellation`: Assert canceled context returns error immediately without leaking connections.
  3. `TestExportPrometheus_Format`: Verify Prometheus text format 0.0.4 output conforms to spec (HELP, TYPE, gauge/counter correctness).
  4. `TestCollectWithOptions_TimeWindow`: Test window boundary filtering.

### Phase 2: Optimizer Aggregation & Time Bounds (`internal/supervisor/optimizer_aggregate_test.go`)
- Create `internal/supervisor/optimizer_aggregate_test.go`.
- Target functions with 0% coverage:
  - `optimizer.go:Aggregate`
  - `optimizer.go:StreamMetrics`
  - `optimizer.go:resolveTimeBounds`
- Test Scenarios:
  1. `TestOptimizer_ResolveTimeBounds`: Validate relative parsing (`"1h"`, `"24h"`, `"7d"`), absolute RFC3339 timestamps, and fallback defaults.
  2. `TestOptimizer_Aggregate`: Feed raw supervisor telemetry into `Aggregate` and verify statistical aggregation (mean cycle seconds, escalation rates, approach counts).
  3. `TestOptimizer_StreamMetrics`: Verify metrics channel streaming and graceful termination upon context cancellation.

### Phase 3: Metric Serialization & Persistence Resilience (`internal/supervisor/metrics_encoding_test.go`)
- Create `internal/supervisor/metrics_encoding_test.go`.
- Target functions with 0% coverage:
  - `metrics.go:EncodeMetrics`
  - `metrics.go:DecodeMetrics`
- Test Scenarios:
  1. `TestMetricsEncodingDecodingRoundtrip`: Validate lossless JSON serialization roundtrip for all telemetry fields.
  2. `TestDecodeMetrics_CorruptedPayload`: Ensure corrupt or malformed metric payloads return clean errors.

### Phase 4: Supervisor Lock Semaphores & Stringer (`internal/supervisor/supervisor_lock_test.go`)
- Create `internal/supervisor/supervisor_lock_test.go`.
- Target functions with 0% coverage:
  - `supervisor.go:Acquire`
  - `supervisor.go:Release`
  - `supervisor.go:String`
  - `planner.go:String`
- Test Scenarios:
  1. `TestSupervisorLock_AcquireRelease`: Test mutual exclusion and semaphore acquisition under concurrent goroutines.
  2. `TestSupervisor_StringMethod`: Test Stringer formatting.

---

## 4. Verification Matrix & DoD
- [x] `go test -count=1 -cover ./internal/supervisor` achieves $\ge 82.0\%$ coverage (achieved **93.6%**).
- [x] Repository aggregate coverage reaches $\ge 83.80\%$ across all 34 packages (achieved **84.16%**).
- [x] `go test -race -count=1 ./internal/supervisor` passes with 0 data races.
- [x] `./tools/pre_push.sh` passes all 12 gates cleanly on macOS, Linux, and Windows runners.
