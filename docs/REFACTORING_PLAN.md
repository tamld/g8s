# g8s Refactoring Masterplan: Python Baseline to Pure-Go

> **Baseline Reference**: `reference/python/` (Extracted from `plugins/agy-dispatch`)  
> **Target Architecture**: Pure Go (Zero-CGO), Cross-Platform, Modular Engine  
> **Status**: Completed (Baseline Ported in `v0.1.0`; Superseded by `DELTA-01..33` Pure-Go Engine)  

---

## 1. Objectives & Architectural Upgrades

| Dimension | Python Prototype (`reference/python/`) | Pure-Go Architecture (`g8s/internal/`) | Upgrade Benefit |
| :--- | :--- | :--- | :--- |
| **Runtime & Dependencies** | Requires Python 3.10+, SQLite C-bindings, virtualenv | Single 15MB Static Binary (Zero-CGO via `modernc.org/sqlite`) | Zero host setup required, starts in < 5ms. |
| **Worker CLI Coupling** | Hardcoded to `agy` CLI (`agy_dispatch.py`) | Abstracted via `WorkerProvider` Interface | Supports multi-CLI: `agy`, `claude`, `gemini`, `ollama`. |
| **Concurrency & Queuing** | Single-threaded file-locked polling | Goroutines, Channels, Context, SQLite WAL | High throughput, zero deadlocks, minimal CPU usage. |
| **Process Group Kill** | Unix-only `os.killpg` (no native Windows support) | Cross-platform `syscall.SysProcAttr` (Setpgid / JobObjects) | Eliminates orphaned zombie processes on all OSes. |
| **OS Service Daemon** | macOS `LaunchAgent` only (`agy_service.py`) | Cross-platform library (`kardianos/service`) | Runs 24/7 on macOS (`launchd`), Linux (`systemd`), and Windows. |
| **Test Suite** | 140 Pytest/Unittest cases | 100% Go Table-Driven Tests + Race Detector (`-race`) | Strict typing, compiler-level race detection. |

---

## 2. 1-to-1 Module Porting Matrix

```
reference/python/                                     g8s/ (Pure Go)
├── scripts/agy_harness.py        ──────────────►     internal/harness/
│   ├── RoleProfile                                   ├── roles.go
│   ├── PermissionProfile                             ├── permissions.go
│   ├── validate_dispatch()                           ├── harness.go
│   └── build_contract_prompt()                       └── harness_test.go
│
├── scripts/agy_control_plane.py   ──────────────►     internal/
│   ├── write_receipts methods   ──────────────►     ├── receipt/
│   │   ├── issue_write_receipt()                     │   ├── receipt.go
│   │   ├── validate_write_receipt()                  │   └── receipt_test.go
│   │   └── revoke_write_receipt()                    │
│   │                                                 │
│   └── tasks & events queue     ──────────────►     └── controlplane/
│       ├── submit_task() / claim_task()              ├── sqlite.go
│       ├── CAS lease heartbeats                      ├── queue.go
│       └── prompt SHA-256 redaction                  └── state_test.go
│
├── scripts/agy_dispatch.py       ──────────────►     internal/
│   ├── resolve_agy_binary()     ──────────────►     ├── provider/ (agy, claude, gemini)
│   ├── detect_violations()      ──────────────►     └── worker/
│   └── execute_process()                             ├── runner.go
│                                                     └── supervisor.go
│
├── scripts/agy_collect.py        ──────────────►     internal/collector/
│   └── Multi-scope batch walker                      └── collector.go
│
├── scripts/agy_service.py        ──────────────►     internal/service/
│   └── launchd / systemd / svc                       └── service.go
│
├── scripts/agy_mcp_server.py     ──────────────►     internal/mcp/
│   └── JSON-RPC 2.0 stdio server                     └── server.go
│
└── scripts/test_*.py (140 tests) ──────────────►     internal/*/*_test.go (140+ Go tests)
```

---

## 3. 5-Phase Execution Plan

