# Living Domain Spec 02 — Worktree Isolation, Agent I/O & Single-Use Write Receipts

- **Domain ID**: `02-isolation-and-receipts`
- **Collision Domains**: `receipt-trust`, `agent-io`, `worker-runtime`
- **Feature IDs**: `FEAT-RECEIPT-01`, `FEAT-FSOPS-01`, `FEAT-WORKER-01`
- **Constitutional Laws**: Law 1 (Zero-Trust Delegation), Law 3 (Blast-Radius Containment), Law 4 (Process GroupReaping)

---

### R1 — Path-Scoped Single-Use Write Receipts {#R1-single-use-write-receipts}

<!-- inherited-from: DELTA-02, DELTA-23, DELTA-31 -->
<!-- feature_id: FEAT-RECEIPT-01 -->

Every `workspace_write` mutation MUST carry a single-use `WriteReceipt` whose `allowed_paths` are enforced in-process and by the post-run `diff_firewall`.

### R2 — Fail-Closed Worktree Isolation & Auto-Prune on Attempt Failure {#R2-worktree-isolation-auto-prune}

<!-- inherited-from: DELTA-26, DELTA-29, DELTA-30, DELTA-39 -->
<!-- feature_id: FEAT-WORKER-01 -->

Every `workspace_write` worker attempt MUST execute inside an isolated git worktree (`wt/<task-id>`). When an attempt fails, times out, or emits a CLI agent `[API Error:` with empty output, the supervisor MUST call `releaseWorktree(false)` to prune the failed worktree and branch immediately.

#### Scenario: Failed workspace_write attempt auto-prunes worktree
<!-- tests: TestWorkspaceWriteFailedAttemptReleasesWorktreeWithKeepFalse -->
- **GIVEN** a `workspace_write` task whose worker command exits non-zero
- **WHEN** `Supervisor.RunOnce` collects the attempt result
- **THEN** `releaseWorktree(false)` is invoked so the failed worktree and `wt/<task-id>` branch are pruned
