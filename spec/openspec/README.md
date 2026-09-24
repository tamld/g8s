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
| **[`DELTA-08`](08-dispatch-wrapper-spec.md)** | PTY & Streaming Diagnostics Engine | `internal/worker` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-09`](09-worker-supervisor-spec.md)** | Diagnostic Doctor Engine | `internal/doctor` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-10`](10-two-class-providers-spec.md)** | Worker Supervisor & Template Bridge | `internal/worker` | M2 (Capabilities) | `APPLIED` |
| **[`DELTA-11`](11-knowledge-vault-spec.md)** | Pure-Go Decoupled Knowledge Vault | `internal/vault` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-11-ROADMAP`](11-orchestration-roadmap-spec.md)** | Supervisor Orchestration & Task Graph Lifecycle | `internal/supervisor` | M5 (Orchestration) | `APPLIED` |
| **[`DELTA-12`](12-lineage-cte-and-stream-pipe-spec.md)** | Recursive CTE Lineage & Stream Pipe | `internal/controlplane` | M3 (OS Daemon) | `APPLIED` |
| **[`DELTA-13`](13-dx-ax-init-and-autorepair-spec.md)** | DX/AX Multi-IDE Init & Auto-Repair | `internal/initwiz` | M4 (DX & AX) | `APPLIED` |
| **[`DELTA-20`](20-code-intel-adapter-spec.md)** | SoC Code Intelligence & AST Navigation Adapter | `internal/codeintel` | M4 (DX & AX) | `APPLIED` |
| **[`DELTA-21`](21-unified-memory-adapter-spec.md)** | Unified Decoupled Memory Layer & Hybrid Retrieval Adapter | `internal/memory` | M5 (Robustness & Evals) | `ACCEPTED` |

---

## 📜 OpenSpec Lifecycle

```
PROPOSED ──► ACCEPTED ──► IMPLEMENTING ──► APPLIED (Passes -race tests) ──► RETIRED
```
