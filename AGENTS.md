# AGENTS.md — Agent-First Onboarding & Execution Protocol for g8s

> **SYSTEM DIRECTIVE FOR AI AGENTS**: Any AI Agent (Claude, Cursor, Copilot, Codex, Antigravity) entering this repository MUST read this document first. This repository uses the **Spec-Driven Development (SDD)** framework and enforces strict **Zero-Trust Capability Delegation**.

---

## 0. The Machine-Readable SSoT Manifest

For programmatic inspection without parsing prose, read [`manifest.json`](manifest.json) at the repository root. It contains the exact JSON schema maps, active OpenSpec deltas, roles, permission matrices, and milestone progress.

---

## 1. Zero-Context Lazy Loading Protocol (Mandatory Reading Order)

Do NOT randomly scan the entire repository or the `reference/` directory. Follow this 4-step progressive disclosure reading order:

```text
1. README.md      -> 2. CONSTITUTION    -> 3. REFACTOR PLAN   -> 4. OPENSPEC
   (ID & Overview)      (Invariant Rules)      (Active Task)         (Spec Delta)
```

1. **Step 1 — Understand Identity & Purpose**: Read [`README.md`](README.md) (1 min).
2. **Step 2 — Anchor Invariant Rules (Constitution)**: Read [`spec/constitution.md`](spec/constitution.md) (Zero-CGO, Pure-Go, 2-Tier governance, Process isolation).
3. **Step 3 — Locate Active Task & Progress**: Read [`docs/REFACTORING_PLAN.md`](docs/REFACTORING_PLAN.md) (Find the first uncompleted `[ ]` task).
4. **Step 4 — Read the Technical Spec Delta**: Read the corresponding spec in [`spec/openspec/`](spec/openspec/) (e.g. `02-receipt-delegation-spec.md`).
5. **Step 5 (JIT Only) — Reference Python Baseline**: Only inspect the specific file in [`reference/python/`](reference/python/) matching your current task.

---

## 2. Deep Reference Documentation

For the complete SSoT Architecture & Governance Map, JIT Task Routing Decision Tree, Strict Development Invariants, OpenSpec Deltas, and Multi-Agent Protocols, see [`docs/AGENTS_FULL.md`](docs/AGENTS_FULL.md).
