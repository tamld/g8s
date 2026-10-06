<p align="center">
  <img src="assets/logo.svg" alt="g8s logo" width="128"/>
</p>

# g8s (The Gatekeepers)

> **Single-binary zero-trust process execution harness (<25MB static pure-Go binary) for AI agent CLI workers.**
> *"k8s orchestrates your compute containers; g8s orchestrates your AI subagents."*

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.26.0-00ADD8?logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-blue)](https://github.com/tamld/g8s)
[![Release](https://img.shields.io/github/v/release/tamld/g8s)](https://github.com/tamld/g8s/releases)

<p align="center">
  <b>English</b> | <a href="README.vi.md">Tiếng Việt</a>
</p>

---

## What g8s is

g8s is a single static binary (pure Go, zero CGO) that lets a high-tier orchestrator ("Brain": Claude, GPT, Codex) delegate mechanical work to external CLI worker processes (agy, Claude, Gemini, Ollama) **without giving them the keys to your machine**. The model layer cannot be trusted with authority; g8s enforces it in process terms:

- **Durable task queue**: SQLite WAL, atomic CAS leases, idempotency keys, parent-child lineage. Tasks survive session death; the queue is the memory.
- **Capability receipts**: a worker cannot write the filesystem without a single-use, time-limited, path-scoped receipt issued by the Brain.
- **Process containment**: every attempt runs in a killable process group inside a private worktree; concurrent attempts stay isolated.
- **Evidence, not verdicts**: every run seals a redacted receipt into the Evidence Lake. Worker verdicts are claims; files on disk are proof.

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
┌────────────────────────────────────────────────────────────────────────┐
│               WORKER TIER (Muscle: Flash / Haiku / Local)              │
│  • Bounded file inventory & code extraction                            │
│  • Bounded-time test generation and log digestion (<60s per attempt)   │
│  • Zero access to credentials / shared session state                   │
└────────────────────────────────────────────────────────────────────────┘
```

## Shipped in v0.14.0

- **Event-driven supervision**: terminal transitions append to `<state_dir>/signals/tasks.jsonl`; `g8s watch --failed` wakes the supervisor with one sleeping process. Polling is the fallback, never the primary.
- **Multi-provider queue**: `providers.json` is the manifest; per-provider claim affinity, `--provider` on submit and worker, no silent fallback.
- **Budgeted retry**: failed tasks auto-resubmit under caps: 2 per task, 10/hour per state dir, exponential backoff, feature flag default OFF. Receipt-free tasks only for unattended ticks.
- **Concurrent dispatch**: `g8s worker --concurrency N` drains through N isolated attempts with per-attempt worktrees.
- **Adversarial eval harness**: probe suite with semantic-class scoring and a Provider Reliability Index; mock providers make it CI-runnable.
- **Fuzzed inputs, measured baseline**: four native fuzz targets over the external-input parsers; queue operation latencies recorded in [docs/user-guide/performance.md](docs/user-guide/performance.md).

Quantitative claims (containment layers, PRI thresholds, gate counts) live in [docs/claims.yml](docs/claims.yml) and [docs/ENTERPRISE_LEDGER.md](docs/ENTERPRISE_LEDGER.md), each bound to a verifying test; [tools/claims_check.sh](tools/claims_check.sh) re-checks them on every commit.

## Install

One line (macOS / Linux):

```bash
curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
```

From a [release](https://github.com/tamld/g8s/releases) (macOS universal, Linux amd64+arm64, Windows amd64, plus `.deb`/`.rpm`/`.apk`): download the archive, verify against `checksums.txt`.

From source:

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
  --model gemini-3.8-flash-high \
  --timeout 60s \
  --prompt "Scan ./src and return a JSON inventory of entry points."

# 2. Drain the queue (workers claim tasks in isolated worktrees)
g8s worker --once                 # single attempt
g8s worker --once=false --concurrency 4   # concurrent drain (git checkout required)

# 3. Wake on completion (no polling)
g8s watch --task <task-id> --milestone worker-complete
g8s watch --failed                # exits when ANY task reaches a terminal state

# 4. Read the sealed result
g8s get <task-id>
```

Delegated writes need a single-use receipt (note `--ttl`: the default is short by design; size it to the task):

```bash
g8s receipt issue --issuer "brain-orchestrator" --path "./tests/*.py" --ttl 3600
AGY_MCP_ALLOW_WORKSPACE_WRITE=1 g8s submit --role test-runner --permission workspace_write \
  --receipt-id <receipt-id> --add-dir ./tests \
  --prompt "Generate pytest tests. receipt_id=<receipt-id> allowed_paths=./tests/*.py"
```

Run a self-audit, reflex gate, and hygiene sweep:

```bash
g8s eval run --provider agy      # adversarial probe suite
g8s reflex triage --summary "raise test deadline" --files "internal/runtime/verify_test.go"
g8s autopilot tick               # stateless maintenance: doctor, retention, hygiene
g8s cleanup --dry-run            # ghosts, orphan sessions, scratch branches
```

## Skills

g8s ships its own operating practice as loadable agent skills: see [`skills/README.md`](skills/README.md) and the versioned [`manifest.json`](skills/manifest.json):

| Skill | Purpose |
|---|---|
| [`g8s-supervisor`](skills/g8s-supervisor/SKILL.md) | The supervisor/worker charter: admission-gated dispatch, multi-worker fan-out, evidence-verified acceptance, security-redaction playbook for upstream reporting. |

Install by copying or symlinking into your platform's skill directory.

## MCP Integration

Plug into Claude Desktop, Cursor, Codex, or Windsurf via stdio JSON-RPC:

```json
{
  "mcpServers": {
    "g8s": { "command": "/usr/local/bin/g8s", "args": ["mcp"] }
  }
}
```

Tool reference: [docs/user-guide/mcp-tools.md](docs/user-guide/mcp-tools.md).

## Non-Goals

| Area | Non-Goal | Rationale |
|------|----------|-----------|
| **Container Orchestration** | Kubernetes/nomad, service mesh | g8s is a *process* harness: run g8s workers on k8s/nomad, not inside them. |
| **Secret Management** | Vault/AWS/GCP Secret Manager | Credentials never enter the worker sandbox; inject via environment before g8s starts. |
| **Multi-Tenancy** | RBAC, namespaces, SaaS audit | Single-tenant CLI; one g8s binary + state dir per tenant ([ADR-0028](docs/decisions/0028-multi-project-tenancy.md)). |
| **GUI Dashboard** | Web UI for tasks/receipts | CLI-first; Evidence Lake + `g8s status` are the observability surface. |
| **Worker SDK** | Go/Rust/Python SDK | Workers are any CLI speaking the AIC protocol. |
| **Model Hosting** | Inference, model registry | g8s delegates to external CLIs; hosting is their problem. |

## Documentation

| By need | Where |
|---|---|
| Zero to first delegated task | [docs/quickstart.md](docs/quickstart.md) |
| Full command matrix & runbooks | [docs/OPERATIONS.md](docs/OPERATIONS.md), [docs/user-guide/cli-reference.md](docs/user-guide/cli-reference.md) |
| Configuration & service lifecycle | [docs/user-guide/configuration.md](docs/user-guide/configuration.md), [docs/user-guide/service.md](docs/user-guide/service.md) |
| Security & verification | [docs/security/VERIFICATION_GUIDE.md](docs/security/VERIFICATION_GUIDE.md) |
| Provider integrations | [docs/integrations/](docs/integrations/) (Antigravity, Claude Desktop, Cursor, Windsurf) |
| Requirements & acceptance | [docs/PRD.md](docs/PRD.md), [docs/SRS.md](docs/SRS.md), [docs/DOD_DOR.md](docs/DOD_DOR.md) |
| Governing rules | [spec/constitution.md](spec/constitution.md): Zero-CGO, two-tier governance, process containment |
| Technical deltas | [spec/openspec/](spec/openspec/): DELTA-01..22 with lifecycle status |
| Architecture decisions | [docs/decisions/](docs/decisions/): ADR-0001…0032 |
| Campaign ledgers & history | [plans/](plans/), [docs/history/](docs/history/) |
| Release history | [CHANGELOG.md](CHANGELOG.md) |

Key ADRs: [ADR-0021](docs/decisions/0021-standard-operating-model.md) (standard operating model) · [ADR-0024](docs/decisions/0024-gate-lane-routing.md) (gate lane routing) · [ADR-0026](docs/decisions/0026-independent-verification-phases.md) (verification phases) · [ADR-0028](docs/decisions/0028-multi-project-tenancy.md) (multi-project tenancy) · [ADR-0029](docs/decisions/0029-budgeted-auto-retry.md) (budgeted auto-retry) · [ADR-0030](docs/decisions/0030-context-router.md) (context router).

## Quality Gates

Every commit passes **dual-pass CI**: `CGO_ENABLED=0` (pure-Go vet + tests) and `CGO_ENABLED=1 -race` (race detector). Pushes clear the pre-push gate battery: doc-contract sync, layer ownership, version sync, coverage ratchet, dogfooding roundtrip, cross-platform build, root hygiene.

## Roadmap

| Milestone | Key Deliverables | Status |
|--------|-----------|:---:|
| v0.12.0 (2026-09-27) | Concurrent dispatch, sessions isolation, memory promotion gate, Context Broker, live eval | **Done** |
| v0.13.0 (2026-09-30) | Multi-provider queue, hardening waves B–E, event-driven signals (signals/tasks.jsonl + `watch --failed`), release gates 7–8 | **Done** |
| v0.14.0 (2026-10-03) | AI-factory loop, stage 1: budgeted auto-retry ✅ (#501/#502), router in user hands — [#513](https://github.com/tamld/g8s/issues/513), lane router — [#514](https://github.com/tamld/g8s/issues/514), verifier registry — [#515](https://github.com/tamld/g8s/issues/515), first unattended closed round — [#516](https://github.com/tamld/g8s/issues/516); cut = [#517](https://github.com/tamld/g8s/issues/517) · [SCORECARD](plans/261002-factory/SCORECARD.md) | **Done** |
| v0.15.0 (target) | Retrospective-as-task — the self-maturity organ — [#519](https://github.com/tamld/g8s/issues/519) | Planned |
| v0.16.0 (target) | Effort optimization: provider-neutral effort manifest (named/budget/baked-name adapters), task-class→effort mapping, per-class cost telemetry — [#550](https://github.com/tamld/g8s/issues/550), plans/261004-effort-optimization | Planned |
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
│   ├── conv/ — executes multi-worker dual-blind design runs and synthesizes proposals into converged specifications
│   ├── dialectic/ — implements the Dialectic Bounce lifecycle (#258 Phase A):
│   ├── diffintel/ — implements pure-Go unified diff parsing, noise pruning,
│   ├── dispatch/ — implements the bounded AGY CLI dispatch wrapper ported
│   ├── doctor/ — implements diagnostic sanity checks for g8s environment,
│   ├── harness/ — enforces worker execution boundaries, permission profiles, role constraints, and prompt safety scanning
│   ├── heartbeat/ — implements per-session worker heartbeat tracking and freshness
│   ├── hooks/ — provides lifecycle hook implementations for g8s orchestrator workers
│   ├── initwiz/ — provides interactive and headless onboarding wizards for g8s,
│   ├── ladder/ — implements the quality-ladder failure classification, policy escalation, and telemetry gauges
│   ├── lane/ — implements ALDC Layer 1 gate-lane routing and Layer 2 Jev-assisted
│   ├── lessons/ — implements schema, append-only ledger, and fail-closed machine checks for retrospective lessons
│   ├── lockfile/ — provides non-blocking exclusive advisory file locks used
│   ├── mcp/ — implements the g8s Model Context Protocol (MCP) server over
│   ├── memory/ — provides a unified memory facade across working, episodic, semantic, and capability storage tiers
│   ├── orchestrator/ — implements the Brain→Worker fan-out layer that sits
│   ├── pathutil/ — provides cross-platform path resolution for g8s data,
│   ├── process/ — provides cross-platform process discovery, inspection, and
│   ├── provider/ — implements native discovery and concurrency governance
│   ├── receipt/ — implements zero-trust write receipts for delegated
│   ├── reflex/ — implements a System 1 non-autoregressive decision gate
│   ├── registry/ — provides a cross-platform wrapper around Windows registry operations
│   ├── review/ — parses, validates, and aggregates code review findings from verifier workers into structured summaries
│   ├── routing/ — routes tasks to providers, models, and roles using deterministic rules and optional LLM assistance
│   ├── runtime/ — provides runtime verification utilities for executable identity
│   ├── server/ — implements the g8s daemon mode with HTTP API server
│   ├── service/ — manages the g8s background worker as an OS daemon
│   ├── settings/ — manages persistent, atomic user and system configurations for g8s
│   ├── signing/ — provides code signing and signature verification primitives
│   ├── sleep/ — manages operator away cycles, tracks background execution events, and generates wake-up briefings
│   ├── state/ — implements pure FSM transition validation and append-only event logging
│   ├── supervisor/ — coordinates the fix loop across planning, role enforcement, worker execution, review, and RCA
│   ├── telemetry/ — ingests execution trace events, distills failure patterns, and provides preflight context injection
│   ├── vault/ — implements a Zero-CGO, decoupled Knowledge Vault for g8s
│   ├── verifier/ — implements verifier-class registry and acceptance verification (issue #515, SCORECARD S-7)
│   ├── watch/ — implements the blocking watch primitive (#371): poll a
│   ├── worker/ — provides worker lifecycle supervision, execution containment,
│   └── ...                 # supporting packages
├── offer.go                # root package: go:embed glue for the offer/ bundle + spec-sync gate scripts
├── assets/                 # Logo and release artwork
├── offer/                  # Pull-bundle for sibling projects (profiles, onboarding, seeds)
├── docs/                   # User guide, ADRs, specs, security, history
├── examples/               # Contributor brief template
├── packaging/              # Windows NSIS/WiX, Chocolatey, winget
├── plans/                  # Campaign ledgers (dated, session-type marked)
├── reference/              # JIT-only Python baseline (read, never import)
├── schemas/                # JSON schemas (task, receipt, result)
├── scripts/                # Public installer (scripts/install.sh curl entrypoint)
├── skills/                 # Vendored agent skills (g8s-supervisor charter + manifest)
├── spec/openspec/          # OpenSpec deltas (DELTA-01..22)
├── tools/                  # CI helper scripts (pre-push gates, release)
└── .github/workflows/      # CI/CD pipelines
```
<!-- structure:end -->

## License

Distributed under the **MIT License**. Copyright (c) 2026 TamLD. See [LICENSE](LICENSE) for details. Dual-licensed variants: [LICENSE-MIT](LICENSE-MIT), [LICENSE-APACHE-2.0](LICENSE-APACHE-2.0), [LICENSE-DISCLAIMER.md](LICENSE-DISCLAIMER.md).
