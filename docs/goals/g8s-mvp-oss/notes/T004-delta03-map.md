# T004 Scout Map — DELTA-03 Control Plane (Python baseline + spec)

> Read-only evidence map for T005 slicing. Line refs enable JIT re-reads during implementation.
> Sources: `spec/openspec/03-controlplane-sqlite-spec.md` (40 lines), `reference/python/scripts/agy_control_plane.py` L70-L976 (receipt tail L977+ already ported), `reference/python/scripts/test_agy_control_plane.py` (377 lines).

## 1. Spec Contract (verbatim)

Go interface mandated by spec:

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

Spec invariants: modernc.org/sqlite only; `journal_mode=WAL`, `synchronous=FULL`, `busy_timeout=30000`; CAS claim via `UPDATE tasks SET state='LEASED', lease_owner=?, lease_expires_at=? WHERE task_id=? AND state='QUEUED'`; prompt NULL + SHA-256 `prompt_hash` on terminal states; optional `parent_task_id`; unique `idempotency_key`.

NOTE: spec does NOT define SubmitTaskRequest/Task/TaskResult/TaskFilter shapes nor maintenance/retry semantics — those come from the Python baseline below. Spec is thinner than baseline; Judge must decide spec-vs-baseline parity target.

## 2. SQLite Schema (`_initialize` L92-188; init uses BEGIN EXCLUSIVE + `PRAGMA user_version` migration gate: accepts 0/1/2/current else RuntimeError "unsupported control-plane schema version"; adds parent_task_id via ALTER if missing)

- `tasks` (20 cols): task_id TEXT PK | parent_task_id TEXT FK->tasks(task_id) | idempotency_key TEXT NOT NULL UNIQUE | schema_version TEXT NOT NULL | state TEXT NOT NULL | priority INTEGER NOT NULL | request_json TEXT NOT NULL | request_hash TEXT NOT NULL | result_json/result_hash TEXT NULL | receipt_hash TEXT NULL | attempts INTEGER NOT NULL DEFAULT 0 | max_attempts INTEGER NOT NULL | lease_owner/lease_token TEXT NULL | lease_expires_at REAL NULL | cancel_requested INTEGER NOT NULL DEFAULT 0 | created_at/updated_at REAL NOT NULL | completed_at REAL NULL | last_error TEXT NULL
- Index `idx_tasks_claim(state, priority DESC, created_at ASC)`
- `task_events`: event_id INTEGER PK AUTOINCREMENT | task_id FK ON DELETE CASCADE | timestamp REAL | event_type TEXT | actor TEXT | details_json TEXT
- Index `idx_task_events_task(task_id, event_id)`
- `control_plane_maintenance`: singleton INTEGER PK CHECK(singleton=1) | owner TEXT | expires_at REAL | updated_at REAL
- `write_receipts`: identical to DELTA-02 schema (already lives in internal/receipt — do not duplicate; integration decision needed)
- Connection pragmas (per-op conn): busy_timeout=30000, foreign_keys=ON, WAL, synchronous=FULL; txns use BEGIN IMMEDIATE; helper `_begin` L191.

## 3. Behavioral Method Map (agy_control_plane.py, class ControlPlane L70)

| Method | Lines | Signature / defaults | Notes |
|---|---|---|---|
| __init__ | 73 | db_path=None, clock=time.time | injectable float clock |
| validate_request | 223 | static, request dict | validation rules incl. path safety (tests 19-21) |
| submit_task | 274 | see body | idempotency via (idempotency_key + request_hash); lineage checks |
| get_task / list_tasks / active_task_count | 366/375/395 | list state=None limit=50 | active count ignores limit |
| reconcile_expired | 406/468 | returns int | expired-lease requeue + retry budget exhaustion |
| begin/end_maintenance | 482/518 | ttl_seconds=300.0, owner | singleton row; blocks claims until released/expired |
| claim_next | 535 | lease_seconds=30.0 | priority DESC, created_at ASC ordering |
| start_task | 599 | task_id, worker_id, lease_token | stale token rejection |
| heartbeat | 622 | see body | extends lease; stale token rejected |
| cancel_task | 650 | actor, reason | queued=terminal; running sets cancel_requested |
| execution_signal | 712 | returns 'active'\|'cancel_requested'\|'lease_lost' | distinguishes cancel from lease loss |
| finish_attempt | 734 | see body | success/fail paths, prompt redaction, receipt_hash |
| pause_task | 823 | see body | |
| events | 893/901 | per task | audit trail |
| export_task | 947 | run_dir | writes artifacts |
| helpers | 40-67 | default_db_path, canonical_json, content_hash, redact_request_json, _atomic_json_write | redaction = SHA-256 |

VERBATIM error strings: NOT extracted here (context economy). Workers MUST read exact method bodies at line ranges above before writing tests asserting messages.

## 4. Test Inventory (test_agy_control_plane.py — 21 tests, class AgyControlPlaneTest; FakeClock mock clock L16)

Idempotency/lineage (4): submit idempotent same request L59; collision rejects different request L67; child preserves lineage + unknown parent rejected L76; collision rejects different parent L93.
Migration/maintenance (3): schema v1 migrates parent column L109; maintenance blocks claims until owner releases L129; maintenance single-owner + expires L149.
Ordering/concurrency (2): priority controls claim order L159; concurrent claim single winner L168 (threads).
Lease lifecycle (6): expired lease requeues then exhausts retry budget L188; heartbeat rejects stale token L203; execution signal distinguishes cancel vs lease loss L221; cancel queued terminal L240; running cancel completed by lease owner L253; stale worker cannot complete L275.
Integrity/redaction (2): receipt hash reproducible unsigned L292; retryable keeps prompt while requeued L321.
Safety/validation (3): workspace_write and no_sandbox blocked L339; scope/application boundaries mandatory L351; traversal to sensitive path blocked L368.
Flags: all timing via FakeClock (no real sleeps found at class level); concurrency via threads L168.

## 5. Candidate Package Layout (input to T005)

Option A verticals: `internal/controlplane/{store.go (schema+migration), queue.go (submit/get/list/claim), lease.go (heartbeat/start/finish/reconcile), maintenance.go, safety.go (validate_request/path rules), model.go (types)}` — single package, file split by concern.
Option B subpackages: store / queue / lease — likely overkill (shared types + tx boundaries argue single package).
Recommendation: Option A single package `internal/controlplane`, mirroring internal/receipt style (Manager struct + NewX(dbPath, clock)), test-first port of 21 tests. Receipt-table duplication resolved by referencing internal/receipt Manager or deferring write_receipts table out of controlplane schema (Judge decision).

## 6. Open Decisions for T005 (Judge)

1. Spec interface names vs Python names (SubmitTask vs submit_task etc.) — spec wins (it is SSoT), but Python semantics fill gaps.
2. write_receipts table ownership: controlplane creates it (baseline parity) vs internal/receipt owns (SoC). Baseline test L292 receipt-hash reproducibility may depend on co-location.
3. Maintenance/reconcile API surface: keep public methods (parity) or fold into ClaimTask?
4. SCHEMA_VERSION constant value + user_version migration support scope (v0/v1/v2 acceptance?).
