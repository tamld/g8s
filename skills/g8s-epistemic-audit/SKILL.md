---
name: g8s-epistemic-audit
description: "Claim decomposition and evidence-tier classifier (MECHANICAL_VERIFIED, OBSERVATIONAL_TELEMETRY, UNVERIFIED_PROSE), enforcing the 4 epistemic laws across supervisor handoffs and receipts."
domain: testing
version: 1.0.1
last_updated: "2026-10-24"
risk_level: medium
evidence_level: runtime_required
args_syntax: "g8s-epistemic-audit [audit|decompose|classify] --input <file.md> [--threshold <ratio>] [--json]"
---

# g8s Epistemic Audit (`g8s-epistemic-audit`)

The **Epistemic Audit** skill audits documentation, session handoffs, PR descriptions, and worker receipts for epistemic purity. It parses unstructured claims into atomic assertions, classifies them into strict evidence tiers, and enforces the **4 Epistemic Laws**.

---

## 1. Purpose & Threat Defeated

### Purpose
To prevent epistemic degradation in autonomous agent systems by decomposing claims into verifiable units and rejecting ungrounded assertions before they infect persistent memory or supervisor decisions.

### Threats Defeated
1. **Epistemic Inflation**: Presenting subjective confidence, narrative plausibility, or model self-reports as verified engineering facts.
2. **Observational Conflation**: Treating runtime telemetry or logs as proof of correctness without mechanical invariant bindings.
3. **Ghost Verification**: Passing verification gates through unchecked claims or vacuous citation links.
4. **Architectural Drift**: Allowing operational runtime state (`AgentMemory`) to overwrite normative specifications.

---

## 2. The 4 Epistemic Laws

Every evaluated document must comply with these foundational invariants:

1. **Law 1 — Telemetry is Observational Unless Mechanically Bound**: Worker logs, execution traces, and telemetry strings are observational data. They do not constitute proof of correctness unless cryptographically bound to deterministic exit codes, content hashes, or independent holdout test runs.
2. **Law 2 — Jev SystemOne is a Routing/Triage Sensor Measured by Decision Delta**: Jev (`System-1`) operates under the strict rule **Sensor ≠ Decider**. Its quality is evaluated strictly by counterfactual decision delta (routing efficiency and defect catch delta), never by self-proclaimed certainty.
3. **Law 3 — F0 Retains Raw Evidence Drill-Down on Conflict**: While F0 supervisors consume `RoundReceiptSchema` summaries by default to maintain cognitive purity, any conflict, canary survival, or gate failure triggers mandatory drill-down to raw diffs and unredacted logs.
4. **Law 4 — Memory Partitioning (LLM Wiki vs AgentMemory)**: Git-tracked OpenSpec/ADRs and `wiki.py` constitute the normative architectural SSoT. Operational `AgentMemory` is ephemeral runtime state and must never override spec contracts.

---

## 3. Evidence Tiers

All assertions are categorized into three discrete tiers:

| Tier | Definition | Admissibility |
|---|---|---|
| `MECHANICAL_VERIFIED` | Proven by deterministic tool execution (test pass, compiler output, SHA-256 hash match, out-of-worktree holdout suite). | Full authority |
| `OBSERVATIONAL_TELEMETRY` | Operational runtime logs, execution metrics, timing data, and sensor readings. | Informational / Contextual |
| `UNVERIFIED_PROSE` | Human or LLM subjective assertions, future intent, untested hypotheses, and ungrounded self-reports. | Zero authority |

---

## 4. Execution Commands

### CLI Invocation via `g8s`
```bash
# Audit an active session handoff for epistemic purity
./bin/g8s epistemic audit --input plans/handoffs/session-handoff.md --json

# Decompose worker summary and verify evidence citations
./bin/g8s epistemic decompose --task <task-id> --threshold 0.85 --json

# Integrated CI check for pull requests
./bin/g8s f0 gate check --epistemic --input pr-description.md
```

### Script Execution
```bash
# Standalone epistemic audit runner
tools/ci_epistemic_audit.sh --file plans/handoffs/2026-10-22-v019-feature-map-blind-verify-handoff.md
```

---

## 5. JSON Verdict Schema

The audit emits an itemized claim assessment:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "schema_version": "epistemic-audit.v1",
  "verdict": "SOUND",
  "epistemic_purity_ratio": 0.92,
  "claims_total": 12,
  "tier_counts": {
    "MECHANICAL_VERIFIED": 11,
    "OBSERVATIONAL_TELEMETRY": 1,
    "UNVERIFIED_PROSE": 0
  },
  "claims": [
    {
      "claim_id": "CLM-001",
      "text": "The lease CAS operation guarantees mutual exclusion across workers.",
      "tier": "MECHANICAL_VERIFIED",
      "citation": "internal/queue/lease_test.go:TestQueueCAS_LeaseConflict",
      "status": "VALIDATED"
    },
    {
      "claim_id": "CLM-002",
      "text": "Task turnaround latency averaged 42ms during pilot dispatch.",
      "tier": "OBSERVATIONAL_TELEMETRY",
      "citation": "telemetry/runs/task-123.json:latency_ms",
      "status": "ACCEPTED_AS_OBSERVATION"
    }
  ],
  "violations_of_4_laws": [],
  "audited_at": "2026-10-22T13:47:00Z"
}
```

### Rejection / Inflated Claims Example
```json
{
  "schema_version": "epistemic-audit.v1",
  "verdict": "INFLATED",
  "epistemic_purity_ratio": 0.45,
  "claims_total": 4,
  "tier_counts": {
    "MECHANICAL_VERIFIED": 1,
    "OBSERVATIONAL_TELEMETRY": 1,
    "UNVERIFIED_PROSE": 2
  },
  "violations_of_4_laws": [
    {
      "law_number": 1,
      "name": "Telemetry is Observational Unless Mechanically Bound",
      "description": "Worker claimed zero bugs based solely on self-reported test summary without attaching exit code or test output artifact.",
      "offending_claim": "The fix is 100% complete and defect-free."
    }
  ],
  "summary": "Document failed epistemic purity threshold (0.45 < 0.80): unverified prose dominates acceptance claims.",
  "audited_at": "2026-10-22T13:47:30Z"
}
```

---

## 6. F0 -> F1 -> F2 Delegation Contract

- **F0 (Supervisor Tier)**:
  - Submits handoffs, release notes, and worker outputs to the epistemic audit gate before approval.
  - Automatically rejects PRs with `INFLATED` verdicts or violations of the 4 Epistemic Laws.
- **F1 (Worker Tier - Epistemic Auditor)**:
  - Ingests prose, runs deterministic NLP/pattern claim extraction.
  - Resolves cited files, test functions, and hashes in the worktree.
  - Computes `epistemic_purity_ratio` and checks rule compliance.
- **F2 (Subagent Tier - Citation Resolver)**:
  - Verifies exact line references, commit SHAs, and test function signatures on disk.
  - Flags dead citations or citations that do not support the claimed assertion.
