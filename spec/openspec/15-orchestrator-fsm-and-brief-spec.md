# OpenSpec DELTA-15: Orchestrator FSM Lifecycle & Brief Dispatch Contract

* **Specification ID**: `DELTA-15`
* **Title**: Orchestrator 8-State FSM Lifecycle, Pure State Validator & Brief Dispatch Store
* **Status**: `APPLIED`
* **Milestone**: `M4` (Orchestrator FSM & Brief Workflow)
* **Target Packages**: `internal/orchestrator`, `internal/state`, `internal/brief`, `internal/controlplane`
* **Tracking Issues**: #82, #83, #84, #91, #101, #108, #117, #123

---

## 1. Context & Motivation

`DELTA-15` establishes the deterministic 8-state Orchestrator Finite State Machine (`PLAN` -> `SPAWN` -> `MONITOR` -> `RECEIPT` -> `MERGE` | `ESCALATE` | `CANCEL` | `CONFLICT`), pure event-sourced state transition validation (`internal/state`), and the structured Brief issuance/consumption contract (`internal/brief` + SQLite `briefs` table) bridging planning and execution.

---

## 2. ADDED Requirements

### Requirement: Deterministic 8-State Orchestrator FSM (`internal/orchestrator`, `internal/state`)

1. The orchestrator lifecycle SHALL enforce transitions across the 8 canonical states:
   `PLAN` -> `SPAWN` -> `MONITOR` -> `RECEIPT` -> (`MERGE` | `ESCALATE` | `CANCEL` | `CONFLICT`).
2. Terminal states (`MERGE`, `ESCALATE`, `CANCEL`, `CONFLICT`) SHALL reject all subsequent transition inputs.
3. `internal/state` SHALL validate both Orchestrator FSM transitions and Task FSM transitions (`QUEUED`, `LEASED`, `RUNNING`, `NEEDS_INFO`, `BLOCKED`, `SUCCEEDED`, `FAILED`, `CANCELLED`) with deterministic event-log replay.

#### Scenario: FSM happy path, retry, escalation, conflict, and terminal immutability
<!-- tests: TestFSMInitialState, TestFSMHappyPath, TestFSMRetryPath, TestFSMEscalationPath, TestFSMConflictPath, TestFSMTerminalRejectsInput, TestStateApplyTransitions, TestEventLogAndReplay -->
- Valid transition sequences advance the FSM and record timestamped history; invalid or post-terminal transitions return typed errors.

### Requirement: Receipt Verification Layer (`internal/orchestrator`)

1. `ReceiptVerifier` and `StdoutEnvelopeVerifier` SHALL inspect worker execution receipts and stdout JSON envelopes before transitioning from `RECEIPT` to `MERGE`.
2. A worker process that exits with code `0` but emits an error envelope (`ok: false` or contract violation) SHALL fail verification and never be accepted as a clean merge.

#### Scenario: stdout envelope verifier rejects false-positive exit zero
<!-- tests: TestStdoutEnvelopeVerifier -->
- Worker receipts carrying `ok: false` in the stdout envelope fail verification even when the process exit code is `0`.

### Requirement: Structured Brief Issuance, TTL & SQLite Persistence (`internal/brief`, `internal/controlplane`)

1. `internal/brief` and `internal/controlplane` (schema migration v6) SHALL persist immutable task briefs with idempotent migration, TTL expiration, single-use consumption (`CONSUMED`), and active listing via `g8s brief-issue` and `g8s brief-consume`.

#### Scenario: brief lifecycle roundtrip, TTL expiry, and SQLite v6 migration
<!-- tests: TestBriefRoundtrip, TestBriefTTLExpiry, TestBriefConsumeUnknownID, TestBriefStoreLifecycle, TestBriefStoreMigrationFromV5, TestBriefStoreMigrationIdempotent -->
- Issuing a brief persists it to SQLite; consuming it transitions its status atomically; expired or unknown briefs are rejected.
