# OpenSpec DELTA-31: Receipt Export Status Persistence and Idempotent Operator Recovery (#658)

* **Specification ID**: `DELTA-31`
* **Title**: Receipt Export Status Persistence and Idempotent Operator Recovery
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Milestone**: `M6` (Agent Ergonomics & Safety Hardening)
* **Target Packages**: `internal/controlplane`, `internal/worker`, `cmd/g8s`
* **ADR Reference**: [`docs/decisions/0032-containment-levels.md`](../../docs/decisions/0032-containment-levels.md)
* **Foundational Axioms**: Axiom 1 (Brain Decides, Worker Proves), Axiom 2 (Capability-Scoped Contracts), Issue #42, Issue #658
* **Issue**: `#658`

---

## 1. Context & Motivation

In previous versions, `Supervisor.snapshot` called `ExportReceipt(ctx, taskID, runDir)`, which had a void return signature and discarded export failures. When either the attempt-local directory (`runDir`) or the centralized Evidence Lake (`evidenceDir`) encountered filesystem write or directory-creation errors:
1. The export failure was either swallowed or logged to stderr as an unparsed warning.
2. The task record returned by `snapshot` and persisted in the control plane showed completion without any structured evidence export status.
3. Consumers and supervisors had no programmatic way to verify whether evidence was safely persisted, disabled, or failed per destination.
4. There was no operator-invokable retry mechanism to repair missing or failed evidence artifacts without re-executing the model task.

This specification establishes normative requirements for:
- Structured per-destination export outcome persistence in the canonical `Task` record.
- Complete separation between task execution outcome and evidence export outcome.
- Independent failure isolation between attempt-local and centralized Evidence Lake destinations.
- Stable, deterministic canonical snapshot bytes and digests across initial export and operator retries.
- Operator-invokable export-only retry with idempotency, preserving existing valid artifacts.
- Sanitized export error categories eliminating raw absolute host paths.
- Explicit required vs. optional export acceptance semantics.

---

## 2. Normative Requirements

### Requirement 1: Structured Per-Destination Status Data Model (DELTA-31 R1)

1. **Task Model Field**: `controlplane.Task` MUST include an optional structured export field:
   ```go
   ReceiptExport *ReceiptExportStatus `json:"receipt_export,omitempty"`
   ```
2. **Per-Destination Status Type**:
   ```go
   type ExportDestinationStatus struct {
       Status   string  `json:"status"`             // "disabled", "succeeded", "failed"
       Time     float64 `json:"time,omitempty"`     // unix timestamp of export attempt
       Digest   string  `json:"digest,omitempty"`   // SHA-256 hex digest on success
       Category string  `json:"category,omitempty"` // sanitized category: "mkdir_failed", "write_failed", "disabled", etc.
       Error    string  `json:"error,omitempty"`    // sanitized stable error message (no raw absolute private paths)
       Path     string  `json:"path,omitempty"`     // relative or clean reference path
   }
   ```
3. **Aggregate Status Type**:
   ```go
   type ReceiptExportStatus struct {
       Local          ExportDestinationStatus `json:"local"`               // attempt-local receipt destination
       Central        ExportDestinationStatus `json:"central"`             // centralized Evidence Lake destination
       UpdatedAt      float64                 `json:"updated_at,omitempty"` // last updated timestamp
       Success        bool                    `json:"success"`             // true if all non-disabled/required destinations succeeded
       FullyEvidenced bool                    `json:"fully_evidenced"`     // true if required evidence policy is met
   }
   ```
4. **Storage Persistence**:
   - The SQLite `tasks` table MUST contain a `receipt_export TEXT` column.
   - Schema initialization and `migrateTasksTable` MUST add `receipt_export` idempotently via `ALTER TABLE tasks ADD COLUMN receipt_export TEXT`.
   - `Store` MUST provide `RecordReceiptExport(ctx context.Context, taskID string, status ReceiptExportStatus) error`.
   - `RecordReceiptExport` MUST update only the `receipt_export` column. It MUST NOT mutate `state`, `result_json`, `result_hash`, `receipt_hash`, or `completed_at`.

