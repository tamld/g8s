---

**Session type**: T2 (execution)
title: "Plan: Documentation Stale References, Specification Gaps & Constitution Remediation"
status: "completed"
created: "2026-09-24"
author: "Antigravity Assistant"
priority: "P1"
tags: ["documentation", "constitution", "openspec", "cli-reference", "ci-contract"]
blockedBy: []
blocks: []
---

# Plan: Documentation Stale References, Specification Gaps & Constitution Remediation

## 1. Executive Summary & Socratic Rationale
- **Context**: An empirical audit of the repository documentation revealed significant drift between the codebase (`cmd/g8s`, `internal/`) and documentation (`spec/constitution.md`, `manifest.json`, `docs/user-guide/cli-reference.md`, `docs/AGENTS_FULL.md`, `spec/openspec/README.md`, `README.md`).
- **Root Cause**: Rapid code evolution (reaching `v0.10.0`, 38 packages, 821+ tests, 9 additional subcommands) while CI documentation gate (`tools/ci_doc_contract_check.sh`) only checked a single file (`docs/ARCHITECTURE_ROADMAP.md`).
- **Objective**: Re-align all documentation with current codebase reality, fix all broken internal markdown links, document all 9 missing subcommands, synchronize versions and metrics, and harden CI documentation contract check to prevent future drift.

---

## 2. Invariants & Architecture Constraints
| Rule | Constraint |
| :--- | :--- |
| **Axiom 3: Pure-Go (Zero-CGO)** | All documentation must strictly declare Pure-Go, Zero-CGO, and stdlib `flag.FlagSet` usage. Remove legacy references to `cobra`/`viper`. |
| **Axiom 4: SSoT Manifest** | `manifest.json` is the machine-readable Single Source of Truth; its version, release, and spec tables must match the repository state. |
| **Deterministic CLI Contracts** | All documented flags, types, and defaults in `docs/user-guide/cli-reference.md` must match `cmd/g8s/main.go` and subcommands verbatim. |
| **No Dead Links** | All relative markdown links across `docs/` and `spec/` must resolve to real files on disk. |

---

## 3. Work Breakdown & Implementation Phases

### Phase 1: Core SSoT & Constitution Alignment
- **Files**:
  - `spec/constitution.md`
  - `manifest.json`
- **Actions**:
  1. In `spec/constitution.md`:
     - Update Go version to `1.26.0` (matching `go.mod`).
     - Remove `spf13/cobra` and `spf13/viper` references in §2. Replace with standard library `flag.FlagSet` and zero-dependency configuration.
  2. In `manifest.json`:
     - Update `go_version` to `"1.26.0"`.
     - Update `latest_release` to reflect current tagged release (`v0.10.0`).
     - Register missing OpenSpec entries (`DELTA-11` orchestration roadmap, `DELTA-18` AIC integration).

### Phase 2: CLI Reference Documentation for 9 Missing Subcommands
- **File**: `docs/user-guide/cli-reference.md`
- **Actions**:
  1. Add comprehensive usage, flags table, and examples for:
     - `g8s cancel <task-id> [--reason <text>]`
     - `g8s resume <task-id> [--prompt <text>]`
     - `g8s worker [--once] [--model <model>] [--lease <sec>]`
     - `g8s analyze [--dir <path>] [--format json|text]`
     - `g8s vault (store|query|get|list|delete)`
     - `g8s autopilot (start|status|trigger)`
     - `g8s serve [--address <host:port>]`
     - `g8s eval [--provider <name>]`
     - `g8s supervisor-metrics-update-false --task-id <id> --false`
  2. Fix duplicate section numbering (`### 15. g8s migrate` and `### 15. g8s converge`).

### Phase 3: OpenSpec Registry & Agent Guide Broken Link Remediation
- **Files**:
  - `spec/openspec/README.md`
  - `docs/AGENTS_FULL.md`
  - `spec/openspec/20-code-intel-adapter-spec.md`
- **Actions**:
  1. In `spec/openspec/README.md`:
     - Correct filenames in index:
       - `DELTA-08`: `08-dispatch-wrapper-spec.md`
       - `DELTA-09`: `09-worker-supervisor-spec.md`
       - `DELTA-10`: `10-two-class-providers-spec.md`
     - Add missing entries:
       - `DELTA-11`: `11-orchestration-roadmap-spec.md`
       - `DELTA-20`: `20-code-intel-adapter-spec.md`
  2. In `docs/AGENTS_FULL.md`:
     - Reconcile references to unwritten specs (`DELTA-15`, `DELTA-17`, `DELTA-19`) or link to canonical roadmap / architecture documents.
  3. In `spec/openspec/20-code-intel-adapter-spec.md`:
     - Fix broken link to ADR-0018 (point to `docs/ARCHITECTURE_ROADMAP.md` or correct decision path).

### Phase 4: Release & Metrics Harmonization
- **Files**:
  - `README.md`
  - `README.vi.md`
  - `docs/REFACTORING_PLAN.md`
- **Actions**:
  1. In `README.md` & `README.vi.md`:
     - Update Go badge to `Go 1.26.0`.
     - Update package count to `38 packages`.
     - Update test function count to `820+ test functions`.
     - Update release artifact table and verification commands from `v0.2.0` to `v0.10.0`.
     - Refresh Key Features section to reference `v0.10.0`.
  2. In `docs/REFACTORING_PLAN.md`:
     - Mark completed milestones for `v0.2.0` and `v0.3.0`.
     - Check off verified acceptance criteria.

### Phase 5: CI Contract Hardening
- **File**: `tools/ci_doc_contract_check.sh`
- **Actions**:
  1. Expand Go toolchain version contract check beyond `docs/ARCHITECTURE_ROADMAP.md` to also verify:
     - `spec/constitution.md`
     - `manifest.json`
     - `README.md`
     - `README.vi.md`
  2. Add relative markdown link verification check for files in `spec/openspec/` to ensure no 404 links can land in `main`.

---

## 4. Verification & Acceptance Criteria
1. `tools/ci_doc_contract_check.sh` passes 100% with the new expanded contract checks.
2. `go test ./...` passes without errors.
3. No broken markdown links in `spec/openspec/README.md` or `docs/AGENTS_FULL.md`.
4. All 9 previously undocumented CLI commands are documented in `docs/user-guide/cli-reference.md`.
5. All version and metric counters match codebase reality (`1.26.0`, `v0.10.0`, 38 packages, 820+ tests).

---

## 5. Risk Assessment & Mitigation
- **Risk**: Modifying documentation might cause regex mismatch in other scripts.
- **Mitigation**: Run `tools/ci_doc_contract_check.sh` and full pre-push checks before pushing.
