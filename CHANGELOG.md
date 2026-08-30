# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- **Onboarding docs**: explicit guidance to pin g8s to a release
  tag, not the local build, to avoid stale-binary drift (aegis
  agent on 2026-08-30 reported 4 BUGs that were already fixed
  in main but not yet in a release).

## [0.5.0] - 2026-08-30

### Added
- **DEBT-40: Windows NSIS & WiX MSI Installer Support** (`.goreleaser.yaml`, `packaging/windows`, `internal/registry`, `internal/doctor`, `docs/WINDOWS_INSTALL.md`, #153):
  - Added NSIS setup wizard (`.exe`) with GUI installer, automatic System PATH registration, Start Menu group, Desktop shortcut, and uninstaller.
  - Added WiX MSI enterprise installer (`.msi`) for corporate rollout, SCCM, Intune, and GPO distributions.
  - Added `internal/registry` cross-platform wrapper using `golang.org/x/sys/windows` registry APIs with zero-CGO compatibility.
  - Enhanced `g8s doctor` on Windows to detect installation source (`MSI/NSIS` vs `ZIP/Manual`), install path, and System PATH registration state.
  - Added comprehensive Windows installation and troubleshooting guide in `docs/WINDOWS_INSTALL.md`.

## [0.4.0] - 2026-08-30

### Added
- **DEBT-25: Brief Workflow Integration** (`internal/brief`, `internal/orchestrator`, #112, #117):
  - Integrated `g8s brief-issue` and `g8s brief-consume` workflow with the orchestrator and Sisyphus dispatch loop.
- **DEBT-26: Automatic Worktree Cleanup** (`internal/cli`, `cmd/g8s`, #113, #115):
  - Automatic isolation worktree pruning for agy subagents upon session completion or error exit.
- **DEBT-27: Automated Self-Dogfooding Cycle** (`.github/workflows/dogfood.yml`, `cmd/g8s`, #114):
  - Weekly automated dogfooding cycle executing brief roundtrip, worktree pruning, and tracking issue creation.
- **DEBT-28: Lifecycle Hygiene & Resource Cleanup** (`internal/cleanup`, `cmd/g8s`, #118, #121):
  - Added `g8s cleanup` subcommand to terminate ghost worker processes, prune orphan worktrees, and evict stale scratch artifacts.
- **DEBT-29: Worker Heartbeat & Status Observability** (`internal/heartbeat`, `cmd/g8s`, #119, #122, #129):
  - Per-session heartbeat file tracking with `g8s status --worker` for real-time worker liveness and status introspection.
- **DEBT-30: Unified JSON Envelope v1** (`internal/cli`, #120, #128):
  - Standardized `Envelope` schema with `v`, `kind`, `cmd`, `sub`, `data`, `error`, `trace_id`, and `at` across all CLI subcommands.
  - Added common flag parser support for `--actor`, `--trace-id`, `--json`, and `--jsonl` (single-line JSONL streaming mode).
  - Standardized structured error envelopes on stdout with Feynman hints (`code`, `message`, `hint`, `cause`).
- **DEBT-32: Auto-Cleanup on Terminal State** (`internal/orchestrator`, #124, #131):
  - Automatic `g8s cleanup` lifecycle hook triggered when orchestration reaches a terminal success state.
- **DEBT-36: Cross-Platform Process Management & Safe-Kill** (`internal/process`, `internal/cleanup`, #130, #133, #134):
  - Introduced cross-platform `ProcessLister` interface with Darwin/Linux `ps` parsing and Windows `tasklist`/WMI implementations.
  - Heartbeat-to-PID mapping validation and safe-kill confirmation to prevent accidental process termination.

## [0.3.0] - 2026-08-29

### Added
- **Supervisor 8-State FSM** (`internal/supervisor`, DELTA-15): Full lifecycle FSM (`PLAN` -> `SPAWN` -> `MONITOR` -> `RECEIPT` -> `MERGE` / `ESCALATE`) with retry budget and Root Cause Analysis (RCA).
- **Control Plane Orchestrator Lineage** (`internal/controlplane`, DELTA-17): Added `orchestrator_id`, `worktree_id`, `worker_name`, and `iter` tracking columns with indexed schema migrations.
- **Supervisor Receipt Evolution** (`internal/receipt`, DELTA-11 Concern B): Backward-compatible `SupervisorMeta` with NULL handling.
- **Meta-Optimizer Aggregate Telemetry** (`internal/supervisor`, DELTA-11 Concern C): Read-only aggregate performance queries and metrics streaming.
- **AIC Integration & Intent Orchestration** (`cmd/g8s`, DELTA-18): Subcommand `g8s orchestrate <intent>` for direct natural language intent dispatch.
- **Stateful Worker Lego Mounts** (`internal/orchestrator`, DELTA-19): Added composable `SkillMount`, `HookMount`, and `MemoryMount` building blocks.
- **Brief Issue & Consume Dispatch Contract** (`internal/brief`, `cmd/g8s`, DEBT-107): Added `g8s brief-issue` and `g8s brief-consume` subcommands.
- **Dogfooding CI Gate** (`.github/workflows/dogfood.yml`, DEBT-20): CI verification that g8s can orchestrate g8s on every PR.
- **AI Anti-Pattern Gate** (`tools/ai_lint.sh`, DEBT-21): AST and regex linter rejecting silent error swallowing, untyped `any`, unhandled panics, and committed TODOs.

## [0.2.0] - 2026-08-29

### Added
- **Supervisor Core Engine** (`internal/supervisor`, DELTA-11 Concern A): Planner, enforcer, reviewer, RCA analyzer, escalator, and telemetry metrics engine wired to SQLite WAL.
- **Dogfood Self-Test Loop** (`cmd/g8s`): Terminal loop `g8s orchestrate --self-test` exercising real CLI workers with mock failure escalation.
- **Supervisor Metrics Command** (`cmd/g8s`): Subcommand `g8s supervisor-metrics` with `--task-id` and `--aggregate` filters.

### Changed
- **Linter & Security Tightening** (DEBT-22/23/24/25): Re-enabled pinned `staticcheck` v0.7.0 (ADR-0015), `errcheck` with `.errcheck_excludes` (ADR-0017), tightened `gosec` rules, and raised aggregate test coverage above 82%.

## [0.1.0] - 2026-08-25

### Added
- **Durable control plane** (`internal/controlplane`, DELTA-03): SQLite-backed task queue
  via `modernc.org/sqlite` (Zero-CGO) with compare-and-swap leases, priority-aware claims,
  attempt counters, cancel propagation, pause states (`NEEDS_INFO`/`BLOCKED`), maintenance
  windows, and legacy-v1 schema migration.
- **Native provider registry** (`internal/provider`, DELTA-05): static default configs for
  `agy` / `claude` / `ollama` with slot semaphores, injectable clock and PATH lookup, and an
  HTTP health probe classifying ollama endpoints as ready/degraded/unavailable.
- **MCP stdio JSON-RPC server** (`internal/mcp`, DELTA-04 + Amendment A): eleven `g8s_*`
  tools including guarded synchronous dispatch, durable submit/get/list/cancel over the
  control plane, role and permission introspection with MCP-surface policy metadata,
  client protocol-version echo, sanitized task views that never expose prompts, and a
  layered guard chain (`blocked_by_policy`, `blocked_by_harness`, `blocked_by_sandbox_policy`,
  `blocked_missing_add_dirs`, `setup_required`).
- **Bounded AGY dispatch wrapper** (`internal/dispatch`, DELTA-08): binary resolution with
  explicit-path/env/PATH/home fallback precedence, command construction honoring sandbox and
  permission flags, read-only contract violation detection (wiki mutation commands/reports,
  side-effect fingerprints, git mutations), credential redaction, and head/tail bounded
  capture with UTF-8 replacement.
- **Worker supervisor** (`internal/worker`, DELTA-09): claim-to-cleanup run loop with POSIX
  process groups (`setpgid` + escalating SIGTERM/SIGKILL teardown), per-attempt run
  directories exporting sealed `receipt.json` snapshots, always-removed prompt files,
  never-persisted stdout/stderr capture, heartbeat-driven lease renewal, timeout and cancel
  terminal branches, and shell-convention signal exit codes.
- **Hardened launchd service manager** (`internal/service`, DELTA-06 Amendment A):
  macOS-only MVP backend installing user LaunchAgents without root; hardened unit
  definitions (fixed umask, absolute pinned binary, sanitized PATH, secret-free encoding),
  bootout-before-bootstrap reinstall ordering with byte-exact rollback, maintenance-window
  gating across installs, and fail-closed symlink/world-writable/platform checks.
- **Receipt engine hardening** (`internal/receipt`, DELTA-02 follow-ups): revocation and
  audit-persistence semantics, cross-handle concurrent validation with exactly-once
  consumption guarantees under the race detector, and delegated-write contract prompts
  injecting scoped path patterns plus wiki-engine policy blocks.
- **CLI surface** (`cmd/g8s`): real `mcp`, `submit`, `get`, and `receipt-issue`
  subcommands backed by the control plane, with state-directory bootstrap under
  `~/.local/state/g8s`.

### Changed
- CI now runs a dual-pass gate on all three OS matrices: Zero-CGO
  (`CGO_ENABLED=0` vet + test) and race-detector (`CGO_ENABLED=1` test -race).
- GitHub Actions bumped to `actions/checkout@v7` and `actions/setup-go@v7`
  (node24 runtime, eliminating node20 deprecation warnings).
- GoReleaser configuration modernized to the v2 `formats` schema; snapshot builds
  produce five cross-platform archives (darwin/linux/windows on amd64/arm64,
  excluding windows/arm64).

### Fixed
- Cross-platform test portability: macOS `/var` → `/private/var` symlink resolution in
  canonical-path assertions, POSIX-permission assertions gated off on Windows, and
  Windows stat-mode false positives in world-writable checks.
- Worker output-capture file handles are closed before removal, fixing a Windows sharing
  violation that left `worker.stdout`/`worker.stderr` behind.
- Bulk receipt-issuing timing budget relaxed to tolerate slower CI runners while still
  detecting pathological regressions.

[Keep a Changelog]: https://keepachangelog.com/en/1.1.0/
[Semantic Versioning]: https://semver.org/spec/v2.0.0.html
