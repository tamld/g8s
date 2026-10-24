---
name: g8s-blind-verifier
description: "Double-blind counterexample & falsification review operating exclusively on BlindReviewPacket (SpecContract + Invariants + RawGitDiff + MechanicalTestOutput), stripping worker self-report and model identity to defeat reviewer sycophancy."
domain: testing
version: 1.0.1
last_updated: "2026-10-24"
risk_level: medium
evidence_level: runtime_required
args_syntax: "g8s-blind-verifier [review|falsify|audit] --packet <path-or-json> [--task <id>] [--json]"
---

# g8s Blind Verifier (`g8s-blind-verifier`)

The **Blind Verifier** executes an adversarial, double-blind falsification review on incoming code mutations. It operates strictly on a sanitized **`BlindReviewPacket`**, evaluating changes without knowledge of the author model, human identity, commit messages, or self-reported summary prose.

---

## 1. Purpose & Threat Defeated

### Purpose
To deliver an objective, uncompromised verification verdict by actively attempting to falsify candidate mutations against formal specifications and architectural invariants.

### Threats Defeated
1. **Reviewer Sycophancy**: Large Language Models acting as code reviewers tend to agree with persuasive self-reports, polished summaries, or prior authority statements rather than scrutinizing the diff.
2. **AI Evaluation Awareness & Test Overfitting**: Workers optimize code solely to pass known in-tree tests while leaving subtle invariant holes.
3. **Persuasive Self-Report Bias**: Reliance on worker-authored `ReportedDoD` or prose summaries (`worker.Summary`) rather than raw diffs and mechanical test execution.

---

## 2. Deterministic Inputs

The verifier consumes **only** the canonical `BlindReviewPacket` schema. Any request containing unredacted identity or unverified self-report metadata is rejected fail-closed.

### Included in `BlindReviewPacket`
- **Spec Contract**: Persona stories, SRS requirements, and target OpenSpec deltas.
- **Architectural Invariants**: Explicit invariants defined in `.g8s/feature-map.yml`, `spec/constitution.md`, and `.g8s/trust-boundaries.yml`.
- **Raw Git Diff**: The unadulterated unified diff of the candidate mutation (`git diff <base>..<head>`).
- **Mechanical Test Outputs**: Deterministic test runner output, coverage metrics, and out-of-worktree holdout test results.

### Strictly Excluded (Sanitized / Redacted)
- Worker model identity (e.g., `claude-3-5`, `gpt-4o`, `gemini-1.5`).
- Worker / author account name or ID.
- Worker-authored `Summary`, `ReportedDoD`, or commentary.
- Git commit messages and branch naming cues.
- Supervisor (`F0`) conversational opinions or hints.

---

## 3. Execution Commands

### CLI Verification via `g8s`
```bash
# Verify a completed task using double-blind mode
./bin/g8s verify --task <task-id> --blind --json

# Run blind verification on an exported BlindReviewPacket
./bin/g8s verify-blind --packet /path/to/packet.json --json

# Integrated F0 verification gate check
./bin/g8s f0 gate check --task <task-id> --mode blind --json
```

### Direct Script / Harness Invocation
```bash
# Run standalone blind verifier runner against a diff and spec
tools/verify_blind.sh --spec spec/openspec/delta-25.md --diff candidate.patch --test-output tests.log --json
```

---

## 4. JSON Verdict Schema

The verifier emits a deterministic JSON payload:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "schema_version": "blind-verdict.v1",
  "verdict": "ACCEPTED",
  "confidence": 0.98,
  "summary": "Mutation satisfies all specification criteria and preserves invariants under fuzzing.",
  "falsification_vector": "none",
  "counterexample": null,
  "invariant_violations": [],
  "evidence_citations": [
    "spec/openspec/delta-25.md#L45-L60",
    ".g8s/trust-boundaries.yml#L12-L18"
  ],
  "holdout_passed": true,
  "verified_at": "2026-10-22T13:45:00Z"
}
```

### Rejection Example
```json
{
  "schema_version": "blind-verdict.v1",
  "verdict": "REJECTED",
  "confidence": 0.95,
  "summary": "State transition race condition detected during concurrent worker lease acquisition.",
  "falsification_vector": "Concurrent lease claim with identical timestamp causes dual allocation.",
  "counterexample": "func TestFalsifyLeaseRace(t *testing.T) { /* ... concurrent CAS failure ... */ }",
  "invariant_violations": [
    {
      "invariant_id": "INV-LEASE-SINGLE-WINNER",
      "description": "Exactly one worker may hold an active lease for task ID at any time.",
      "source_file": "internal/queue/lease.go",
      "line_range": "88-104"
    }
  ],
  "evidence_citations": [
    "internal/queue/lease_test.go#L112"
  ],
  "holdout_passed": false,
  "verified_at": "2026-10-22T13:45:30Z"
}
```

---

## 5. F0 -> F1 -> F2 Delegation Contract

- **F0 (Supervisor Tier)**:
  - Generates the sanitized `BlindReviewPacket` by stripping all worker self-reports and identities.
  - Mounts the out-of-worktree holdout test suite (`$G8S_STATE_DIR/holdout/<feature-id>`).
  - Dispatches F1 Verifier Worker with single-use, read-only Write Receipt.
- **F1 (Worker Tier - Blind Verifier)**:
  - Runs in an isolated worktree with no network and no access to author metadata.
  - Analyzes diff against invariant rules and executes test suites.
  - Delegates counterexample construction to F2 subagents.
- **F2 (Subagent Tier - Adversarial Probe)**:
  - Spawned recursively by F1 (`invoke_subagent`).
  - Synthesizes targeted edge-case unit tests and counterexample proofs attempting to break the patch.
  - Reports counterexample evidence back to F1 for compilation into the final `blind-verdict.v1` receipt.
