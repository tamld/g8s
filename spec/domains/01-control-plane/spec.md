# Living Domain Spec 01 — Durable SQLite Control Plane & State Machine

- **Domain ID**: `01-control-plane`
- **Collision Domain**: `state-store`
- **Feature IDs**: `FEAT-CONTROLPLANE-01`
- **Constitutional Laws**: Law 2 (Deterministic State), Law 6 (Evidence over Verdicts)

---

### R1 — Atomic CAS Task State Transitions & Single-Winner Claiming {#R1-atomic-cas-claiming}

<!-- inherited-from: DELTA-03, DELTA-12, DELTA-15, DELTA-17 -->
<!-- feature_id: FEAT-CONTROLPLANE-01 -->

All task lifecycle transitions (`QUEUED` $\to$ `RUNNING` $\to$ `COMPLETED` / `FAILED` / `CANCELLED` / `NEEDS_INFO`) MUST execute inside the pure-Go SQLite WAL control plane with Compare-And-Swap (`CAS`) lease semantics so that concurrent workers never double-claim a task.

#### Scenario: Canonical control-plane CLI surface is registered
<!-- tests: TestCanonicalCommandsAndAliases -->
- **GIVEN** the canonical CLI surface registry
- **WHEN** `CanonicalCommands()` is queried
- **THEN** `submit`, `get`, `tasks`, `cancel`, `resume`, `resubmit`, `lineage`, `children`, `state`, and `migrate` are registered