### Phase 1: Core Harness & Receipt Gate (Complete)
* [x] Copy Python baseline to `reference/python/`.
* [x] Port `harness` (Roles, Permissions, Blocked patterns, Denied paths).
* [x] Port `receipt` Manager (SQLite WAL, atomic single-use consume, TTL validation, mock clock). Done in Milestone 1 (`internal/receipt`, commit `1a2a561`).
* [x] Port Receipt Delegation tests to Go (`internal/receipt/receipt_test.go`). 41 tests including the T018 safety-coordination hardening suite (commit `08007c1`).

### Phase 2: Control Plane & Process Group Worker
* [x] Implement pure-Go SQLite WAL Task Queue with atomic CAS leases and heartbeat renewals. DELTA-03 control plane (commit `a652675`).
* [x] Implement `worker.Supervisor` using `os/exec` with process group termination (`Setpgid`). DELTA-09 supervisor (commit `fedee74`).
* [x] Implement post-run mutation detector (`DetectReadOnlyContractViolations` in `internal/dispatch`). DELTA-08 wrapper (commit `184d85d`).
* [x] Port ControlPlane and Worker tests to Go: 32 controlplane + 14 worker tests.

### Phase 3: Pluggable Providers & MCP Server
* [x] Define `WorkerProvider` interface and implement Agy/Claude/Ollama providers. DELTA-05 registry (commit `cd31d1e`).
* [x] Implement Stdio JSON-RPC 2.0 MCP server (`internal/mcp/server.go`). DELTA-04 (commit `203afe8`) plus Amendment A surface expansion to eleven `g8s_*` tools (commit `6fb3b0d`).
* [x] Port MCP server tests to Go: 28 tests including guard-chain and durable round-trip coverage.

### Phase 4: Cross-Platform OS Service & CLI Commands
* [~] Integrate a cross-platform service layer for macOS `launchd`, Linux `systemd`, and Windows Service. Shipped as native stdlib-only launchd manager (DELTA-06A, commit `37e13c6`); kardianos integration deferred post-MVP per the recorded platform decision.
* [~] Complete CLI subcommands in `cmd/g8s/main.go`. Stdlib flag-based subcommands shipped (commit `1f98a08`: mcp/submit/get/receipt-issue); cobra migration deferred post-MVP.

### Phase 5: Parity Verification & CI Pipeline
* [x] Run side-by-side verification: Go `g8s` vs Python `reference/` on identical workloads. Executed 2026-08-25 against the v0.1.0 candidate — all five parity-matrix rows pass or are documented deviations (see docs/RELEASE_READINESS.md).
* [x] Achieve $\ge 140$ passing Go tests: current state (2026-09-25) is 38 packages / 820+ test functions green under dual-pass verification (CGO_ENABLED=0 full suite and CGO_ENABLED=1 race detector with zero reports).
* [x] Configure multi-OS automated build with GoReleaser. Config present since `eb5b14d`; snapshot smoke verified in T019 producing darwin/linux/windows amd64+arm64 archives (modernized v2 formats schema, commit `c73e0b1`).

---

## 4. Release Roadmap

### v0.2.0 — Orchestration MVP (Completed 2026-09-15)
**Delivered**: 2026-09-15  
**Scope**: DELTA-11 Concerns A, B, C (read-only) + DELTA-18 AIC/from-intent

| Component | Status | Notes |
|-----------|--------|-------|
| Supervisor-driven fix loop (Concern A) | ✅ Complete | T020: planner, enforcer, reviewer, rca, escalator |
| Receipt evolution (Concern B) | ✅ Complete | T021: Schema v3, SupervisorMeta, idempotent migration, nil-safe API |
| Meta-optimizer read-only (Concern C) | ✅ Complete | T022: Aggregate/StreamMetrics API, flag collision guard |
| AIC contract (`orchestrate-aic`) | ✅ Complete | DELTA-18 §18.1: `--pr` + `--intent` → JSON envelope |
| From-intent orchestration | ✅ Complete | DELTA-18 §18.2: `--from-intent` / `--from-file` → FanOut |
| Dual-pass gates (CGO=0 + CGO=1 -race) | ✅ Green | All 38 packages |
| GoReleaser cross-platform artifacts | ✅ Configured | darwin/linux/windows amd64+arm64 |

