# g8s Architectural Philosophy & System Design Manifesto

> **Core Axiom**: *"Intelligence directs; Muscle executes; Harness protects."*  
> **Target Audience**: Systems Architects, Agent Engineers, and Autonomous AI Controllers.

---

## 1. The Two-Tier Agent Taxonomy: Worker vs. Supervisor

Multi-agent failure stems from role confusion. When a single model attempts to simultaneously reason about architecture, execute shell commands, and verify its own output, hallucination and blast-radius blindness inevitably follow. `g8s` enforces an uncompromising two-tier taxonomy:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                 TIER 1: SUPERVISOR (THE BRAIN / ARCHITECT)              │
│  • Models: Claude 3.7 Sonnet (Thinking), Claude Opus, GPT-4o, DeepSeek R1 │
│  • Nature: High-context, deep reasoning, strategic, expensive           │
│  • SOW: Architecture, decomposition, receipt issuance, claim verification │
│  • Rules: Never do mechanical scanning; Never trust worker unverified   │
│  • Permissions: Root capability issuer, exclusive SSoT / Git committer   │
└────────────────────────────────────┬────────────────────────────────────┘
                                     │
                                     │ Asymmetric Contract + Write Receipt
                                     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                 TIER 2: WORKER (THE MUSCLE / SPECIALIST)                │
│  • Models: Gemini 3.5/3.7 Flash, Claude 3.5 Haiku, DeepSeek V3, Ollama  │
│  • Nature: Ultra-fast, high token throughput, cost-efficient, stateless  │
│  • SOW: Code inventory, test boilerplate synthesis, log compression     │
│  • Rules: Single-turn snapshot; Stop on ambiguity (NEEDS_INFO / BLOCKED) │
│  • Permissions: Default `read_only`; `workspace_write` only with receipt│
└─────────────────────────────────────────────────────────────────────────┘
```

### Detailed Matrix of Responsibilities

| Dimension | Supervisor (Brain) | Worker (Muscle) |
| :--- | :--- | :--- |
| **Model Selection** | Frontier Reasoning Models (Claude 3.7 Sonnet, Opus, GPT-4o, R1) | High-Speed Flash Models (Gemini Flash, Haiku, Ollama) |
| **Scope of Work (SoW)** | Strategic planning, spec design, risk analysis, receipt issuance, final verification. | Mechanical scanning, regex extraction, AST parsing, unit test generation, log summarization. |
| **Operational Rules** | 1. Never waste reasoning budget on broad file walks.<br>2. Always inspect worker diffs before commit.<br>3. Issue narrowest viable `allowed_paths` globs. | 1. Strictly operate within assigned `RoleProfile`.<br>2. Never claim unverified files as proven.<br>3. Halt and return `NEEDS_INFO` if APIs are ambiguous. |
| **Mutation Policy** | Full authority over knowledge vault promotion and Git commits. | `read_only` by default. Can only mutate files matching a valid Write Receipt. |
| **Memory Lifecycle** | Multi-turn contextual continuity across project milestones. | Stateless snapshot execution. Ephemeral process dies upon task completion. |

---

## 2. Assembly & Handoff Mechanics (Brain ⟷ Muscle Interface)

The transition between "Brain" and "Muscle" is mediated by a **Deterministic Asymmetric Contract**:

```
[BRAIN: Opus / Sonnet]
   │
   ├─► 1. Prepares Task Spec (Curated file paths + Explicit return schema)
   ├─► 2. Calls ControlPlane.IssueReceipt() (If workspace mutation is required)
   │
   ▼
[g8s ENGINE (Go Zero-CGO)]
   │
   ├─► 3. Validates Harness Gates (Role, blocked patterns, denied paths, receipt)
   ├─► 4. Injects Contract Prompt (Role boundaries + Wiki/Session rules)
   ├─► 5. Spawns Worker in Isolated Process Group (`Setpgid: true` + Sandbox)
   │
   ▼
[WORKER: Flash / Haiku]
   │
   ├─► 6. Executes bounded task within memory/timeout envelope
   ├─► 7. Emits structured JSON / Markdown stream
   │
   ▼
[g8s ENGINE (Post-Execution Audit)]
   │
   ├─► 8. Scans stdout/stderr against READ_ONLY_VIOLATION_PATTERNS
   ├─► 9. Sanitizes output (Redacts credentials, bounds buffer to 2MB)
   ├─► 10. Atomically consumes Write Receipt (One-time use)
   ├─► 11. Redacts raw prompt to SHA-256 `prompt_hash` in SQLite WAL
   │
   ▼
[BRAIN: Ingestion & Verification]
   │
   └─► 12. Inspects structured findings, runs physical test verifications, promotes state.
