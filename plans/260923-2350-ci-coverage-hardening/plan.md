---
title: "Plan: Repo-Wide Coverage Hardening to Exceed 80% CI Threshold"
status: "in_progress"
created: "2026-09-23"
author: "Antigravity Assistant"
priority: "P0"
tags: ["ci-cd", "coverage", "testing", "pre-push", "quality-gate", "pure-go"]
blockedBy: []
blocks: []
---

# Plan: Repo-Wide Coverage Hardening to Exceed 80% CI Threshold

## 1. Executive Summary & Root Cause Analysis

### 1.1 The Symptom
Pushes to remote (`main` and PR branches) consistently fail on GitHub Actions:
* ❌ `Quality / Quality Gate (push)`: Failing at step `Coverage threshold >= 80%`
* ❌ `Quality / Verify Gate (push)`: Failing after 3s because `quality` failed
* ✅ All other 8 checks (Ubuntu, macOS, Windows, Cross-Platform Build, Crash-Survival, Version Sync) pass 100%.

### 1.2 The Two Root Causes
1. **Mathematical Deficit in CI Formula**:
   GitHub Actions workflow `.github/workflows/quality.yml` computes the **unweighted arithmetic mean** across 34 packages:
   $$\text{Aggregate Coverage} = \frac{\sum_{i=1}^{34} \text{PkgCoverage}_i}{34}$$
   Current aggregate coverage is **79.71%**, failing the strict **80.00%** threshold by just **0.29%**.
   Three packages with low unit-test coverage heavily drag down the average:
   * `internal/server`: **8.3%** (604 lines of HTTP API endpoints; 500+ statements uncovered)
   * `internal/supervisor`: **44.6%** (`system_metrics.go`, `metrics.go` untouched)
   * `internal/reflex`: **77.0%** (`WithTimeout`, `rotateKey`, `.env` parsing uncovered)
2. **Local vs Remote Discrepancy (Blind Spot in `pre_push.sh`)**:
   `tools/pre_push.sh` Gate 10 runs `go test -count=1 ./...` without the `-cover` flag and does not calculate aggregate coverage. Developers see `ALL PRE-PUSH CHECKS PASSED` locally, but push code that fails remote CI.

---

## 2. Invariant Rules & Constraints

| Rule | Requirement |
| :--- | :--- |
| **Axiom 3: Pure-Go (Zero-CGO)** | All new tests must execute cleanly under `CGO_ENABLED=0` and standard `net/http/httptest`. |
| **Strict Behavioral Equivalence** | No production business logic or public contracts are modified; changes are strictly additive test coverage and CI synchronization. |
| **DLP & Credential Hygiene** | Mock HTTP servers and `.env` parsing tests must use dummy strings (`dummy-key-1`, `mock-token`), never live credentials. |
| **Pre-Push Parity** | `tools/pre_push.sh` must calculate the exact same aggregate metric as `.github/workflows/quality.yml`. |

---

## 3. High-Leverage Coverage Strategy

To gain the **+0.29%** needed to cross 80.00% (and reach **82.5%+** for a healthy buffer), we focus on the highest-leverage packages where small test suites yield massive coverage jumps:

```
┌───────────────────────┬──────────────┬──────────────┬───────────────────────────────┐
│ Package               │ Current Cov  │ Target Cov   │ Gain on 34-Pkg Aggregate Mean │
├───────────────────────┼──────────────┼──────────────┼───────────────────────────────┤
│ internal/reflex       │    77.0%     │    95.0%     │ +18.0% / 34 = +0.53%          │
│ internal/server       │     8.3%     │    55.0%     │ +46.7% / 34 = +1.37%          │
│ internal/supervisor   │    44.6%     │    65.0%     │ +20.4% / 34 = +0.60%          │
├───────────────────────┴──────────────┴──────────────┼───────────────────────────────┤
│ Total Expected Aggregate Coverage:                  │ 79.71% + 2.50% = 82.21%       │
└─────────────────────────────────────────────────────┴───────────────────────────────┘
```

---

## 4. Implementation Phases

### Phase 1: Harden `internal/reflex` Coverage (77.0% ➔ 95.0%)
* Target file: `internal/reflex/jev_test.go`
* Tests to add:
  1. `TestReflexGateOptions`: Test `WithTimeout(500 * time.Millisecond)`.
  2. `TestKeyRotationAndCurrentKey`: Test key cycling through `currentKey()` and `rotateKey()` across multiple mock keys.
  3. `TestLoadKeysFromEnvFile`: Test `.env` file scanning and parser with dummy `TYPESAFE_API_KEYS` and `TYPESAFE_API_KEY_FALLBACK` using `t.TempDir()`.
  4. `TestEmitSignalNetworkAndRateLimitErrors`: Exercise HTTP 429 rate limit key rotation and HTTP 500 error failover paths in `EmitSignal`.

### Phase 2: Harden `internal/server` Coverage (8.3% ➔ 55.0%+)
* Target file: `internal/server/server_test.go`
* Tests to add using `httptest.NewServer` or `httptest.NewRecorder`:
  1. `TestOpenAPIRoutes`: Test `/openapi.json` and `/openapi` return 200 with JSON / HTML.
  2. `TestTaskRoutes`: Test `GET /api/v1/tasks`, `POST /api/v1/tasks`, and `GET /api/v1/tasks/{id}`.
  3. `TestReceiptRoutes`: Test `GET /api/v1/receipts`, `POST /api/v1/receipts`, and `GET /api/v1/receipts/{id}`.
  4. `TestBriefRoutes`: Test `GET /api/v1/briefs` and `GET /api/v1/briefs/{id}`.
  5. `TestCORSMiddleware`: Test OPTIONS preflight requests and allowed vs forbidden origins.

### Phase 3: Harden `internal/supervisor` Coverage (44.6% ➔ 65.0%+)
* Target file: `internal/supervisor/system_metrics_test.go` & `metrics_test.go`
* Tests to add:
  1. `TestSystemMetricsCollector`: Exercise `NewSystemMetricsCollector().Collect(ctx)` and `ExportPrometheus()`.
  2. `TestMetricsEncodingDecoding`: Exercise `EncodeMetrics` and `DecodeMetrics` roundtrip.
  3. `TestOptimizerAggregation`: Exercise `optimizer.Aggregate(ctx, req)` and `resolveTimeBounds`.

### Phase 4: Local Pre-Push Parity Gate
* Target file: `tools/pre_push.sh`
* Action:
  Add Gate 10b to `tools/pre_push.sh`:
  Compute aggregate coverage using the exact `awk` script from `.github/workflows/quality.yml`.
  If aggregate coverage is `< 80%`, fail immediately with a clear diagnostic table of lowest-performing packages.

---

## 5. Verification Matrix & DoD
- [x] `go test -count=1 -cover ./internal/reflex` reaches $\ge 90\%$ (Achieved: **93.7%**).
- [x] `go test -count=1 -cover ./internal/server` reaches $\ge 50\%$ (Achieved: **93.8%**).
- [x] Total repository aggregate coverage exceeds $81.5\%$ across all 34 packages (Achieved: **82.71%**).
- [x] `./tools/pre_push.sh` runs cleanly with new coverage verification gate (Passed all 12 gates).
- [ ] Pushed to GitHub Actions and all 10 checks turn **GREEN** (including `Quality Gate` and `Verify Gate`).
