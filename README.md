<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers)

> **A Lightweight, Zero-Trust Process Execution & Capability Harness for AI Agent CLI Workers.**  
> *"k8s orchestrates your compute containers; g8s orchestrates your AI subagents."*

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-blue)](https://github.com/tamld/g8s)

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
* **🔌 Stdio MCP Protocol**: Plugs directly into Claude Desktop, Cursor, Codex, and Windsurf via standard JSON-RPC.
* **🖥️ macOS Service Manager (LaunchAgent)** — Linux/Windows backends deferred: One-command background service installation for macOS (`launchd`), Linux (`systemd`), and Windows (`service`).

---

## 📦 Quickstart

### 1. Build from Source
```bash
git clone https://github.com/tamld/g8s.git
cd g8s
go build -o bin/g8s ./cmd/g8s
```

### 2. Run a Read-Only Scout Task
```bash
./bin/g8s run \
  --provider agy \
  --role scout \
  --permission read_only \
  --add-dir ./src \
  --prompt "Scan ./src for MCP server candidate implementations and return JSON."
```

### 3. Issue a Write Receipt (Brain-Only)
```bash
./bin/g8s receipt issue \
  --issuer "opus-session-01" \
  --allow "./tests/*.py" \
  --ttl 600
# Output: Returns receipt_id: 3c8e41bf-23d9-4822-86ec-350e82c16fa8
```

### 4. Run Delegated Write Worker
```bash
./bin/g8s run \
  --provider agy \
  --role test-runner \
  --permission workspace_write \
  --receipt-id "3c8e41bf-23d9-4822-86ec-350e82c16fa8" \
  --add-dir ./tests \
  --prompt "Generate pytest test cases for user authentication."
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

* [Spec Kit Constitution (`spec/constitution.md`)](spec/constitution.md)
* [OpenSpec Technical Deltas (`spec/openspec/`)](spec/openspec/)
* [Product Requirements Document (PRD)](docs/PRD.md)
* [Software Requirements Specification (SRS)](docs/SRS.md)
* [Definition of Done & Definition of Ready](docs/DOD_DOR.md)
* [Architecture Design](docs/ARCHITECTURE.md)

---

### User Guide

- [Quick Start](docs/quickstart.md) — zero to first delegated task.
- [CLI Reference](docs/user-guide/cli-reference.md) — every subcommand and flag.
- [Configuration](docs/user-guide/configuration.md) — env vars, profiles, bounds.
- [Receipt Delegation](docs/user-guide/receipt-workflow.md) — write receipts end-to-end.
- [MCP Tools](docs/user-guide/mcp-tools.md) — connect Claude Desktop / Cursor.
- [Service Management](docs/user-guide/service.md) — macOS LaunchAgent lifecycle.

## 📄 License

Distributed under the **MIT License**. Copyright (c) 2026 TamLD. See [LICENSE](LICENSE) for details.
