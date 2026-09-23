# Brainstorming Design: Supervisor Targeted Coverage & Invariant Hardening

- **Date**: 2026-09-24
- **Author**: Antigravity Assistant
- **Status**: Agreed / Approved by User (Socratic Dialogue)
- **Scope**: `internal/supervisor`
- **Current Coverage**: 44.6%
- **Target Coverage**: $\ge 82.0\%$ (Pushing repo aggregate to $\sim 84.0\%$)

---

## 1. Problem Statement & Socratic Challenge

### 1.1 The Symptom & The Fallacy
- After raising repository coverage from 77.57% to 82.72% (unblocking CI Quality Gate), a naive goal would be blindly aiming for $\ge 85\%$ across random packages.
- **Socratic Dilemma (Goodhart's Law)**: Chasing arbitrary percentage points leads to fragile mock-theaters and zero security gain.
- **Empirical Reality**: Profiling (`go tool cover -func`) reveals that `internal/supervisor` is the **single lowest package** in the repository at **44.6%**, with foundational telemetry and optimization modules sitting at **0.0%** coverage.

### 1.2 Identified Zero-Coverage Voids in Supervisor
1. `system_metrics.go`: `NewSystemMetricsCollector`, `Collect`, `CollectWithOptions`, `ExportPrometheus` (all 0.0%).
2. `optimizer.go`: `Aggregate`, `StreamMetrics`, `resolveTimeBounds` (all 0.0%).
3. `metrics.go`: `EncodeMetrics`, `DecodeMetrics` (all 0.0%).
4. `supervisor.go`: `Acquire`, `Release` lock semaphores (0.0%).

---

## 2. Evaluated Approaches & Trade-offs

| Approach | Pros | Cons | Verdict |
| :--- | :--- | :--- | :--- |
| **Approach 1: Broad Repo Dilution**<br>(Raise 4 packages a little bit: supervisor, controlplane, provider, autopilot) | Reaches 85% aggregate number on paper. | High maintenance surface, mocks LLM APIs (fragile), distracts from the core brain. | ❌ Rejected (Cowboy/Naive) |
| **Approach 2: Targeted Surgical Hardening (Recommended)**<br>(Focus 100% on `internal/supervisor` from 44.6% to $\ge 82\%$) | Directly protects the decision brain; zero fake mocks; fixes untested telemetry; adds +1.19% aggregate gain without flakiness. | Does not hit 85% aggregate alone (reaches ~83.9%), but delivers 10x higher engineering value. | ✅ **Selected** |
| **Approach 3: Fuzzing Only**<br>(Ignore statement coverage, write state machine fuzzers) | Discovers edge-case crashes. | Leaves 0% statement blocks in metrics untouched; doesn't satisfy CI/CD deterministic gates. | ❌ Deferred |

---

## 3. Detailed Technical Design: `internal/supervisor` Hardening

### 3.1 Suite 1: System Metrics & OpenMetrics/Prometheus (`system_metrics_test.go`)
- **Scope**: Cover `system_metrics.go` (100% currently untested).
- **Test Scenarios**:
  1. `TestSystemMetricsCollector_MultiStateLifecycle`: Populate test store with realistic task mix (`QUEUED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `CANCELLED`). Verify calculated `TaskSuccessRate`, `TaskFailureRate`, queue latency, and execution durations.
  2. `TestSystemMetricsCollector_ContextCancellation`: Verify `Collect(canceledCtx)` returns immediate error without leaking store connections.
  3. `TestExportPrometheus_FormatCompliance`: Verify output conforms to Prometheus text format 0.0.4 (HELP, TYPE, gauge/counter correctness).
  4. `TestCollectWithOptions_TimeWindow`: Verify custom time window filtering behaves deterministically.

### 3.2 Suite 2: Optimizer Aggregation & Time Bounds (`optimizer_aggregate_test.go`)
- **Scope**: Cover `optimizer.go:212` (`Aggregate`), `StreamMetrics`, `resolveTimeBounds`.
- **Test Scenarios**:
  1. `TestOptimizer_ResolveTimeBounds`: Test relative durations (`"1h"`, `"24h"`, `"7d"`), absolute RFC3339 timestamps, and empty fallback.
  2. `TestOptimizer_AggregateTelemetry`: Feed raw supervisor telemetry into `Aggregate` and verify metric summary statistics (mean, success rate, escalation ratios).
  3. `TestOptimizer_StreamMetricsCancel`: Test cancellation of `StreamMetrics` channel when context closes.

### 3.3 Suite 3: Metrics Serialization & Storage (`metrics_test.go`)
- **Scope**: Cover `metrics.go:82-99` (`EncodeMetrics`, `DecodeMetrics`).
- **Test Scenarios**:
  1. `TestMetricsEncodingDecodingRoundtrip`: Validate lossless JSON serialization roundtrip for all telemetry fields.
  2. `TestDecodeMetrics_CorruptedPayload`: Ensure corrupt or malformed metric payloads return clean descriptive errors.

### 3.4 Suite 4: Supervisor Lock Semaphores (`supervisor_lock_test.go`)
- **Scope**: Cover `supervisor.go:120-128` (`Acquire`, `Release`).
- **Test Scenarios**:
  1. `TestSupervisorLock_AcquireRelease`: Test concurrency lock semantics under concurrent goroutines.
  2. `TestSupervisor_StringMethod`: Test Stringer formatting.

---

## 4. Invariants & Guardrails

1. **Axiom 3 (Pure-Go / Zero-CGO)**: Use in-memory / temporary SQLite with `t.Cleanup(func() { _ = store.Close() })` to prevent Windows file-lock regressions.
2. **Zero-Flake Concurrency**: Goroutine tests must synchronize with `sync.WaitGroup` and bounded channels. No arbitrary `time.Sleep` calls.
3. **Layer Ownership (DEBT-34)**: `internal/supervisor` must not import forbidden higher layers.

---

## 5. Success Metrics & Evidence Checklist

- [ ] `internal/supervisor` coverage increases from **44.6%** to **$\ge 82.0\%$**.
- [ ] Repository aggregate coverage increases from **82.72%** to **$\ge 83.80\%$**.
- [ ] All 12 gates of `./tools/pre_push.sh` pass cleanly (including race detector).
- [ ] No regression on any platform (macOS, Linux, Windows).
