# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.11.0] - 2026-09-26

Minor release: distributed reflex architecture (L1/L3/L6), closed-loop telemetry, adversarial eval harness, controlled API access. No breaking changes.

### Added
- **`g8s watch` push-channel primitive** (#372): blocking command exits when a watched condition is terminal — replaces supervisor sleep-polling
- **`g8s eval` adversarial harness** (#377): `eval list` + `eval run` over 24 probes × 6 categories with Provider Reliability Index scoring; StaticProvider for CI, live providers via WorkerProvider
- **`g8s reflex triage` CLI** (#345): System-1 reflex gate as enforce-code pre-mutation assessment
- **Closed-loop telemetry** (#363): opt-in `G8S_TELEMETRY=1` — worker attempts emit terminal trace events; pre-flight injection surfaces distilled negative patterns on briefs (#375)
- **L3 post-run Jev quality gate** (#387): after a worker completes, Jev assesses output quality (evidence vs hallucination) before it enters the control plane
- **Workspace jail** (#355): scope-roots + `--scope-root` opt-in for cross-root workflows
- **Dialectic Bounce FSM** (#377): hard ceiling N≤3, evidence-gated bounces, adversarial simulation suite

### Fixed
- **Windows SQLite file URIs** (#361): platform-aware escaping preserves drive colons/separators
- **Windows two-stage signal handling** (#343): graceful `taskkill /T` for SIGTERM, force `/F` for SIGKILL
- **Windows PEB CWD resolution** (#364): `NtQueryInformationProcess` replaces executable-dir shortcut
- **Windows CPU sampling** (#362): real `GetProcessTimes` replaces the static 5.0 stub
- **Worker output fidelity** (#376): sanitization preserves public literals (`token=False`); blocked patterns permission-aware
- **DB connection pooling** (#386): `SetMaxOpenConns`/`SetMaxIdleConns`/`SetConnMaxLifetime` on controlplane + receipt + memory; memory adapter uses `pathutil.SQLiteURI`
- **Session index** (#386): `idx_tasks_session ON tasks(session_id, state)` — per-session queries no longer full-scan
- **Windows native batch ingest** (#356): telemetry batch loss fixed (1-of-N → full persist)

### Security
- **HTTP API controlled access** (#378): opt-in bearer token (`G8S_API_TOKEN`), loopback bind default, CORS off, 1 MiB body bounds, consistent JSON error envelope
- **Reflex scope bypass** (#366): absolute paths rejected in `isWithinScope`; globstar (`**`) matching added
- **Signtool hardening** (#368/#369): `CertThumbprint` mode (no secret in argv), password redaction in errors, https default timestamper
- **Permission-aware patterns** (#376): destructive-execution patterns gate mutation-capable permissions only; sensitive-read patterns enforce everywhere

### Docs
- **Governance standards** (#357): campaign-proven DoR/DoD in DOD_DOR.md section 3
- **Roadmap truth alignment** (#333): README + REFACTORING_PLAN aligned with shipped reality (v0.2.0→v0.10.1)
- **ADR-0021** (standard operating model) + **ADR-0020** (reflex-gated campaign)

## [0.10.1] - 2026-09-25

Maintenance release produced by the reflex-gated debt campaign (ADR-0020) and
the docs drift sweep (#332). No breaking changes.

### Fixed
- **Windows installer version derivation** (#319): NSIS/WiX steps now derive
  the version from the canonical `version --json` envelope (`.data.version`);
  stale `0.4.0` fallback literals in `g8s.nsi` / `g8s.wxs` / chocolatey URL
  synced to the canonical version. New **version-sync CI gate** fails the
  build when packaging templates drift from the canonical version.
- **Flaky `TestRunWithTimeoutAndVerify`** (#321): success-path deadline raised
  5s → 30s; the old deadline was load-sensitive under full-suite and `-race`
  runs (fork/exec + code-sign validation latency).
- **Windows CI test guards**: POSIX `0600` permission assertions in the
  memory package now skip on Windows (no permission bits).
- **Dogfood hardening** (#326–#331): cleanup channel findings, CLI envelope
  rendering, worker completion-state validation, and live Jev calibration
  are issue-tracked for v0.11.0.

### Added
- **Adversarial probe suite** (`internal/harness/probe`, ~20 probes × 6
  categories) and **docs-contract CI gate** (`tools/ci_doc_contract_check.sh`)
  via #324 (progress on #254).
- **ADR-0020** (reflex-gated debt campaign), **ADR-0021** (standard operating
  model), campaign ledger, and roadmap truth alignment (#332, #333).

### Docs
- README + REFACTORING_PLAN aligned with shipped reality (all releases
  v0.2.0 → v0.10.0 verified tagged/published); removed 4 stale draft GitHub
  releases.

## [0.10.0] - 2026-09-20

### Added
- **System-wide effectiveness metrics** (#292): `SystemMetricsCollector` with task throughput, latency, success rates, worker/session metrics, supervisor aggregates. HTTP endpoints: `GET /api/v1/system/metrics` (JSON), extended `/metrics` (Prometheus).
- **Session ownership and isolation** (#291): Schema v10 with `session_id` columns in tasks/supervisor_tasks tables, session-scoped queries, per-session quota tracking (`session_quotas` table).
- **Supervisor intervention benchmark** (#296): 6 benchmarks covering happy path, failure recovery, escalation, cycle duration, optimizer latency, aggregate metrics computation.
- **Real reviewer with scope + test gates** (#302): `RealReviewer` with diffScope (orchestrator) and test gate (go test on modified packages), 30s timeout, 1MB output cap.
- **Checkpoint/recovery for long-running tasks** (#290): Checkpointing infrastructure with resume capability.

### Changed
- **v0.3.0 → v0.10.0**: Complete P0-P2 enhancement issues resolved (#289, #291, #292, #294, #295, #296, #302).

## [0.9.2] - 2026-09-18

### Security
- **Fix SQLite connection string injection** (CVE-risk): Wrapped `dbPath` in `url.PathEscape()` before embedding in SQLite file URI. Prevents injection via crafted file paths containing `?` or `&` characters. (PR #281)

### Changed
- **Go toolchain upgraded to 1.26.0**: All CI workflows updated from 1.25 to 1.26.0 to match `go.mod` requirement. (commit f849527)
- **Test timeout increased**: Extended test timeout to accommodate race detector on slower CI runners.

### Performance
- **#221**: Analyzer uses `bytes.Contains/Count` instead of `strings.Contains/Count` to avoid massive string allocation on file payload
- **#234**: Process enumeration in cleanup uses `strings.EqualFold` on slice to avoid `strings.ToLower` allocation

## [0.9.0] - 2026-09-06

### Added (Concern B: receipt provenance-and-replay context)
- **`CanonicalEnvelope`** on `WriteReceipt`: schema URI, field order, required fields. Pins wire format at issue time so parser remains stable across producer versions. Mandatory at issuance via `WithCanonicalEnvelope()`; system validates `g8s://` prefix and non-empty field order.
- **`RuleGraphSnapshot`** on `WriteReceipt`: ruleset version, pipeline digest (SHA256), optional ADR ref. Pins rule registry state at issuance time so receipts remain replayable after the registry evolves. Mandatory at issuance; helper `WithCurrentRuleGraph()` reads from internal registry.
- **`ProvenanceLineage`** on `WriteReceipt`: issued-by, tool version, trace ID, actor chain, source commit. Auto-populated in `IssueReceipt` from runtime context so Brain never has to hand-author `"unknown"`.
- **`PurgeExpired(maxAge, maxRows)`** method on `*Manager`: bounded delete of consumed-or-expired receipts. Caller responsibility to schedule (cron, supervisor loop).
- **UUID v4 primary key** (retained from v0.8.0): `ReceiptID` generated by `uuid.NewString()` (RFC 4122 v4). UUID v7 was deferred during review because B3 reorder + B4 NULL/empty handling provided sufficient correctness, and v7's time-ordering was not load-bearing for any current consumer. Chronological ordering remains available via `ORDER BY created_at`.
- **Schema migration v2 → v3**: idempotent `ALTER TABLE ADD COLUMN` for 11 new nullable columns. Legacy v0.8.0 receipts remain readable.
- **Spec delta 02 §4 (Concern B)** documenting strictness model and out-of-scope items.

### Changed
- `internal/receipt.SchemaVersion` 2 → 3.
- `IssueReceipt` signature unchanged externally; new behavior via `IssueOption`s.

## [0.8.0] - 2026-09-06
## [0.7.0] - 2026-09-01
## [0.6.0] - 2026-09-01

### Added
- Windows version-info resource (ProductName, FileVersion, LegalCopyright)
- macOS notarization support (when APPLE_ID, APPLE_PASSWORD, APPLE_TEAM_ID secrets are set)
- AV heuristics documentation in docs/CODING_SIGNING.md
- Makefile target for building Windows .syso resource

### Changed
- **Onboarding docs**: explicit guidance to pin g8s to a release
  tag, not the local build, to avoid stale-binary drift (aegis
  agent on 2026-08-30 reported 4 BUGs that were already fixed
  in main but not yet in a release).

### Fixed
- **#220**: Worker dispatch now correctly spawns `agy` instead of `g8s` (removed `WithBinaryPath(os.Args[0])` in `runWorker`, defaulted Supervisor `binaryPath` to empty)
- **#222**: Added `--worktree-base-dir` flag to `g8s cleanup` to clean blind worktree directories outside working directory (Safety Net bypass)

### Performance
- **#221**: Analyzer uses `bytes.Contains/Count` instead of `strings.Contains/Count` to avoid massive string allocation on file payload
- **#234**: Process enumeration in cleanup uses `strings.EqualFold` on slice to avoid `strings.ToLower` allocation