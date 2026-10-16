# OpenSpec DELTA-24: Differential Governance, Self-Silencing Rules & Sovereign HITL Calibration (`#594`, `#595`, `#596`)

* **Specification ID**: `DELTA-24`
* **Title**: Differential Governance, Non-Blocking Spatial JIT Whisper, Pure-Go Net-Delta Diff Guard & Sovereign HITL Hypothesis Falsification Engine
* **Status**: `APPLIED`
* **Milestone**: `M6` (Governance Hardening & Empirical Falsification)
* **Target Packages**: `internal/vault`, `internal/fsops`, `internal/verifier`, `internal/telemetry`, `internal/worker`, `cmd/g8s`
* **Foundational Axioms**: Axiom 1 (Zero-CGO Pure-Go Binary), Axiom 2 (Capability-Scoped Write Receipts), ADR-0021 (Sensor != Decider), ADR-0032 (F0/F1/F2 Containment Tiers)

---

## 1. Context & Motivation

Mechanical pre-edit blockers inside an isolated worktree degrade LLM intelligence into lint-appeasement, while external LLM judges risk provider lock-in (`Jev`). Furthermore, dynamic rule theories (`H1`: Carrying Capacity $K_{\max} \le 3$, `H2`: Sine-Wave $W_t$ Dormancy & Re-Awakening, `H3`: Statistical Weight Regression) are **falsifiable hypotheses** rather than self-evident axioms, and automated test/git outcomes are only **Delayed Reality Proxies** rather than full Ground Truth.

`DELTA-24` implements a 100% Pure-Go **Differential Governance ("Trust, But Verify")** architecture anchored by **Sovereign Human-In-The-Loop (HITL) Ground Truth**:
1. **Non-Blocking Spatial JIT Whisper (`#594`)**: Scopes recall to `SpatialAnchor` (`File` + `Package`) capped at configurable $K_{\max}$ (`DefaultMaxActiveRulesPerPkg = 3`, overridable via `G8S_JIT_MAX_RULES`), attaching 1-line `jit_whisper` hints on `g8s read` and `g8s edit` without blocking in-worktree edits.
2. **Pure-Go Net-Delta Diff Guard (`#595`)**: Adds `CheckTypeDiffGuard = "diff-guard"` to `internal/verifier`, evaluating only added lines (`+`) in the git Net-Delta ($\Delta = \text{After} - \text{Before}$) at handoff (`g8s verify`), separating non-overridable `Hard Invariants` (secrets) from `F0`-overridable `Advisory Signals` (`DeniedFragments`, dormant rule re-awakening, and Tier-0 silent error swallowing).
3. **Sovereign HITL Calibration & Hypothesis Falsification Engine (`#596`)**: Implements the 3-level watchman hierarchy (`Level 1: Guard <-> F0 OverrideReceipt`, `Level 2: Delayed Reality Proxy`, `Level 3: Sovereign HITL Ground Truth` with $\alpha_{\text{HITL}} \gg \alpha_{\text{auto}}$) and `g8s feedback <record|override|report>` to empirically validate or falsify Hypotheses `H1`, `H2`, and `H3`.

---

## 2. ADDED Requirements

### Requirement: Spatially-anchored JIT recall and non-blocking whisper (`internal/vault`, `internal/fsops`)

`Vault.MatchSpatial` retrieves active (`APPLIED` / `ACCEPTED`) records matching a target file or package, ranking exact file matches ahead of package matches and capping output at `ResolveMaxActiveRules()` ($K_{\max}$). `fsops.Read` and `fsops.Edit` attach `JITWhisper` metadata when matching anchors exist in `G8S_VAULT_DB` without ever blocking in-flight file edits.

#### Scenario: spatial matching ranks exact file first and respects carrying capacity K_max
<!-- tests: TestMatchSpatialAndCarryingCapacity -->
- `Vault.MatchSpatial` ranks exact file matches ahead of package-level matches, enforces `DefaultMaxActiveRulesPerPkg` (`3`) or `G8S_JIT_MAX_RULES`, and `MatchSpatialAllStates` includes dormant (`DEPRECATED`) records for `diff-guard`.

#### Scenario: non-blocking JITWhisper attached on Read and Edit
<!-- tests: TestJITWhisperOnReadAndEdit -->
- `fsops.Read` and `fsops.Edit` populate `JITWhisper` from `G8S_VAULT_DB` and never block in-worktree mutations even when edited content contains denied fragments.

---

### Requirement: Pure-Go Net-Delta Diff Guard at verification handoff (`internal/verifier`)

`CheckTypeDiffGuard` (`diff-guard`) parses only added lines (`+`) from unified git diffs across `AllowedPaths`, blocks unconditionally on `Hard Invariants` (secret literals in added code), and emits `Advisory Signals` on `SpatialAnchor.DeniedFragments` (flagging `DormantReawakened` when a `Cold`/`DEPRECATED` rule triggers) and Tier-0 empty `if err != nil {}` branches. `F0` may override `Advisory Signals` by providing `OverrideReason`.

#### Scenario: unified diff parser extracts only added lines with line numbers
<!-- tests: TestParseUnifiedDiffAddedLines -->
- `ParseUnifiedDiffAddedLines` extracts `+` lines (excluding `+++` headers) with accurate 1-indexed destination line numbers and ignores removed or context lines.

#### Scenario: Hard Invariants refuse override while Advisory Signals support F0 OverrideReason and dormant re-awakening
<!-- tests: TestDiffGuardHardVsAdvisoryAndDormantReawakening -->
- `Verifier.Verify` with `diff-guard` flags `DormantReawakened` when a `DEPRECATED` rule matches added lines, permits `Advisory Signals` when `TaskRef.OverrideReason` is non-empty, and refuses `OverrideReason` when a `Hard Invariant` secret pattern is present.

---

### Requirement: Sovereign HITL Ground Truth calibration and Hypothesis H1/H2/H3 falsification (`internal/telemetry`, `cmd/g8s`)

`TelemetryEngine` persists `rule_weights`, `override_receipts`, `hitl_feedback`, and `k_yield_samples`, updates rule weights $W_t$ with Sovereign HITL precedence ($\alpha_{\text{HITL}} = 0.50 \gg \alpha_{\text{auto}} = 0.10$), enforces `EnforceCarryingCapacity` ("One-In, One-Out"), and computes `EvaluateHypotheses` (`hypothesis` / `validated` / `falsified`) exposed via `g8s feedback <record|override|report>`.

#### Scenario: Sovereign HITL feedback anchors rule weights and overrides automated decay
<!-- tests: TestHITLSovereignCalibrationPrecedence, TestCarryingCapacityOneInOneOut -->
- `RecordHITLFeedback` immediately re-anchors a decayed `Cold` rule to `ThermalHot` on `veto`/`tp`, demotes noisy rules on `fp`, and `EnforceCarryingCapacity` demotes excess `Hot` rules beyond $K_{\max}$ to `ThermalWarm`.

#### Scenario: Hypothesis H1, H2, and H3 falsification lifecycle and CLI surface
<!-- tests: TestEvaluateHypothesesFalsificationLifecycle, TestFeedbackCLIRecordOverrideAndReport -->
- `EvaluateHypotheses` reports `epistemic_status: hypothesis` when `< 5` samples exist, transitions to `validated` or `falsified` once `>= 5` empirical samples are recorded, and `g8s feedback` exposes `record`, `override`, and `report`.
