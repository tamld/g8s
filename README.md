<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers)

> **A Lightweight, Zero-Trust Process Execution & Capability Harness for AI Agent CLI Workers.**  
> *"k8s orchestrates your compute containers; g8s orchestrates your AI subagents."*

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-blue)](https://github.com/tamld/g8s)

<p align="center">
  <b>English</b> | <a href="README.vi.md">Tiếng Việt</a>
</p>

---

## 🌟 Overview

`g8s` (pronounced **"Gates"** — short for **G**atekeeper**s**) is a standalone, single-binary runtime designed for **Two-Tier Multi-Agent Systems**. It enables high-tier "Brain" orchestrators (Claude 3.7 Sonnet / Opus, GPT-4o, DeepSeek R1) to safely delegate heavy mechanical tasks (code scanning, unit test synthesis, MCP mapping, artifact extraction) to fast, lightweight CLI workers (Antigravity `agy`, Claude Code CLI, Gemini CLI, Ollama) behind **strict role contracts, sandboxes, and cryptographic/time-limited write receipts**.

```
┌─────────────────────────────────────────────────────────────┐
│               BRAIN TIER (Orchestrator: Opus / Codex)        │
│  • Strategic reasoning, architecture decisions              │
│  • Owns knowledge vault promotion and Git commits           │
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
│  │   Pluggable CLI Providers: agy | claude | gemini | ...│  │
│  └───────────────────────────────────────────────────────┘  │
└──────────────────────────────┬──────────────────────────────┘
                               │  Isolated Process Group & Sandbox
                               ▼
┌─────────────────────────────────────────────────────────────┐
│               WORKER TIER (Muscle: Flash / Haiku / Local)   │
│  • Bounded file inventory & code extraction                 │
│  • Fast test generation & log digestion                     │
│  • Zero access to credentials / shared session state        │
└─────────────────────────────────────────────────────────────┘
```

---

## 🚀 Key Features

* **⚡ Ultra Fast & Lightweight**: Written in Pure Go (Zero CGO). Single ~11MB binary, starts in < 15ms, uses < 15MB RAM as a background daemon.
* **🛡️ Defense-in-Depth Safety Gates**:
  * **6 Built-in Roles**: `collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`.
  * **3 Permission Profiles**: `read_only`, `automation_read`, `workspace_write`.
  * **Blocked Command Patterns**: Chokes `rm -rf`, `drop database`, `mkfs`, `cat .env`.
  * **Sensitive Path Protections**: Rejects access to `.ssh`, `.aws`, `.env`, `id_rsa` (including symlinks and `..` traversal).
* **🎟️ Receipt-Based Write Delegation**: Workers cannot mutate files unless presented with a single-use, time-limited write receipt issued by the Brain.
* **📦 Durable Control Plane**: SQLite WAL task queue with atomic Compare-And-Swap (CAS) leases, idempotency keys, and parent-child task lineage.
* **🧠 Supervisor & Intent Orchestrator**: 8-State FSM (`PLAN` → `SPAWN` → `MONITOR` → `RECEIPT` → `MERGE`/`ESCALATE`) with automated Root Cause Analysis (RCA), iterative fix loops, and `SkillMount`/`HookMount`/`MemoryMount` composability (`g8s orchestrate`).
* **💓 Worker Heartbeat & Real-Time Observability**: Live per-session heartbeat monitoring and process status introspection (`g8s status --worker`).
* **🧹 Lifecycle Hygiene & Resource Pruning**: Built-in sweeper to reap ghost processes, prune orphan worktrees, and evict stale scratch artifacts (`g8s cleanup`), with auto-cleanup hooks on orchestrator completion.
* **📜 Decoupled Brief Dispatch Workflow**: Contract-driven brief issuance and atomic consumption (`g8s brief-issue`, `g8s brief-consume`).
* **🔌 Stdio MCP Protocol**: Plugs directly into Claude Desktop, Cursor, Codex, and Windsurf via standard JSON-RPC.
* **🖥️ macOS Service Manager (LaunchAgent)** — Linux/Windows backends deferred: one-command hardened background service installation for macOS (`launchd`); Linux (`systemd`) and Windows backends are on the roadmap.

---

## 📦 Quickstart

### 1. Build from Source
```bash
git clone https://github.com/tamld/g8s.git
cd g8s
go build -o bin/g8s ./cmd/g8s
```

### 2. Submit a Read-Only Scout Task
```bash
g8s submit \
  --idempotency-key scout-1 \
  --role scout \
  --permission read_only \
  --add-dir ./src \
  --model gemini-3.7-flash-high \
  --timeout 60s \
  --payload '{"prompt": "Scan ./src for MCP server candidate implementations and return JSON."}'
```
Workers claim queued tasks automatically; poll with `g8s get <task-id>`.

### 3. Issue a Write Receipt (Brain-Only)
```bash
g8s receipt-issue \
  -issuer "opus-session-01" \
  -path "./tests/*.py" \
  -ttl 600
```
The command prints a JSON receipt envelope (`receipt_id`, `allowed_paths`,
`expires_at`). Pass the receipt fields to the worker payload so it can consume
the receipt exactly once during its delegated-write run.

### 4. Run a Delegated-Write Worker Task
Submit the task with `workspace_write` and embed the issued receipt in the
worker payload; the worker validates and consumes it exactly once at runtime.

```bash
g8s submit \
  --idempotency-key testwriter-1 \
  --role test-runner \
  --permission workspace_write \
  --add-dir ./tests \
  --model gemini-3.7-flash-high \
  --timeout 120s \
  --payload '{"prompt": "Generate pytest test cases for user authentication.", "receipt_id": "<receipt_id>", "receipt_issuer": "opus-session-01", "allowed_paths": ["./tests/*.py"]}'
```

> Note: receipts cannot be carried through the MCP surface — they are consumed
> by workers directly against the control plane.

### 5. Orchestrate an Intent via Supervisor Fix Loop
```bash
g8s orchestrate "Refactor auth middleware to use pure-Go context tokens" \
  --max-iterations 3 \
  --actor "brain-supervisor"
```

### 6. Monitor Worker Heartbeat & Process Status
```bash
# Check real-time heartbeat and worker liveness across active sessions
g8s status --worker --json
```

### 7. Run Lifecycle & Orphan Resource Cleanup
```bash
# Inspect and purge ghost worker processes, orphan worktrees, and stale artifacts
g8s cleanup --dry-run
g8s cleanup --force
```

---

## 🔌 Claude Desktop & Cursor Integration

Add to your `claude_desktop_config.json` or `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "g8s": {
      "command": "/usr/local/bin/g8s",
      "args": ["mcp"]
    }
  }
}
```

---

## 📜 Documentation & Spec-Driven Development

* [Decoupled Memory Architecture (`docs/DECOUPLED_MEMORY_ARCHITECTURE.md`)](docs/DECOUPLED_MEMORY_ARCHITECTURE.md)
* [Tri-Anchor Knowledge Distillation (`docs/CONTEXTUAL_DISTILLATION_SPEC.md`)](docs/CONTEXTUAL_DISTILLATION_SPEC.md)
* [Spec Kit Constitution (`spec/constitution.md`)](spec/constitution.md)
* [OpenSpec Technical Deltas (`spec/openspec/`)](spec/openspec/)
* [Architecture Design (`docs/ARCHITECTURE.md`)](docs/ARCHITECTURE.md)
* [Product Requirements Document (PRD)](docs/PRD.md)
* [Software Requirements Specification (SRS)](docs/SRS.md)
* [Definition of Done & Definition of Ready](docs/DOD_DOR.md)

---

### User Guide & Integrations

- [Quick Start](docs/quickstart.md) — zero to first delegated task.
- [Operations Runbook](docs/OPERATIONS.md) — complete command matrix, maintenance, and daemon runbooks.
- [Security & Verification Guide](docs/security/VERIFICATION_GUIDE.md) — checksum, pure-Go, and Cosign verification.
- [CLI Reference](docs/user-guide/cli-reference.md) — every subcommand and flag.
- [Configuration](docs/user-guide/configuration.md) — control plane and runtime settings.
- [MCP Tools Reference](docs/user-guide/mcp-tools.md) — Stdio JSON-RPC tools reference.
- [Claude Desktop Integration](docs/integrations/claude-desktop.md) — connect Claude 3.7 / Opus.
- [Cursor IDE Integration](docs/integrations/cursor.md) — connect Cursor MCP.
- [Google Antigravity Integration](docs/integrations/antigravity.md) — supervise AGY workers.
- [Windsurf Integration](docs/integrations/windsurf.md) — connect Cascade MCP.
- [Service Management](docs/user-guide/service.md) — background daemon lifecycle.

## 📄 License

Distributed under the **MIT License**. Copyright (c) 2026 TamLD. See [LICENSE](LICENSE) for details.