**Acceptance criteria**:
- [x] `./bin/g8s orchestrate "self-test"` escalates at 9 attempts deterministically
- [x] `./bin/g8s supervisor-metrics --aggregate` returns 8 metrics on seeded data
- [x] `./bin/g8s orchestrate-aic --pr 1 --intent "test"` emits JSON envelope
- [x] No race detector warnings, no CGO dependencies

---

### v0.3.0 — Autopilot & Daemon (Completed 2026-09-18)
**Delivered**: 2026-09-18  
**Scope**: Autopilot scheduler + meta-optimizer write/tuning + daemon mode + HTTP API

| Feature | Status | Notes |
|---------|--------|-------|
| Autopilot scheduler daemon | ✅ Complete | Cron tick scanning, CLI commands (`g8s autopilot`) |
| Meta-optimizer tuning & feedback | ✅ Complete | `g8s supervisor-metrics-update-false`, feedback loop |
| Daemon mode (`g8s serve`) | ✅ Complete | Long-lived process + HTTP API REST endpoints |
| HTTP API & metrics | ✅ Complete | `/api/v1/tasks`, `/api/v1/receipts`, `/metrics` |

---

### v0.10.0 — Jev AI Reflex, DiffDistiller & Governance (Completed 2026-09-20)
**Delivered**: 2026-09-20  
**Scope**: System 1 Reflex sensor decoupling, DiffDistiller pipeline, and governance hardening

| Feature | Status | Notes |
|---------|--------|-------|
| Jev AI Reflex Sensor Decoupling | ✅ Complete | `internal/reflex` System 1 sensor decoupled from System 2 |
| Native DiffDistiller & Verifier | ✅ Complete | `internal/diffintel`, `internal/review` token pruning & reviewer |
| Supervisor Coverage Hardening | ✅ Complete | Package coverage lifted to $\ge 93.6\%$, repo aggregate $\ge 84.1\%$ |
| MCP Expanded Surface | ✅ Complete | 11 registered MCP tools |

---

