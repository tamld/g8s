---
title: "Plan: Decouple Jev Reflex Sensor from Supervisor Policy Engine"
status: "completed"
created: "2026-09-23"
author: "Antigravity Assistant"
priority: "P1"
tags: ["reflex", "jev", "sensor", "supervisor", "architecture", "pure-go"]
blockedBy: []
blocks: []
---

# Plan: Decouple Jev Reflex Sensor from Supervisor Policy Engine

## 1. Executive Summary & Problem Definition

### 1.1 Architectural Flaw (The Category Error)
In `internal/reflex/jev.go`, the initial prototype implemented a Jev integration with TypeSafe AI (`api.typesafe.ai/v1/systemone`). However, the implementation committed a critical architectural category error:
* **The Error**: It asked the Jev model: *"What action should g8s supervisor take on this subagent workspace mutation?"* with choices `grant_receipt`, `escalate_human`, `abort_process`.
* **The Root Cause**: This improperly outsourced the **Supervisor's strategic decision-making authority** to a fast System 1 classifier.

### 1.2 The Principle: Sensor vs Supervisor
* **Sensor (Perception Layer)**: Measures data and produces empirical telemetry signals (e.g. `RiskScore`, `BreachProb`, `AnomalyScore`). A sensor has **no agency or authority**.
* **Supervisor (Cognition / Governance Layer)**: Evaluates signals against project policy, Definition of Done (DoD), attempt budgets, and state machine constraints. The Supervisor **owns 100% of the strategic decisions**.

### 1.3 The Objective
Refactor `internal/reflex` to:
1. Strip all action delegation questions (`grant_receipt`, `abort_process`) from Jev payloads.
2. Reposition Jev as an empirical **Telemetry / Risk Sensor** emitting `ReflexSignal`.
3. Establish a deterministic **Policy Engine** within `g8s` that consumes `ReflexSignal` and decides state transitions and receipt granting.
4. Maintain full offline deterministic fallback when API keys are absent.

---

## 2. Invariant Rules & Boundaries

| Rule | Requirement |
| :--- | :--- |
| **Axiom 1: Two-Tier Governance** | Receipt issuance and process abortion remain strictly under `g8s` Supervisor control. No external model directly signs receipts. |
| **Axiom 3: Pure-Go (Zero-CGO)** | All HTTP client and telemetry parsing logic uses Go standard library (`net/http`, `encoding/json`). |
| **Rule 4: Zero-Leakage & Data Hygiene** | Request payloads to external sensors never transmit raw credential files or full confidential source code; only sanitized path metadata and bounded diff statistics. |
| **Rule 5: Clock Dependency Injection** | Latency and timestamp metrics use injectable clock. |

---

## 3. Architecture & Data Flow

```
┌────────────────────────────────────────────────────────┐
│               SUBAGENT MUTATION REQUEST                │
│       (TaskID, FilesModified, DiffSummary, Paths)       │
└───────────────────────────┬────────────────────────────┘
                            │
                            ▼
┌────────────────────────────────────────────────────────┐
│           TẦNG CẢM BIẾN (REFLEX SENSOR)                │
│  `ReflexSensor` interface:                             │
│   - JevSensor (api.typesafe.ai/v1/systemone)           │
│   - DeterministicFallbackSensor (Pure-Go Heuristic)    │
│                                                        │
│  Output: `ReflexSignal`                                │
│   • RiskScore: float64 (0.0 - 5.0)                     │
│   • BreachProb: float64 (0.0 - 1.0)                    │
│   • AnomalyFlag: bool                                  │
│   • LatencyMs: int64                                   │
│   • Source: "jev" | "fallback"                         │
└───────────────────────────┬────────────────────────────┘
                            │ (Pure Telemetry Signal)
                            ▼
┌────────────────────────────────────────────────────────┐
│        TẦNG GIÁM SÁT (g8s SUPERVISOR POLICY ENGINE)    │
│  Evaluates `ReflexSignal` against Repo Policy:         │
│   - If BreachProb >= Threshold ➔ ActionInstantKill     │
│   - If RiskScore <= SafeThreshold & Scope Clean        │
│       ➔ ActionGrantReceipt (Issues Schema v3 Receipt)  │
│   - Else ➔ ActionEscalateHITL                          │
└────────────────────────────────────────────────────────┘
```

---

## 4. Implementation Steps

### Phase 1: Model & Contract Refactoring
1. Define `ReflexSignal` representing pure sensor telemetry:
   ```go
   type ReflexSignal struct {
       RiskScore   float64 `json:"risk_score"`   // 0.0 to 5.0
       BreachProb  float64 `json:"breach_prob"`  // 0.0 to 1.0
       Confidence  float64 `json:"confidence"`   // 0.0 to 1.0
       LatencyMs   int64   `json:"latency_ms"`
       Source      string  `json:"source"`       // "jev" | "deterministic"
       Reason      string  `json:"reason"`
   }
   ```
2. Refactor Jev prompt payload in `internal/reflex/jev.go`:
   * Remove question `action` (`grant_receipt`, `escalate_human`, `abort_process`).
   * Retain questions `risk_tier` (score) and `sandbox_breach` (noul).
3. Introduce `PolicyEngine` in `internal/reflex` or `internal/supervisor`:
   * Deterministically evaluates `ReflexSignal` + `TriageRequest` to produce `TriageVerdict` (`ActionGrantReceipt`, `ActionEscalateHITL`, `ActionInstantKill`).

### Phase 2: Unit & Live Tests
1. Update `internal/reflex/jev_test.go`:
   * Verify Jev returns `ReflexSignal` without action prescriptions.
   * Verify `PolicyEngine` enforces strict deterministic thresholds on mock signals.
   * Verify `deterministicFallback` operates smoothly offline.
2. Update `internal/reflex/jev_live_test.go` to test real System 1 API telemetry.

---

## 5. Verification Matrix
- [x] `CGO_ENABLED=0 go test -count=1 ./internal/reflex/...` passes.
- [x] `CGO_ENABLED=1 go test -race -count=1 ./internal/reflex/...` passes with zero race warnings.
- [x] Pre-push CI quality gates (`./tools/pre_push.sh`) pass 12/12.
