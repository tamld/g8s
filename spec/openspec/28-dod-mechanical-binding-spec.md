# OpenSpec DELTA-28: Definition of Done Mechanical Evidence Binding (#648)

* **Specification ID**: `DELTA-28`
* **Title**: Definition of Done Mechanical Evidence Binding
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Milestone**: `M6` (Verifier Binding & Evidence Lake)
* **Target Packages**: `internal/verifier`, `cmd/g8s`
* **Issue**: `#648`

## Background & Problem Statement

Prior verification logic permitted Definition of Done (DoD) criteria to be marked `MET` based on unverified worker self-reports, aggregate check success, or artifact file existence without binding to concrete mechanical evidence. Specifically:
1. `IsMechanicallyVerifiedEvidence` accepted bare words such as `"ok"`, `"pass"`, `"test"`, and substrings such as `"not passed"` (matching `"pass"`).
2. `EvaluateDoDMatrix` marked all declared criteria as `MET` whenever aggregate `checksOK` was true, even when worker prose lacked mechanical backing or when unrelated checks succeeded without executing checks for declared criteria.
3. Artifact files on disk (even outside the worktree via `..` traversal or symlinks) could substitute for executed checks, enabling a passing unrelated check to mark a criterion `MET` with zero mechanical check binding.
4. Fuzzy substring matching allowed collisions such as `./pkg/security-old` satisfying a criterion for `./pkg/security`, and generic type-only hints (e.g. `gotest`) satisfied arbitrary criteria.
5. Worker-reported items could claim arbitrary criteria completion using fabricated hashes or prose anchors.

## Frozen Acceptance Rules & Invariants

1. **Zero-Trust for Worker Self-Reports**: Worker prose, bare words (`ok`, `pass`, `test`), hash-shaped tokens, invented or missing artifacts, and mismatched or stale references NEVER prove DoD criteria.
2. **Authoritative Declared Hints**: Declared `BriefDoDItem.VerifierHint` is the sole binding authority. Worker-reported evidence strings are never authoritative for check or artifact binding.
3. **Mechanical Failure Is Fatal**: If aggregate mechanical checks fail (`checksOK == false`, holdout checks fail, or canary mutants survive), all DoD items are unconditionally evaluated as `UNMET`.
4. **Invocation-Local Evidence Binding**: Positive criterion status (`MET`) MUST bind to trusted, verifier-generated check records from THIS verification invocation (matching exact check type and target, or declared exact target).
5. **Exact Target Matching (No Fuzzy Substrings / No Type-Only Hints)**: Check targets must match exactly after path normalization (`./pkg/security` does NOT match `./pkg/security-old`). Generic type-only hints (e.g. `gotest`, `script`) without an exact target are strictly rejected.
6. **Artifacts Are Supporting Evidence Only**: Artifact existence or SHA-256 hash alone must NEVER substitute for a declared matching check. Every explicit criterion requires an exact trusted check binding; artifacts serve solely as supporting evidence.
7. **Trusted Root & Artifact Confinement**: Artifact paths must be relative to a trusted root (`evidence.WorktreeDir` or `evidence.RepoRoot`). Absolute paths, directory traversal (`..`), and symlinks resolving outside the trusted root are strictly rejected.
8. **Multiple Required Hints (AND Conjunction)**: When multiple hints are declared in a criterion, all required hints must have matching passing checks and verified artifacts (AND logic, not OR logic).
9. **Fail-Closed Legacy Calls**: Legacy evaluation calls lacking trusted execution check records fail closed (`dodOK = false`, all criteria `UNMET`) when explicit DoD criteria are declared.
10. **Automated Fallback for Tasks Without Criteria**: Tasks without explicit criteria preserve automated fallback based on actual mechanical check results: passing if and only if actual mechanical checks pass.

## Precise Verifier Hint Syntax

A criterion declared in the brief DoD markdown (e.g. `- [ ] D1: Description [hint: ...]`) may declare one or more hint tokens separated by comma (`,`) or semicolon (`;`):

1. **Exact Check Type and Target**:
   - Syntax: `<type>:<target>`
   - Examples: `gotest:./internal/verifier`, `script:tools/check.sh`, `diffguard:internal/verifier/verifier.go`
   - Rules: `<type>` must be a valid check type (`go-test`/`gotest`, `script`, `diff-guard`/`diffguard`, `holdout`, `canary`). `<target>` must be non-empty and must match the executed check target exactly after path normalization (`clean(pattern) == clean(target)`).
