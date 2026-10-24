# OpenSpec DELTA-30: Worker Claim Affinity Enforcement (Provider and Model) (#657)

* **Specification ID**: `DELTA-30`
* **Title**: Worker Claim Affinity Enforcement (Provider and Model)
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Milestone**: `M6` (Agent Ergonomics & Safety Hardening)
* **Target Packages**: `internal/controlplane`, `internal/worker`, `cmd/g8s`
* **ADR Reference**: [`docs/decisions/0032-containment-levels.md`](../../docs/decisions/0032-containment-levels.md)
* **Foundational Axioms**: Axiom 1 (Brain Decides, Worker Proves), Axiom 2 (Capability-Scoped Contracts), Issue #457, Issue #458, Issue #657
* **Issue**: `#657`

---

## 1. Context & Motivation

In previous versions, `g8s worker` declared `--provider` and `--model` CLI flags to restrict queue task claims to compatible worker environments. However:
1. In `cmd/g8s/main.go`, the parsed `--model` flag was discarded during serial worker execution, passing only `Provider` in `worker.RunOptions`.
2. In `cmd/g8s/worker_concurrency.go`, `runWorkerConcurrentDrain` supplied neither `Provider` nor `Model` to `worker.LoopOptions`, losing claim affinity entirely in concurrent mode.
3. In `internal/worker/worker.go`, `RunOptions` lacked a `Model` field, and `WorkerControlPlane` only offered `ClaimTask` and `ClaimTaskProvider`.
4. In `internal/controlplane/store.go`, atomic task claims only supported an optional provider filter without support for model affinity or combined provider+model affinity.

As a consequence, workers running with `--model X` could claim and attempt tasks requiring unrelated models, and concurrent workers could claim tasks outside their declared provider and model capabilities.

This specification defines the normative behavior for provider and model claim affinity across control-plane transactional queries, supervisor loop execution, and CLI command routing, guaranteeing strict selectivity, identical semantics across serial and concurrent modes, and zero mutation on out-of-affinity queue entries.

---

## 2. Normative Requirements

### Requirement 1: Atomic Transactional Queue Claim Filtering (DELTA-30 R1)

1. **Additive Interface**: The control plane MUST provide an additive claim method:
   ```go
   ClaimTaskFiltered(ctx context.Context, workerID string, leaseDurationSeconds int, filter ClaimFilter) (*Task, error)
   ```
   on `TaskLeaseStore` and `*controlplane.Store`.
2. **Filter Type**: `ClaimFilter` MUST be defined in `internal/controlplane/interfaces.go` with optional string fields:
   ```go
   type ClaimFilter struct {
       Provider string `json:"provider,omitempty"`
       Model    string `json:"model,omitempty"`
   }
   ```
3. **Legacy Wrapper Preservation**:
   - `ClaimTask(ctx, workerID, leaseDurationSeconds)` MUST be preserved as a convenience wrapper delegating to `ClaimTaskFiltered(ctx, workerID, leaseDurationSeconds, ClaimFilter{})`.
   - `ClaimTaskProvider(ctx, workerID, leaseDurationSeconds, provider)` MUST be preserved as a convenience wrapper delegating to `ClaimTaskFiltered(ctx, workerID, leaseDurationSeconds, ClaimFilter{Provider: provider})`.
4. **Pre-Mutation Transactional Selection**: Task filtering MUST occur directly within the candidate selection SQL query (`SELECT ... WHERE ...`) inside the `BEGIN IMMEDIATE` (or serializing) SQLite transaction prior to updating task state to `LEASED`.
5. **No Claim-Then-Reject**: The control plane MUST NOT lease or mutate an out-of-affinity task and subsequently reject or roll back. Mismatched tasks must never be selected as claim candidates.

### Requirement 2: Affinity Filtering Semantics & Non-Wildcard Integrity (DELTA-30 R2)

