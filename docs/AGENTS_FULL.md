# AGENTS_FULL.md — Deep Reference & Execution Manual for g8s

> **SYSTEM DIRECTIVE**: This document is the comprehensive, in-depth companion to [`AGENTS.md`](../AGENTS.md). It details the complete architecture governance map, JIT task routing, strict engineering invariants, OpenSpec specifications, and AGY multi-agent orchestration protocols.

---

## 1. Overview & SSoT Alignment

The single source of truth (SSoT) for machine-readable state is [`manifest.json`](../manifest.json) in the repository root. `g8s` follows **Spec-Driven Development (SDD)** with **Zero-Trust Capability Delegation**.

```
┌──────────────────────────────────────────────────────────────────────────┐
│                         TWO-TIER AGENT TAXONOMY                          │
├───────────────────────────────────┬──────────────────────────────────────┤
│  BRAIN TIER (Orchestrator/Planner)│  MUSCLE TIER (Worker/Executor)       │
│  • Strategic planning & triage    │  • Scoped task execution             │
│  • Issues scoped Write Receipts   │  • Operates strictly under Sandbox   │
│  • Promotes to Knowledge Vault    │  • Emits Heartbeats during run       │
│  • Owns final git commits & PRs   │  • Zero credential/shared state      │
└───────────────────────────────────┴──────────────────────────────────────┘
```

---

## 2. Complete SSoT Architecture & Governance Map

| Scope / Dimension | SSoT Document | Key Topics Covered |
| :--- | :--- | :--- |
| **System Overview** | [`README.md`](../README.md) | High-level quickstart, build commands, CLI architecture diagram. |
| **Master Matrix** | [`docs/MASTER_MATRIX.md`](MASTER_MATRIX.md) | Multi-dimensional cross-cutting system matrix and 5-phase roadmap. |
| **Project Constitution** | [`spec/constitution.md`](../spec/constitution.md) | 5 Foundational Axioms (Zero-CGO, 2-Tier, Receipts, Isolation, SDE). |
| **The 8 Iron Laws** | [`docs/RULES.md`](RULES.md) | Non-negotiable engineering rules (Deterministic time, Post-scan, etc.). |
| **Design Philosophy** | [`docs/DESIGN_PHILOSOPHY.md`](DESIGN_PHILOSOPHY.md) | Two-tier agent taxonomy, Lego-block model, Non-invasive SoC sidecar. |
| **Lifecycle & Evidence** | [`docs/LIFECYCLE_AND_EVIDENCE.md`](LIFECYCLE_AND_EVIDENCE.md)| Task FSM (8 states), 1:N Governor, 3-Tier Evidence Lake. |
| **DX & AX Experience** | [`docs/CLI_AND_EXPERIENCE_DESIGN.md`](CLI_AND_EXPERIENCE_DESIGN.md)| Interactive `init` wizard, `doctor --json`, Feynman error hints. |
| **Architecture Roadmap** | [`docs/ARCHITECTURE_ROADMAP.md`](ARCHITECTURE_ROADMAP.md)| Multi-phase system architecture and evolution roadmap (DEBT-33). |
| **Contributing Workflow**| [`CONTRIBUTING.md`](../CONTRIBUTING.md) | Spec-Driven Development cycle: Spec $\rightarrow$ Test $\rightarrow$ Code $\rightarrow$ Verify. |
| **SemVer & Releases** | [`docs/VERSIONING.md`](VERSIONING.md) & [`docs/RELEASE_SOP.md`](RELEASE_SOP.md)| Semantic Versioning 2.0.0, 6-Gate Pre-release audit, GoReleaser. |
| **JSON Schemas** | [`schemas/`](../schemas/) | Machine-readable schemas: `task`, `receipt`, `result`. |

---

## 3. JIT Agent Task Routing (Decision Tree)

When receiving a prompt or instruction, match your task to the routing table below:

```text
Incoming Task:
├── "Implement a new feature or engine" ──────► 1. Check spec/openspec/ -> 2. Write tests first -> 3. Implement Pure-Go
├── "Fix a bug or test failure" ─────────────► 1. Reproduce with failing Go test -> 2. Fix logic -> 3. Verify with -race
├── "Add/modify a Role or Permission" ───────► 1. Update internal/harness/ -> 2. Update manifest.json -> 3. Update tests
├── "Add a new Provider backend (e.g. Claude)"► 1. Read spec/openspec/05 -> 2. Implement internal/provider/
├── "Modify Process / Lifecycle / Cleanup" ──► 1. Read internal/process/ & internal/cleanup/ -> 2. Cross-platform test
└── "Prepare a release or version bump" ─────► 1. Read docs/RELEASE_SOP.md -> 2. Execute 6-Gate audit
```

