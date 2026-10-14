# g8s Refactoring Masterplan: Python Baseline to Pure-Go

> **Baseline Reference**: `reference/python/` (Extracted from `plugins/agy-dispatch`)  
> **Target Architecture**: Pure Go (Zero-CGO), Cross-Platform, Modular Engine  
> **Status**: Active Execution  

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

---

### v0.17.0 — Security DLP, Delivery Integrity, Telemetry & Effort Ergonomics (Completed 2026-10-14)
**Delivered**: 2026-10-14

| Issue / Item | Description | Priority | Status |
|--------------|-------------|----------|--------|
| [x] #586 | Mask/redact `KeyUsed` (`key_used`) in `internal/reflex` (`ReflexSignal` / `TriageVerdict`) so `g8s reflex triage --json` never emits plaintext `TYPESAFE_API_KEY` | P0 | ✅ Complete |
| [x] #588 | Fix `FindGhostProcesses` subshell ancestor PID skip and prune dead `.heartbeat/agy/*.json` files on `g8s cleanup --target ghost-process --force` | P0 | ✅ Complete |
| [x] #575 | Fix AGY `result.duration` and `result.duration_seconds` reporting wall-clock elapsed time + `provider_duration_seconds` and populate `usage` on timeout/error/cancel | P0 | ✅ Complete |
| [x] #569 | `g8s deliver` copy-apply v2: propagate worker-deleted files (`git rm` inside worktree) with receipt scope enforcement and `deleted` list in envelope | P0 | ✅ Complete |
| [x] #570 (R4-1, R4-2) | Validate `g8s ladder advance --out` path against scope/denied fragments (R4-1); emit typed JSON error envelope on unreadable `submit --prompt-file` (R4-2) | P1 | ✅ Complete |
| [x] #567 & #570 (R1-1) | Effort-class registry glob matching (`docs/**` nested + root files, slash-normalized `path.Match` for Windows R1-1), `--class` flag, and receipt `allowed_paths` fallback | P1 | ✅ Complete |
| [x] #566 | Provider-less manual submit baked-name resolution against stale manifests and `doctor` stale provider model ID diagnostic warning | P1 | ✅ Complete |
| [x] #570 (R3-2/4/5/8) | Quality Ladder continuity, monotonic rung order, exit-code-127 vs brief marker precedence, and duplicate task ID deduplication in evidence history | P1 | ✅ Complete |
| [x] #587 | ADR-0032 `F1->F2` recursive subagent containment self-audit probe (`sa-005` `f2-recursive-containment`) | P2 | ✅ Complete |

---

### v1.0.0 — GA Release
**Target**: 2026-12-15  
**Criteria**: All P0/P1 issues resolved, ≥6 months homelab stability, enterprise security signoff, distributed fleet mTLS, migration guide from Python baseline published.

