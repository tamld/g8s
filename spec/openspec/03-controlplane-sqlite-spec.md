# OpenSpec DELTA-03: SQLite WAL Control Plane, Atomic CAS Leases & Task Queue

**Status**: `PROPOSED`  
**Milestone**: M1 (Foundation)  
**Package**: `internal/controlplane`  

---

## 1. Goal & Context
Define the pure-Go SQLite WAL Task Queue and Control Plane for `g8s`. This subsystem guarantees atomic Compare-And-Swap (CAS) task claiming, distributed heartbeats, idempotent submissions, parent-child task lineage, and prompt hash redaction upon task termination.

---

## 2. Core Specifications

1. **Pure-Go Driver**: Exclusively uses `modernc.org/sqlite` with `PRAGMA journal_mode = WAL`, `PRAGMA synchronous = FULL`, and `PRAGMA busy_timeout = 30000`.
2. **Atomic CAS Task Claim**: Worker leases a task via `UPDATE tasks SET state = 'LEASED', lease_owner = ?, lease_expires_at = ? WHERE task_id = ? AND state = 'QUEUED'`.
3. **Prompt Redaction**: When a task transitions to terminal states (`SUCCEEDED`, `FAILED`, `CANCELLED`), `prompt` is overwritten with NULL and stored as SHA-256 `prompt_hash`.
4. **Task Lineage & Idempotency**: Tasks support optional `parent_task_id` and unique `idempotency_key`.

---

## 3. Go Interface Definition

```go
package controlplane

import "context"

type ControlPlane interface {
    SubmitTask(ctx context.Context, req SubmitTaskRequest) (*Task, error)
    ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*Task, error)
    RenewHeartbeat(ctx context.Context, taskID string, workerID string, extensionSeconds int) error
    CompleteTask(ctx context.Context, taskID string, result TaskResult) error
    FailTask(ctx context.Context, taskID string, reason string, exitCode int) error
    CancelTask(ctx context.Context, taskID string, reason string) error
    GetTask(ctx context.Context, taskID string) (*Task, error)
    ListTasks(ctx context.Context, filter TaskFilter) ([]Task, error)
}
```
