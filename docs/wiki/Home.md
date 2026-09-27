# Welcome to the g8s Wiki

> **g8s (The Gatekeepers)** — *A Lightweight, Zero-Trust Process Execution & Capability Harness for AI Agent CLI Workers.*  
> *"k8s orchestrates your compute containers; g8s orchestrates your AI subagents."*

---

## 🧭 Navigation & Knowledge Base

| Section | Description | Target Page |
| :--- | :--- | :--- |
| **🏛️ Architecture & Governance** | Foundational Axioms, Two-Tier Hierarchy, Process Group Sandbox | [Two-Tier Governance & Architecture](Two-Tier-Governance-and-Receipt-Engine.md) |
| **🎟️ Capability Receipts (Schema v3)** | Zero-Trust Write Delegation, Single-Use Invalidation, Path Scoping | [Receipt Engine & Lifecycle](../user-guide/receipt-workflow.md) |
| **🧠 Supervisor & Self-Healing Loop** | Bounded 3x3 Fix Loops, RCA Confidence, Envelope Gating (DoR/DoD) | [Supervisor & Fix Loop](../designs/supervisor-fix-loop.md) |
| **🔌 Tool Integrations & MCP** | Claude Desktop, Windsurf, Cursor, AGY integration via 11 Stdio MCP tools | [MCP & Tool Integrations](../user-guide/mcp-tools.md) |
| **🗄️ Decoupled Memory & Vault** | SQLite FTS5 Full-Text Search, BM25 ranking, Contextual Distillation | [Decoupled Memory Vault](Decoupled-Memory-Vault.md) |
| **🛠️ CLI Command Reference** | Command syntax, flags, JSON output envelopes, and troubleshooting | [CLI Command Reference](CLI-Command-Reference.md) |

---

## 🌟 Executive Summary: What is `g8s`?

Modern agentic coding workflows often suffer from the **Agent Fragility Dilemma**:
1. High-tier reasoning models (Claude 3.7 Sonnet / Opus, GPT-4o) are expensive and prone to context pollution when running raw shell commands.
2. Low-tier CLI workers (Gemini Flash, Claude Haiku, Ollama) are fast and cheap, but dangerous if given unrestricted write access or long-running unbounded loops.

`g8s` solves this by introducing a **Zero-Trust Capability Harness**:
- **Brain Tier (Planner/Governor)**: Retains exclusive authority over architecture, write receipt issuance, and git state promotion.
- **Worker Tier (Muscle/Executor)**: Executes mechanical sub-tasks inside isolated process groups with deterministic timeouts, blocked commands, and path-scoped write receipts.
- **Zero CGO, Pure Go**: Single static binary (~15MB), sub-15ms startup, with modern SQLite WAL task queue.

```text
┌─────────────────────────────────────────────────────────────┐
│               BRAIN TIER (Orchestrator: Opus / Codex)        │
│  • Strategic reasoning & high-level architecture decisions  │
│  • Issues time-limited, path-scoped Write Receipts          │
└──────────────────────────────┬──────────────────────────────┘
                               │  JSON-RPC MCP / CLI Dispatch
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                   g8s (Zero-CGO Static Binary)              │
│  ┌──────────────────┐  ┌──────────────────┐  ┌───────────┐  │
│  │ Role & Perm Gate │  │ Write Receipt DB │  │ WAL Queue │  │
│  └──────────────────┘  └──────────────────┘  └───────────┘  │
│  ┌───────────────────────────────────────────────────────┐  │
│  │   Pluggable CLI Providers: agy │ claude │ gemini │ ... │  │
│  └───────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────────────────────────────────┐  │
│  │   Supervisor Fix Loop (planner→enforcer→reviewer→rca) │  │
│  └───────────────────────────────────────────────────────┘  │
└──────────────────────────────┬──────────────────────────────┘
                               │  Isolated Process Group & Sandbox
                               ▼
┌─────────────────────────────────────────────────────────────┐
│               WORKER TIER (Muscle: Flash / Haiku / Local)   │
│  • Bounded file inventory & code extraction                 │
│  • Fast test generation & log digestion                     │
│  • Zero access to credentials or sensitive paths            │
└─────────────────────────────────────────────────────────────┘
```

---

## ⚡ Quickstart

### 1. Verification
```bash
# Check version and runtime configuration
g8s --version

# Run system diagnostic self-check
g8s doctor --json
```

### 2. Issuing a Single-Use Write Receipt
```bash
# Brain issues a 600-second write receipt scoped strictly to internal/worker/
g8s receipt-issue \
  --role test-runner \
  --allowed-paths "internal/worker/*" \
  --ttl 600
```

### 3. Dispatching a Worker Task
```bash
# Worker executes within sandbox using the receipt
g8s dispatch \
  --role test-runner \
  --provider agy \
  --receipt-id <RECEIPT_ID> \
  --prompt "Generate table-driven tests for internal/worker/stream.go"
```

---

## 🔒 The 5 Foundational Axioms

1. **Two-Tier Governance**: High-tier models hold strategic ownership; low-tier models perform sandboxed mechanical execution.
2. **Zero-Trust Capability**: No filesystem mutation without a single-use, time-limited, path-scoped Write Receipt.
3. **Pure Go & Zero-CGO**: 100% portable static binaries across macOS, Linux, and Windows using `modernc.org/sqlite`.
4. **Process & State Containment**: Process group isolation (`Setpgid` / Windows JobObjects) and POSIX `0600`/`0700` state hygiene.
5. **Self-Describing Executable**: The binary is the Single Source of Truth (SSoT); all capabilities discoverable via `--help` and `--json`.