1. **Empty Filter Behavior**: When `ClaimFilter` has both empty `Provider` and empty `Model`, candidate selection remains unrestricted, preserving legacy FIFO/priority queue order among eligible `QUEUED` tasks.
2. **Provider Affinity**: When `filter.Provider` is non-empty:
   - Candidate selection MUST enforce exact match: `json_extract(request_json, '$.provider') = ?`.
   - Tasks with absent or mismatching `provider` MUST NOT match.
3. **Model Affinity**: When `filter.Model` is non-empty:
   - Candidate selection MUST enforce exact match: `json_extract(request_json, '$.model') = ?`.
   - Tasks with absent or mismatching `model` MUST NOT match.
   - Wildcards such as `OR json_extract(request_json, '$.model') IS NULL` are strictly forbidden.
4. **Combined Affinity (Both Set)**: When both `filter.Provider` and `filter.Model` are non-empty:
   - Candidate selection MUST apply logical `AND` between both criteria.
   - A task must match both the declared provider AND the declared model to be claimed.
5. **Untouched Out-of-Affinity Tasks**: Any task in the queue that does not satisfy the active claim filter MUST remain in `QUEUED` status with `attempts = 0` (or its prior attempt count), `lease_owner = NULL`, `lease_token = NULL`, and `lease_expires_at = NULL` completely untouched.

### Requirement 3: Parameter Propagation Across Worker Interfaces (DELTA-30 R3)

1. **WorkerControlPlane Contract**: `internal/worker/worker.go` MUST include `ClaimTaskFiltered` in `WorkerControlPlane`:
   ```go
   ClaimTaskFiltered(ctx context.Context, workerID string, leaseDurationSeconds int, filter controlplane.ClaimFilter) (*controlplane.Task, error)
   ```
2. **RunOptions Expansion**: `worker.RunOptions` MUST include `Model string` alongside `Provider string`.
3. **Supervisor RunOnce Dispatch**:
   - If either `opts.Provider != ""` or `opts.Model != ""`, `Supervisor.RunOnce` MUST call `s.cp.ClaimTaskFiltered` with the corresponding `ClaimFilter`.
   - If both are empty, `Supervisor.RunOnce` calls `s.cp.ClaimTask`.
4. **LoopOptions Expansion**: `worker.LoopOptions` MUST include both `Provider string` and `Model string`.
5. **Supervisor RunLoop Dispatch**:
   - `runSerialLoop` MUST pass `Provider: opts.Provider` and `Model: opts.Model` in the `RunOptions` handed to `RunOnce`.
   - `runConcurrentLoop` MUST pass `Provider: opts.Provider` and `Model: opts.Model` in the `RunOptions` handed to `RunOnce` for every concurrent worker goroutine.

### Requirement 4: CLI Flag Propagation & Equivalence (DELTA-30 R4)

1. **Serial Execution**: In `cmd/g8s/main.go`, `runWorker` MUST propagate both `--provider` and `--model` to `RunOptions` for `--once` mode and serial draining.
2. **Concurrent Execution**: `runWorkerConcurrentDrain` in `cmd/g8s/worker_concurrency.go` MUST receive both `provider` and `model` flags and forward them to `worker.LoopOptions`.
3. **Behavioral Equivalence**: The task selection outcomes for empty, provider-only, model-only, and combined filters MUST be strictly equivalent between serial mode (`--concurrency 1`) and concurrent mode (`--concurrency > 1`).
4. **Documentation Alignment**: The `g8s worker` CLI help text and reference documentation MUST explicitly reflect that `--provider` and `--model` restrict queue claims in both serial and concurrent execution modes.

### Requirement 5: Concurrency Safety and Worktree Isolation (DELTA-30 R5)

1. **Single-Winning Claim**: Under concurrent claims by multiple workers competing for the same filtered queue, exactly one worker shall successfully claim each matching task; no duplicates, race conditions, or double-claims shall occur.
2. **Isolation Invariant**: Concurrent mode continues to enforce per-attempt worktree isolation and session registration fail-closed invariants without regression.