### Requirement 2: Canonical Snapshot Stability & Digest Invariance (DELTA-31 R2)

1. **Exclusion of Mutable Status**: When generating the canonical `receipt.json` snapshot bytes for export:
   - The exported JSON snapshot MUST exclude the mutable `receipt_export` field (`ReceiptExport = nil`).
   - If `task.CompletedAt` is non-nil, `UpdatedAt` in the snapshot MUST be pinned to `CompletedAt` to eliminate `updated_at` churn.
2. **Digest Invariance**:
   - The SHA-256 digest computed for a given task's export snapshot MUST remain identical across initial export and subsequent retries.
3. **Sealed Execution Receipt Preservation**:
   - The sealed execution `task.ReceiptHash` computed by `buildReceiptTx` MUST NEVER be redefined, overwritten, or synthesized during evidence export or retry.

### Requirement 3: Independent Destination Failure Isolation (DELTA-31 R3)

1. **Independent Recording**:
   - Failure to export to `Local` (attempt directory) MUST NOT prevent an export attempt to `Central` (Evidence Lake), and vice versa.
   - Each destination's status (`disabled`, `succeeded`, `failed`) MUST be evaluated and recorded independently.
2. **Disabled Central Destination**:
   - When Evidence Lake is unconfigured (`evidenceDir == ""`), `Central.Status` MUST be recorded as `disabled`.
3. **Preservation of Execution Outcome**:
   - Failures during evidence export MUST NOT change the task's execution state (`WORKER_COMPLETED`, `SUCCEEDED`, `FAILED`, etc.) or execution result.
4. **Immediate Visibility**:
   - `Supervisor.snapshot` MUST invoke `ExportReceipt` prior to fetching the final `Task` snapshot so that `s.cp.GetTask(ctx, taskID)` immediately reflects the persisted export status.

### Requirement 4: Operator-Invokable Export Retry (DELTA-31 R4)

1. **Narrow Command Surface**:
   - `cmd/g8s` MUST provide an operator-invokable `receipt-export` subcommand (also accessible via `g8s receipt export`).
   - Usage: `g8s receipt-export --task <task-id> [--run-dir <dir>] [--evidence-dir <dir>] [--json]`.
2. **Zero-Execution Guarantee**:
   - The retry operation MUST operate directly on the existing stored task record.
   - It MUST NOT claim tasks, acquire leases, spawn worker subprocesses, or invoke LLM models.
3. **Preservation of Existing Artifacts**:
   - The retry operation MUST verify existing artifacts: if an artifact already exists and its content matches the canonical snapshot digest, it MUST be preserved without re-writing.
   - Only missing or previously failed destinations shall be written.
4. **Idempotency**:
   - Repeated invocations of `receipt-export` against a fully repaired task MUST return success with identical digests and no artifact corruption.

### Requirement 5: Error Sanitization & Privacy Boundary (DELTA-31 R5)

1. **No Absolute Private Paths**:
   - The `Error` string stored in `ExportDestinationStatus` MUST NOT contain raw absolute filesystem paths (e.g. `/Users/...` or `/home/...`).
2. **Stable Error Categories**:
   - The `Category` field MUST use discrete, stable tokens including: `mkdir_failed`, `write_failed`, `stat_failed`, `read_failed`, `digest_mismatch`, `disabled`, and `io_error`.

### Requirement 6: Required vs. Optional Acceptance Semantics (DELTA-31 R6)

1. **Export Policy**:
   - `controlplane.ExportPolicy` specifies required vs. optional export destinations:
     ```go
     type ExportPolicy struct {
         RequireLocal   bool `json:"require_local"`
         RequireCentral bool `json:"require_central"`
     }
     ```
2. **Acceptance Guard**:
   - A task cannot be considered fully evidenced (`FullyEvidenced = false`) if any destination designated as required has failed or is missing.
   - `task.CheckExportAcceptance(policy)` returns an explicit error if required destinations are not successfully exported.

---