### Routing Execution Protocols

1. **Spec-Driven Feature Addition**:
   - Inspect active delta in `spec/openspec/`.
   - Write comprehensive unit tests in `internal/<pkg>/<pkg>_test.go` covering edge cases.
   - Implement Pure-Go logic with zero CGO dependencies.
   - Ensure all public functions have docstrings and clean error propagation.

2. **Bug Remediation**:
   - Create a minimal reproducing test case that fails against current code.
   - Patch the defect while preserving backward compatibility.
   - Run `CGO_ENABLED=0 go test -v -race ./...` to verify zero race conditions.

3. **Schema & Contract Updates**:
   - Modify JSON schema in `schemas/`.
   - Synchronize Go struct definitions in `internal/harness/` or `internal/receipt/`.
   - Validate against `manifest.json`.

---

## 4. Strict Development Invariants

1. **Zero-CGO**: All SQLite operations must use `modernc.org/sqlite`. Never import `github.com/mattn/go-sqlite3`.
2. **Deterministic Clocks**: Any time-dependent logic (TTL, leases, heartbeats) must accept an injectable `clock func() time.Time` to allow deterministic sub-millisecond testing.
3. **Cross-Platform Compatibility**: Code must run seamlessly across macOS (Darwin), Linux, and Windows. Never hardcode POSIX `/tmp` or Unix-specific `ps` calls; use abstractions in `internal/process` and `os.TempDir()`.
4. **100% English**: Code comments, docstrings, error messages, and git commits must be 100% professional English with zero non-ASCII diacritics.
5. **Spec-First**: Never write code before verifying or updating the corresponding OpenSpec delta in `spec/openspec/`.
6. **Test Parity & Race Detector**: Every feature must pass `CGO_ENABLED=0 go test -v -race ./...`.
7. **Unified JSON Envelope**: All CLI commands must support standard JSON envelopes (`--json`, `--jsonl`, `--actor`, `--trace-id`) with Feynman error hints on failure.
8. **Zero Silent Error Swallowing & SAST Triad**: Never discard filesystem or state errors via `_ = os.WriteFile(...)` / `_ = os.Rename(...)`; return wrapped errors or log structured `[warn]` diagnostics to `os.Stderr`. All production file/dir writes must use `0o600`/`0o700` permissions and pass the SAST Triad (`Gosec` in `.golangci.yml`, offline `Semgrep` in `.semgrep/g8s-rules.yml`, and `CodeQL` in `.github/workflows/codeql.yml`).

---

## 5. Active OpenSpec Deltas & Specifications

