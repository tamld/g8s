# g8s Refactoring Masterplan: Python Baseline to Pure-Go

> **Baseline Reference**: `reference/python/` (Extracted from `/Users/tamld/plugins/agy-dispatch`)  
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

### Phase 1: Core Harness & Receipt Gate (In Progress)
* [x] Copy Python baseline to `reference/python/`.
* [x] Port `harness` (Roles, Permissions, Blocked patterns, Denied paths).
* [ ] Port `receipt` Manager (SQLite WAL, atomic single-use consume, TTL validation, mock clock).
* [ ] Port 38 Receipt Delegation tests to Go (`internal/receipt/receipt_test.go`).

### Phase 2: Control Plane & Process Group Worker
* [ ] Implement pure-Go SQLite WAL Task Queue with atomic CAS leases and heartbeat renewals.
* [ ] Implement `worker.Supervisor` using `os/exec` with process group termination (`Setpgid`).
* [ ] Implement post-run mutation detector (`READ_ONLY_VIOLATION_PATTERNS`).
* [ ] Port ControlPlane and Worker tests (30 tests) to Go.

### Phase 3: Pluggable Providers & MCP Server
* [ ] Define `WorkerProvider` interface and implement `AgyProvider`, `ClaudeProvider`, `GeminiProvider`.
* [ ] Implement Stdio JSON-RPC 2.0 MCP server (`internal/mcp/server.go`).
* [ ] Port MCP server tests to Go.

### Phase 4: Cross-Platform OS Service & CLI Commands
* [ ] Integrate `kardianos/service` for macOS `launchd`, Linux `systemd`, and Windows Service.
* [ ] Complete CLI subcommands in `cmd/g8s/main.go` using `cobra`.

### Phase 5: Parity Verification & CI Pipeline
* [ ] Run side-by-side verification: Go `g8s` vs Python `reference/` on identical workloads.
* [ ] Achieve $\ge 140$ passing Go tests with `CGO_ENABLED=0 go test -race ./...`.
* [ ] Configure multi-OS automated build with GoReleaser.
