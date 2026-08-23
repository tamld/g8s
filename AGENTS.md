# AGENTS.md — Agent Onboarding & Execution Protocol for g8s

> **SYSTEM DIRECTIVE FOR AI AGENTS**: Any AI Agent (Claude, Cursor, Copilot, Codex, Antigravity) entering this repository MUST read this document first. This repository uses the **Spec-Driven Development (SDD)** framework.

---

## 0. Zero-Context Lazy Loading Protocol (Mandatory Reading Order)

Do NOT randomly scan the entire repository or the `reference/` directory. Follow this 4-step progressive disclosure reading order:

1. **Step 1 — Understand Identity & Purpose**: Read [`README.md`](README.md) (1 min).
2. **Step 2 — Anchor Invariant Rules (Constitution)**: Read [`spec/constitution.md`](spec/constitution.md) (Zero-CGO, Pure-Go, 2-Tier governance, Process isolation).
3. **Step 3 — Locate Active Task & Progress**: Read [`docs/REFACTORING_PLAN.md`](docs/REFACTORING_PLAN.md) (Find the first uncompleted `[ ]` task).
4. **Step 4 — Read the Technical Spec Delta**: Read the corresponding spec in [`spec/openspec/`](spec/openspec/) (e.g. `02-receipt-delegation-spec.md`).
5. **Step 5 (JIT Only) — Reference Python Baseline**: Only inspect the specific file in [`reference/python/`](reference/python/) matching your current task.

---

## 1. Project North Star (Grand Goal)

Transform the Python prototype (`reference/python/`) into **`g8s` (The Gatekeepers)**: A standalone, 100% Pure-Go (Zero-CGO), cross-platform, single-binary capability and process harness for multi-agent LLM systems with 100% test parity (140+ tests passing with `-race`).

---

## 2. Next Immediate Goal (Current Sprint)

### Target: **Milestone 1 — Subtask 2: Write Receipt Delegation Engine**
* **Package**: `internal/receipt`
* **Specification**: [`spec/openspec/02-receipt-delegation-spec.md`](spec/openspec/02-receipt-delegation-spec.md)
* **Python Reference**: [`reference/python/scripts/agy_control_plane.py:L978-L1119`](reference/python/scripts/agy_control_plane.py) and [`reference/python/scripts/test_receipt_delegation.py`](reference/python/scripts/test_receipt_delegation.py)
* **Deliverables**:
  1. `internal/receipt/receipt.go`: SQLite WAL table `write_receipts`, `IssueReceipt`, `ValidateAndConsume`, `RevokeReceipt`, `ListActiveReceipts`.
  2. `internal/receipt/receipt_test.go`: 38 unit & integration tests covering Happy Path, Security, TTL expiry with mock clock, and Concurrency.
* **Exit Criteria**: `CGO_ENABLED=0 go test -v -race ./internal/receipt/...` passes 100%.

---

## 3. Strict Development Rules

1. **Zero-CGO**: All SQLite operations must use `modernc.org/sqlite`. Never import `github.com/mattn/go-sqlite3`.
2. **Deterministic Clocks**: Any time-dependent logic (TTL, leases) must accept an injectable `clock func() time.Time` to allow deterministic fast testing.
3. **Pure English**: Code comments, docstrings, error messages, and git commits must be 100% professional English.
4. **Spec-First**: Never write code before updating or verifying the corresponding OpenSpec delta in `spec/openspec/`.
5. **Test Parity**: For every Python test file in `reference/python/scripts/test_*.py`, create an identical Go test file in `internal/<pkg>/<pkg>_test.go`.