---

## 3. Scenarios & Mechanical Test Pins

#### Scenario: Control plane filters queue candidates by provider and model affinity
<!-- tests: TestClaimAffinityFiltering -->
- Given tasks in the queue with varying combinations of provider and model metadata.
- When `ClaimTaskFiltered` is called with provider-only, model-only, combined provider+model, or empty filters.
- Then only tasks matching the declared criteria are leased, while tasks with absent or mismatching fields are not selected.
- And legacy wrappers `ClaimTask` and `ClaimTaskProvider` retain identical legacy filtering behavior.

#### Scenario: Concurrent claimers elect exactly one winner per matching task
<!-- tests: TestClaimAffinityConcurrency -->
- Given multiple concurrent workers competing for filtered queue tasks.
- When concurrent atomic claims execute across workers.
- Then exactly one worker successfully leases each matching task without duplicates or race conditions.

#### Scenario: Supervisor RunOnce dispatches claims across all filter permutations
<!-- tests: TestWorkerClaimAffinity_RunOnce_FilterPermutations -->
- Given a worker supervisor with configured `RunOptions` setting `Provider`, `Model`, both, or neither.
- When `RunOnce` executes.
- Then the matching task is claimed and dispatched through the runner with proper environment parameters.

#### Scenario: Non-matching tasks remain queued with untouched leases and attempt counts
<!-- tests: TestWorkerClaimAffinity_MismatchRemainsQueuedUntouched, TestWorkerCLIAffinity_MismatchRemainsQueued -->
- Given tasks in the queue targeting distinct providers or models from the running worker.
- When the worker executes in `--once`, serial drain, or concurrent modes.
- Then out-of-affinity tasks remain in `QUEUED` state with `attempts == 0`, `lease_owner == nil`, and `lease_token == nil`.

#### Scenario: Serial and concurrent worker loops preserve claim affinity
<!-- tests: TestWorkerClaimAffinity_SerialDrain, TestWorkerClaimAffinity_ConcurrentDrain -->
- Given a queue populated with mixed-affinity tasks.
- When `runSerialLoop` or `runConcurrentLoop` runs with specified `LoopOptions.Provider` and `LoopOptions.Model`.
- Then only affinity-matching tasks are claimed and executed until the queue is drained of matching tasks.

#### Scenario: Equivalence of claim outcomes between serial and concurrent modes
<!-- tests: TestWorkerClaimAffinity_SerialConcurrentEquivalence, TestWorkerCLIAffinity_SerialConcurrentEquivalence -->
- Given identical sets of queued tasks with diverse provider/model combinations.
- When processed by serial workers (`--concurrency 1`) versus concurrent workers (`--concurrency > 1`).
- Then the exact same set of matching task IDs are claimed and processed in both execution modes.

#### Scenario: CLI propagates provider and model flags in serial and concurrent modes
<!-- tests: TestWorkerCLIAffinity_SerialOnce, TestWorkerCLIAffinity_SerialDrain, TestWorkerCLIAffinity_ConcurrentDrain -->
- Given `g8s worker` CLI invocations specifying `--provider` and `--model` in `--once`, serial loop, and concurrent drain modes.
- When workers execute.
- Then flags are propagated through `RunOptions` and `LoopOptions`, strictly enforcing claim filtering.

#### Scenario: Single-winning claim concurrency and isolation fail-closed in CLI
<!-- tests: TestWorkerCLIAffinity_SingleWinningClaimConcurrency, TestWorkerCLIAffinity_ReadonlyAndIsolationFailClosed -->
- Given concurrent worker processes and workspace write attempts executed via CLI.
- When tasks execute under concurrency.
- Then single-winner claim semantics are preserved, read-only tasks run without worktree isolation, and write tasks enforce fail-closed worktree isolation.
