# 22 — Brief DoR Floor + Advisory Skill Routing Spec (DELTA-22, #398)

**Status**: `PROPOSED` (implements the ratified 2026-09-26 strategy split)

## ADDED Requirements

### Requirement: L2 brief quality gate has a deterministic blocking floor

Brief issuance runs mechanical DoR floor checks that BLOCK a malformed brief
with no Jev and no broker dependency: goal present (non-empty title), scope
files listed (the payload declares at least one concrete file path), DoD
present, and a receipt reference present when the dispatch permission is
`workspace_write`. Checks are table-driven; every check has its own test; a
floor failure is a usage error listing every failed check.

#### Scenario: missing scope blocks issuance
<!-- tests: TestFloorScopeFilesListed -->
- A brief whose payload lists no file path fails the `scope_files_listed`
  check and issuance exits with a usage error naming it.

#### Scenario: workspace_write without receipt reference blocks issuance
<!-- tests: TestFloorReceiptPathForWorkspaceWrite -->
- A `workspace_write` brief whose payload/DoD references no receipt fails
  `receipt_path_present`.

### Requirement: L4 skill routing is advisory-only in v1

Brief issue envelopes may carry a `skill_suggestions` field computed by a
deterministic keyword/manifest match over the operator-local skill bank
(`~/.agents/skills/`). Suggestions are display-only — no code path enforces
them, and consumption semantics are unchanged. An empty or unreadable skill
bank yields no suggestions and no error.

#### Scenario: keyword match suggests a skill
<!-- tests: TestSuggestSkillsKeywordMatch -->
- A brief titled "review the auth diff" suggests the matching review skill by
  manifest keyword overlap.

#### Scenario: empty skill bank degrades silently
<!-- tests: TestSuggestSkillsEmptyBankDegrades -->
- With no skill bank on disk, the envelope carries an empty suggestions list
  and issuance succeeds.

### Requirement: L2 Jev-judged quality is feature-flagged behind broker availability

The Jev-judged "is this brief well-formed for this situation?" check is
advisory only and disabled unless a Context-Broker-backed hook is wired
(`G8S_BRIEF_JEV_QUALITY=1` AND a non-nil hook). Cold-start Jev scoring without
context is forbidden (ADR-0021 §8 anti-pattern).

#### Scenario: flag off by default
<!-- tests: TestApplyJevQualityFlagOffNeverCallsHook -->
- Default issuance never invokes the Jev quality hook.
