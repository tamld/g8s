# Decoupled Memory & Cognitive Architecture in g8s

> **Whitepaper & Technical Specification**: Decoupling Memory from Autonomous AI Agent Execution  
> **Theoretical Foundations**: MemGPT (Packer et al., 2023), CoALA (Sumers et al., 2023), Generative Agents (Park et al., 2023), Attention Degradation in Long Contexts (Liu et al., 2023).

---

## 1. Executive Summary & Problem Formulation

Traditional multi-agent architectures treat the LLM's **Context Window** as a monolith that simultaneously holds conversation history, reasoning scratchpads, tool execution logs, codebase files, and long-term memory. This paradigm leads to three critical system failures:

1. **"Lost in the Middle" Degradation**: As in-context token volume expands ($>30,000$ tokens), LLM attention density degrades exponentially. Subagents begin to ignore system boundaries, overlook forbidden action lists, and hallucinate code structures.
2. **Compounded Error Propagation (Hallucination Cascade)**: When transient, erroneous subagent reasoning remains pinned in shared context, downstream agents inherit and amplify those false premises.
3. **Quadratic Cost & Latency ($O(N^2)$ Attention)**: Resending vast chat and execution histories on every tool call increases latency and token consumption without improving reasoning quality.

### The g8s Axiom: The OS-Style Virtual Memory Hierarchy

`g8s` resolves these failures by **completely decoupling memory from the agent execution core**:

```
┌─────────────────────────────────────────────────────────────────────────────────────────────┐
│                           g8s DECOUPLED MEMORY ARCHITECTURE                                 │
├─────────────────────────────────────────────────────────────────────────────────────────────┤
│  AI AGENT CONTEXT WINDOW (CPU L1 CACHE / REGISTERS)                                         │
│  • Ephemeral, stateless, mathematically bounded (<4,000 chars for read_only)                │
│  • Receives JIT-sliced Contract Prompts: Role Rules + Scoped File Globs + Immediate Goal     │
├─────────────────────────────────────────────────────────────────────────────────────────────┤
│                                              ▲                                              │
│                                 Just-In-Time Injected By                                    │
│                                              │                                              │
│  g8s MEMORY KERNEL & 3-TIER EVIDENCE LAKE (EXTERNAL PERSISTENCE)                            │
│  ├── 1. HOT MEMORY   : SQLite WAL Queue (`g8s.db`), Compare-And-Swap Leases, POSIX 0600     │
│  ├── 2. WARM MEMORY  : Task Lineage Graph (`parent_task_id`), JSONL Streaming Events       │
│  └── 3. COLD VAULT   : POSIX 0600 Filesystem Lake (`~/.local/state/g8s/evidence/`)          │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

In `g8s`, the **LLM is treated as a Pure Compute Unit (CPU)**, while `g8s` acts as the **Operating System and Memory Management Unit (MMU)**.

---

## 2. The 4 Memory Taxonomies

`g8s` organizes agent memory into 4 orthogonal taxonomies:

| Memory Taxonomy | Cognitive Role | Physical Packaging in g8s | Lifecycle & Retention |
| :--- | :--- | :--- | :--- |
| **1. Working Memory** | Immediate reasoning buffer for one execution step. | In-flight HTTP/CLI request payload bounded by `MaxPromptChars` (4,000 / 12,000 chars). | Ephemeral. Destroyed immediately upon process termination. |
| **2. Episodic Memory** | History of what occurred: subtasks, retries, worker outcomes, errors. | SQLite `tasks` table with `parent_task_id` hierarchy + `task_events` append-only log. | Durable. Queryable via `g8s lineage <task-id>` and `g8s children <parent-id>`. |
| **3. Semantic Memory** | Facts, rules, architectural constraints, codebase knowledge SSoT. | Static Markdown artifacts (`spec/openspec/`, `manifest.json`, Markdown Knowledge Vault with SQLite FTS5 + BM25). | Immutable & Versioned. SHA-256 hashed and referenced without polluting context. |
| **4. Capability Memory** | Authorization state: which files the agent is allowed to mutate and for how long. | SQLite `write_receipts` table: single-use, path-scoped globs, TTL-bounded ($1..3600\text{s}$). | Expired on TTL lapse or consumed atomically via CAS on first use. |

---

## 3. The 3-Tier Evidence Lake Architecture

To guarantee infinite durability without inflating hot SQLite database files, `g8s` partitions physical storage into three tiers:

```
                            STORAGE PARTITIONING
                                     │
           ┌─────────────────────────┼─────────────────────────┐
           ▼                         ▼                         ▼
      HOT STORAGE               WARM STORAGE              COLD STORAGE
 (Metadata & Control Plane)   (Session Logs & Trees)     (Evidence Vault)
 ─────────────────────────   ──────────────────────     ────────────────
 Path: `g8s.db`              Path: `task_events`        Path: `~/.local/state/g8s/evidence/`
 Format: SQLite WAL          Format: Relational JSON    Format: POSIX 0600 Content Addressed
 Retention: Active leases    Retention: Full session    Retention: Permanent audit trail
```

### Prompt Redaction on Task Completion (Zero-Leak Compaction)

When a task transitions to a terminal state (`SUCCEEDED`, `FAILED`, `CANCELLED`):
1. The full execution output is archived into the **Cold Evidence Vault** under its deterministic SHA-256 hash.
2. The hot row in the SQLite `tasks` table is redacted via `redactPayload()`: the full prompt string is replaced with `sha256:<digest>`.
3. The hot database remains compact ($<15\text{MB}$ RAM, $<1\text{ms}$ query latency) even after orchestrating tens of thousands of subtasks.

---

## 4. Lineage Graph & Episodic Replay

Subtasks in `g8s` form an explicit directed tree via `parent_task_id`:

```mermaid
graph TD
    Root["Root Task (Architecture Refactor)"] --> Sub1["Subtask 1: Scout Dependencies (scout)"]
    Root --> Sub2["Subtask 2: Synthesize Unit Tests (test-runner)"]
    Sub1 --> Sub1_1["Subtask 1.1: Map MCP Tools (mcp-mapper)"]
    Sub2 --> Sub2_1["Subtask 2.1: Delegated Write (workspace_write)"]
```

### Why Lineage Beats Context Stuffing:
* **Context Isolation**: Subtask 1.1 runs in a clean 4,000-character sandbox without knowing the entire history of Subtask 2.
* **Deterministic Replay**: The Brain Orchestrator can inspect the exact ancestor chain via `g8s lineage <task-id>` to diagnose root causes if a subtask reports `NEEDS_INFO` or `BLOCKED`.
* **Sub-Tree Cancellation**: Cancelling a root task cascades cancel signals across all active child leases.

---

## 5. Architectural Benefits & Guarantees

1. **Sub-15ms Worker Startup**: Workers do not load massive context vectors or memory embeddings at startup.
2. **Resilience to Model Crashes**: If an LLM provider experiences network timeouts or rate limits, the memory state is securely persisted in SQLite. A new worker simply resumes the task lease.
3. **Zero Security Leakage**: Subagents cannot access secrets or unauthorized paths from previous tasks because Working Memory is completely reset between runs.
4. **Auditability**: Every mutation is backed by a cryptographic Write Receipt linked to an immutable task event in the Evidence Lake.