```

---

## 3. Separation & Multi-Agent Coordination

1. **Stateless Snapshots vs. State Ownership**:
   * Workers are completely stateless. They receive all necessary context in the injected prompt and produce a single self-contained output.
   * Only the Supervisor owns state transitions. Tasks maintain parent-child lineage (`parent_task_id`) and idempotency keys (`idempotency_key`).
2. **Concurrency Safety**:
   * Concurrent workers are isolated via SQLite WAL transactions (`BEGIN EXCLUSIVE`) and atomic Compare-And-Swap (CAS) leases. Multiple workers can read concurrently without blocking the queue.

---

## 4. The "Trust but Verify" Engineering Architecture

`g8s` rejects subjective trust. It implements a 4-stage verification funnel:

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ 1. PRE-GATE     │ ──► │ 2. RUNTIME CAGE │ ──► │ 3. POST-SCAN    │ ──► │ 4. BRAIN AUDIT  │
│ Harness Filters │     │ OS Sandbox      │     │ Regex Violation │     │ Physical Test   │
│ Receipt Check   │     │ Process Group   │     │ Sanitization    │     │ SSoT Promotion  │
└─────────────────┘     └─────────────────┘     └─────────────────┘     └─────────────────┘
```

1. **Pre-Execution Gate**: Hard-blocks dangerous commands (`rm -rf`, `drop table`) and denied path traversal (`.ssh`, `.aws`, `.env`).
2. **Runtime Cage**: Executes worker in an OS sandbox with hard execution deadlines and process group isolation.
3. **Post-Execution Scan**: Scans stdout/stderr for unauthorized mutation attempts. If a read-only worker secretly ran `git commit` or `wiki.py write`, the exit code is converted to `3` (`READ_ONLY_CONTRACT_EXIT`).
4. **Supervisor Audit**: The Supervisor verifies worker claims against physical disk evidence or runs verification commands before accepting the result.

---

## 5. The Lego-Block Modular Taxonomy

`g8s` deconstructs agent systems into 4 composable, orthogonal building blocks:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          LEGO-BLOCK AGENT TAXONOMY                      │
├─────────────────────────────────────────────────────────────────────────┤
│  🧩 SKILLS (The Mind / Software):                                       │
│     • Reusable cognitive procedures and workflows (Markdown / YAML).     │
│     • Example: `wiki-test-designer`, `homelab-provisioning`.            │
│                                                                         │
│  🔧 TOOLS (The Hands / Hardware):                                       │
│     • Callable APIs and mechanical interfaces exposed via Stdio MCP.     │
│     • Example: `g8s_run`, `g8s_submit`, `g8s_receipt_issue`.             │
│                                                                         │
│  🛡️ HARNESS (The Armor / Boundary):                                     │
│     • Process cages, role profiles, permission gates, and receipts.      │
│     • Example: `collector` role, `read_only` profile, `allowed_paths`.  │
│                                                                         │
│  ⚡ HOOKS (The Nervous System / Triggers):                               │
│     • Event-driven interceptors executed at lifecycle transitions.      │
│     • Example: Pre-dispatch validator, Post-run scan, Git pre-commit.   │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 6. Non-Invasive Ecosystem Embedding (Separation of Concerns - SoC)

A fundamental design goal of `g8s` is that **embedding it into a project must never break or pollute the host ecosystem**:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           HOST REPOSITORY                               │
│   (TypeScript / Python / Rust / Java / LLM Wiki / Monorepo)             │
│                                                                         │
│   • Zero code modifications required.                                   │
│   • Zero Go dependencies injected into host project.                    │
│   • Communicates exclusively via standard Stdio JSON-RPC (MCP).         │
└────────────────────────────────────▲────────────────────────────────────┘
                                     │
                          Clean Unix Stdio Stream
                                     │
┌────────────────────────────────────▼────────────────────────────────────┐
│                       g8s SIDECAR RUNTIME (Go)                          │
│                                                                         │
│   • Isolated State Store: `~/.local/state/g8s/` or `$XDG_STATE_HOME`    │
│   • Isolated Process Groups: Spawned workers run in separate PGIDs.     │
│   • Independent Daemon: Managed via macOS LaunchAgent / Linux systemd.  │
└─────────────────────────────────────────────────────────────────────────┘
```

### The 4 SoC Embedding Principles:
1. **Zero Runtime Leakage**: `g8s` is a standalone binary. It never forces the host codebase to add dependencies or modify `package.json`, `pyproject.toml`, or `Cargo.toml`.
2. **Standard Protocol Interoperability**: `g8s` speaks the standard Model Context Protocol (MCP) over Stdio JSON-RPC, making it universally compatible with Claude Desktop, Cursor, Codex, and Windsurf out of the box.
3. **State Isolation**: All task queues, write receipts, event logs, and caches reside in the user's local state directory (`~/.local/state/g8s/`), completely isolated from the project source tree.
4. **Fail-Closed Default**: If `g8s` encounters an unknown role, missing receipt, or malformed parameter, it halts and fails closed without touching the filesystem.
