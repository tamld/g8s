# OpenSpec DELTA-34: F0/F1/F2 Digest-Pinned Skill Hydration, Typed FailureDiagnosis & CAS Head Delivery

* **Specification ID**: `DELTA-34`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/brief`, `internal/mcp`, `internal/routing`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **Unverified Skill Injection Across F0/F1/F2 Tiers**: While `F0` supervisors have direct filesystem access to `~/.agents/skills/` and `skills/`, `F1` (`g8s worker` / `g8s llm-call`) and `F2` subagents lacked a digest-pinned, tier-enforced mechanism to discover and hydrate `SKILL.md` contracts (`skill://g8s/<tier>/<name>`) without risking TOCTOU tampering or leaking `F0`-exclusive supervisor skills into `F1`/`F2`.
2. **Untyped Model Failure Quarantine**: Rolling model scorecards (`internal/routing/scorecard.go`) previously treated transient HTTP `429` rate limits identically to structural model capability failures (`stagnation`, `code_error`, `test_failure`), risking premature quarantine of healthy models during temporary rate-limit bursts.
3. **Blind Worktree Delivery**: `g8s deliver` copied worktree artifacts onto the target repository without an optional compare-and-swap (CAS) check against `git rev-parse HEAD`, risking silent overwrites when the target repository `HEAD` advanced concurrently.

---

## 2. Normative Requirements

### R1 — Digest-Pinned Skill Resources & F0/F1/F2 Tier Containment (`internal/brief`, `internal/mcp`, `cmd/g8s`)
- `internal/brief` MUST compute a canonical `sha256:<hex>` digest for every `SKILL.md` manifest (`skill://g8s/f0/<name>` for `g8s-supervisor`, `skill://g8s/shared/<name>` for shared verification/execution skills).
- `ListVerifiedSkills` and `ReadVerifiedSkill` MUST filter out `F0`-tier skills when `G8S_TIER` is `F1` or `F2`, returning `E_SKILL_TIER_FORBIDDEN` on direct read attempts.
- `ReadVerifiedSkill(nameOrURI, expectedDigest, ...)` MUST fail closed with `E_SKILL_DIGEST_MISMATCH` whenever `expectedDigest` is non-empty and does not match the on-disk `SKILL.md` SHA-256 digest (TOCTOU guard).
- `internal/mcp` MUST expose `g8s_skills_list` and `g8s_skills_read` (expanding the MCP tool surface to 21 tools), and `cmd/g8s/llm_call.go` MUST support `--skills` (and `G8S_ACTIVE_SKILLS`) pre-execution skill hydration.

### R2 — Typed `FailureDiagnosis` & Rate-Limit Backoff (`internal/routing`)
- `ClassifyFailureDiagnosis(errReason, stagnated, verifyFailed)` MUST classify failures into `rate_limit`, `transient_network`, `timeout`, `auth`, `permission`, `stagnation`, `test_failure`, `code_error`, or `unknown`.
- `ModelOutcomeRecord` and `ModelQualityScore` MUST persist `failure_diagnosis`, `last_failure_diagnosis`, and `failure_breakdown`.
- When a previously proven model (`SuccessCount > 0`) experiences consecutive failures composed exclusively of `rate_limit` (`429`), `evaluateModelWindow` MUST classify it as `DEGRADED` (`BACKOFF_RATE_LIMIT`) rather than `QUARANTINED`.

### R3 — CAS Target `HEAD` Verification on `g8s deliver` (`cmd/g8s/deliver.go`)
- `g8s deliver` MUST accept `--expected-head-oid <sha>` (or `G8S_EXPECTED_HEAD_OID`) and verify `git rev-parse HEAD` in the target repository before staging or renaming any files, failing closed with `E_CONFLICT` (`STALE_HEAD`) on mismatch.

---

## 3. Verification Scenarios

#### Scenario: Digest-Pinned Skill Verification and F0/F1/F2 Tier Containment
<!-- tests: TestReadVerifiedSkillDigestPinningAndTierContainment, TestMCPSkillsListAndReadTools -->
- **GIVEN** a skill bank containing `g8s-red-prove` (`shared`) and `g8s-supervisor` (`F0`)
- **WHEN** `ReadVerifiedSkill` or `g8s_skills_read` is called with a valid or mismatched `expected_digest`, or from `G8S_TIER=F1`
- **THEN** matching digests return the verified `SkillResource`, mismatched digests fail closed with `E_SKILL_DIGEST_MISMATCH`, and `F1`/`F2` callers are blocked from `F0` skills with `E_SKILL_TIER_FORBIDDEN`.

#### Scenario: Typed FailureDiagnosis and Rate-Limit Backoff Without False Quarantine
<!-- tests: TestClassifyFailureDiagnosisAndRateLimitBackoff, TestComputeScorecardAndDynamicQuarantine -->
- **GIVEN** a rolling model quality ledger recording execution outcomes across `9router` models
- **WHEN** `ClassifyFailureDiagnosis` and `ComputeScorecard` evaluate capability failures versus pure HTTP `429` rate-limit bursts
- **THEN** capability failures trigger `QUARANTINED` (`DEMOTE_OR_FALLBACK`), whereas pure `429` bursts on a proven model report `DEGRADED` (`BACKOFF_RATE_LIMIT`) with accurate `failure_breakdown` counts.

#### Scenario: CAS Head Verification on Deliverable Application
<!-- tests: TestDeliverExpectedHeadOIDConflictAndMatch -->
- **GIVEN** a completed worktree task and a target git repository at commit `actualHead`
- **WHEN** `g8s deliver` is invoked with a mismatched `--expected-head-oid` and then with `actualHead`
- **THEN** the mismatched invocation fails closed with `STALE_HEAD` (`E_CONFLICT`) without mutating the workspace, and the matching invocation applies the deliverable cleanly.
