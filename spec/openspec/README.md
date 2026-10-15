# OpenSpec Registry for g8s

This directory contains the **Technical Delta Specifications (OpenSpec)** governing all incremental modifications, schema additions, and engine enhancements for `g8s`.

---

## 📋 OpenSpec Index

| Spec ID | Title | Target Package | Milestone | Status |
| :--- | :--- | :--- | :--- | :--- |
| **[`DELTA-01`](01-core-harness-spec.md)** | Core Role & Permission Harness | `internal/harness` | M1 (Foundation) | `APPLIED` |
| **[`DELTA-02`](02-receipt-delegation-spec.md)** | Write Receipt Delegation Engine | `internal/receipt` | M1 (Foundation) | `APPLIED` |
| **[`DELTA-03`](03-controlplane-sqlite-spec.md)** | SQLite WAL Control Plane & Leases | `internal/controlplane` | M1 (Foundation) | `APPLIED` |
| **[`DELTA-04`](04-mcp-stdio-server-spec.md)** | Stdio JSON-RPC 2.0 MCP Server | `internal/mcp` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-05`](05-provider-and-resource-pool-spec.md)**| Provider Registry & Resource Pool Governor | `internal/provider` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-06`](06-os-daemon-service-spec.md)** | Cross-Platform OS Daemon Service | `internal/service` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-07`](07-blast-radius-analyzer-spec.md)** | LSP & Dependency Blast Radius Analyzer | `internal/analyzer` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-08`](08-dispatch-wrapper-spec.md)** | Bounded AGY CLI Dispatch Wrapper | `internal/dispatch` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-09`](09-worker-supervisor-spec.md)** | Worker Process Supervisor & Sandbox | `internal/worker` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-10`](10-two-class-providers-spec.md)** | Two-Class Providers Architecture (API vs Dispatch) | `internal/provider` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-11`](11-knowledge-vault-spec.md)** | Pure-Go Decoupled Knowledge Vault | `internal/vault` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-11-ROADMAP`](11-orchestration-roadmap-spec.md)** | Supervisor Fix Loop & Orchestration Roadmap | `internal/supervisor` | M4 (DX & AX) | `APPLIED` |
| **[`DELTA-12`](12-lineage-cte-and-stream-pipe-spec.md)** | Recursive CTE Lineage & Stream Pipe | `internal/controlplane` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-13`](13-dx-ax-init-and-autorepair-spec.md)** | DX/AX Multi-IDE Init & Auto-Repair | `internal/initwiz` | M4 (DX & AX) | `APPLIED` |
| **[`DELTA-14`](14-core-hardening-and-doctor-spec.md)** | Core Hardening, Doctor Security Guards, Autopilot & HTTP Daemon | `internal/doctor`, `internal/autopilot`, `internal/server` | M4 (Hardening & DX/AX) | `APPLIED` |
| **[`DELTA-15`](15-orchestrator-fsm-and-brief-spec.md)** | Orchestrator 8-State FSM Lifecycle & Brief Dispatch Store | `internal/orchestrator`, `internal/state`, `internal/brief` | M4 (Orchestrator FSM) | `APPLIED` |
| **[`DELTA-16`](16-lifecycle-hygiene-and-envelope-spec.md)** | Lifecycle Hygiene, Worker Heartbeats, Process Lister & Unified JSON Envelope v1 | `internal/heartbeat`, `internal/process`, `internal/cleanup`, `internal/cli` | M4 (Lifecycle Hygiene) | `APPLIED` |
| **[`DELTA-17`](17-receipt-and-evidence-lake-spec.md)** | Receipt Lake Control-Plane Wiring & Closed-Loop Telemetry Ledger | `internal/controlplane`, `internal/telemetry` | M4 (Evidence Lake) | `APPLIED` |
| **[`DELTA-18`](18-aic-and-diffintel-review-spec.md)** | AIC Automated PR Review, DiffDistiller & From-Intent FanOut | `cmd/g8s`, `internal/diffintel`, `internal/review` | M4 (AIC & Intent) | `APPLIED` |
| **[`DELTA-19`](19-lego-mounts-and-reflex-governance-spec.md)** | Stateful Worker Lego Mounts, System-1 Reflex & Governance Registries | `internal/orchestrator`, `internal/reflex`, `internal/routing`, `internal/verifier` | M5 (Lego Mounts & Reflex) | `APPLIED` |
| **[`DELTA-20`](20-code-intel-adapter-spec.md)** | Multi-Tier Code Intelligence Adapter & Dynamic Blast Radius | `internal/codeintel` | M5 (Robustness & Evals) | `APPLIED` |
| **[`DELTA-21`](21-unified-memory-adapter-spec.md)** | Unified Decoupled Memory Layer & Hybrid Retrieval Adapter | `internal/memory` | M5 (Robustness & Evals) | `APPLIED` |
| **[`DELTA-22`](22-brief-dor-floor-spec.md)** | Brief DoR Floor + Advisory Skill Routing | `internal/brief` | M6 (Memory & Routing) | `APPLIED` |
| **[`DELTA-23`](23-native-agent-io-spec.md)** | Native Cross-Platform Agent I/O (`search`, `read`, `edit`, `--select`) | `internal/fsops`, `internal/search` | M6 (Agent Ergonomics) | `APPLIED` |

---

## 📜 OpenSpec Lifecycle

```
PROPOSED ──► ACCEPTED ──► IMPLEMENTING ──► APPLIED (Passes -race tests) ──► RETIRED
```
