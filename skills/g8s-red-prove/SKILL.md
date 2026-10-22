---
name: g8s-red-prove
description: "Mechanical Red -> Green reproduction + property/mutation proof + Seeded Mutant Canary verification (--canary-mutant) to eliminate vacuous tests and phantom bugfixes."
domain: verification
version: 1.0.0
last_updated: "2026-10-22"
risk_level: medium
evidence_level: runtime_required
args_syntax: "g8s-red-prove [repro|prove|canary] --repro <test> [--patch <file>] [--canary-mutant <file>] [--json]"
---

# g8s Red Prove (`g8s-red-prove`)

The **Red Prove** skill enforces strict mechanical reproduction and falsification proof before any fix is accepted. It requires a distinct two-stage `Red -> Green` verification cycle and confirms test non-vacuity via Seeded Mutant Canary (`--canary-mutant`) injection.

---

## 1. Purpose & Threat Defeated

### Purpose
To mechanically prove that a defect exists (Red), that a candidate patch genuinely cures the root cause (Green), and that the accompanying tests are sensitive enough to catch regressions by detecting deliberate mutant corruptions.

### Threats Defeated
1. **Vacuous Tests**: Unit tests that pass unconditionally due to missing assertions, silent `nil` checks, or misconfigured mocks.
2. **Phantom Bugfixes**: Code changes that suppress errors, delete failing assertions, or modify production code without addressing the root invariant failure.
3. **Tests-That-Pass-Without-Fix**: Tests written after the fix that inadvertently test a tautology or unrelated path.

---

## 2. Deterministic Inputs

Verification requires four deterministic inputs:
1. **Baseline State**: Clean base commit or unpatched worktree.
2. **Reproduction Test**: Isolated test function or executable test script demonstrating the invariant failure.
3. **Candidate Patch**: Proposed bugfix / implementation patch.
4. **Seeded Canary Mutant**: A deliberate synthetic defect (`--canary-mutant`) designed to verify that the reproduction suite actually fails when the invariant is broken.

---

## 3. Execution Commands

### CLI Verification via `g8s`
```bash
# Execute two-stage Red -> Green reproduction proof
./bin/g8s verify --task <task-id> --red-green --json

# Run seeded mutant canary check to ensure test sensitivity
./bin/g8s verify --task <task-id> --canary-mutant internal/verifier/canary_sample.go --json

# Combined red-prove and mutation proof gate
./bin/g8s verify --red-green --canary-mutant <file> --json
```

### Script Execution
```bash
# Run isolated red-prove harness against target Go test
tools/ci_red_prove.sh --package ./internal/queue --test TestQueueCAS_LeaseConflict
```

---

## 4. JSON Verdict Schema

The verification engine produces a structured proof verdict:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "schema_version": "red-prove-verdict.v1",
  "verdict": "PROVED",
  "stage_1_red": {
    "status": "PASSED_FAILING",
    "exit_code": 1,
    "failure_output": "lease_test.go:42: expected error ErrLeaseConflict, got nil"
  },
  "stage_2_green": {
    "status": "PASSED_GREEN",
    "exit_code": 0,
    "pass_output": "PASS: TestQueueCAS_LeaseConflict (0.04s)"
  },
  "canary_mutant_killed": true,
  "mutation_score": 1.0,
  "mutants_evaluated": 1,
  "mutants_killed": 1,
  "repro_test_name": "TestQueueCAS_LeaseConflict",
  "evidence_citations": [
    "internal/queue/lease_test.go#L35-L55",
    "internal/queue/lease.go#L90-L115"
  ],
  "verified_at": "2026-10-22T13:46:00Z"
}
```

### Unproved / Vacuous Test Example
```json
{
  "schema_version": "red-prove-verdict.v1",
  "verdict": "UNPROVED",
  "rejection_reason": "VETO_CANARY_SURVIVED",
  "stage_1_red": {
    "status": "PASSED_FAILING",
    "exit_code": 1
  },
  "stage_2_green": {
    "status": "PASSED_GREEN",
    "exit_code": 0
  },
  "canary_mutant_killed": false,
  "mutation_score": 0.0,
  "summary": "Seeded canary mutant survived: test suite passed despite deliberate invariant corruption.",
  "evidence_citations": [
    "internal/queue/lease_test.go#L35"
  ],
  "verified_at": "2026-10-22T13:46:30Z"
}
```

---

## 5. F0 -> F1 -> F2 Delegation Contract

- **F0 (Supervisor Tier)**:
  - Defines the reproduction condition and canary requirement in the task brief.
  - Demands `red-prove-verdict.v1` with `canary_mutant_killed == true` before admitting bugfix merges.
- **F1 (Worker Tier - Proof Runner)**:
  - Reverts fix, runs repro test to prove `Stage 1 Red` (`exit_code != 0`).
  - Applies candidate fix, runs repro test to prove `Stage 2 Green` (`exit_code == 0`).
  - Injects synthetic mutant into ephemeral worktree copy and confirms canary is killed.
- **F2 (Subagent Tier - Mutation Fuzzer)**:
  - Invoked by F1 to generate subtle boundary mutants (e.g. flipping `<` to `<=`, stripping lock acquisition, replacing error returns with `nil`).
  - Ensures test suite is resilient and non-vacuous across edge mutations.
