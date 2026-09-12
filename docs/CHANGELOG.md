# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased] — v0.3.0 Development

### Added
- **Autopilot scheduler** (Issue #1): Cron-based supervisor trigger scanning GitHub issues (`agy-fix`, `good-first-issue` labels), failing CI runs, static analysis findings, and stale `@agy-fix-me` TODOs. Priority queue: severity × confidence × cost-inverse.
- **Meta-optimizer write tranche** (Issue #2): `Optimizer.Propose(currentConfig, metrics)` applies tuned iteration caps, RCA confidence thresholds, and envelope selection weights. Separated from read-only ingestion (v0.2.0).
- **False escalation feedback** (Issue #3): CLI flag to mark escalations as "should have worked" → updates `false_escalation_rate` metric for optimizer learning.
- **Configurable priority weights** (Issue #4): YAML config for autopilot priority queue weights (severity, confidence, cost-inverse).

### Changed
- **Supervisor daemon mode** (Issue #5): Long-lived `g8s serve` process with graceful shutdown, replacing per-invocation CLI topology.

### Fixed
- None yet.

---

## [0.2.0] — 2026-09-15

### Added
- **DELTA-11 Concern A — Supervisor-driven fix loop** (`internal/supervisor/`):
  - Planner: envelope selection (SRS/PRD/DoR/DoD/DnD/Validateds/FSM) by task complexity
  - Enforcer: DoR/DoD gating with cycle-level `DnD` definition
  - Reviewer: receipt inspection (OK, ScopeViolations, Validateds) — scope violation always fails
  - RCA: structured failure analysis with confidence scoring (0.6 threshold → NEEDS_INFO pause)
  - Escalator: HITL JSON digest after 3×3=9 attempts exhausted
- **DELTA-11 Concern B — Receipt evolution** (`internal/receipt/`):
  - Schema v3: 15 columns (4 supervisor metadata + 11 provenance/replay)
  - SupervisorMeta: `approach_idx`, `attempt_idx`, `rca_confidence`, `adr_path`
  - Idempotent migration via `PRAGMA table_info` + `ALTER TABLE ADD COLUMN`
  - Nil-safe `WithSupervisorMeta` option — legacy callers unmodified
  - NULL columns → `SupervisorMeta == nil` (not zero, not error)
- **DELTA-11 Concern C (read-only) — Meta-optimizer ingestion** (`internal/supervisor/optimizer.go`):
  - `Aggregate()` — 8 metrics: total_runs, first_attempt_success_rate, avg_attempts_to_success, avg_approaches_to_success, rca_confidence_avg, escalation_rate, avg_cycle_duration_seconds, false_escalation_rate
  - `StreamMetrics()` — incremental per-task streaming
  - CLI: `g8s supervisor metrics --aggregate`, `--json-stream`, `--task-id`, `--time-range`, `--worker-name`
  - Flag collision guard: `--task-id` + `--aggregate` / `--json-stream` → usage error (exit 2)
  - Read-only assertion: row-count snapshots of 6 tables before/after
- **DELTA-18 — AIC & From-Intent Orchestration**:
  - `g8s orchestrate-aic --pr <num> --intent <text>` — fetches PR diff, delegates to `--from-intent`
  - `g8s orchestrate --from-intent <text>` / `--from-file <path>` — comma/newline split → FanOut sub-tasks
  - JSON envelope output with `supervisor_task_id`, `outcome`, `sub_tasks`, `receipt_summary`

### Changed
- **Dual-pass CI gates mandatory**: CGO_ENABLED=0 (full suite) + CGO_ENABLED=1 -race (all packages green)
- **GoReleaser v2**: Cross-platform artifacts (darwin/linux/windows × amd64/arm64)
- **ADR-0001, ADR-0002, ADR-0003**: Architecture decisions for supervisor, receipt evolution, meta-optimizer

### Fixed
- Race detector clean across all 31 packages
- Zero CGO dependencies (`modernc.org/sqlite` only)
- Receipt backward compatibility: v1 schema receipts still verify

### Security
- Database permissions restricted to 0600 on creation
- SQLite WAL with `synchronous=FULL`, `foreign_keys=ON`, `busy_timeout=30s`

---

## [0.1.0] — 2026-08-25

### Added
- **Pure-Go port of Python baseline** (`reference/python/` → `g8s/internal/`):
  - `internal/harness/`: Roles, Permissions, contract validation, prompt building
  - `internal/receipt/`: SQLite WAL receipt manager (issue, validate, revoke, list, TTL)
  - `internal/controlplane/`: Task queue with atomic CAS leases, heartbeat renewal, event log
  - `internal/orchestrator/`: Process group worker supervisor (`FanOut`, scope violation detection)
  - `internal/provider/`: Pluggable WorkerProvider (Agy, Claude, Ollama, Gemini)
  - `internal/mcp/`: Stdio JSON-RPC 2.0 server (11 `g8s_*` tools)
  - `internal/collector/`: Multi-scope batch file walker
  - `internal/service/`: Cross-platform service manager (launchd native, systemd/Windows deferred)
- **CLI commands** (`cmd/g8s/`): `submit`, `get`, `mcp`, `receipt-issue`, `cleanup`, `status`, `doctor`
- **Test suite**: 187 Go table-driven tests (dual-pass: CGO=0 + CGO=1 -race)
- **GoReleaser config**: Multi-OS automated build pipeline

### Changed
- **Architecture**: Zero-CGO single binary (15MB) replacing Python + virtualenv + SQLite C-bindings
- **Worker abstraction**: `WorkerProvider` interface decouples supervisor from specific CLIs
- **Concurrency**: Goroutines + channels + SQLite WAL replacing file-locked polling

### Security
- Idempotency keys with request hash deduplication
- Lease tokens with expiry and owner validation
- Prompt SHA-256 redaction on task completion

---

## Issue/PR Backlog

### v0.3.0 — Autopilot & Tuning (Target: 2026-10-15)

| ID | Title | Type | Priority | Labels | Assignee |
|----|-------|------|----------|--------|----------|
| #1 | Autopilot scheduler: cron tick + priority queue | Feature | P0 | autopilot, scheduler | — |
| #2 | Meta-optimizer write tranche: Optimizer.Propose() | Feature | P0 | meta-optimizer, tuning | — |
| #3 | False escalation feedback loop | Feature | P1 | meta-optimizer, feedback | — |
| #4 | Configurable priority weights (YAML) | Feature | P1 | autopilot, config | — |
| #5 | Supervisor daemon mode (`g8s serve`) | Feature | P2 | daemon, cli | — |

### v0.4.0 — Observability & Hardening (Target: 2026-11-15)

| ID | Title | Type | Priority | Labels | Assignee |
|----|-------|------|----------|--------|----------|
| #6 | Structured logging + OpenTelemetry | Feature | P1 | observability, logging | — |
| #7 | Prometheus `/metrics` endpoint | Feature | P1 | observability, metrics | — |
| #8 | Web UI supervisor dashboard | Feature | P2 | ui, dashboard | — |
| #9 | Windows service (kardianos/service) | Feature | P2 | windows, service | — |
| #10 | Receipt lake compaction/retention | Feature | P2 | receipt, maintenance | — |

### v1.0.0 — GA Release (Target: 2026-12-15)

| ID | Title | Type | Priority | Labels | Assignee |
|----|-------|------|----------|--------|----------|
| #11 | Documentation audit & migration guide | Docs | P0 | docs, migration | — |
| #12 | 6-month stability validation on ct122 | Chore | P0 | stability, homelab | — |
| #13 | Performance benchmarks & regression suite | Feature | P1 | benchmark, perf | — |
| #14 | Security audit (OWASP + STRIDE) | Security | P1 | security, audit | — |
| #15 | Plugin ecosystem documentation | Docs | P2 | plugin, ecosystem | — |

---

## Release Process

1. **Version bump**: Update `cmd/g8s/version.go` and `go.mod` (if module path changes)
2. **CHANGELOG**: Move `[Unreleased]` → `[x.y.z] — YYYY-MM-DD`
3. **Tag**: `git tag -a vx.y.z -m "Release vx.y.z"`
4. **Build**: `goreleaser release --clean` (requires `GITHUB_TOKEN`)
5. **Push**: `git push origin main --tags`
6. **Verify**: Download artifacts, smoke test on all platforms

---

## Gap Prevention Checklist

Before every release, verify:

- [ ] **Spec-first**: All new features have ACCEPTED spec in `spec/openspec/`
- [ ] **ADR coverage**: Every architectural decision has an ADR in `docs/decisions/`
- [ ] **Dual-pass gates**: CGO_ENABLED=0 + CGO_ENABLED=1 -race green
- [ ] **Test coverage**: ≥90% on new packages, no race detector warnings
- [ ] **Documentation**: README, CLI help, ADRs, spec updated
- [ ] **Changelog**: `[Unreleased]` section moved, all entries categorized
- [ ] **Version sync**: `version.go`, `go.mod`, GoReleaser config, Docker tags aligned
- [ ] **Artifacts**: All 6 platform binaries produced and signed
- [ ] **Homelab validation**: Deploy to ct122, run self-test, verify orchestration loop