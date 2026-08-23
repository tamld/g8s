# g8s Lifecycle, 1:N Topology, Approval Protocols & Evidence Architecture

> **Document Status**: Production Specification  
> **Target Audience**: Systems Architects, Distributed AI Engineers, and Security Auditors  

---

## 1. Formal Task & Worker Lifecycle State Machine

`g8s` models all worker tasks as a strictly typed, deterministic Finite State Machine (FSM):

```
                        ┌──────────────┐
                        │   SUBMIT     │
                        └──────┬───────┘
                               │
                               ▼
                        ┌──────────────┐
            ┌──────────►│    QUEUED    │◄──────────┐
            │           └──────┬───────┘           │
            │                  │ CAS Lease Claim   │ Requeue (attempts < max)
            │                  ▼                   │
            │           ┌──────────────┐           │
            │           │    LEASED    │           │
            │           └──────┬───────┘           │
            │                  │ Spawn in PGID     │
            │                  ▼                   │
            │           ┌──────────────┐           │
            │           │   RUNNING    ├───────────┘
            │           └──┬───┬───┬───┘
            │              │   │   │
            │  ┌───────────┘   │   └───────────┐
            │  ▼               ▼               ▼
      ┌────────────┐    ┌────────────┐   ┌────────────┐
      │ NEEDS_INFO │    │  BLOCKED   │   │ CANCELLED  │
      └────────────┘    └────────────┘   └────────────┘
            │                  │               │
            ▼                  ▼               ▼
   [Clarified Task]    [Supervisor Gate]  [Process Killed]
```

### Terminal vs. Non-Terminal States

| State | Type | Description | Prompt Lifecycle |
| :--- | :--- | :--- | :--- |
| `QUEUED` | Non-Terminal | Task waiting in SQLite WAL queue for an idle worker. | Stored in SQLite (Plaintext) |
| `LEASED` | Non-Terminal | Worker claimed task via atomic CAS lease (`lease_owner`, `lease_expires_at`). | Stored in SQLite (Plaintext) |
| `RUNNING` | Non-Terminal | Worker process active in its own OS Process Group (`Setpgid`). | Stored in SQLite (Plaintext) |
| `NEEDS_INFO`| Non-Terminal | Worker halted due to missing parameters or ambiguous APIs. | Plaintext redacted to SHA-256 |
| `BLOCKED` | Non-Terminal | Worker halted by security filter or missing Write Receipt. | Plaintext redacted to SHA-256 |
| `SUCCEEDED` | **Terminal** | Worker completed successfully and passed post-run mutation scan. | **Redacted to `prompt_hash`** |
| `FAILED` | **Terminal** | Execution failed or exhausted `max_attempts`. | **Redacted to `prompt_hash`** |
| `CANCELLED` | **Terminal** | Explicitly cancelled by Supervisor; process tree SIGTERM/SIGKILLed. | **Redacted to `prompt_hash`** |

---

## 2. 1-to-N Topology & Concurrency Governor

A single Supervisor (Brain) coordinates an arbitrary number ($N$) of Workers. The capacity limit $N$ is governed dynamically by a **4-Factor Concurrency Governor**:

