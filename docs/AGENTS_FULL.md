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
| **DELTA-07** | [`07-blast-radius-analyzer-spec.md`](../spec/openspec/07-blast-radius-analyzer-spec.md) | `internal/blastradius` | APPLIED |
| **DELTA-08** | [`08-dispatch-wrapper-spec.md`](../spec/openspec/08-dispatch-wrapper-spec.md) | `internal/dispatch` | APPLIED |
| **DELTA-09** | [`09-worker-supervisor-spec.md`](../spec/openspec/09-worker-supervisor-spec.md) | `internal/worker` | APPLIED |
| **DELTA-10** | [`10-two-class-providers-spec.md`](../spec/openspec/10-two-class-providers-spec.md) | `internal/provider` | APPLIED |
| **DELTA-11** | [`11-knowledge-vault-spec.md`](../spec/openspec/11-knowledge-vault-spec.md) | `internal/vault` | APPLIED |
| **DELTA-12** | [`12-lineage-cte-and-stream-pipe-spec.md`](../spec/openspec/12-lineage-cte-and-stream-pipe-spec.md) | `internal/lineage` | APPLIED |
| **DELTA-13** | [`13-dx-ax-init-and-autorepair-spec.md`](../spec/openspec/13-dx-ax-init-and-autorepair-spec.md) | `internal/initwiz` | APPLIED |
| **DELTA-15** | [`15-fsm-orchestrator-spec.md`](../spec/openspec/15-fsm-orchestrator-spec.md) | `internal/supervisor` | APPLIED |
| **DELTA-17** | [`17-controlplane-orchestrator-spec.md`](../spec/openspec/17-controlplane-orchestrator-spec.md) | `internal/controlplane` | APPLIED |
| **DELTA-18** | [`18-aic-integration-spec.md`](../spec/openspec/18-aic-integration-spec.md) | `cmd/g8s` | APPLIED |
| **DELTA-19** | [`19-lego-mounts-spec.md`](../spec/openspec/19-lego-mounts-spec.md) | `internal/orchestrator` | APPLIED |

---

## 6. AGY Dispatch Reference & Multi-Agent Protocol

When orchestrating subagents via `agy` or other CLI workers:

1. **Brief Formulation**: Every task delegated to a subagent must have a clear objective, role definition, bounded scope, and explicit exit criteria.
2. **Write Receipt Lifecycle**:
   - Brain issues write receipts with bounded paths and strict TTLs.
   - Worker consumes receipt upon mutation; receipts cannot be reused once validated/consumed.
   - Expired or revoked receipts immediately reject mutation requests.
3. **Heartbeat Emission**:
   - Workers emit per-session heartbeat timestamps periodically into `.g8s/heartbeats/<session_id>.json`.
   - The supervisor/control plane monitors heartbeats to detect hung or crashed workers.
4. **Lifecycle & Worktree Cleanup**:
   - Transient worktrees created for subagent execution are automatically pruned on terminal state (`COMPLETED`, `FAILED`, `CANCELLED`).
   - `g8s cleanup` performs safe garbage collection of ghost processes, orphan worktrees, and stale branches.

---

## 7. Verification & CI Quality Gates

All pull requests and code modifications must pass the following local and CI quality gates:

```bash
# 1. Run full test suite with race detector (Zero-CGO)
CGO_ENABLED=0 go test -v -race ./...

# 2. Run static analysis and formatting
go vet ./...
test -z "$(gofmt -l .)"

# 3. Verify cross-platform compilation
GOOS=linux GOARCH=amd64 go build ./cmd/g8s
GOOS=windows GOARCH=amd64 go build ./cmd/g8s
GOOS=darwin GOARCH=arm64 go build ./cmd/g8s

# 4. Check AI anti-pattern linting
bash tools/ai_lint.sh
```
