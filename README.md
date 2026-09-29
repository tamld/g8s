<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers)

> **A Lightweight, Zero-Trust Process Execution & Capability Harness for AI Agent CLI Workers.**
> *"k8s orchestrates your compute containers; g8s orchestrates your AI subagents."*

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.26.0-00ADD8?logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-blue)](https://github.com/tamld/g8s)
[![Release](https://img.shields.io/github/v/release/tamld/g8s)](https://github.com/tamld/g8s/releases)

<p align="center">
  <b>English</b> | <a href="README.vi.md">Tiếng Việt</a>
</p>

---

## What is g8s

`g8s` (pronounced **"Gates"** — short for **G**atekeeper**s**) is a standalone, single-binary runtime for **Two-Tier Multi-Agent Systems**. A high-tier "Brain" orchestrator (Claude, GPT, DeepSeek) delegates mechanical work — code scanning, test synthesis, log digestion — to fast CLI workers (Antigravity `agy`, Claude Code CLI, Gemini CLI, Ollama) through `g8s`, which enforces the trust boundary the model layer cannot:

- **Durable task queue** — SQLite WAL with atomic CAS leases, idempotency keys, parent-child lineage.
- **Capability receipts** — workers cannot mutate the filesystem without a single-use, time-limited, path-scoped write receipt issued by the Brain.
- **Process containment** — every attempt runs in a killable process group inside a private run directory; concurrent attempts get private worktrees.
- **Evidence** — every run seals a redacted receipt into the Evidence Lake; telemetry distills failure patterns back into preflight context.

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
│  │   Context Broker │ Memory Gate │ Reflex Sensor (L1/L3)│  │
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

## Highlights (v0.12.0)

- **⚡ Pure-Go single binary** — Zero CGO (`modernc.org/sqlite` only), ~15MB, starts in <15ms.
- **🛡️ Defense-in-depth gates** — 6 roles × 3 permission profiles, blocked command patterns, sensitive-path protection (symlinks and `..` traversal included).
- **🎟️ Receipt-based write delegation** — single-use, time-limited, path-scoped (Schema v3, backward-compatible migration).
- **🚀 Concurrent dispatch** — `g8s worker --concurrency N` drains the queue through N isolated attempts (per-attempt worktrees, sessions-registry crash recovery, hard usage error outside a git checkout).
- **🧠 Memory promotion gate** — entry FSM + trust labels + payload-hash tombstones; the vault cannot be poisoned by unverified worker output (ADR-0023).
- **📡 Context Broker** — bounded situational `ContextPacket`s (vault + telemetry + SOM state) enriching Jev triage, fail-open per source.
- **⚡ System-1 reflex gate** — `g8s reflex triage` runs the Jev sensor + deterministic policy over a *planned* mutation: `grant_receipt` / `escalate_hitl` / `instant_kill`.
- **🧪 Adversarial eval harness** — 24-probe suite with deterministic semantic-class scoring, live providers (agy/claude), Provider Reliability Index.
- **💓 Observability & hygiene** — worker heartbeats, sessions registry, orphan/zombie reaping, Evidence Lake, closed-loop telemetry.

## Install

**From a release** (macOS universal / Linux amd64+arm64 / Windows amd64 — plus `.deb`/`.rpm`/`.apk` packages; derive exact asset names from the [release page](https://github.com/tamld/g8s/releases)):

```bash
curl -fsSL https://github.com/tamld/g8s/releases/latest/download/checksums.txt -o checksums.txt
# download the archive for your platform, then verify:
sha256sum g8s_v*-linux_amd64.tar.gz   # must match checksums.txt
```

**From source:**

```bash
git clone https://github.com/tamld/g8s.git && cd g8s
go build -o bin/g8s ./cmd/g8s
```

## Quickstart

```bash
# 1. Submit a read-only scout task
g8s submit \
  --idempotency-key scout-1 \
  --role scout \
  --permission read_only \
  --add-dir ./src \
  --model gemini-3.7-flash-high \
  --timeout 60s \
  --prompt "Scan ./src and return a JSON inventory of entry points."

# 2. Run a worker to claim and execute queued tasks
g8s worker --once                 # single attempt
g8s worker --once=false --concurrency 4   # concurrent drain (git checkout required)

# 3. Read the sealed result
g8s get <task-id> --json
```

**Delegated writes** need a single-use receipt issued by the Brain:

```bash
g8s receipt issue --issuer "brain-orchestrator" --path "./tests/*.py" --ttl 600
g8s submit --role test-runner --permission workspace_write \
  --receipt-id <receipt-id> --add-dir ./tests \
  --prompt "Generate pytest tests. receipt_id=<receipt-id> issuer=brain-orchestrator allowed_paths=./tests/*.py"
```

**Orchestrate and operate:**

```bash
g8s orchestrate --from-intent "Scan for security issues, generate tests, update docs" --json
g8s reflex triage --summary "raise test deadline" --files "internal/runtime/verify_test.go"
g8s memory list && g8s memory revoke --session <session-id>
g8s supervisor-metrics --aggregate --json
g8s cleanup --dry-run            # ghosts, orphan sessions, scratch branches
```

## Skills

g8s ships the operating practice for running itself as **loadable agent skills** —
see [`skills/`](skills/):

| Skill | Purpose |
|---|---|
| [`g8s-supervisor`](skills/g8s-supervisor/SKILL.md) | The supervisor/worker charter: admission-gated dispatch, multi-worker fan-out (1 task → N workers, mixed roles), evidence-verified acceptance, leak-free upstream reporting. Includes the security-redaction playbook for reporting findings without exposing project data. |

Install by copying (or symlinking) into your platform's skill directory — full
instructions and the versioned [`manifest.json`](skills/manifest.json) live in
[`skills/README.md`](skills/README.md).

## MCP Integration

Plug into Claude Desktop, Cursor, Codex, or Windsurf via stdio JSON-RPC (11 tools):

```json
{
  "mcpServers": {
    "g8s": { "command": "/usr/local/bin/g8s", "args": ["mcp"] }
  }
}
```

## Non-Goals

| Area | Non-Goal | Rationale |
|------|----------|-----------|
| **Container Orchestration** | Kubernetes/nomad, service mesh | g8s is a *process* harness — run g8s workers on k8s/nomad, not inside them. |
| **Secret Management** | Vault/AWS/GCP Secret Manager | Credentials never enter the worker sandbox; inject via environment before g8s starts. |
| **Multi-Tenancy** | RBAC, namespaces, SaaS audit | Single-tenant CLI; one g8s binary + state dir per tenant. |
| **GUI Dashboard** | Web UI for tasks/receipts | CLI-first; Evidence Lake + `g8s status` are the observability surface. |
| **Worker SDK** | Go/Rust/Python SDK | Workers are any CLI speaking the AIC protocol. |
| **Model Hosting** | Inference, model registry | g8s delegates to external CLIs — hosting is their problem. |

## Documentation

| By need | Where |
|---|---|
| Zero to first delegated task | [docs/quickstart.md](docs/quickstart.md) |
| Full command matrix & runbooks | [docs/OPERATIONS.md](docs/OPERATIONS.md), [docs/user-guide/cli-reference.md](docs/user-guide/cli-reference.md) |
| Configuration & service lifecycle | [docs/user-guide/configuration.md](docs/user-guide/configuration.md), [docs/user-guide/service.md](docs/user-guide/service.md) |
| Security & verification | [docs/security/VERIFICATION_GUIDE.md](docs/security/VERIFICATION_GUIDE.md) |
| Provider integrations | [docs/integrations/](docs/integrations/) (Antigravity, Claude Desktop, Cursor, Windsurf) |
| MCP tools reference | [docs/user-guide/mcp-tools.md](docs/user-guide/mcp-tools.md) |
| Requirements & acceptance | [docs/PRD.md](docs/PRD.md), [docs/SRS.md](docs/SRS.md), [docs/DOD_DOR.md](docs/DOD_DOR.md) |
| Governing rules | [spec/constitution.md](spec/constitution.md) — Zero-CGO, two-tier governance, process containment |
| Technical deltas | [spec/openspec/](spec/openspec/) — DELTA-01..22 with lifecycle status |
| Architecture decisions | [docs/decisions/](docs/decisions/) — ADR-0001…0023 |
| Release history | [CHANGELOG.md](CHANGELOG.md) |

Key ADRs: [ADR-0001](docs/decisions/0001-supervisor-driven-fix-loop.md) (supervisor fix loop) · [ADR-0020](docs/decisions/0020-reflex-gated-debt-campaign.md) (System-1 reflex gate) · [ADR-0021](docs/decisions/0021-standard-operating-model.md) (standard operating model, accepted) · [ADR-0022](docs/decisions/0022-strategic-session-protocol.md) (session protocol, accepted) · [ADR-0023](docs/decisions/0023-memory-lifecycle.md) (memory lifecycle, accepted).

## Quality Gates

Every commit passes **dual-pass CI**: `CGO_ENABLED=0` (pure-Go vet + tests) and `CGO_ENABLED=1 -race` (race detector) — **45 packages, 988 test functions**, zero race warnings, zero CGO dependencies. Pushes clear **12 pre-push gates** (doc-contract, layer ownership, version sync, dual-pass, dogfooding roundtrip, cross-platform build).

### Claims

Quantitative claims (containment layers, PRI scores, gate counts) are bound to verifying tests and artifacts in [`docs/claims.yml`](docs/claims.yml) and verified by [`tools/claims_check.sh`](tools/claims_check.sh) to prevent claim rot into aspirations.

## Release Roadmap

| Milestone | Key Deliverables | Status |
|--------|-----------|:---:|
| v0.10.0 (2026-09-24) | Jev AI reflex sensor decoupling, DiffDistiller & Verifier, 11 MCP tools | **Done** |
| v0.11.0 (2026-09-26) | Distributed reflex architecture (L1/L3/L6), closed-loop telemetry, eval harness, dialectic FSM | **Done** |
| v0.12.0 (2026-09-27) | **Concurrent dispatch, sessions isolation, memory promotion gate, Context Broker, brief DoR floor, live eval** | **Done** |
| v1.0.0 (2026-12-15) | GA: 6-month homelab stability, enterprise security signoff, distributed fleet mTLS | Planned |

## Project Structure

<!-- structure:start -->
```
g8s/
├── cmd/g8s/                # CLI entrypoint (stdlib flag-based, no cobra)
├── internal/               # All packages (private, not importable)
│   ├── analyzer/ — implements AST-based reference tracking and Blast Radius
│   ├── autopilot/ — implements the cron-based supervisor trigger that scans
│   ├── brief/ — implements structured brief dispatch and consumption contracts
│   ├── cleanup/ — implements ghost process and orphan resource detection
│   ├── cli/ — defines the unified JSON envelope and standard flag parsing
│   ├── codeintel/ — implements multi-tier code intelligence and blast radius
│   ├── completion/ — generates shell auto-completion scripts for bash, zsh, and fish
│   ├── config/ — loads the operator-declared provider registry that feeds
│   ├── context/ — is the Context Broker (ADR-0021 §8
│   ├── controlplane/ — implements the DELTA-03 SQLite-backed task queue with
│   ├── conv/ — (no package doc — add one)
│   ├── dialectic/ — implements the Dialectic Bounce lifecycle (#258 Phase A):
│   ├── diffintel/ — implements pure-Go unified diff parsing, noise pruning,
│   ├── dispatch/ — implements the bounded AGY CLI dispatch wrapper ported
│   ├── doctor/ — implements diagnostic sanity checks for g8s environment,
│   ├── harness/ — (no package doc — add one)
│   ├── heartbeat/ — implements per-session worker heartbeat tracking and freshness
│   ├── hooks/ — provides lifecycle hook implementations for g8s orchestrator workers
│   ├── initwiz/ — provides interactive and headless onboarding wizards for g8s,
│   ├── lockfile/ — provides non-blocking exclusive advisory file locks used
│   ├── mcp/ — implements the g8s Model Context Protocol (MCP) server over
│   ├── memory/ — (no package doc — add one)
│   ├── orchestrator/ — implements the Brain→Worker fan-out layer that sits
│   ├── pathutil/ — provides cross-platform path resolution for g8s data,
│   ├── process/ — provides cross-platform process discovery, inspection, and
│   ├── provider/ — implements native discovery and concurrency governance
│   ├── receipt/ — implements zero-trust write receipts for delegated
│   ├── reflex/ — implements a System 1 non-autoregressive decision gate
│   ├── registry/ — provides a cross-platform wrapper around Windows registry operations
│   ├── review/ — (no package doc — add one)
│   ├── runtime/ — provides runtime verification utilities for executable identity
│   ├── server/ — implements the g8s daemon mode with HTTP API server
│   ├── service/ — manages the g8s background worker as an OS daemon
│   ├── settings/ — manages persistent, atomic user and system configurations for g8s
│   ├── signing/ — provides code signing and signature verification primitives
│   ├── sleep/ — (no package doc — add one)
│   ├── state/ — implements pure FSM transition validation and append-only event logging
│   ├── supervisor/ — enforcer
│   ├── telemetry/ — (no package doc — add one)
│   ├── vault/ — implements a Zero-CGO, decoupled Knowledge Vault for g8s
│   ├── watch/ — implements the blocking watch primitive (#371): poll a
│   ├── worker/ — provides worker lifecycle supervision, execution containment,
│   └── ...                 # supporting packages
├── skills/                 # Vendored agent skills (g8s-supervisor charter + manifest)
├── packaging/              # Windows NSIS/WiX, Chocolatey, winget
├── docs/                   # User guide, ADRs, specs, security
├── plans/                  # Campaign ledgers (dated, session-type marked)
├── spec/openspec/          # OpenSpec deltas (DELTA-01..22)
├── schemas/                # JSON schemas (task, receipt, result)
├── tools/                  # CI helper scripts (pre-push gates, release)
└── .github/workflows/      # CI/CD pipelines
```
<!-- structure:end -->

## License

Distributed under the **MIT License**. Copyright (c) 2026 TamLD. See [LICENSE](LICENSE) for details.
