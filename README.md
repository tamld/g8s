<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers)

> **A Lightweight, Zero-Trust Process Execution & Capability Harness for AI Agent CLI Workers.**  
> *"k8s orchestrates your compute containers; g8s orchestrates your AI subagents."*

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.26.0-00ADD8?logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-blue)](https://github.com/tamld/g8s)

<p align="center">
  <b>English</b> | <a href="README.vi.md">Tiếng Việt</a>
</p>

---

## 🌟 Overview

`g8s` (pronounced **"Gates"** — short for **G**atekeeper**s**) is a standalone, single-binary runtime designed for **Two-Tier Multi-Agent Systems**. It enables high-tier "Brain" orchestrators (Claude 3.7 Sonnet / Opus, GPT-4o, DeepSeek R1) to safely delegate heavy mechanical tasks (code scanning, unit test synthesis, MCP mapping, artifact extraction) to fast, lightweight CLI workers (Antigravity `agy`, Claude Code CLI, Gemini CLI, Ollama) behind **strict role contracts, sandboxes, and cryptographic/time-limited write receipts**.

**v0.10.0 (2026-09-20)** delivers the complete **DELTA-11 Orchestration Roadmap**, Jev AI reflex sensor decoupling, native DiffDistiller & Verifier subagent pipeline, plus hardened governance gates:

| Concern | Deliverable | Status |
|---------|-------------|--------|
| **A** | Supervisor-driven fix loop (`internal/supervisor` + `g8s orchestrate`) | ✅ Complete |
| **B** | Receipt evolution (Schema v3, backward-compat migration) | ✅ Complete |
| **C** | Meta-optimizer read-only ingestion (`g8s supervisor metrics`) | ✅ Complete |
| **Δ18** | AIC contract + From-Intent orchestration | ✅ Complete |

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
│  │   Pluggable CLI Providers: agy │ claude │ gemini │ ... │  │
│  └───────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────────────────────────────────┐  │
│  │   Supervisor Fix Loop (planner→enforcer→reviewer→rca) │  │
│  │   Meta-Optimizer Read-Only (aggregate/stream metrics) │  │
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

## 🚀 Key Features (v0.10.1)

* **⚡ Ultra Fast & Lightweight**: Written in Pure Go (Zero CGO). Single ~15MB binary, starts in < 15ms, uses < 15MB RAM as a background daemon.
* **🛡️ Defense-in-Depth Safety Gates**:
  * **6 Built-in Roles**: `collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`.
  * **3 Permission Profiles**: `read_only`, `automation_read`, `workspace_write`.
  * **Blocked Command Patterns**: Chokes `rm -rf`, `drop database`, `mkfs`, `cat .env`.
  * **Sensitive Path Protections**: Rejects access to `.ssh`, `.aws`, `.env`, `id_rsa` (including symlinks and `..` traversal).
* **🎟️ Receipt-Based Write Delegation (Schema v3)**: Workers cannot mutate files unless presented with a single-use, time-limited write receipt issued by the Brain. Supervisor metadata columns (`approach_idx`, `attempt_idx`, `rca_confidence`, `adr_path`) + provenance/replay context (11 columns) — backward-compatible idempotent migration.
* **📦 Durable Control Plane**: SQLite WAL task queue with atomic Compare-And-Swap (CAS) leases, idempotency keys, and parent-child task lineage.
* **🧠 Supervisor-Driven Fix Loop (Concern A)**: Bounded iteration policy (3 attempts × 3 approaches = 9 max), planner (envelope selection: SRS/PRD/DoR/DoD/DnD/Validateds/FSM), enforcer (DoR/DoD gating), reviewer (receipt inspection — scope violation always fails), RCA (structured analysis with confidence scoring; <0.6 → NEEDS_INFO pause), escalator (HITL JSON digest on stdout). **ADR-0001** accepted.
* **📊 Meta-Optimizer Read-Only Ingestion (Concern C)**: `g8s supervisor-metrics --aggregate` computes 8 metrics across all runs (total_runs, first_attempt_success_rate, avg_attempts_to_success, avg_approaches_to_success, rca_confidence_avg, escalation_rate, avg_cycle_duration_seconds, false_escalation_rate). Streaming via `--json-stream`. Flag collision guard: `--task-id` + `--aggregate/--json-stream` → usage error. **ADR-0003** proposed.
* **⚡ System-1 Reflex Mutation Gate**: `g8s reflex triage --summary ... --files ...` runs the Jev sensor (TypeSafe AI) + deterministic supervisor policy over a *planned* mutation and returns `grant_receipt` / `escalate_hitl` / `instant_kill` — enforce-code pre-mutation gating for scripts and CI. **ADR-0020** accepted.
* **🔌 DELTA-18 AIC & From-Intent Orchestration**: `g8s orchestrate-aic --pr <num> --intent <text>` fetches PR diff via `gh` and delegates to `--from-intent`. `g8s orchestrate --from-intent <text>` / `--from-file <path>` splits comma/newline intent into collector sub-tasks via FanOut. JSON envelope output with `supervisor_task_id`, `outcome`, `sub_tasks`, `receipt_summary`.
* **💓 Worker Heartbeat & Real-Time Observability**: Live per-session heartbeat monitoring and process status introspection (`g8s status --worker`).
* **🧹 Lifecycle Hygiene & Resource Pruning**: Built-in sweeper to reap ghost processes, prune orphan worktrees, and evict stale scratch artifacts (`g8s cleanup`), with auto-cleanup hooks on orchestrator completion.
* **📜 Decoupled Brief Dispatch Workflow**: Contract-driven brief issuance and atomic consumption (`g8s brief-issue`, `g8s brief-consume`).
* **🔌 Stdio MCP Protocol (11 tools)**: Plugs directly into Claude Desktop, Cursor, Codex, and Windsurf via standard JSON-RPC. Tools: `g8s_dispatch`, `g8s_get_task`, `g8s_list_tasks`, `g8s_cancel_task`, `g8s_submit`, `g8s_blast_radius`, `g8s_run`, `g8s_self_awareness`, `g8s_receipt_issue`, `g8s_list_roles`, `g8s_list_permissions`.
* **🖥️ macOS Service Manager (LaunchAgent)** — Linux/Windows backends deferred: one-command hardened background service installation for macOS (`launchd`); Linux (`systemd`) and Windows backends are on the roadmap.

---

## 🚫 Non-Goals (Explicit)

What g8s will **NOT** ship, and why:

| Area | Non-Goal | Rationale |
|------|----------|-----------|
| **Container Orchestration** | Kubernetes/nomad integration, pod scheduling, service mesh | Out of scope — g8s is a *process* harness, not a cluster orchestrator. Use k8s/nomad to run g8s workers. |
| **Secret Management** | Vault, AWS Secrets Manager, GCP Secret Manager integration | Workers run with `read_only`/`automation_read` by design; credentials never enter the worker sandbox. If you need secrets, inject via environment *before* g8s starts. |
| **Multi-Tenancy** | Built-in RBAC, namespaces, quotas, audit logging for SaaS | Single-tenant CLI tool. Multi-tenancy is an orchestration-layer concern (e.g., each tenant gets their own g8s binary + state dir). |
| **GUI/Web Dashboard** | Web UI for task monitoring, receipt visualization | CLI-first. Evidence lake (JSONL receipts) + `g8s status` + `g8s receipt list/verify` provide programmatic observability. Build a dashboard on top if needed. |
| **Worker SDK** | Go/Rust/Python SDK for custom workers | Workers are *any CLI* that speaks the AIC protocol (stdin/stdout JSON). No SDK needed — just implement the contract. |
| **Model Hosting** | Built-in LLM serving, inference, model registry | g8s delegates to external CLIs (`agy`, `claude`, `gemini`, `ollama`). Model hosting is their problem. |
| **Distributed Tracing** | OpenTelemetry, Jaeger, Zipkin integration | Single-binary, single-process. Add tracing at the orchestration layer if needed. |
| **Configuration Management** | Consul, etcd, ZooKeeper for config sync | Config is local files (`providers.json`, `state dir`). GitOps for team config. |

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
  --model gemini-3.8-flash-high \
  --timeout 60s \
  --prompt "Scan ./src for MCP server candidate implementations and return JSON."
```
Workers claim queued tasks automatically; poll with `g8s get <task-id>`.

### 3. Delegated Write with a Single-Use Receipt
```bash
# Brain issues a path-scoped, time-limited receipt.
g8s receipt issue --issuer "brain-orchestrator" --path "./tests/*.py" --ttl 600

# Worker task consumes the receipt exactly once at runtime. Delegated writes
# are opt-in: set AGY_MCP_ALLOW_WORKSPACE_WRITE=1 in the worker environment.
g8s submit \
  --idempotency-key testwriter-1 \
  --role test-runner \
  --permission workspace_write \
  --add-dir ./tests \
  --model gemini-3.8-flash-high \
  --timeout 120s \
  --prompt "Generate pytest test cases for user authentication. receipt_id=<receipt_id> issuer=brain-orchestrator allowed_paths=./tests/*.py"
```
> Receipts cannot be carried through the MCP surface — workers consume them directly against the control plane.

### 4. Orchestrate
```bash
# Supervisor fix loop self-test (deterministic escalation at 9 attempts)
g8s orchestrate "Refactor auth middleware to use pure-Go context tokens" --self-test --actor "brain-supervisor"

# From-intent: free text split into collector sub-tasks (DELTA-18)
g8s orchestrate --from-intent "Scan for security issues, generate tests, update docs" --role collector --add-dir ./src --json

# AIC automated PR review (requires authenticated gh CLI)
g8s orchestrate-aic --pr 123 --intent "Review security changes for auth middleware" --json
```

### 5. Operate
```bash
g8s supervisor-metrics --aggregate --json   # meta-optimizer metrics (Concern C)
g8s status --worker --json                  # heartbeat + process introspection
g8s cleanup --dry-run                       # lifecycle sweep: ghosts, orphans, scratch branches
g8s cleanup --force --scratch               # apply, including worker scratch branches (#327)
g8s reflex triage --summary "raise test deadline" --files "internal/runtime/verify_test.go"   # System-1 mutation gate (ADR-0020)
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

### Architecture & Specs
* [Spec Kit Constitution (`spec/constitution.md`)](spec/constitution.md)
* [OpenSpec Technical Deltas (`spec/openspec/`)](spec/openspec/)
  * **DELTA-11** — Orchestration Roadmap (Concerns A, B, C) — **v1.3.0-ACCEPTED**
  * **DELTA-18** — AIC Integration & From-Intent Orchestration — **IMPLEMENTED**
* [Architecture Design (`docs/designs/supervisor-fix-loop.md`)](docs/designs/supervisor-fix-loop.md) — **v1.2.0-ACCEPTED**
* [Product Requirements Document (PRD)](docs/PRD.md)
* [Software Requirements Specification (SRS)](docs/SRS.md)
* [Definition of Done & Definition of Ready](docs/DOD_DOR.md)

### Architecture Decision Records
* [ADR-0001: Supervisor-driven fix loop with code-level enforcement](docs/decisions/0001-supervisor-driven-fix-loop.md) — **Accepted**
* [ADR-0002: Receipt evolution for supervisor](docs/decisions/0002-receipt-evolution-for-supervisor.md) — **Proposed**
* [ADR-0003: Meta-optimizer read-only ingestion](docs/decisions/0003-meta-optimizer-read-only.md) — **Proposed**

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

### Release & Changelog
- [Refactoring Masterplan & Release Roadmap](docs/REFACTORING_PLAN.md) — v0.2.0 → v1.0.0 milestones
- [Changelog](docs/CHANGELOG.md) — Keep a Changelog format, issue/PR backlog

---

## 📦 Release Artifacts (v0.10.1)

Archives (GoReleaser v2 — macOS ships a single universal binary):

| Artifact | Platform |
|----------|----------|
| `g8s_v0.10.1_darwin_all.tar.gz` | macOS universal (Intel + Apple Silicon) |
| `g8s_v0.10.1_linux_amd64.tar.gz` | Linux x86_64 |
| `g8s_v0.10.1_linux_arm64.tar.gz` | Linux ARM64 |
| `g8s_v0.10.1_windows_amd64.zip` | Windows x86_64 |

Packages (Linux, additionally published): `g8s_0.10.1_amd64.deb`, `g8s_0.10.1_arm64.deb`, `g8s-0.10.1-1.x86_64.rpm`, `g8s-0.10.1-1.aarch64.rpm`, `g8s_0.10.1_amd64.apk`, `g8s_0.10.1_aarch64.apk`. Always derive the exact asset names from the release page (`gh release view v0.10.1 --json assets`) — #338.

### Verification
```bash
# Checksums (SHA256)
sha256sum g8s_v0.10.1_*.tar.gz g8s_v0.10.1_*.zip checksums.txt

# Cosign signature verification (when published)
cosign verify-blob --signature g8s_v0.10.1_darwin_all.tar.gz.sig \
  --certificate g8s_v0.10.1_darwin_all.tar.gz.pem \
  g8s_v0.10.1_darwin_all.tar.gz
```

---

## 🧪 Testing & Quality Gates

**Dual-pass CI mandatory** on every commit:
```bash
# CGO_ENABLED=0 (pure Go, fast)
CGO_ENABLED=0 go vet ./... && go test -count=1 ./...

# CGO_ENABLED=1 -race (race detector, strict)
CGO_ENABLED=1 go test -race -count=1 ./...
```

- **38 packages** — all green under both gates
- **820+ test functions** — table-driven, injectable clock, deterministic
- **Zero race detector warnings**
- **Zero CGO dependencies** (`modernc.org/sqlite` only)

---

## 🗺️ Release Roadmap

| Target | Milestone | Key Deliverables | Status |
|--------|-----------|------------------|:---:|
| **2026-09-13** | **v0.2.0** | **Concern A + B + C complete** — Supervisor fix loop, Receipt evolution (Schema v3), Meta-optimizer read-only. DELTA-18 AIC + From-Intent. | **Done** |
| **2026-09-15** | **v0.3.0** | Autopilot scheduler, HTTP API daemon (`g8s serve`), False escalation feedback loop, Configurable priority weights, Prometheus `/metrics`. | **Done** |
| **2026-08-30** | **v0.4.0** | Observability & hardening. | **Done** |
| **2026-09** | **v0.5.0 → v0.9.2** | Incremental releases: DELTA-20 code-intel adapter (v0.7.0), Windows service backend (DEBT-44), knowledge vault, DX/AX wizard. | **Done** |
| **2026-09-24** | **v0.10.0** | **Jev AI + DiffIntel + Governance** — Reflex sensor decoupling (`internal/reflex`), Native DiffDistiller & Verifier (`internal/diffintel`, `internal/review`), Supervisor coverage $\ge 93.6\%$, 11 MCP tools. | **Done** |
| **2026-10-05** | v0.11.0 | DELTA-21 Unified Memory Facade (merged #325), Closed-Loop Telemetry Engine (#253), Adversarial Evaluations Harness (#254). | In Progress |
| **2026-11-01** | v0.12.0 | DELTA-20 Code Intelligence Tiers 0.5–2, kardianos/service unification, debt-squad fixes (#326–#331). | Planned |
| **2026-12-15** | v1.0.0 | GA Release: 6-month homelab stability, Enterprise security signoff, Distributed fleet mTLS. | Planned |

---

## 📄 License

Distributed under the **MIT License**. Copyright (c) 2026 TamLD. See [LICENSE](LICENSE) for details.