2. **Declared Exact Target (Type Omitted)**:
   - Syntax: `<target>`
   - Examples: `./internal/verifier`, `tools/check.sh`
   - Rules: Target must match an executed check target exactly. Bare check types without a target are rejected as type-only hints.
3. **Supporting Artifact Evidence**:
   - Syntax: `file:<relative-path>` or `artifact:<relative-path>`
   - Examples: `file:docs/schema.json`, `artifact:report.json`
   - Rules: Path must be strictly relative to the trusted root. Absolute paths, `..` traversal, and escaping symlinks are rejected. Artifact existence alone cannot satisfy a criterion; an exact matching check is also required.
4. **Multiple Hints Conjunction**:
   - Syntax: `<hint1>, <hint2>` or `<hint1>; <hint2>`
   - Examples: `gotest:./internal/verifier, file:docs/schema.json`
   - Rules: All tokens must pass. If any check or artifact fails or is missing, the entire criterion evaluates as `UNMET`.

## Scenarios & Mechanical Test Pins

#### Scenario: Worker self-report prose or bare words are rejected
<!-- tests: TestDoDMechanicalBinding_RejectsWorkerProseAndBareWords, TestVerifyCLI_DoDMechanicalBinding_RejectsBareProse -->
- Given a DoD item `D1` and worker reported evidence `"all tests pass"`, `"ok"`, `"pass"`, or `"exit code 0"` without matching trusted check records.
- When DoD evaluation runs.
- Then `D1` is marked `UNMET` with evidence recording that worker prose was rejected.

#### Scenario: Mechanical check failure forces all DoD items UNMET
<!-- tests: TestDoDMechanicalBinding_Negative_ChecksOKFalse -->
- Given explicit DoD items and `checksOK == false`.
- When DoD evaluation runs.
- Then every DoD item is evaluated as `UNMET` regardless of worker claims or individual check successes.

#### Scenario: Legacy no-record evaluation fails closed for explicit DoD
<!-- tests: TestDoDMechanicalBinding_LegacyNoRecordCallFailsClosed -->
- Given explicit DoD items and a legacy evaluation call without trusted check records.
- When `EvaluateDoDMatrix` runs.
- Then evaluation returns `ok = false` and all criteria marked `UNMET`.

#### Scenario: Unrelated check success does not satisfy unexecuted criteria
<!-- tests: TestDoDMechanicalBinding_PositiveTwoDistinctChecks, TestVerifyCLI_DoDMechanicalBinding_UnrelatedCheckDoesNotSatisfy -->
- Given criterion `D1` targeting `./internal/verifier` and criterion `D2` targeting `./cmd/g8s`.
- When only check for `./internal/verifier` executes and passes.
- Then `D1` is marked `MET` and `D2` is marked `UNMET`, causing overall DoD evaluation to fail.

#### Scenario: Automated fallback for tasks without explicit criteria
<!-- tests: TestDoDMechanicalBinding_AutomatedFallbackWithoutExplicitCriteria -->
- Given a task with no declared DoD items (`len(items) == 0`) and no worker reported items.
- When mechanical checks pass (`checksOK == true`).
- Then evaluation passes (`ok = true`, `matrix = nil`).
- When mechanical checks fail (`checksOK == false`).
- Then evaluation fails (`ok = false`).

#### Scenario: Valid criterion successfully binds to passing check record
<!-- tests: TestDoDMechanicalBinding_PositiveCheckBinding, TestVerifyCLI_DoDMechanicalBinding_Positive -->
- Given criterion `D1` with hint `gotest:./internal/verifier`.
- When check `gotest:./internal/verifier` executes with `OK = true` in this invocation.
- Then `D1` is marked `MET` with evidence binding to the check type, target, task ID, attempt, and invocation log SHA.

#### Scenario: Two criteria with distinct exact checks both pass
<!-- tests: TestDoDMechanicalBinding_PositiveTwoDistinctChecks, TestVerifyCLI_DoDMechanicalBinding_PositiveTwoDistinctChecks -->
- Given criterion `D1` with hint `gotest:./internal/verifier` and criterion `D2` with hint `gotest:./cmd/g8s` (or `./internal/pathutil`).
- When both distinct checks execute and pass.
- Then both criteria are marked `MET` and overall DoD evaluation passes.

