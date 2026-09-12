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
* [x] Achieve $\ge 140$ passing Go tests: 187 test functions green under dual-pass verification (CGO_ENABLED=0 full suite and CGO_ENABLED=1 race detector with zero reports).
* [x] Configure multi-OS automated build with GoReleaser. Config present since `eb5b14d`; snapshot smoke verified in T019 producing darwin/linux/windows amd64+arm64 archives (modernized v2 formats schema, commit `c73e0b1`).

---

## 4. Release Roadmap

### v0.2.0 — Orchestration MVP (Current Sprint)
**Target**: 2026-09-15  
**Scope**: DELTA-11 Concerns A, B, C (read-only) + DELTA-18 AIC/from-intent

| Component | Status | Notes |
|-----------|--------|-------|
| Supervisor-driven fix loop (Concern A) | ✅ Complete | T020: planner, enforcer, reviewer, rca, escalator |
| Receipt evolution (Concern B) | ✅ Complete | T021: Schema v3, SupervisorMeta, idempotent migration, nil-safe API |
| Meta-optimizer read-only (Concern C) | ✅ Complete | T022: Aggregate/StreamMetrics API, flag collision guard |
| AIC contract (`orchestrate-aic`) | ✅ Complete | DELTA-18 §18.1: `--pr` + `--intent` → JSON envelope |
| From-intent orchestration | ✅ Complete | DELTA-18 §18.2: `--from-intent` / `--from-file` → FanOut |
| Dual-pass gates (CGO=0 + CGO=1 -race) | ✅ Green | All 31 packages |
| GoReleaser cross-platform artifacts | ✅ Configured | darwin/linux/windows amd64+arm64 |

**Release artifacts**:
- `g8s_v0.2.0_darwin_amd64.tar.gz`
- `g8s_v0.2.0_darwin_arm64.tar.gz`
- `g8s_v0.2.0_linux_amd64.tar.gz`
- `g8s_v0.2.0_linux_arm64.tar.gz`
- `g8s_v0.2.0_windows_amd64.zip`
- `g8s_v0.2.0_windows_arm64.zip`

**Acceptance criteria**:
- [ ] `./bin/g8s orchestrate "self-test"` escalates at 9 attempts deterministically
- [ ] `./bin/g8s supervisor metrics --aggregate` returns 8 metrics on seeded data
- [ ] `./bin/g8s orchestrate-aic --pr 1 --intent "test"` emits JSON envelope
- [ ] No race detector warnings, no CGO dependencies

---

### v0.3.0 — Autopilot & Tuning (Next Sprint)
**Target**: 2026-10-15  
**Scope**: Autopilot scheduler + meta-optimizer write/tuning tranche

| Issue | Description | Priority |
|-------|-------------|----------|
| #1 | Autopilot scheduler: cron tick scanning GH issues, CI failures, lints | P0 |
| #2 | Meta-optimizer write tranche: `Optimizer.Propose()` applies tuned configs | P0 |
| #3 | `false_escalation_rate` feedback loop: user marks escalations | P1 |
| #4 | Priority queue weights tunable via config file | P1 |
| #5 | Daemon mode: long-lived supervisor process | P2 |

---

### v0.4.0 — Observability & Hardening (Future)
**Target**: 2026-11-15

| Issue | Description | Priority |
|-------|-------------|----------|
| #6 | Structured logging + OpenTelemetry integration | P1 |
| #7 | Prometheus metrics endpoint (`/metrics`) | P1 |
| #8 | Web UI for supervisor task dashboard | P2 |
| #9 | Windows service hardening (kardianos/service integration) | P2 |
| #10 | Receipt lake compaction / retention policies | P2 |

---

### v1.0.0 — GA Release
**Target**: 2026-12-15  
**Criteria**: All P0/P1 issues resolved, ≥6 months stable on ct122 homelab, documentation complete, migration guide from Python baseline published.
