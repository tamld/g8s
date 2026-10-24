# OpenSpec DELTA-26: Fail-Closed Isolation for Workspace Write Attempts (#656)

* **Specification ID**: `DELTA-26`
* **Title**: Fail-Closed Worktree Isolation for Workspace Write Tasks
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Milestone**: `M6` (Agent Ergonomics & Safety Hardening)
* **Target Packages**: `internal/worker`
* **ADR Reference**: [`docs/decisions/0032-containment-levels.md`](../../docs/decisions/0032-containment-levels.md)
* **Foundational Axioms**: Axiom 2 (Capability-Scoped Write Receipts), ADR-0032 (F0/F1/F2 Containment Tiers)
* **Issue**: `#656`

---

## 1. Context & Motivation

In `#427`, per-attempt worktree isolation was introduced for `workspace_write` attempts so concurrent tasks within one worker process do not clobber each other's uncommitted deliverables. However, `#427` permitted a fallback to the shared checkout whenever an isolator was nil or failed to acquire a worktree.

Under strict zero-trust containment (Axiom 2, ADR-0032), unisolated workspace mutations risk silent state corruption, dirty-checkout races, and receipt boundary escapes. Issue `#656` mandates that isolation for `workspace_write` MUST fail closed:
1. A nil isolator is rejected immediately.
2. An `AcquireWorktree` or `AcquireRepoWorktree` error is rejected immediately.
3. An empty worktree directory returned with nil error is rejected immediately.
4. When isolation is unavailable, the worker MUST NOT invoke the child command, MUST release any partial worktree resources without leaking leases, and MUST finish the attempt terminally with a structured `isolation_unavailable` status.
5. Read-only attempts (`read_only` permission) and valid isolated `workspace_write` executions remain unaffected.

---

## 2. Normative Requirements

### Requirement: Fail-closed isolation for workspace_write (DELTA-26 R1)

When a claimed task specifies `permission: "workspace_write"`:
1. **Nil Isolator Rejection**: If `s.isolator == nil`, the supervisor MUST abort the attempt without executing the child process.
2. **Acquisition Error Rejection**: If `s.isolator.AcquireWorktree` (or `RepoWorktreeIsolator.AcquireRepoWorktree` for cross-repo dispatches) returns a non-nil error, the supervisor MUST abort the attempt without executing the child process.
3. **Empty Directory Rejection**: If the isolator returns an empty or whitespace-only directory string with a nil error, the supervisor MUST abort the attempt without executing the child process.
4. **Structured Failure Status**: The aborted attempt MUST be finished via `FinishAttempt` with:
   - `Success: false`
   - `Retryable: false`
   - `Result`: JSON envelope containing `"ok": false`, `"status": "isolation_unavailable"`, and diagnostic `"error"` / `"reason"` text.
   - `Err`: Sanitized failure description.
5. **No Resource / Lease Leak**: If the isolator returned a non-nil `release` function before failing, `release(false)` MUST be invoked. The attempt MUST be marked finished on the control plane so the lease is released cleanly.
6. **No Shared-Write Bypass**: No flag, environment variable, or configuration option shall permit falling back to shared checkout for `workspace_write`.

### Requirement: Preservation of Read-Only & Isolated Execution (DELTA-26 R2)

1. **Read-Only Invariant**: Claimed tasks with `permission: "read_only"` (or non-`workspace_write`) MUST NOT consult the worktree isolator and MUST continue executing in the shared or requested directory.
2. **Isolated Execution & Deliverable Invariant**: When isolation succeeds (`err == nil` and non-empty directory), the child process MUST spawn within the acquired worktree, and the worktree MUST be released with `keep: true` upon attempt completion to preserve deliverable artifacts for F0 harvesting.
3. **Cross-Repo Dispatch Invariant**: When `req.AddDirs` specifies a distinct external repository, a `RepoWorktreeIsolator` MUST receive the resolved repository directory and task ID.

---

## 3. Scenarios & Mechanical Test Pins

#### Scenario: Nil worktree isolator fails closed without command execution
<!-- tests: TestIsolationFailureNilIsolator -->
- Given a task with `permission: "workspace_write"` and a worker supervisor initialized without an isolator (`isolator == nil`).
- When `RunOnce` executes the attempt.
- Then the child process runner is NEVER spawned (`spawnCount == 0`).
- And the task transitions to `WORKER_COMPLETED` with non-retryable failure, `ok: false`, and `status: "isolation_unavailable"`.
- And control-plane leases are fully released (`lease_token == nil`, `lease_owner == nil`).

#### Scenario: Worktree acquisition error fails closed without leaking leases
<!-- tests: TestIsolationFailureAcquireError -->
- Given a task with `permission: "workspace_write"` and an isolator whose `AcquireWorktree` returns an error.
- When `RunOnce` executes the attempt.
- Then the child command is NEVER spawned.
- And the attempt terminates immediately with `ok: false` and `status: "isolation_unavailable"`.
- And any partial worktree resources are released with `keep: false`, leaving no leaked lease tokens on the control plane.

#### Scenario: Empty worktree directory string fails closed
<!-- tests: TestIsolationFailureEmptyDir -->
- Given a task with `permission: "workspace_write"` and an isolator returning an empty directory string with nil error.
- When `RunOnce` executes the attempt.
- Then the child command is NEVER spawned.
- And the attempt terminates with `ok: false` and `status: "isolation_unavailable"`.
- And the lease is cleared cleanly.

#### Scenario: External repository worktree acquisition executes in isolated path
<!-- tests: TestIsolationRepoWorktreeIsolatorSuccess -->
- Given a task specifying an external repository directory via `req.AddDirs` and a configured `RepoWorktreeIsolator`.
- When `RunOnce` executes the attempt.
- Then `AcquireRepoWorktree` receives the resolved external repository path and task ID.
- And the child command spawns inside the isolated repository worktree directory.
- And the worktree is released with `keep: true` upon attempt completion.

#### Scenario: External repository worktree acquisition failure fails closed
<!-- tests: TestIsolationRepoWorktreeIsolatorFailure -->
- Given a task specifying an external repository directory via `req.AddDirs` and a `RepoWorktreeIsolator` returning an error.
- When `RunOnce` executes the attempt.
- Then the child command is NEVER spawned.
- And the task finishes terminally with `status: "isolation_unavailable"`.

#### Scenario: External repository worktree empty directory fails closed
<!-- tests: TestIsolationRepoWorktreeIsolatorEmptyDir -->
- Given a task targeting an external repository and a `RepoWorktreeIsolator` returning an empty directory string with nil error.
- When `RunOnce` executes the attempt.
- Then the child command is NEVER spawned.
- And the task finishes terminally with `status: "isolation_unavailable"`.

#### Scenario: Read-only tasks preserve shared directory execution without isolator
<!-- tests: TestIsolationReadOnlyPreserved -->
- Given a task with `permission: "read_only"`.
- When `RunOnce` executes the attempt with or without an isolator.
- Then `AcquireWorktree` is NEVER called.
- And the child command spawns in the shared or requested directory, completing successfully.
