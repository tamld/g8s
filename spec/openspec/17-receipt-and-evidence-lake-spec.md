# OpenSpec DELTA-17: Receipt Lake Wiring & Closed-Loop Telemetry Ledger

* **Specification ID**: `DELTA-17`
* **Title**: Receipt Lake Control-Plane Wiring & Closed-Loop Telemetry Ingestion Engine
* **Status**: `APPLIED`
* **Milestone**: `M4` (Evidence Lake & Telemetry)
* **Target Packages**: `internal/controlplane`, `internal/orchestrator`, `internal/telemetry`
* **Tracking Issues**: #85, #103, #253

---

## 1. Context & Motivation

`DELTA-17` wires cryptographic Write Receipts and worker execution artifacts directly into the SQLite control plane (`v5` Receipt Lake schema migration) and establishes the closed-loop telemetry ingestion and failure-distillation engine (`internal/telemetry`).

---

## 2. ADDED Requirements

### Requirement: Control Plane Receipt Lake Schema & Propagation (`internal/controlplane`, `internal/orchestrator`)

1. The SQLite control plane (`migrateReceiptLake`, schema v5) SHALL persist `orchestrator_id`, `worktree_id`, `write_receipt_id`, `work_summary`, `artifact_hashes`, and `completed_at` on task records.
2. `orchestrator.FanOut` and `Drive` SHALL propagate `WriteReceipt` metadata from worker execution receipts into the control plane Receipt Lake.

#### Scenario: receipt lake migration and orchestrator receipt propagation
<!-- tests: TestFreshDatabaseSchemaExact, TestFanOutReceiptPropagation, TestDriveReceiptLakePropagation -->
- Worker receipts and artifact hashes are persisted into the SQLite control plane task row upon attempt completion.

### Requirement: Closed-Loop Telemetry Ingestion & Preflight Pattern Injection (`internal/telemetry`)

1. `internal/telemetry` SHALL ingest structured execution events into SQLite, distill recurring failure signatures into ranked patterns, and inject bounded preflight warnings (`InjectPreflight`) within a strict token/byte budget, degrading gracefully when offline.

#### Scenario: telemetry ingestion, failure distillation, and budget-capped preflight injection
<!-- tests: TestTelemetryEngine_IngestAndQuery, TestTelemetryEngine_DistillFailure, TestTelemetryEngine_PreflightInjection, TestInjectPreflightBudgetCap, TestInjectWorksWithSensorDown -->
- Execution failures are distilled into queryable patterns and injected into preflight prompts without exceeding the configured budget cap.