### Historical releases v0.4.0 → v0.9.2 (Completed)
All shipped and published on GitHub Releases (verified 2026-09-25 by roadmap-truth audit, issue #332):

| Version | Highlight |
|---------|-----------|
| v0.4.0 (2026-08-30) | Observability & hardening baseline |
| v0.5.0 → v0.6.1 | Incremental hardening releases |
| v0.7.0 | DELTA-20 Multi-Tier Code Intelligence Adapter (`internal/codeintel`) |
| v0.8.0 | Windows service backend via sc.exe wrapper (DEBT-44, PR #174) |
| v0.9.0 → v0.9.2 | Knowledge vault, DX/AX wizard, governance polish |

---

### Historical releases v0.11.0 → v0.16.0 (Completed)
All shipped, tagged, and verified on GitHub Releases:

| Version | Highlight |
|---------|-----------|
| v0.11.0 | Unified Decoupled Memory Facade (DELTA-21), delegated-write E2E jail, closed-loop trace telemetry |
| v0.12.0 (2026-09-27) | Concurrent dispatch (`--concurrency N`), session isolation, memory promotion gate, Context Broker, live eval |
| v0.13.0 (2026-09-30) | Multi-provider queue affinity, hardening waves B–E, event-driven signals (`signals/tasks.jsonl` + `watch --failed`) |
| v0.14.0 (2026-10-03) | AI-factory stage 1: budgeted auto-retry (ADR-0029), context router (ADR-0030), gate-lane router (ADR-0024), verifier registry (#515), unattended closed round (#534), F0/F1/F2 containment vocabulary (ADR-0032) |
| v0.15.0 (2026-10-05) | Lessons pipeline (`g8s lesson create|verify|list`), agent-models catalog v1 (`.g8s/agent-models.yml`), live Jev SystemOne benchmark (#543), claims-integrity gate (50 claims) |
| v0.16.0 (2026-10-07) | Provider-neutral effort optimization (#550/#556/#557), effort-class registry (#558), signal-based effort demotion (#564), Quality Ladder CLI & diagnosis dispatch (#561/#563), baked-name realignment (#568) |
| v0.17.0 (2026-10-16) | Native Pure-Go Agent I/O (DELTA-23: search, read, edit, --select), F0/F1/F2 recursive containment (ADR-0032), Offer Bundle Wave 1–4 rollout (#442, #569), OpenSpec DELTA-01..23 & 50/50 bound claims |
| v0.18.0 (2026-10-21) | F0 Verified Delegation & Symmetric Zero-Trust Control Plane ([ADR-0033](decisions/0033-f0-verified-delegation.md), Epic #620, #621–#630): append-only evidence ledger, signed operator mandates (T0–T4), commit/merge gates G0–G7, agent hooks, circuit breaker, lossless verify firewall |
| v0.19.0 (2026-10-21) | Universal Config-Driven LLM Call Consumer & Multi-Model Gateway ([ADR-0034](decisions/0034-config-driven-llm-consumer-9router.md), DELTA-25): direct CLI invocation (g8s llm-call), queue bridge via g8s llm-call --worker-envelope, live model discovery g8s providers discover, standardized SWE combos (reasoning-free, reasoning-high, deep-coding, fast-agent, docs-retriever), 16-tool MCP server, single MIT license, and F0 Cognitive Purity |
| v0.20.0 (Planned — Issue #639) | Test Suite Execution Optimization & Empirical PRI Lane Routing (>=90% first-attempt pass), Industry Security Standards Alignment (OWASP Top 10 for LLM/Agentic Apps 2025, SLSA Build Provenance, CycloneDX/SPDX SBOM), Cross-Platform Packaging Hardening & Internal Package Consolidation |
| v0.21.0+ (Planned — Pre-1.0 Burn-In) | 90-day Multi-Repo Offer Retention (SCORECARD.md M4 across 5 repositories with 0 gate regressions) and formal pre-1.0 API freeze criteria (v1.0.0 is gated on empirical burn-in evidence, not calendar dates) |

---

### v0.17.0 — Native Cross-Platform Agent I/O, Offer Bundle Rollout, Security DLP & SDD/DDD/TDD Standardization (Completed 2026-10-16)
**Delivered**: 2026-10-16

| Issue / Item | Description | Priority | Status |
|--------------|-------------|----------|--------|
| [x] #593 | Native Cross-Platform Agent I/O (`DELTA-23`): `internal/fsops` (`g8s read`, `g8s edit`, `g8s select`, `--select`) across CLI, MCP (`g8s_search`/`g8s_read`/`g8s_edit`), and HTTP (`/api/v1/fs/*`) with F0/F1/F2 `WriteReceipt` enforcement | P0 | ✅ Complete |
| [x] #591 | Native Pure-Go `internal/search` & `g8s search` CLI (`rg`/`grep`/`find` replacement) with `--receipt-id` scope gate and `doctor` probe | P0 | ✅ Complete |
| [x] #590 | Preserve unquoted Python credential-parser call and subscript expressions in `internal/sanitizer` / `internal/dispatch` | P0 | ✅ Complete |
| [x] #574 | Fix SQLite connection string (DSN) parameter injection via URI path escaping | P0 | ✅ Complete |
| [x] #586 | Mask/redact `KeyUsed` (`key_used`) in `internal/reflex` (`ReflexSignal` / `TriageVerdict`) so `g8s reflex triage --json` never emits plaintext `TYPESAFE_API_KEY` | P0 | ✅ Complete |
| [x] #588 | Fix `FindGhostProcesses` subshell ancestor PID skip and prune dead `.heartbeat/agy/*.json` files on `g8s cleanup --target ghost-process --force` | P0 | ✅ Complete |
| [x] #575 | Fix AGY `result.duration` and `result.duration_seconds` reporting wall-clock elapsed time + `provider_duration_seconds` and populate `usage` on timeout/error/cancel | P0 | ✅ Complete |
| [x] #569 | `g8s deliver` copy-apply v2: propagate worker-deleted files (`git rm` inside worktree), cross-repo delivery, `--worktree`, and `--prune-worktree` | P0 | ✅ Complete |
| [x] #442 | Complete Offer Bundle Wave 1–4 rollout (`tamld-llm-wiki`, `defense-in-depth`, `aegis`, `homelab-proxmox`, `tuneflow`) and merge 13 Mode-3 return packets | P1 | ✅ Complete |
| [x] #570 (R4-1, R4-2) | Validate `g8s ladder advance --out` path against scope/denied fragments (R4-1); emit typed JSON error envelope on unreadable `submit --prompt-file` (R4-2) | P1 | ✅ Complete |
| [x] #567 & #570 (R1-1) | Effort-class registry glob matching (`docs/**` nested + root files, slash-normalized `path.Match` for Windows R1-1), `--class` flag, and receipt `allowed_paths` fallback | P1 | ✅ Complete |
| [x] #566 | Provider-less manual submit baked-name resolution against stale manifests and `doctor` stale provider model ID diagnostic warning | P1 | ✅ Complete |
| [x] #570 (R3-2/4/5/8) | Quality Ladder continuity, monotonic rung order, exit-code-127 vs brief marker precedence, and duplicate task ID deduplication in evidence history | P1 | ✅ Complete |
| [x] #587 | ADR-0032 `F1->F2` recursive subagent containment self-audit probe (`sa-005` `f2-recursive-containment`) | P2 | ✅ Complete |
| [x] SDD/DDD/TDD | Complete `DELTA-01..23` OpenSpec registry (0 grandfathered warnings), bind 50/50 claims in `docs/claims.yml`, segregate `controlplane.Store` domain interfaces, bridge `codeintel`↔`analyzer`, and unskip all bug tests | P1 | ✅ Complete |

---

### v0.18.0 — F0 Verified Delegation, Zero-Trust Control Plane & Evidence-Gated Autonomy (Completed 2026-10-21)
**Delivered**: 2026-10-21

| Issue / Item | Description | Priority | Status |
|--------------|-------------|----------|--------|
| [x] #620 | F0 Verified Delegation & Symmetric Zero-Trust Control Plane ([ADR-0033](decisions/0033-f0-verified-delegation.md), `DELTA-24`): evidence-gated autonomy instead of synchronous HITL | P0 | ✅ Complete |
| [x] #621 | ADR-0033 Ratification & supervisor skill alignment (`skills/g8s-supervisor/SKILL.md`) | P0 | ✅ Complete |
| [x] #622 | Append-only hash-chained evidence ledger (per-repo + global, anchored) + `f0 run` + G5 vacuous-pass guard | P0 | ✅ Complete |
| [x] #623 | Signed campaign operator mandates (`f0 mandate issue|show|check`) with action tier classification (`T0`–`T4`) | P0 | ✅ Complete |
| [x] #624 | Merge/commit gates `G0`–`G6` (`f0 gate check`), merger Gate e, and agent hooks (`f0 hook pre-tool|stop`) | P0 | ✅ Complete |
| [x] #625 | Gate-appeasement detector `G4` (token rewrites, unread YAML config keys, test-skip evasions) | P1 | ✅ Complete |
| [x] #626 | Per-criterion issue-closure gate `G7` (`Closes #NNN` verified against acceptance criteria and evidence) | P1 | ✅ Complete |
| [x] #627 | Autonomous `T2` merge executor with post-merge verification and automated rollback | P1 | ✅ Complete |
| [x] #628 | `T3` independent worktree second verifier with disparate model validation | P1 | ✅ Complete |
| [x] #629 | Error-budget circuit breaker (`f0 breaker`) with automatic tier demotion and clean-streak restoration | P1 | ✅ Complete |
| [x] #630 | Pluggable read-only auditor, session digest (`f0 digest`), and Jev SystemOne advisory integration | P1 | ✅ Complete |
| [x] #631 | Supervisor skill update: tier classification, `f0` toolkit integration, and C1–C10 empirical operational lessons | P1 | ✅ Complete |
| [x] Slice 06b-1 | Lossless-Signal Information Firewall: worktree-targeted `g8s verify`, failure log persistence, and `g8s watch --pr` repo fix | P0 | ✅ Complete |
| [x] Slice 06b-2 | Portable Ground Truth (`<!-- f0:bundle:v1 -->`), `TreeSHA` equivalence, declarative `.g8s/f0.yml` parsers, and G6 claim linter precision | P0 | ✅ Complete |
| [x] #615 | Unicode & Vietnamese diacritic hotfix/refactor detection in `internal/lane` | P1 | ✅ Complete |
| [x] #616 | Runtime `--tool-policy none` and `supplied_text` permission profile boundary | P1 | ✅ Complete |
| [x] #617 | Worker pool worktree and temporary branch cleanup on pool termination | P1 | ✅ Complete |
| [x] #632 & #633 | Telemetry preflight eligibility filter (`occurrences >= 2`) and pattern lowercasing | P1 | ✅ Complete |

---

### v0.19.0 — Universal Config-Driven LLM Consumer, 9Router Multi-Model Fleet & F0 Cognitive Purity (Completed 2026-10-21)
**Delivered**: 2026-10-21

| Issue / Item | Description | Priority | Status |
|--------------|-------------|----------|--------|
| [x] DELTA-25 / ADR-0034 | Universal Config-Driven LLM Consumer & Multi-Protocol Gateway: multi-protocol `api_call` (`openai`, `anthropic`, `9router`), direct CLI `g8s llm-call`, worker queue bridge via `g8s llm-call --worker-envelope`, live model discovery `g8s providers discover` | P0 | ✅ Complete |
| [x] 9Router SWE Fleet | Standardized SWE combo catalog (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`) and prefix wildcard matching (`nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`) in `.g8s/agent-models.yml` and `internal/provider/catalog.go` | P0 | ✅ Complete |
| [x] F0 Cognitive Purity | Brain vs Hands division: F0 consumes structured `RoundReceiptSchema` and gate verdicts while delegating raw log hunting and janitor operations to F1 workers (`agy` and `9router` combos); updated supervisor skills and charters | P0 | ✅ Complete |
| [x] #641 | Repository Security Hardening & Mode-3 Returns: Resolved all 15 GitHub Code Scanning alerts (`go/path-injection`, `actions/unpinned-tag`, `actions/missing-workflow-permissions`), upgraded Go toolchain floor to `1.26.10` (`GO-2026-6809`), added `--context-files` file context hydration (Mode-3 #16), and unified `.g8s/*.yml` `schema_version` across Offer Bundle profile seeds (Mode-3 #15, #17) | P0 | ✅ Complete |

---

### v0.20.0 — Test Suite Execution Optimization, PRI Lane Routing & Industry Security Alignment (Planned — Issue #639)
**Scope**:
- Test suite execution optimization and empirical PRI lane routing (`>=90%` first-attempt pass).
- Industry security standards alignment (OWASP Top 10 for LLM/Agentic Apps 2025, SLSA Build Provenance, CycloneDX/SPDX SBOM generation).
- Cross-platform packaging hardening & internal package consolidation.

---

### v0.21.0+ — Multi-Repo Offer Retention & Pre-1.0 Burn-In (Planned)
**Scope**:
- 90-day multi-repo offer retention (`SCORECARD.md` M4 across 5 repositories with 0 gate regressions).
- Formal pre-1.0 API freeze criteria (`v1.0.0` is gated on empirical burn-in evidence, not calendar dates).

