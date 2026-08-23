# AGENTS.md — Agent-First Onboarding & Execution Protocol for g8s

> **SYSTEM DIRECTIVE FOR AI AGENTS**: Any AI Agent (Claude, Cursor, Copilot, Codex, Antigravity) entering this repository MUST read this document first. This repository uses the **Spec-Driven Development (SDD)** framework and enforces strict **Zero-Trust Capability Delegation**.

---

## 0. The Machine-Readable SSoT Manifest

For programmatic inspection without parsing prose, read [`manifest.json`](manifest.json) at the repository root. It contains the exact JSON schema maps, active OpenSpec deltas, roles, permission matrices, and milestone progress.

---

## 1. Zero-Context Lazy Loading Protocol (Mandatory Reading Order)

Do NOT randomly scan the entire repository or the `reference/` directory. Follow this 4-step progressive disclosure reading order:

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ 1. README.md    │ ──► │ 2. CONSTITUTION │ ──► │ 3. REFACTOR PLAN│ ──► │ 4. OPENSPEC     │
│ Understand ID   │     │ Anchor Invariants│     │ Locate active   │     │ Target spec     │
│ & 30s overview  │     │ (Zero-CGO/Rules)│     │ task [ ]        │     │ delta in spec/  │
└─────────────────┘     └─────────────────┘     └─────────────────┘     └─────────────────┘
```

1. **Step 1 — Understand Identity & Purpose**: Read [`README.md`](README.md) (1 min).
2. **Step 2 — Anchor Invariant Rules (Constitution)**: Read [`spec/constitution.md`](spec/constitution.md) (Zero-CGO, Pure-Go, 2-Tier governance, Process isolation).
3. **Step 3 — Locate Active Task & Progress**: Read [`docs/REFACTORING_PLAN.md`](docs/REFACTORING_PLAN.md) (Find the first uncompleted `[ ]` task).
4. **Step 4 — Read the Technical Spec Delta**: Read the corresponding spec in [`spec/openspec/`](spec/openspec/) (e.g. `02-receipt-delegation-spec.md`).
5. **Step 5 (JIT Only) — Reference Python Baseline**: Only inspect the specific file in [`reference/python/`](reference/python/) matching your current task.

---

## 2. Complete SSoT Architecture & Governance Map

| Scope / Dimension | SSoT Document | Key Topics Covered |
| :--- | :--- | :--- |
| **System Overview** | [`README.md`](README.md) | High-level quickstart, build commands, CLI architecture diagram. |
| **Master Matrix** | [`docs/MASTER_MATRIX.md`](docs/MASTER_MATRIX.md) | Multi-dimensional cross-cutting system matrix and 5-phase roadmap. |
| **Project Constitution** | [`spec/constitution.md`](spec/constitution.md) | 5 Foundational Axioms (Zero-CGO, 2-Tier, Receipts, Isolation, SDE). |
| **The 8 Iron Laws** | [`docs/RULES.md`](docs/RULES.md) | Non-negotiable engineering rules (Deterministic time, Post-scan, etc.). |
| **Design Philosophy** | [`docs/DESIGN_PHILOSOPHY.md`](docs/DESIGN_PHILOSOPHY.md) | Two-tier agent taxonomy, Lego-block model, Non-invasive SoC sidecar. |
| **Lifecycle & Evidence** | [`docs/LIFECYCLE_AND_EVIDENCE.md`](docs/LIFECYCLE_AND_EVIDENCE.md)| Task FSM (8 states), 1:N Governor, 3-Tier Evidence Lake. |
| **DX & AX Experience** | [`docs/CLI_AND_EXPERIENCE_DESIGN.md`](docs/CLI_AND_EXPERIENCE_DESIGN.md)| Interactive `init` wizard, `doctor --json`, Feynman error hints. |
| **Contributing Workflow**| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Spec-Driven Development cycle: Spec $\rightarrow$ Test $\rightarrow$ Code $\rightarrow$ Verify. |
| **SemVer & Releases** | [`docs/VERSIONING.md`](docs/VERSIONING.md) & [`docs/RELEASE_SOP.md`](docs/RELEASE_SOP.md)| Semantic Versioning 2.0.0, 6-Gate Pre-release audit, GoReleaser. |
| **JSON Schemas** | [`schemas/`](schemas/) | Machine-readable schemas: `task`, `receipt`, `result`. |

---

## 3. Current Sprint Goal (Milestone 1 — Subtask 2)

* **Target Package**: `internal/receipt`
* **Target Specification**: [`spec/openspec/02-receipt-delegation-spec.md`](spec/openspec/02-receipt-delegation-spec.md)
* **Python Baseline**: [`reference/python/scripts/agy_control_plane.py:L978-L1119`](reference/python/scripts/agy_control_plane.py) and [`reference/python/scripts/test_receipt_delegation.py`](reference/python/scripts/test_receipt_delegation.py)
* **Deliverables**:
  1. `internal/receipt/receipt.go`: SQLite WAL table `write_receipts`, `IssueReceipt`, `ValidateAndConsume`, `RevokeReceipt`, `ListActiveReceipts`.
  2. `internal/receipt/receipt_test.go`: 38 unit & integration tests covering Happy Path, Security, TTL expiry with mock clock, and Concurrency.
* **Exit Criteria**: `CGO_ENABLED=0 go test -v -race ./internal/receipt/...` passes 100%.

---

## 4. JIT Agent Task Routing (Decision Tree)

When receiving a prompt, match your task to the routing table below:

```text
Incoming Task:
├── "Implement a new feature or engine" ──────► 1. Check spec/openspec/ -> 2. Write tests first -> 3. Implement Pure-Go
├── "Fix a bug or test failure" ─────────────► 1. Reproduce with failing Go test -> 2. Fix logic -> 3. Verify with -race
├── "Add/modify a Role or Permission" ───────► 1. Update internal/harness/ -> 2. Update manifest.json -> 3. Update tests
├── "Add a new Provider backend (e.g. Claude)"► 1. Read spec/openspec/05 -> 2. Implement internal/provider/
└── "Prepare a release or version bump" ─────► 1. Read docs/RELEASE_SOP.md -> 2. Execute 6-Gate audit
```

---

## 5. Strict Development Invariants

1. **Zero-CGO**: All SQLite operations must use `modernc.org/sqlite`. Never import `github.com/mattn/go-sqlite3`.
2. **Deterministic Clocks**: Any time-dependent logic (TTL, leases) must accept an injectable `clock func() time.Time` to allow sub-millisecond deterministic testing.
3. **100% English**: Code comments, docstrings, error messages, and git commits must be 100% professional English with zero non-ASCII diacritics.
4. **Spec-First**: Never write code before verifying or updating the corresponding OpenSpec delta in `spec/openspec/`.
5. **Test Parity & Race Detector**: Every feature must pass `CGO_ENABLED=0 go test -v -race ./...`.
