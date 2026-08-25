# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