$$N_{\text{optimal}} = \min \left( N_{\text{CPU}}, \, N_{\text{RAM}}, \, N_{\text{RateLimit}}, \, N_{\text{ContextBudget}} \right)$$

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    1:N WORKER TOPOLOGY & CONCURRENCY                    │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│                       ┌───────────────────────┐                         │
│                       │   Supervisor (Brain)  │                         │
│                       │   Claude 3.7 / Opus   │                         │
│                       └───────────┬───────────┘                         │
│                                   │                                     │
│                        g8s Concurrency Governor                         │
│                     (Priority Queue + Token Bucket)                     │
│                                   │                                     │
│         ┌─────────────────────────┼─────────────────────────┐           │
│         ▼                         ▼                         ▼           │
│  ┌──────────────┐          ┌──────────────┐          ┌──────────────┐   │
│  │ Worker 1     │          │ Worker 2     │          │ Worker N     │   │
│  │ Gemini Flash │          │ Claude Haiku │          │ Local Ollama │   │
│  │ (Scout Role) │          │ (Test Runner)│          │ (Collector)  │   │
│  └──────────────┘          └──────────────┘          └──────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
```

### Standard Concurrency Profiles

* **Developer Workstation (Default)**: $N = 4$ concurrent workers (RAM budget ~600MB).
* **High-Throughput Server / Homelab (Proxmox/Docker)**: $N = 16 - 32$ concurrent workers.
* **Priority Routing**: Tasks accept a `priority INTEGER` (higher values jump to the front of the `QUEUED` index).

---

## 3. Communication, Clarification & Approval Protocols (Worker ⟷ Brain)

Workers NEVER engage in free-form, unconstrained chat with the Supervisor (which causes context bloating). All exchanges follow strict **Structured Asymmetric Protocols**:

### Protocol 1: Clarification Request (`NEEDS_INFO`)
When a worker encounters an ambiguous signature or missing dependency:
1. Worker terminates immediately and returns structured JSON:
   ```json
   {
     "status": "NEEDS_INFO",
     "missing_inputs": [
       "internal/auth/jwt.go: function ValidateToken signature not found"
     ],
     "inspected_files": ["internal/api/login.go"]
   }
   ```
2. Supervisor inspects the structured request, resolves the missing context, and queues a refined task referencing `parent_task_id`.

### Protocol 2: Boundary Collision (`BLOCKED`)
When a worker attempts an operation requiring write permissions without a receipt:
1. `g8s` catches the attempt and returns:
   ```json
   {
     "status": "BLOCKED",
     "reason": "workspace_write requested but no valid receipt-id provided",
     "target_paths": ["tests/auth_test.go"]
   }
   ```
2. Supervisor evaluates the request:
   * If approved $\rightarrow$ Calls `g8s receipt issue --allow "tests/auth_test.go"` and resubmits.
   * If rejected $\rightarrow$ Cancels the task with an explanation.

### Protocol 3: Dry-Run Proposal & Approval (`PROPOSAL`)
For sensitive refactoring tasks:
1. Worker generates a unified git diff patch in memory without modifying the disk.
2. Supervisor reviews the diff. If accepted, issues a single-use receipt to apply the patch.

---

## 4. Ground Truth Evidence, Memory & Audit Ledger

`g8s` separates **Operational State** from **Physical Evidence** across 3 tiers:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                      THE 3-TIER EVIDENCE ARCHITECTURE                   │
├─────────────────────────────────────────────────────────────────────────┤
│  🔥 TIER 1: HOT METADATA (SQLite WAL)                                   │
│     • Tasks table: Task IDs, state machine, idempotency keys, leases.   │
│     • Fast CAS queries, index `idx_tasks_claim`.                        │
│                                                                         │
│  ⚡ TIER 2: WARM EVENT STREAM (Append-Only JSONL / Task Events)         │
│     • Full audit log: `task_id`, `timestamp`, `event_type`, `payload`.  │
│     • Immutable record of every state change and heartbeat lease.       │
│                                                                         │
│  ❄️ TIER 3: COLD EVIDENCE VAULT (Physical Files in 0600 Permissions)    │
│     • `~/.local/state/g8s/evidence/<task_id>.result.json`               │
│     • `~/.local/state/g8s/evidence/<task_id>.receipt.json`              │
│     • Contains: Inspected file lists, SHA-256 checksums, stdout logs.   │
└─────────────────────────────────────────────────────────────────────────┘
```

### Strict Evidence Citation Standard
A claim produced by a worker is only promoted to the Knowledge Vault when backed by:
1. `[CODE <filepath>:<line>]` — Physical file and line verification.
2. `[RUNTIME <exit_code>]` — Verifiable command execution receipt.
3. `[RECEIPT <uuid>]` — Trackable write authorization token.

---

## 5. Build vs. Embed: The Architectural Boundary

| Subsystem | Strategy | Technical Rationale |
| :--- | :---: | :--- |
| **ControlPlane & Queue** | **BUILD (Native Go)** | Zero external database dependencies. SQLite WAL in Pure-Go (`modernc.org/sqlite`) guarantees single 15MB binary portability. |
| **Process Supervisor & Cages** | **BUILD (Native Go)** | Native OS syscalls (`Setpgid`, `SIGTERM`, Windows JobObjects) ensure 100% process tree containment. |
| **Receipt Delegation Engine** | **BUILD (Native Go)** | Single-use atomic transactions and TTL verification are core security invariants of `g8s`. |
| **Concurrency Governor** | **BUILD (Native Go)** | Go channels, WaitGroups, and priority workers provide ultra-low CPU overhead. |
| **LLM Execution Engines** | **EMBED / PLUG-IN** | `g8s` acts as a driver harness for existing installed CLIs (`agy`, `claude`, `gemini`, `ollama`). Never bundles LLM runtimes. |
| **Language Servers (LSP)** | **EMBED / PLUG-IN** | `g8s` connects as a lightweight JSON-RPC Stdio Client to existing `gopls`, `pyright`, `vtsls`. |
| **IDE & Chat Clients** | **EMBED / PLUG-IN** | `g8s` exposes standard Model Context Protocol (MCP) over Stdio for Claude Desktop, Cursor, and Windsurf. |
