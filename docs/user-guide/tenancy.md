# Multi-Project Host Tenancy

This guide defines the operator-facing contract for running multiple concurrent `g8s` sessions, supervisors, or workers on a single host machine, formalizing the tenancy model ratified in [ADR-0028](../decisions/0028-multi-project-tenancy.md).

---

## Overview

`g8s` is designed around a single-node operating posture where isolation and resource lifecycle containment are bounded by state directory ownership. When multiple development projects or test sessions run concurrently on the same host, resource domains must remain strictly partitioned to prevent cross-contamination.

Historically (issue #465), host-global default paths allowed background routines from one session to inadvertently inspect, signal, or reap processes and worktrees created by another session. Under the ratified tenancy model, complete isolation is achieved through the **state directory ownership boundary**.

---

## The Core Rule: Distinct State Directories

> **The One Rule**: Concurrent `g8s` sessions on the same host must use **distinct `G8S_STATE_DIR` paths**.

The state directory serves as the authoritative ownership boundary. All runtime databases, worktrees, locks, process signals, and execution artifacts belong strictly to the instance pointing to that directory.

### Configuration

By default, `g8s` places its state in `~/.local/state/g8s` (or `%LOCALAPPDATA%\g8s` on Windows). For single-session hosts, this default operates without additional configuration.

When running multiple concurrent sessions, set `G8S_STATE_DIR` to a distinct directory for each session:

```bash
# In Session A (e.g. repo-alpha)
export G8S_STATE_DIR="$HOME/.local/state/g8s-alpha"
g8s worker

# In Session B (e.g. repo-beta)
export G8S_STATE_DIR="$HOME/.local/state/g8s-beta"
g8s worker
```

Alternatively, pass `G8S_STATE_DIR` inline per command invocation:

```bash
G8S_STATE_DIR=~/.local/state/g8s-alpha g8s submit --prompt "..."
```

### State Directory Layout

Each `G8S_STATE_DIR` contains the following isolated runtime structures:

| Resource | Path | Description |
| :--- | :--- | :--- |
| `g8s.db` | `$G8S_STATE_DIR/g8s.db` | SQLite database (WAL journal mode) storing queue tasks, worker leases, and orchestrator state. |
| `receipts.db` | `$G8S_STATE_DIR/receipts.db` | SQLite database recording write capability receipts and cryptographic delegations. |
| `signals/` | `$G8S_STATE_DIR/signals/` | File-based IPC signals for worker lifecycle transitions, heartbeats, and stop notices. |
| `worktrees/` | `$G8S_STATE_DIR/worktrees/` | Pool of isolated git worktrees checked out for worker task execution. |
| `runs/` | `$G8S_STATE_DIR/runs/` | Execution logs, step transcripts, and iteration traces from supervisor runs. |
| `evidence/` | `$G8S_STATE_DIR/evidence/` | Verification artifacts, diff bundles, and structured worker deliverables. |

Because worker worktrees and execution signals reside entirely under `$G8S_STATE_DIR`, sessions with distinct state directories never share filesystem trees or conflict during pool allocations.

---

## Identity-Scoped Jurisdiction

Destructive primitives in `g8s` (process termination, orphan cleanup, worktree removal) operate strictly within the jurisdiction of the invoking instance's state directory.

### What Identity-Scoping Means in Practice

1. **Process Discovery and Ghost Killing**:
   - `g8s cleanup` inspects only workers associated with its own state directory.
   - Stale or invalid heartbeats alone do not warrant termination. Before sending any signal, process discovery checks PID-recycle safety and verifies that the process working directory (CWD) and command line corroborate ownership of the current session repository.
   - Processes belonging to other repositories or state directories are recognized as foreign and ignored.
2. **Worktree Preservation and Reaping**:
   - Worktree cleanup routines only scan and prune worktrees residing inside `$G8S_STATE_DIR/worktrees/`.
   - Preserved worktrees from foreign projects remain untouched, eliminating the risk of accidental `RemoveAll` cascades.
3. **Lease Resilience**:
   - Transient database contention does not cause workers to self-terminate. Workers verify authoritative lease ownership (`awaitOutcome`) before surrendering execution or aborting.

### Cross-Instance Administration (`--force-foreign`)

Cross-instance sweeping is prohibited by default. A cleanup command will never silently sweep resources across different state directories.

When an operator explicitly requires host-wide or cross-tenant maintenance:
- An explicit administrative flag (`--force-foreign` class) must be passed.
- **Attribution Logging**: Prior to executing any destructive action against a foreign resource, `g8s` writes an audit log specifying:
  - The exact target resource path or PID.
  - The foreign owner identity or origin state directory (if identifiable).
  - The exact action taken (e.g. `SIGKILL`, `RemoveAll`).
  - The operator attribution and justification.

---

## Deliberate Shared-Queue Mode

Some multi-repo setups deliberately share a single task queue across repositories so that a single pool of workers can service incoming tasks from multiple projects (ADR-0028 D3).

### Configuration and Rules

- Shared-queue mode is an **explicit, declared configuration**: all participants configure the same `G8S_STATE_DIR` (or shared `G8S_DB`).
- **Destructive Primitives Remain Instance-Scoped**: Even when sharing a task queue, destructive primitives (cleanup, process termination, worktree sweeping) do not cross instance boundaries. Sharing a queue does not grant a shared trigger finger.
- **Lease Coordination**: Tenants sharing a queue inherit concurrent lease reconciliation governed by SQLite WAL single-winner semantics. Workers respect atomic claim transactions and lease timeouts without interfering with another worker's process tree.

---

## Containment Hierarchy and the Cardinality Law

Tenancy and process containment in `g8s` follow the three-tier containment model formalized in [ADR-0032](../decisions/0032-containment-levels.md). The tokens **F0**, **F1**, and **F2** are reserved exclusively for these containment tiers:

```text
F0 (Brain / Supervisor)
 └── F1 (Harness Worker)
      └── F2 (Recursive Subagent)
```

1. **F0 — Brain / Supervisor Tier**: The sole authority holder per session (Operator↔F0 is 1-1). Manages git commits, knowledge vault promotions, and write receipt issuance.
2. **F1 — Worker Tier**: Spawned by F0 through the `g8s` harness (F0↔F1 is 1-n). Bounded by task leases, isolated git worktrees, path-scoped write receipts, and killable process groups.
3. **F2 — Subagent Tier**: Spawned recursively by an F1 worker (e.g. via platform-native `define_subagent` / `invoke_subagent`). Bounded by **`F2 ⊆ F1 ⊆ F0`**, inheriting F1's sandbox and receipt constraints.

### The Cardinality Law

Within any single containment tree, every relationship is decomposed into 1-1 or 1-n contracts (e.g. 1 task = 1 worker = 1 receipt). Direct n-n edges are prohibited inside a tree:
- **F1 ↔ F1**: No direct communication edge; coordination occurs solely through frozen briefs and disjoint receipts.
- **F0 ↔ F2**: No direct layer-skipping edge; F2 is visible to F0 only through F1's collected evidence.

An n-n topology occurs on a host machine only when multiple independent trees (supervisors) run simultaneously. Partitioning sessions by `G8S_STATE_DIR` ensures that each tree remains isolated as an independent `1-n` hierarchy, preventing cross-tree contention.

---

## Reopen Trigger (ADR-0028 D4)

The present posture—retaining the host-global default path for single-project use while enforcing distinct `G8S_STATE_DIR` for concurrent sessions—is ratified and stable.

Per the trigger-contract pattern, this policy will only be reopened if empirical operational evidence demands it:

1. **Frequency Threshold**: Running concurrent multi-project sessions on one host becomes routine (defined as ≥3 projects executing recurring concurrent drains within a single month); or
2. **Contention Incident**: A verified live contention or cross-contamination incident occurs despite following the distinct `G8S_STATE_DIR` doctrine.

Until one of these explicit conditions is met, the existing state directory boundary stands as the normative operational contract.

---

## Related Documentation

- [ADR-0028: Multi-Project Tenancy on One Host](../decisions/0028-multi-project-tenancy.md)
- [ADR-0032: Containment Levels F0/F1/F2](../decisions/0032-containment-levels.md)
- [Configuration Guide](configuration.md)
- [Provider Configuration](providers.md)
