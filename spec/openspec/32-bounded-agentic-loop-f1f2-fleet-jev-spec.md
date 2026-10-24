# OpenSpec DELTA-32: Bounded Agentic Loop (`g8s llm-call --max-turns --verify-cmd`), Adaptive Effort Timeout, `F1 -> F2` Subtask Containment, `g8s fleet status`, and Monotonic `Jev` Gate-Lane/Effort/Skill Routing

* **Specification ID**: `DELTA-32`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `cmd/g8s`, `internal/routing`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **One-Shot `9router` Limitation**: Previously, `g8s llm-call --apply-files` executed a single turn (`K = 1`) without running a verification command inside `scopeRoot`. If a `9router` worker emitted a minor syntax or test issue on Turn 1, it could not self-repair before returning to F0.
2. **Static `30s` Default Submit Timeout**: `g8s submit` defaulted `--timeout` to `30s` regardless of resolved `--effort`, causing `high` / `xhigh` reasoning combos (`reasoning-high`, `deep-coding`) to time out unless the operator passed `--timeout` manually.
3. **Unbounded Subtask Depth (`--parent-task-id`)**: `g8s submit --parent-task-id <id>` recorded lineage without enforcing `ADR-0032` containment depth (`depth <= 2`: `F0 -> F1 -> F2` allowed, `F2 -> F3+` forbidden) or checking that child `add-dir` paths stay within the parent task's `add_dirs`.
4. **Cross-Repo Fleet Drift**: Multi-repository supervisor campaigns lacked a native single-command gate (`g8s fleet status`) to detect uncommitted files, unpushed commits, orphan worktrees, or stale local branches across all sibling repositories.
5. **Partial `Jev` Routing Coverage**: `jevAssist` routed `(provider, model, role)` and L1 triage (`reflex.go`), but did not enrich ALDC gate-lane (`0/D/R/F/S`) or effort (`low..xhigh`) with safety monotonicity, and `skill-router.py` only supported keyword matching without optional `Jev` semantic reranking.

---

## 2. Normative Requirements

### R1 — Bounded Verify-and-Repair Loop (`g8s llm-call`)
- `g8s llm-call` MUST accept `--max-turns <int>` (default `1`, clamped to `[1, 5]`, env fallback `G8S_MAX_TURNS`) and `--verify-cmd <string>` (env fallback `G8S_VERIFY_CMD`).
- When `--verify-cmd` is non-empty, after applying `file:` blocks within `scopeRoot` (validated by `fsops.VerifyScope`), `g8s llm-call` executes `sh -c <verify-cmd>` inside `scopeRoot`.
- If verification exits `0`, the loop terminates immediately (`verify_passed: true`). If it exits non-zero and `turn < maxTurns`, the bounded failure output (`<= 4096` bytes) is appended to the prompt for `turn + 1`.
- JSON output envelopes MUST report `turns_used` and (when `--verify-cmd` is configured) `verify_passed`.

### R2 — Adaptive Effort Timeout Scaling (`g8s submit`)
- When `--timeout` is not explicitly passed to `g8s submit`, the default timeout MUST scale with `effortRes.Applied`:
  - `none` / `minimal` / `low` $\to$ `45s`
  - `medium` / `adaptive` / `dynamic` $\to$ `90s`
  - `high` / `xhigh` / `max` $\to$ `240s`

### R3 — `F1 -> F2` Subtask Containment (`g8s submit --parent-task-id`)
- When `--parent-task-id` references an existing parent task in the control-plane store:
  - If the parent task already has a non-empty `ParentTaskID` (`depth >= 2`), `g8s submit` MUST reject the submission (`E_HARNESS`: `containment depth exceeded`).
  - If the parent task declares `add_dirs`, child `dirs` MUST pass `harness.ValidateScopeJail(dirs, parentAddDirs)` (`E_HARNESS`: `subtask scope escape`).

### R4 — Cross-Repo Fleet Hygiene Gate (`g8s fleet status`)
- `g8s fleet status [--dir <workspace>] [--require-clean] [--json]` MUST inspect every git repository in the workspace and report `branch`, `dirty_count`, `unpushed_count`, `worktree_count`, `extra_branches`, `latest_tag`, `commits_since_tag`, and `clean`.
- With `--require-clean`, `g8s fleet status` MUST exit non-zero (`1`) if `dirty_repos > 0`.

### R5 — Monotonic `Jev` Gate-Lane & Effort Routing (`internal/routing`)
- `jevAssist` MUST query `lane` (`0`, `D`, `R`, `F`, `S`) and `effort` (`low`, `medium`, `high`, `xhigh`) alongside `provider`, `model`, and `role`.
- `EnforceMonotonicLane(detLane, jevLane)` MUST never downgrade the deterministic baseline lane (`0 < D < R < F < S`).
- `EnforceMonotonicEffort(detEffort, jevEffort, isTrustBoundary)` MUST never downgrade effort on trust-boundary tasks (`detLane == "S"`).

---

## 3. Verification Scenarios

#### Scenario: Bounded Verify-and-Repair Loop Self-Heals on Turn 2
<!-- tests: TestLLMCallBoundedAgenticLoop -->
- **GIVEN** `g8s llm-call --apply-files --max-turns 3 --verify-cmd "grep -q VERIFIED_GREEN status.txt"`
- **WHEN** Turn 1 writes `BROKEN` to `status.txt` and Turn 2 repairs it to `VERIFIED_GREEN`
- **THEN** `g8s llm-call` exits `0` with `turns_used: 2` and `verify_passed: true`.

#### Scenario: F2 to F3 Subtask Depth Rejection and Fleet Status Hygiene Gate
<!-- tests: TestSubmit_SubtaskContainmentAndAdaptiveTimeout, TestFleetStatus_CleanAndDirtyRepos -->
- **GIVEN** an F1 task `task-f1` and a valid child F2 subtask `task-f2` (`ParentTaskID == task-f1`)
- **WHEN** a caller executes `g8s submit --parent-task-id <task-f2>`
- **THEN** `g8s submit` rejects the submission with `E_HARNESS` (`containment depth exceeded`).

#### Scenario: Monotonic Jev Gate-Lane Blocks Trust-Boundary Downgrade
<!-- tests: TestJevAssist_LaneAndEffortSafetyMonotonicity -->
- **GIVEN** a task targeting `internal/receipt/verifier.go` (`detLane == "S"`, `detEffort == "high"`)
- **WHEN** `Jev` suggests `lane: "D"` and `effort: "low"`
- **THEN** `jevAssist` preserves `SuggestedLane: "S"` and `SuggestedEffort: "high"`.

