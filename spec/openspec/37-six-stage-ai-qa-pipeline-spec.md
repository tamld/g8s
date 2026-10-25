# OpenSpec DELTA-37: End-to-End 6-Stage AI QA Pipeline (BA-to-Spec Matrix, Holdout Partition, and Epistemic QA Report)

* **Specification ID**: `DELTA-37`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/verifier`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **Unstructured BA Input Leading to Vacuous AI Tests**: When organizations replace manual QA with AI test generation without normalizing BA inputs into traceable `US-*`, `REQ-FUNC-*`, `AC-*` (`Given`/`When`/`Then`), and `FSM` state transitions, AI agents generate tautological tests that mirror implementation bugs.
2. **Holdout Contamination by Implementation Workers**: If `S3-M` (`implementer`) workers share write permissions with the QA acceptance test suite, agents modify or weaken tests to pass (`Reward Hacking`).
3. **Unverified Prose Masquerading as QA Sign-Off**: QA reports that do not classify each Acceptance Criterion into `MECHANICAL_VERIFIED`, `OBSERVATIONAL_TELEMETRY`, or `UNVERIFIED_PROSE` allow unverified self-reports to pass release gates.

---

## 2. Normative Requirements

### R1 — Stage 1 & Stage 2 BA-to-Spec Normalization & 12D Scenario Matrix (`internal/verifier/qapipeline.go`)
- `ValidateBASpecContract(specText)` MUST verify the presence of `US-*`, `REQ-FUNC-*`/`FR-*`, `AC-*`, `Given`/`When`/`Then` clauses, and `FSM` state transitions, and compute coverage across the 12 canonical scenario dimensions (`CanonicalScenarioDimensions`).

### R2 — Stage 3 Holdout Partition Disjointness (`VerifyHoldoutDisjointness`)
- `VerifyHoldoutDisjointness(holdoutPaths, allowedWritePaths)` MUST reject any overlap (including wildcard `*` / `**`) between `S3-M` worker `allowedWritePaths` and `holdoutPaths` with `E_HOLDOUT_CONTAMINATION`.

### R3 — Stage 4..6 Canary Gate & Epistemic QA Report (`EvaluateQAPipeline`, `g8s qa-report`)
- `EvaluateQAPipeline` MUST fail closed (`Passed: false`, `Verdict: "FAIL"`) if Stage 1/2 BA normalization fails, Stage 3 Holdout Disjointness fails, Stage 4 Seeded Mutant Canary (`VETO_CANARY_SURVIVED`) fails, Stage 5 checks fail, or Stage 6 leaves any `AC-*` claim in `UNVERIFIED_PROSE`.

---

## 3. Verification Scenarios

#### Scenario: BA-to-Spec Contract Normalization and Holdout Partition Isolation
<!-- tests: TestValidateBASpecContractAndScenarioMatrix, TestVerifyHoldoutDisjointnessBlocksContamination -->
- **GIVEN** a BA/QA specification and worker `allowed_paths` vs. `holdout_paths`
- **WHEN** `ValidateBASpecContract` and `VerifyHoldoutDisjointness` are evaluated
- **THEN** specs missing `US-*`, `REQ-FUNC-*`, `AC-*`, `Given/When/Then`, or `FSM` transitions fail closed, and any `allowed_paths` overlapping `holdout_paths` is rejected with `E_HOLDOUT_CONTAMINATION`.

#### Scenario: Six-Stage Epistemic QA Pipeline Fails Closed on Unverified Prose or Surviving Mutant Canary
<!-- tests: TestEvaluateQAPipelineEnforcesSixStagesAndEpistemicAudit -->
- **GIVEN** a 6-stage `QAPipelineRequest` with `AC-01` and `AC-02`
- **WHEN** `EvaluateQAPipeline` runs with full mechanical/telemetry bindings and killed canary versus missing `AC-02` binding or surviving mutant canary
- **THEN** the complete pipeline returns `Verdict: "PASS"` with `UnverifiedProseCount == 0`, while missing bindings or surviving mutants fail closed with `Verdict: "FAIL"`.