| Delta ID | Specification Document | Target Package | Status |
| :--- | :--- | :--- | :--- |
| **DELTA-01** | [`01-core-harness-spec.md`](../spec/openspec/01-core-harness-spec.md) | `internal/harness` | APPLIED |
| **DELTA-02** | [`02-receipt-delegation-spec.md`](../spec/openspec/02-receipt-delegation-spec.md) | `internal/receipt` | APPLIED |
| **DELTA-03** | [`03-controlplane-sqlite-spec.md`](../spec/openspec/03-controlplane-sqlite-spec.md) | `internal/controlplane` | APPLIED |
| **DELTA-04** | [`04-mcp-stdio-server-spec.md`](../spec/openspec/04-mcp-stdio-server-spec.md) | `internal/mcp` | APPLIED |
| **DELTA-05** | [`05-provider-and-resource-pool-spec.md`](../spec/openspec/05-provider-and-resource-pool-spec.md) | `internal/provider` | APPLIED |
| **DELTA-06** | [`06-os-daemon-service-spec.md`](../spec/openspec/06-os-daemon-service-spec.md) | `internal/service` | APPLIED |
| **DELTA-07** | [`07-blast-radius-analyzer-spec.md`](../spec/openspec/07-blast-radius-analyzer-spec.md) | `internal/analyzer` | APPLIED |
| **DELTA-08** | [`08-dispatch-wrapper-spec.md`](../spec/openspec/08-dispatch-wrapper-spec.md) | `internal/dispatch` | APPLIED |
| **DELTA-09** | [`09-worker-supervisor-spec.md`](../spec/openspec/09-worker-supervisor-spec.md) | `internal/worker` | APPLIED |
| **DELTA-10** | [`10-two-class-providers-spec.md`](../spec/openspec/10-two-class-providers-spec.md) | `internal/provider` | APPLIED |
| **DELTA-11** | [`11-knowledge-vault-spec.md`](../spec/openspec/11-knowledge-vault-spec.md) | `internal/vault` | APPLIED |
| **DELTA-11-ROADMAP** | [`11-orchestration-roadmap-spec.md`](../spec/openspec/11-orchestration-roadmap-spec.md) | `internal/supervisor` | APPLIED |
| **DELTA-12** | [`12-lineage-cte-and-stream-pipe-spec.md`](../spec/openspec/12-lineage-cte-and-stream-pipe-spec.md) | `internal/controlplane` | APPLIED |
| **DELTA-13** | [`13-dx-ax-init-and-autorepair-spec.md`](../spec/openspec/13-dx-ax-init-and-autorepair-spec.md) | `internal/initwiz` | APPLIED |
| **DELTA-14** | [`14-core-hardening-and-doctor-spec.md`](../spec/openspec/14-core-hardening-and-doctor-spec.md) | `internal/doctor`, `internal/autopilot`, `internal/server` | APPLIED |
| **DELTA-15** | [`15-orchestrator-fsm-and-brief-spec.md`](../spec/openspec/15-orchestrator-fsm-and-brief-spec.md) | `internal/orchestrator`, `internal/state`, `internal/brief` | APPLIED |
| **DELTA-16** | [`16-lifecycle-hygiene-and-envelope-spec.md`](../spec/openspec/16-lifecycle-hygiene-and-envelope-spec.md) | `internal/heartbeat`, `internal/process`, `internal/cleanup`, `internal/cli` | APPLIED |
| **DELTA-17** | [`17-receipt-and-evidence-lake-spec.md`](../spec/openspec/17-receipt-and-evidence-lake-spec.md) | `internal/controlplane`, `internal/telemetry` | APPLIED |
| **DELTA-18** | [`18-aic-and-diffintel-review-spec.md`](../spec/openspec/18-aic-and-diffintel-review-spec.md) | `cmd/g8s`, `internal/diffintel`, `internal/review` | APPLIED |
| **DELTA-19** | [`19-lego-mounts-and-reflex-governance-spec.md`](../spec/openspec/19-lego-mounts-and-reflex-governance-spec.md) | `internal/orchestrator`, `internal/reflex`, `internal/routing`, `internal/verifier` | APPLIED |
| **DELTA-20** | [`20-code-intel-adapter-spec.md`](../spec/openspec/20-code-intel-adapter-spec.md) | `internal/codeintel` | APPLIED |
| **DELTA-21** | [`21-unified-memory-adapter-spec.md`](../spec/openspec/21-unified-memory-adapter-spec.md) | `internal/memory` | APPLIED |
| **DELTA-22** | [`22-brief-dor-floor-spec.md`](../spec/openspec/22-brief-dor-floor-spec.md) | `internal/brief` | APPLIED |
| **DELTA-23** | [`23-native-agent-io-spec.md`](../spec/openspec/23-native-agent-io-spec.md) | `internal/fsops`, `internal/search` | APPLIED |
| **DELTA-24** | [`24-differential-governance-hitl-spec.md`](../spec/openspec/24-differential-governance-hitl-spec.md) | `internal/vault`, `internal/verifier`, `internal/telemetry` | APPLIED |
| **DELTA-25** | [`25-config-driven-llm-consumer-spec.md`](../spec/openspec/25-config-driven-llm-consumer-spec.md) | `internal/config`, `internal/provider`, `cmd/g8s` | IMPLEMENTED |
| **DELTA-26** | [`26-worker-isolation-fail-closed-spec.md`](../spec/openspec/26-worker-isolation-fail-closed-spec.md) | `internal/worker` | APPLIED |
| **DELTA-27** | [`27-server-bind-auth-spec.md`](../spec/openspec/27-server-bind-auth-spec.md) | `internal/server` | APPLIED |
| **DELTA-28** | [`28-dod-mechanical-binding-spec.md`](../spec/openspec/28-dod-mechanical-binding-spec.md) | `internal/verifier`, `cmd/g8s` | APPLIED |
| **DELTA-29** | [`29-output-schema-validation-spec.md`](../spec/openspec/29-output-schema-validation-spec.md) | `internal/worker` | APPLIED |
| **DELTA-30** | [`30-worker-claim-affinity-spec.md`](../spec/openspec/30-worker-claim-affinity-spec.md) | `internal/controlplane`, `internal/worker`, `cmd/g8s` | APPLIED |
| **DELTA-31** | [`31-receipt-export-status-spec.md`](../spec/openspec/31-receipt-export-status-spec.md) | `internal/controlplane`, `internal/worker`, `cmd/g8s` | APPLIED |
| **DELTA-32** | [`32-bounded-agentic-loop-f1f2-fleet-jev-spec.md`](../spec/openspec/32-bounded-agentic-loop-f1f2-fleet-jev-spec.md) | `cmd/g8s`, `internal/routing` | APPLIED |
| **DELTA-33** | [`33-stratum-federation-and-charter-spec.md`](../spec/openspec/33-stratum-federation-and-charter-spec.md) | `internal/charter`, `internal/fsops`, `internal/mcp`, `cmd/g8s` | APPLIED |
| **DELTA-34** | [`34-mcp-skill-hydration-and-diagnosis-spec.md`](../spec/openspec/34-mcp-skill-hydration-and-diagnosis-spec.md) | `internal/brief`, `internal/mcp`, `internal/routing`, `cmd/g8s` | APPLIED |
| **DELTA-35** | [`35-pre-execution-research-debate-gate-spec.md`](../spec/openspec/35-pre-execution-research-debate-gate-spec.md) | `internal/brief`, `internal/harness`, `cmd/g8s` | APPLIED |
| **DELTA-36** | [`36-contextpack-ast-knapsack-spec.md`](../spec/openspec/36-contextpack-ast-knapsack-spec.md) | `internal/contextpack`, `internal/mcp`, `cmd/g8s` | APPLIED |
| **DELTA-37** | [`37-six-stage-ai-qa-pipeline-spec.md`](../spec/openspec/37-six-stage-ai-qa-pipeline-spec.md) | `internal/verifier`, `cmd/g8s` | APPLIED |
| **DELTA-38** | [`38-device-lease-and-mobile-gate-spec.md`](../spec/openspec/38-device-lease-and-mobile-gate-spec.md) | `internal/provider`, `skills/g8s-mobile-gate` | APPLIED |
| **DELTA-39** | [`39-universal-cli-agent-adapter-spec.md`](../spec/openspec/39-universal-cli-agent-adapter-spec.md) | `internal/provider`, `internal/worker`, `internal/routing` | APPLIED |
| **DELTA-40** | [`40-lifecycle-uninstall-update-spec.md`](../spec/openspec/40-lifecycle-uninstall-update-spec.md) | `internal/lifecycle`, `internal/cleanup`, `cmd/g8s` | APPLIED |
| **DELTA-41** | [`41-feature-map-v2-collision-domains-spec.md`](../spec/openspec/41-feature-map-v2-collision-domains-spec.md) | `internal/featuremap`, `cmd/g8s` | APPLIED |
| **DELTA-42** | [`42-premise-challenge-and-sycophancy-gate-spec.md`](../spec/openspec/42-premise-challenge-and-sycophancy-gate-spec.md) | `internal/premise`, `internal/harness/probe`, `cmd/g8s` | APPLIED |
| **DELTA-43** | [`43-provider-conformance-matrix-spec.md`](../spec/openspec/43-provider-conformance-matrix-spec.md) | `internal/provider/conformance`, `cmd/g8s` | APPLIED |
| **DELTA-45** | [`45-five-axis-kaizen-scorecard-spec.md`](../spec/openspec/45-five-axis-kaizen-scorecard-spec.md) | `internal/routing`, `cmd/g8s` | APPLIED |
| **DELTA-46** | [`46-peer-coordinator-ledger-spec.md`](../spec/openspec/46-peer-coordinator-ledger-spec.md) | `internal/mesh`, `internal/mcp`, `cmd/g8s` | PROPOSED |

