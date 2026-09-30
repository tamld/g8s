# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.13.0] - 2026-09-30

### Added
- Multi-provider worker dispatch on the durable queue path (#457): `g8s submit --provider` / `g8s worker --provider` with strict claim affinity; providers.json (`~/.config/g8s/providers.json`) remains the single worker manifest per DELTA-10; provider-name template > model template > legacy agy resolution; explicit unknown provider fails the attempt (no silent fallback); `default_provider` setting (#458, #460).
- Deterministic delivery contract (#448): the supervisor injects a `deliverable {mode, dir}` pointer into attempt results (#467) and `g8s deliver <task-id>` applies receipt-validated worktree files to the checkout with atomic refusal on out-of-scope paths (#472). The receipt, not the agent, governs what lands.
- Harness self-audit suite (#451, ADR-0026 §4): four self-audit probes registered in the eval suite (refusal-echo #443, worktree-discard #443-f2/#450, sanitizer-fidelity #434/#445, receipt-bypass) — **first recorded run: 4/4 passed** (`g8s eval run --category self-audit`, 2026-09-30).
- Enterprise transparency (#441): `docs/ENTERPRISE_LEDGER.md` claim inventory (PROVEN/UNPROVEN/PARTIAL), access-audit design note, ADR-0027 (availability "not measured, by design" with recorded triggers).
- Directive clocks ledger `docs/directives.md` (ADR-0026 §5.2): every directive carries its incident, clock, and simplify-if condition.
- Release gates 7-8 wired into the release SOP (ADR-0026 §5.1/§5.3): Red Cell pass (hard, waiver path) and per-release self-audit probe run.
- Multi-project ownership hardening (#465 P0): ghost-process sweep requires CWD/command-line identity corroboration (PID-recycle kills eliminated); orphan-worktree sweep and the worktree pool root moved under the state dir — host-global TMPDIR is no longer a shared blast radius (#468).

### Fixed
- `g8s eval list` printed nothing: `Probe.Runner` (func field) broke suite JSON marshaling; the envelope writer swallowed the error. `Runner` is now `json:"-"` + regression test (caught at the v0.13.0 release gate).
- Memory adapter: bounded SQLITE_BUSY retry-with-backoff in working-context paths (#440/#464).
- Link-integrity gate: configurable content roots with honest examined-counts reporting and a zero-examined NOT-EVALUATED guard (#435/#462); survives stock macOS bash 3.2 (#466/#475).
- `TestRunWithTimeout` budget 1s → 5s (spawn latency, not product behavior) (#459/#463).

### Changed
- Release tooling hardened (#461/#471): manifest.json synced to the tag and guarded by the version-sync check; RELEASE_SOP Gate 6 names the correct file; `claims_check.sh` wired into pre-push and the Quality workflow.
- Release-governance ratified (ADR-0026 §5): Red Cell gate HARD with named-waiver path; directive ledger `docs/directives.md`; self-audit cadence per-release.

### Release-governance record for v0.13.0
- Gate 7 (Red Cell, hard): **WAIVED by the operator** for this release — the waiver is the named artifact; the pass itself rides the next release cycle.
- Gate 5 (Spec parity): **DELTA-20 remains PROPOSED (M5 scope)** — Gate 5 waived by the operator for this release; no shipped code depends on it.
- Gate 8 (Self-audit): **4/4 probes passed** (sa-001 refusal-echo, sa-002 worktree-discard, sa-003 sanitizer-fidelity, sa-004 receipt-bypass) via `g8s eval run --category self-audit --provider mock-compliant`.

## [0.12.0] - 2026-09-27

### Added
- **Concurrent worker drain (#394)**: `g8s worker --concurrency N` — up to N simultaneous supervised attempts, per-attempt worktree isolation via the orchestrator pool, sessions-registry crash recovery, hard usage error outside a git checkout. Internal `RunLoop` gains `Concurrency`/`Isolation`/`OnTask`/`OnError`.
- **Memory promotion gate (#395, ADR-0023)**: entry FSM (7 states) + label schema (kind/lifecycle/trust/salience/scope/provenance), payload-hash tombstones (G1, monotone), naked-payload Jev gate with allowlist verdicts (G3), meta-entry rejection (G4), provenance bulk revocation (`g8s memory revoke --session`), out-of-sample-only salience. Jev-free read path by construction.
- **Context Broker (#396, ADR-0021 §8.2)**: `internal/context` assembles bounded `ContextPacket`s (≤4096 chars, fail-open per source, `broker_failure` observability); `TriageRequest` v2 with byte-compatible flags; L1 `reflex triage` pilot wiring (`--no-enrich` for legacy).
- **Brief DoR floor + advisory skill routing (#398, DELTA-22)**: deterministic blocking floor (goal/scope/DoD/receipt-for-workspace_write), `skill_suggestions` envelope field (zero enforcement), Jev quality advisory behind `G8S_BRIEF_JEV_QUALITY`.
- **Live eval (#379)**: agy/claude `WorkerProvider` adapters (read-only dispatch, bounded timeout, sanitized capture) + deterministic semantic-class scoring (refusal signatures → BLOCKED). First live PRI recorded: 0.625 (15/24).
- **Vendored operator skills**: `skills/g8s-supervisor` charter v4.0.0 + `skills/manifest.json` — the supervisor/worker operating practice ships with the repo.

### Fixed
- **Worker result schema validation (#383, SEC)**: worker-authored result files are whitelist-schema-validated (boolean `ok`, non-empty `status`, size cap); self-reported successes failing validation are rewritten to `ok=false` rejection envelopes; the deliverable `response` is surfaced through central sanitization; `result_validation`/`error_call_history` survive `FinishAttempt`.
- **Telemetry lifecycle (#253 latent)**: per-run close silenced every later run's telemetry in one process; engine lifecycle is now refcounted (concurrency-safe, closes exactly once).
- **L3 Jev quality gate is advisory by default (#411)**: context-blind rejects no longer fail attempts (`G8S_L3_JEV_BLOCK=1` to opt back in); the memory promotion sensor is wired in production (fail-closed on unknown verdicts).
- **eval list JSON envelope** silently emitted zero bytes (unmarshalable `Runner` func).

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
- **#234**: Process enumeration in cleanup uses `strings.EqualFold` on slice to avoid `strings.ToLower` allocation## [0.2.0] — 2026-09-15

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

### v0.3.0 — Autopilot & Daemon (Target: 2026-10-15)

| ID | Title | Type | Priority | Labels | Assignee |
|----|-------|------|----------|--------|----------|
| #1 | Autopilot scheduler: cron tick + priority queue | Feature | P0 | autopilot, scheduler | — |
| #2 | Meta-optimizer write tranche: Optimizer.Propose() | Feature | P0 | meta-optimizer, tuning | — |
| #3 | Daemon mode (`g8s serve`): long-lived process + HTTP API | Feature | P0 | daemon, cli | — |
| #4 | HTTP API + OpenAPI spec: /api/v1/*, /metrics, /healthz | Feature | P0 | api, openapi | — |
| #5 | Export Go API docs + semantic versioning | Feature | P1 | docs, api | — |
| #6 | False escalation feedback loop | Feature | P1 | meta-optimizer, feedback | — |
| #7 | Configurable priority weights (YAML) | Feature | P1 | autopilot, config | — |
| #8 | Windows service hardening (kardianos/service) | Feature | P2 | windows, service | — |

### v0.4.0 — Observability & Hardening (Target: 2026-11-15)

| ID | Title | Type | Priority | Labels | Assignee |
|----|-------|------|----------|--------|----------|
| #9 | Structured logging (slog + trace_id correlation) | Feature | P1 | observability, logging | — |
| #10 | Prometheus text endpoint (`/metrics`) — zero deps | Feature | P1 | observability, metrics | — |
| #11 | Receipt lake compaction/retention | Feature | P2 | receipt, maintenance | — |
| #12 | Windows service hardening (kardianos/service) | Feature | P2 | windows, service | — |

### v1.0.0 — GA Release (Target: 2026-12-15)

| ID | Title | Type | Priority | Labels | Assignee |
|----|-------|------|----------|--------|----------|
| #13 | Documentation audit & migration guide | Docs | P0 | docs, migration | — |
| #14 | 6-month stability validation on ct122 | Chore | P0 | stability, homelab | — |
| #15 | Performance benchmarks & regression suite | Feature | P1 | benchmark, perf | — |
| #16 | Security audit (OWASP + STRIDE) | Security | P1 | security, audit | — |
| #17 | Plugin ecosystem documentation | Docs | P2 | plugin, ecosystem | — |

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