#### Scenario: Multiple required hints all must pass (AND conjunction)
<!-- tests: TestDoDMechanicalBinding_MultipleRequiredHintsAllMustPass -->
- Given criterion `D1` with compound hint `gotest:./pkg/one, gotest:./pkg/two`.
- When only `./pkg/one` executes.
- Then `D1` is marked `UNMET`.
- When both `./pkg/one` and `./pkg/two` execute and pass.
- Then `D1` is marked `MET` citing both check executions.

#### Scenario: Supporting artifact verified alongside exact matching check
<!-- tests: TestDoDMechanicalBinding_PositiveArtifactSupportingEvidence -->
- Given criterion `D1` with hint `gotest:./internal/verifier, file:docs/schema.json`.
- When check `./internal/verifier` passes and `docs/schema.json` is verified on disk inside the worktree with matching SHA-256.
- Then `D1` is marked `MET` citing both the bound check and verified disk artifact.

#### Scenario: Arbitrary file with correct hash alone cannot substitute matching check
<!-- tests: TestDoDMechanicalBinding_Negative_ArbitraryFileEvenWithCorrectHash, TestVerifyCLI_DoDMechanicalBinding_Negative_ArbitraryFileWithHashRejected -->
- Given criterion `D1` with artifact hint `file:docs/schema.json`.
- When the file exists on disk with authentic SHA-256, but no matching check is bound to `D1`.
- Then `D1` is marked `UNMET` because artifact existence cannot substitute for an exact matching check.

#### Scenario: External absolute artifact path rejected
<!-- tests: TestDoDMechanicalBinding_Negative_ExternalAbsolute -->
- Given criterion `D1` with hint `file:/etc/passwd` or `file:/tmp/outside.md`.
- When DoD evaluation runs.
- Then `D1` is marked `UNMET` rejecting absolute paths outside the trusted root.

#### Scenario: Directory traversal in artifact path rejected
<!-- tests: TestDoDMechanicalBinding_Negative_Traversal, TestVerifyCLI_DoDMechanicalBinding_Negative_ArtifactTraversalRejected -->
- Given criterion `D1` with hint `file:../outside.md`.
- When DoD evaluation runs.
- Then `D1` is marked `UNMET` rejecting directory traversal outside the trusted root.

#### Scenario: Symlink escape outside trusted root rejected
<!-- tests: TestDoDMechanicalBinding_Negative_SymlinkEscape -->
- Given criterion `D1` with hint `file:escape.txt` pointing to a symlink whose target is outside the worktree.
- When DoD evaluation runs.
- Then `D1` is marked `UNMET` rejecting symlinks that escape the trusted root.

#### Scenario: Substring target collision rejected
<!-- tests: TestDoDMechanicalBinding_Negative_SubstringTargetCollision -->
- Given criterion `D1` with hint `gotest:./pkg/security`.
- When check `./pkg/security-old` executes and passes.
- Then `D1` is marked `UNMET` because exact target matching prevents substring collision.

#### Scenario: Generic type-only hint rejected
<!-- tests: TestDoDMechanicalBinding_Negative_TypeOnlyHint -->
- Given criterion `D1` with hint `gotest`, `gotest:`, or `script`.
- When DoD evaluation runs.
- Then `D1` is marked `UNMET` because generic check types without an exact target cannot satisfy criteria.

#### Scenario: Mismatched or stale artifact hash rejected
<!-- tests: TestDoDMechanicalBinding_Negative_MismatchedStaleProvenance -->
- Given criterion `D1` with hint `gotest:./internal/verifier, file:docs/schema.json`.
- When worker reports a stale or mismatched SHA-256 hash.
- Then `D1` is marked `UNMET` rejecting the stale provenance.

#### Scenario: Worker evidence strings not authoritative for binding
<!-- tests: TestDoDMechanicalBinding_WorkerEvidenceNotAuthoritative -->
- Given criterion `D1` with hint `gotest:./internal/security`.
- When worker reports imitation evidence claiming check `gotest:./internal/other` and only `./internal/other` executed.
- Then `D1` is marked `UNMET` because declared brief hints are authoritative.

#### Scenario: End-to-end verifier DoD binding execution
<!-- tests: TestDoDMechanicalBinding_EndToEndVerifyDoDBinding -->
- Given a task target with explicit DoD criteria verified via `v.Verify`.
- When all required checks execute and pass.
- Then the resulting `Verdict.DoDMatrix` records authentic check bindings and `Outcome` is `PASS`.