---

## 6. Containment Hierarchy (F0/F1/F2) & Multi-Agent Protocol

When orchestrating workers via `agy`, `claude`, or other CLI providers ([`ADR-0032`](decisions/0032-containment-levels.md) & [`skills/g8s-supervisor/SKILL.md`](../skills/g8s-supervisor/SKILL.md)):

1. **Exponential Containment Levels (`F0 ⊇ F1 ⊇ F2`)**:
   - **F0 (Brain / Supervisor)**: Holds exclusive authority over architecture, git commits, vault promotion, and Write Receipt issuance (`Operator ↔ F0 = 1-1`).
   - **F1 (Worker)**: Spawned by F0 through the `g8s` harness (`g8s submit` + `g8s worker`) in an isolated git worktree and OS process group (`Setpgid`). Strict **Cardinality Law**: `1 issue = 1 worker = 1 receipt`, `F1 ↔ F1 = 0`.
   - **F2 (Recursive Subagent of Worker)**: Spawned recursively by F1 inside F1's native platform (`define_subagent` / `invoke_subagent`), inheriting F1's sandbox and receipt scope (`F0 ↔ F2 = 0 direct`).
2. **Brief Formulation & Admission**:
   - Always rebuild `./bin/g8s` (`make local`) and run `./bin/g8s doctor --json` after every merge before dispatching (Lesson #6: Stale-Binary Trap).
   - Isolate concurrent supervisor sessions with `G8S_STATE_DIR=~/.local/state/g8s-sess-<id>`.
   - Every delegated task requires a bounded brief, explicit role/permission profile, and acceptance oracle.
3. **Write Receipt & Delivery Lifecycle**:
   - F0 issues single-use Write Receipts with bounded `allowed_paths` globs and strict TTLs.
   - Wake on completion via event-driven signals (`./bin/g8s watch --task <id> --milestone worker-complete` or `./bin/g8s watch --failed`); polling is fallback only.
   - Harvest deliverables from `$G8S_STATE_DIR/worktrees`, run `./bin/g8s verify --task <id>`, and verify the physical diff on disk before accepting.
4. **System-1 Reflex (`Jev` / TypeSafe) & Cognitive Harness (`wiki.py`)**:
   - **Jev (TypeSafe SystemOne)**: Operates at L1 Pre-Mutation Triage (`./bin/g8s reflex triage`), Router Assist (`G8S_ROUTER_MODE=jev_assisted`), and L3 Post-Run Output Quality / Memory Promotion Gate while preserving **Sensor ≠ Decider**. Never emit raw API keys in JSON outputs or logs.
   - **Offer Bundle (`offer/`) & `wiki.py`**: Sibling repositories adopt `g8s` via [`offer/ONBOARDING.md`](../offer/ONBOARDING.md) and return sanitized Mode 3 packets. Align session state with Second Brain governance via `~/.local/bin/wiki.py orient`.
5. **F0 Cognitive Purity & Information Firewall (`ADR-0033` Phase 06b + Wave B)**:
   - F0 operates strictly as Brain (reasoning, brief authoring, consuming `RoundReceiptSchema` from `g8s verify --task <id> --json`, `watch_verdict` from `g8s watch --pr <N> --json`, `f0 gate check --json`). F0 NEVER manually hunts raw CI logs, scans disk bloat, or runs procedural janitor tasks. All log-hunting, CI failure extraction, and file sync tasks are delegated to F1 workers.
6. **Universal Config-Driven LLM Call Consumer, `9router` Integration & Zero-Trust Hardening (`DELTA-25..32`, `v0.20.0`)**:
   - Direct synchronous & bounded agentic loop execution: `./bin/g8s llm-call --provider 9router --model <combo> --prompt "..." [--json] [--apply-files] [--max-turns <1..5>] [--verify-cmd "..."]`.
   - Durable worker queue execution, adaptive effort timeout & claim affinity (`DELTA-27..32`): `./bin/g8s submit --provider 9router --model <combo> [--output-schema <schema.json>]` (auto-scales default timeout by effort: `45s`/`90s`/`240s`; enforces `depth <= 2` and parent `add_dirs` jail on `--parent-task-id`) + `./bin/g8s worker --once [--provider <p>] [--model <m>]`.
   - Cross-repository fleet hygiene gate (`DELTA-32`): `./bin/g8s fleet status [--dir <workspace>] [--require-clean] [--json]`.
   - Zero-trust worker execution invariants (`DELTA-26..32`): fail-closed worktree isolation (`#656`), recursive Draft-7 `--output-schema` enforcement (`#659`), structured `receipt_export` visibility + `./bin/g8s receipt-export --task <id>` recovery (`#658`), non-loopback `g8s serve` token auth (`#655`), mechanical `EvaluateDoDMatrix` check binding (`#648`), and monotonic `Jev` gate-lane (`0/D/R/F/S`) & effort (`low..xhigh`) routing.
   - Live model discovery: `./bin/g8s providers discover [--provider 9router] [--json]`.
   - Standardized `9router` SWE combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`) and prefix wildcards (`nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`) in `.g8s/agent-models.yml`.
   - **Two-Layer Separation Rule**: Public repo mechanism (`openai`/`anthropic`/`9router`, standard combos/wildcards, `http://127.0.0.1:20128/v1`) vs. local untracked operator policy (`~/.config/g8s/providers.json` & `~/.agents/skills/`). Never commit homelab LAN IPs or private topology.
7. **Orthogonal Stratum Federation (`S0..S3` + `O-Plane`) & Epoch-Fenced Charter Engine (`DELTA-33` / [`ADR-0035`](decisions/0035-orthogonal-stratum-federation.md))**:
   - **Axis X (Physical Containment Rings)**: `F0` (Host/Brain), `F1` (Isolated git worktree + `Setpgid` worker), `F2` (Nested subagent/subtask `F2 ⊆ F1 ⊆ F0`).
   - **Axis Y (Hierarchical Control Strata)**: `S0 Principal` (Human) $\to$ `S1 FederationCoordinator` (Multi-Repo Charter Issuer) $\to$ `S2 DomainGovernor` (Single-Repo Sovereign Supervisor) $\to$ `S3-M BoundedMutator` / `S3-A BlindAdjudicator`, plus `O-Plane TelemetryProjector` (`g8s charter portfolio --json`).
   - **Strict Adjacency & Mutation Firewall**: `S1`, `S3-A`, and `O-Plane` are kernel-blocked from direct file mutation (`E_S1_DIRECT_MUTATION_FORBIDDEN`) and `S1` is blocked from calling `g8s submit` directly (`E_STRATUM_ADJACENCY_VIOLATION`).
   - **Verified `9router` Routing Priority**: Prefer `deep-coding` (complex coding), `reasoning-high` (Socratic/architectural analysis), `docs-retriever` (fast scouting), and `cl/anthropic/claude-sonnet-4.6` / `cl/openai/gpt-4.1-mini` / `cl/google/gemini-2.5-flash` (disjoint-family `S3-A` blind adjudication).

---

## 7. Verification & CI Quality Gates (Including SAST Triad)

All pull requests and code modifications must pass the local and CI quality & security gates (`./tools/pre_push.sh`, `.github/workflows/quality.yml`, `.github/workflows/codeql.yml`):

```bash
# 1. Run full test suite with race detector (Zero-CGO + Race)
CGO_ENABLED=0 go test -v ./...
CGO_ENABLED=1 go test -v -race ./...

# 2. Run SAST Triad (Gosec in golangci-lint + Semgrep AST rules), DAST, IAST, Fuzzing & vulnerability check
golangci-lint run ./...
bash tools/ci_semgrep_check_test.sh && bash tools/ci_semgrep_check.sh
go test -race ./internal/server/... ./internal/mcp/... -run DAST
go test -race ./internal/harness/probe/... -run TestIAST
go test -run '^$' -fuzz FuzzVerifyScope -fuzztime 2000x ./internal/fsops/
go test -run '^$' -fuzz FuzzSelectPath -fuzztime 5000x ./internal/cli/
govulncheck ./...

# 3. Run static analysis, formatting, and doc-code anti-stale contracts
CGO_ENABLED=0 go vet ./...
test -z "$(gofmt -l .)"
bash tools/ci_doc_contract_check.sh
bash tools/ci_link_integrity.sh
bash tools/claims_check.sh

# 4. Verify cross-platform compilation
GOOS=linux GOARCH=amd64 go build ./cmd/g8s
GOOS=windows GOARCH=amd64 go build ./cmd/g8s
GOOS=darwin GOARCH=arm64 go build ./cmd/g8s

# 5. Check AI & Brief anti-pattern linting
bash tools/ai_lint.sh
bash tools/brief_lint.sh
```

