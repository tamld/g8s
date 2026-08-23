# OpenSpec Registry for g8s

This directory contains the **Technical Delta Specifications (OpenSpec)** governing all incremental modifications, schema additions, and engine enhancements for `g8s`.

---

## 📋 OpenSpec Index

| Spec ID | Title | Target Package | Milestone | Status |
| :--- | :--- | :--- | :--- | :--- |
| **[`DELTA-01`](01-core-harness-spec.md)** | Core Role & Permission Harness | `internal/harness` | M1 (Foundation) | `APPLIED` |
| **[`DELTA-02`](02-receipt-delegation-spec.md)** | Write Receipt Delegation Engine | `internal/receipt` | M1 (Foundation) | `PROPOSED` |
| **[`DELTA-03`](03-controlplane-sqlite-spec.md)** | SQLite WAL Control Plane & Leases | `internal/controlplane` | M1 (Foundation) | `PROPOSED` |
| **[`DELTA-04`](04-mcp-stdio-server-spec.md)** | Stdio JSON-RPC 2.0 MCP Server | `internal/mcp` | M2 (Capabilities) | `PROPOSED` |
| **[`DELTA-05`](05-provider-and-resource-pool-spec.md)**| Provider Registry & Resource Pool Governor | `internal/provider` | M2 (Capabilities) | `PROPOSED` |
| **[`DELTA-06`](06-os-daemon-service-spec.md)** | Cross-Platform OS Daemon Service | `internal/service` | M3 (OS Daemon) | `PROPOSED` |
| **[`DELTA-07`](07-blast-radius-analyzer-spec.md)** | LSP & Dependency Blast Radius Analyzer | `internal/analyzer` | M2 (Capabilities) | `PROPOSED` |

---

## 📜 OpenSpec Lifecycle

```
PROPOSED ──► ACCEPTED ──► IMPLEMENTING ──► APPLIED (Passes -race tests) ──► RETIRED
```
