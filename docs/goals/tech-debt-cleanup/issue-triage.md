# Issue Triage - Tech Debt Cleanup

**Scout Task**: T005 - Triage 23 open issues
**Date**: 2026-09-18
**Assignee**: Scout

---

## All Open Issues (23)

| # | Title | Labels | Priority | Group |
|---|-------|--------|----------|-------|
| 304 | Governance: explicit non-goals — what g8s will NOT ship (and why) | documentation | - | Governance |
| 303 | Chore: release version drift — README v0.2.0 vs code v0.9.2 vs manifest v0.9.0 | documentation | - | Governance |
| 302 | P1: Real reviewer to replace StubReviewer (tests + scope gates) | enhancement | P1 | Command Execution |
| 301 | P0: Visible receipt lake — `receipt list/verify` as the trust moat | enhancement | P0 | Onboarding |
| 300 | P0: 30-second `g8s init` to first verified receipt | enhancement | P0 | Onboarding |
| 298 | Output redaction corrupts JSONL/source drafts while task reports SUCCEEDED | - | - | Bug |
| 296 | feat: add supervisor intervention benchmark using real Wiki tasks (P1) | - | P1 | Architecture |
| 295 | feat: implement result acceptance mechanism with error tracking (P0) | - | P0 | Command Execution |
| 294 | feat: implement command lifecycle handling (hang/denial/cancel/executable verification) (P0) | - | P0 | Command Execution |
| 293 | feat: implement evaluation-driven improvement loop with holdout validation (P2) | - | P2 | Architecture |
| 292 | feat: add system-wide effectiveness metrics (P1) | - | P1 | Architecture |
| 291 | feat: enforce session ownership and isolation (P1) | - | P1 | Architecture |
| 290 | feat: add checkpoint/recovery for long-running tasks (P1) | - | P1 | Architecture |
| 289 | feat: implement verifiable task contracts with limits and output schema (P0) | - | P0 | Command Execution |
| 287 | Runtime diagnostics: AGY command stalls on substituted host python3 until task deadline | - | - | Bug |
| 280 | CI/CD Audit: Pipeline Full Stabilization, GolangCI-Lint v2 Migration, and Pre-Push Guardrails | documentation, ... | - | Governance |
| 258 | ARCH/PROTO-01: Layered Agent Protocol Stack (A2A + MCP + Agent Protocol) & Dialectic Lifecycle Engine | enhancement | - | Architecture |
| 257 | ARCH/MEM-01: Unified Decoupled Memory Layer & Hybrid Retrieval Adapter | enhancement, lifecycle, architecture | - | Architecture |
| 255 | DOCS/GOV: Update Master Roadmap, Definition of Ready (DoR), and Definition of Done (DoD) for Agent Lifecycle... | - | - | Governance |
| 254 | FEAT/SEC: Adversarial Safety Probes & Automated Worker Behavioral Evals | enhancement, security, architecture | - | Architecture |
| 253 | FEAT/ARCH: Closed-Loop Worker Trace Telemetry & Negative Knowledge Distillation | enhancement, lifecycle, arch | - | Architecture |

---

## Issue Groups

### Group 1: Command Execution Reliability (P0 - Critical)
**Issues**: #294, #295, #289, #302
- #294: Command lifecycle handling (hang/denial/cancel/executable verification) - P0
- #295: Result acceptance mechanism with error tracking - P0
- #289: Verifiable task contracts with limits and output schema - P0
- #302: Real reviewer to replace StubReviewer (tests + scope gates) - P1

**Scope**: internal/worker/, internal/supervisor/, cmd/g8s/, test/
**PR Strategy**: Single PR "Command Execution Reliability"

---

### Group 2: Onboarding UX (P0 - Critical)
**Issues**: #301, #300
- #301: Visible receipt lake (`receipt list/verify`) - P0
- #300: 30-second `g8s init` to first verified receipt - P0

**Scope**: cmd/g8s/, internal/controlplane/, test/
**PR Strategy**: Single PR "Onboarding UX"

---

### Group 3: Output Redaction Bug (Critical Bug)
**Issues**: #298
- Output redaction corrupts JSONL/source drafts while task reports SUCCEEDED

**Scope**: internal/worker/, internal/brief/, test/
**PR Strategy**: Single PR "Output Redaction Bug Fix"

---

### Group 4: AGY Stall Bug (Critical Bug)
**Issues**: #287
- Runtime diagnostics: AGY command stalls on substituted host python3 until task deadline

**Scope**: internal/dispatch/, internal/worker/, test/
**PR Strategy**: Single PR "AGY Stall Fix"

---

### Group 5: Governance & Documentation (P1)
**Issues**: #304, #303, #280, #255
- #304: Explicit non-goals documentation
- #303: Release version drift (README v0.2.0 vs code v0.9.2) - **PARTIALLY FIXED** (release recreation done)
- #280: CI/CD Audit: Pipeline Full Stabilization
- #255: Update Master Roadmap, DoR, DoD

**Scope**: docs/, README.md, .github/workflows/
**PR Strategy**: Single PR "Governance & Documentation"

---

### Group 6: Architecture & Platform (P1-P2)
**Issues**: #258, #257, #296, #292, #291, #290, #293, #254, #253
- #258: Layered Agent Protocol Stack (A2A + MCP) - arch
- #257: Unified Decoupled Memory Layer - arch
- #296: Supervisor intervention benchmark - P1
- #292: System-wide effectiveness metrics - P1
- #291: Session ownership and isolation - P1
- #290: Checkpoint/recovery for long-running tasks - P1
- #293: Evaluation-driven improvement loop - P2
- #254: Adversarial Safety Probes - security/arch
- #253: Closed-Loop Worker Trace Telemetry - arch

**Scope**: docs/architecture/, internal/, tests/
**PR Strategy**: These are large architectural features - should be separate PRs/epics, not grouped into one PR. Recommend:
  - Create GitHub Projects/Epics for each
  - Implement incrementally

---

## Stale/Duplicate Candidates

| Issue | Reason |
|-------|--------|
| #280 | CI/CD Audit - largely addressed by version-sync.yml, pre_tag.sh, Release workflow fixes |
| #255 | Master Roadmap/DoR/DoD - may overlap with #304 governance docs |
| #253 | Closed-Loop Telemetry - overlaps with #296, #292, #290 |

---

## PR Assignment Plan

| PR | Issues | Priority | Scope | Est. Effort |
|----|--------|----------|-------|-------------|
| PR 1 | #294, #295, #289, #302 | P0 | Command Execution | High |
| PR 2 | #301, #300 | P0 | Onboarding | Medium |
| PR 3 | #298 | Bug | Output Redaction | Medium |
| PR 4 | #287 | Bug | AGY Stall | Medium |
| PR 5 | #304, #303, #280, #255 | P1 | Governance/Docs | Low |
| PR 6+ | #258, #257, #296, #292, #291, #290, #293, #254, #253 | P1-P2 | Architecture | Very High (separate epics) |

---

## Receipt

```yaml
receipt:
  result: done
  summary: "Triaged 23 open issues into 6 groups. 4 critical groups (P0/bug) ready for PRs. 2 groups (governance, architecture) need separate handling. 3 stale candidates identified."
  evidence:
    - "docs/goals/tech-debt-cleanup/issue-triage.md"
  spawned_tasks:
    - T006 (Judge: approve grouping)
```