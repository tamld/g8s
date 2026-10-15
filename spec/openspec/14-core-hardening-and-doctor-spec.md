# OpenSpec DELTA-14: Core Hardening, Security Guards, Autopilot & HTTP Daemon

* **Specification ID**: `DELTA-14`
* **Title**: Core Hardening, Doctor Security Guards, Autonomous Autopilot & HTTP Daemon
* **Status**: `APPLIED`
* **Milestone**: `M4` (Resilience, Hardening & DX/AX)
* **Target Packages**: `internal/doctor`, `internal/dispatch`, `internal/vault`, `internal/autopilot`, `internal/server`
* **Tracking Issues**: #77, #78, #79, #80, #89, #98, #99, #102

---

## 1. Context & Motivation

Phase 2 of the `g8s` roadmap hardened the core runtime against allocation bottlenecks, SQLite connection-string injection in diagnostics, TDD anti-pattern traps, and added the autonomous background scheduler (`internal/autopilot`) and local REST/OpenAPI HTTP server (`internal/server`).

---

## 2. ADDED Requirements

### Requirement: Bounded Stream Capture & Code Symbol Normalizer Hardening (`internal/dispatch`, `internal/vault`)

1. `CaptureBounded` SHALL bound stream memory allocation to `MaxCaptureBytes` without unbounded intermediate slice growth, preserving head and tail slices joined by `\n<OUTPUT_TRUNCATED>\n`.
2. `TokenizeCodeSymbols` SHALL decompose `camelCase`, `PascalCase`, and `snake_case` identifiers into normalized search tokens in a single pass so SQLite FTS5 `unicode61` queries match sub-word code symbols.

#### Scenario: bounded capture and code symbol decomposition
<!-- tests: TestCaptureOversizeTruncatesWithMarker, TestTokenizeCodeSymbols, TestVaultSearchCodeSymbolDecomposition -->
- Oversized subprocess streams truncate deterministically with `<OUTPUT_TRUNCATED>` and FTS5 queries match decomposed camelCase/snake_case symbols.

### Requirement: Doctor Diagnostics, SQLite URI Escaping & TDD Pitfall Guard (`internal/doctor`, `internal/controlplane`)

1. `SqlitePathEscapeFor` and `doctor` database checks SHALL sanitize filesystem paths before constructing SQLite URI connection strings, preventing parameter injection via `?` or `#` characters in workspace paths.
2. `g8s doctor` SHALL verify SQLite database health, POSIX `0600` permissions (with `--fix` auto-remediation), stale binary drift against git `HEAD`, free disk space thresholds, and AST-based TDD pitfall checks.

#### Scenario: SQLite path escaping and doctor self-healing diagnostics
<!-- tests: TestSqlitePathEscapeFor, TestRunDiagnosticsHealthy, TestRunDiagnosticsWithAutoFixPermissions, TestCheckStaleBinary, TestCheckTDDPitfallsCleanRepo -->
- Paths with reserved URI characters are safely escaped, and `doctor` detects stale binaries, repairs database file permissions under `--fix`, and validates TDD test hygiene.

### Requirement: Autonomous Priority Queue & Retry Backoff Guard (`internal/autopilot`)

1. `internal/autopilot` SHALL maintain a deterministic priority queue (`PriorityQueue`) scoring work items by priority label, age, and retry penalty, and a background `Scheduler` scanning repository TODOs and GitHub issues.
2. Retry and resubmit flows SHALL enforce exponential backoff ladders, reject derived-key collisions, require fresh write receipts for `workspace_write` tasks, and block cyclic lineage amplification.

#### Scenario: priority queue ordering and retry safety guarantees
<!-- tests: TestPriorityQueue_Basic, TestCalculateScore, TestScheduler_StartStop, TestRedRC2_Guarantee1_DerivedKeyCollisionRefusedHonestly, TestRedRC2_Guarantee2_WorkspaceWrite_ReceiptOmissionAndTamper -->
- Work items dequeue in deterministic score order and retry/resubmit gates refuse key collisions or missing `workspace_write` receipts.

### Requirement: Local REST & OpenAPI HTTP Server (`internal/server`)

1. `internal/server` SHALL expose `/healthz`, `/readyz`, `/metrics`, `/api/v1/tasks`, `/api/v1/receipts`, `/api/v1/briefs`, `/api/v1/supervisor`, `/api/v1/fs/*`, and `/openapi.json` with optional bearer-token authentication and CORS middleware.

#### Scenario: HTTP server lifecycle, auth middleware, and REST endpoints
<!-- tests: TestServerLifecycle, TestAuthMiddleware, TestTasksEndpoints, TestReceiptsEndpoints, TestBriefsEndpoints -->
- The HTTP server validates configuration, enforces bearer token auth when configured, and serves task, receipt, and brief resources with typed JSON error envelopes.
