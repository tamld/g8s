# OpenSpec DELTA-35: Mandatory Pre-Execution Research, CVE & Socratic Debate Gate and 10-Role Taxonomy

* **Specification ID**: `DELTA-35`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/brief`, `internal/harness`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **Shallow Execution Without World-State / CVE Grounding**: Coding agents frequently jump into implementation relying on stale training-cutoff memory, missing real-world CVEs/CWEs, platform binary packaging requirements, or DOM side-effects (as observed during `TuneFlow` dogfooding).
2. **Absence of Pre-Execution Socratic Falsification**: Without a mechanical pre-execution check requiring competing hypotheses and explicit counterexamples/falsification in `Lane F`, `Lane R`, and `Lane S` briefs, single-hypothesis plans pass into `workspace_write` execution unchallenged.
3. **Coarse 6-Role Taxonomy**: Overloading `test-runner` for both feature implementation and test execution conflated `S3-M` code mutation with verification, and lacked dedicated `researcher`, `implementer`, `sec-auditor`, and `analyst` profiles.

---

## 2. Normative Requirements

### R1 — Risk-Tiered Pre-Execution Research, CVE & Socratic Debate Gate (`internal/brief/floor.go`, `cmd/g8s/brief_issue.go`)
- `CheckDoRFloorForLane(title, payload, dod, permission, lane)` MUST exempt `lane` values `""`, `"0"`, and `"D"` from the heavy pre-execution gate so trivial documentation and config tasks incur zero ceremony overhead.
- When `lane` is `"F"` (Feature), `"R"` (Refactor), or `"S"` (Security/Hardening), `CheckDoRFloorForLane` MUST fail closed unless all three additional mechanical checks pass:
  1. `research_world_state_present`: cites world-state / upstream / local / CVE / CWE research (`Local:`, `Upstream:`, `Docs:`, `CVE-YYYY-NNNN`, `CWE-NNN`, etc.), with `Lane S` additionally requiring explicit security/CVE/CWE/STRIDE/OWASP/trust-boundary evidence.
  2. `socratic_debate_present`: documents both a hypothesis/thesis (`Hypothesis`, `H1`, `H2`, `Thesis`) and an adversarial falsification/counterexample (`Counterexample`, `Antithesis`, `Falsification`, `Alternative Rejected`, `Trade-off`, `Red-Team`).
  3. `spec_contract_present`: cites formal specification anchors (`PRD`, `SRS`, `AC-*`, `REQ-*`, `US-*`, `FSM`, `ADR-*`, or `DELTA-*`).
- `cmd/g8s/brief_issue.go` MUST expose `--lane <0|D|R|F|S>` (with fallback to `G8S_GATE_LANE`).

### R2 — Canonical 10-Role Harness Taxonomy (`internal/harness/roles.go`, `manifest.json`)
- `internal/harness.Roles` MUST preserve all 6 baseline roles (`collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`) and register 4 specialized roles:
  - `researcher` (`read_only`, `S3-A`, skills: `g8s-epistemic-audit`, `ck:xia`, `ck:research`)
  - `implementer` (`workspace_write`, `S3-M`, skills: `g8s-red-prove`, `ck:cook`, `code-simplification`)
  - `sec-auditor` (`read_only`, `S3-A`, skills: `g8s-sec-gate`, `ck:security`, `g8s-blind-verifier`)
  - `analyst` (`read_only`, `S3-A`, skills: `g8s-scenario-matrix`, `fable-thinking`, `wiki-business-analyst`)

---

## 3. Verification Scenarios

#### Scenario: Risk-Tiered Pre-Execution Research, CVE and Socratic Debate Gate by ALDC Lane
<!-- tests: TestFloorPreExecutionResearchDebateGateByLane -->
- **GIVEN** a brief draft evaluated under lanes `0`, `D`, `F`, `R`, and `S`
- **WHEN** `CheckDoRFloorForLane` runs against a minimal brief versus a brief containing World-State/CVE citations, Socratic hypothesis+falsification debate, and PRD/SRS/AC/FSM/DELTA references
- **THEN** lanes `0` and `D` pass without heavy gate ceremony, while lanes `F`, `R`, and `S` fail closed when any of `research_world_state_present`, `socratic_debate_present`, or `spec_contract_present` is missing and pass when all required evidence is present.

#### Scenario: Ten Canonical Role Profiles and Recommended Skill Metadata
<!-- tests: TestRolesAndPermissions, TestListRolesReturnsAllRegisteredProfiles -->
- **GIVEN** the `internal/harness` role registry and `g8s_list_roles` MCP tool
- **WHEN** `RoleNames()`, `GetRole()`, and `g8s_list_roles` are queried
- **THEN** all 10 roles (`collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`, `researcher`, `implementer`, `sec-auditor`, `analyst`) are returned with non-empty `default_permission`, `stratum`, and `recommended_skills`.