## 3. Scenarios & Mechanical Test Pins

#### Scenario: Schema migration persists receipt export status across database close and reopen
<!-- tests: TestReceiptExportPersistenceAndReopen, TestRecordReceiptExportUnknownTask -->
- Given an existing tasks table and a completed task record.
- When `RecordReceiptExport` persists a structured `ReceiptExportStatus` with local and central destination records.
- Then the record persists across database closing and reopening, deserializing with identical fields.
- And recording export status against an unknown task ID returns a not-found error.

#### Scenario: Receipt export status persistence preserves execution state and result hashes
<!-- tests: TestReceiptExportInvariantsExecutionOutcomePreserved -->
- Given a task record with terminal state `WORKER_COMPLETED`, execution results, result hash, and sealed receipt hash.
- When `RecordReceiptExport` is called repeatedly with varying export statuses.
- Then `task.State`, `task.Result`, `task.ResultHash`, `task.ReceiptHash`, and `task.CompletedAt` remain strictly unchanged.

#### Scenario: Canonical snapshot stability guarantees digest invariance across retries
<!-- tests: TestCanonicalReceiptSnapshotStability -->
- Given a task with `ReceiptExport` status and `CompletedAt` timestamp.
- When `CanonicalReceiptSnapshot` generates the export payload for initial write and subsequent retries.
- Then mutable `ReceiptExport` is excluded, `UpdatedAt` is pinned to `CompletedAt`, and SHA-256 byte digests are completely identical.

#### Scenario: Export acceptance policy enforces required destination guarantees
<!-- tests: TestCheckExportAcceptancePolicy -->
- Given tasks with various combinations of local and central export status (`succeeded`, `failed`, `disabled`).
- When evaluated against `ExportPolicy{RequireLocal: true, RequireCentral: true}` or partial requirements.
- Then acceptance succeeds only when all required destinations have succeeded.

#### Scenario: Worker supervisor exports receipt to local and central destinations successfully
<!-- tests: TestWorkerReceiptExport_Success -->
- Given a completed worker task attempt with both attempt directory and centralized Evidence Lake configured.
- When supervisor snapshot exports evidence.
- Then both local and central destinations record `succeeded` status with matching SHA-256 digests and `fully_evidenced: true`.

#### Scenario: Unconfigured Evidence Lake records disabled status without failure
<!-- tests: TestWorkerReceiptExport_DisabledCentral -->
- Given a supervisor run without centralized Evidence Lake (`evidenceDir == ""`).
- When receipt export executes.
- Then `Local.Status` is `succeeded`, `Central.Status` is `disabled`, and overall success is preserved.

#### Scenario: Local-only and central-only export failures isolate independently
<!-- tests: TestWorkerReceiptExport_LocalOnlyFailure, TestWorkerReceiptExport_CentralOnlyFailure, TestWorkerReceiptExport_BothFailed -->
- Given a filesystem write or directory-creation blockage at either attempt-local or central Evidence Lake locations.
- When receipt export runs.
- Then the blocked destination records `failed` with sanitized category and error (no absolute host paths), while the unblocked destination proceeds and succeeds independently.
- And when both destinations are blocked, both record `failed` without mutating the task execution outcome.

#### Scenario: Operator receipt-export command repairs failed exports idempotently
<!-- tests: TestReceiptExportCLI_SuccessAndIdempotency, TestReceiptExportCLI_RepairWorkflow -->
- Given a task whose export previously encountered a destination failure.
- When the operator resolves the filesystem obstruction and runs `g8s receipt-export --task <id>`.
- Then the missing destination is exported without re-executing the model, existing valid destinations are verified and preserved, and repeated invocations remain strictly idempotent.

#### Scenario: Control plane GetTask immediately reflects persisted export status
<!-- tests: TestCanonicalGetTaskReflectsReceiptExport -->
- Given a task completing through the worker supervisor.
- When `GetTask` is called by the supervisor or external caller immediately following attempt snapshot.
- Then the returned task model contains populated `ReceiptExport` status reflecting actual export outcomes.
