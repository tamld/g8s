# Verifier Classes & Automated Autonomy

The `g8s` machine acceptance pipeline provides bounded, verifiable autonomy for delegated task execution.

In unattended closed rounds, delegated tasks progress through planning, execution, verification, and automated merge without human intervention. To operate safely without risking regressions or silent drift, acceptance is partitioned into two distinct components:

1. **The Verifier** (`internal/verifier`, CLI `g8s verify`): Evaluates completed task deliverables against configured machine checks based on the task's write scope.
2. **The Merger** (`tools/merger.sh`): Executes gated auto-merges of pull requests under operator-controlled autonomy settings (`autonomy_level`).

---

## 1. Why: Trust Earned per Class, Never Assumed

In autonomous coding systems, unregistered write scopes fall back to human acceptance (exit 0) rather than auto-merging. Bounded autonomy requires that trust is earned incrementally on a per-class basis rather than assumed repository-wide (#515).

### The Advisory→Hard Model

To prevent unproven checks from creating brittle barriers or false confidence, every verifier class begins in `advisory` status:

- **Advisory status (`status: advisory`)**: Checks execute and verdicts are recorded for observability, auditing, and telemetry. Advisory checks exercise the evaluation machinery on real deliverables without blocking automated pipelines or pretending to have proved quality before real failure modes are demonstrated.
- **Hard status (`status: hard`)**: A class earns promotion to hard status only after citing concrete historical failure modes (real catches) that demonstrate the check bundle actively prevents regressions.
- **Fail-closed promotion**: A class cannot unilaterally declare itself hard. Without documented evidence of real catches, the class is refused and treated as unregistered by the loader.

### The Deny-by-Default Floor

The baseline operational policy is deny-by-default:

- If a task touches paths that do not match any registered class globs, or if a task lacks a write receipt (`allowed_paths` is empty, such as read-only or unreceipted tasks), the verifier resolves to `class: "unregistered"`, `registered: false`, `status: "advisory"`, `outcome: "unregistered"`.
- An `unregistered` verdict signifies that **human acceptance** is required.
- The `g8s verify` tool explicitly reports this verdict and exits with code 0 (`exit 0`). Human acceptance is the baseline operational floor—it is a valid and expected verdict, not an exceptional error.

---

## 2. The Registry (`.g8s/verifier-classes.yml`)

Verifier classes are defined in the central configuration file [`.g8s/verifier-classes.yml`](../../.g8s/verifier-classes.yml) at the repository root.

### Entry Anatomy

Each registered class entry contains six fields:

```yaml
classes:
  - name: docs
    status: advisory
    founding_catch: "#208 (docs drifted from code until the doc-contract gate existed)"
    priority: 10
    paths:
      - "*.md"
      - "docs/**"
      - "plans/**"
      - "skills/**"
      - "assets/**"
      - "offer/**"
    checks:
      - type: script
        run: tools/ci_doc_contract_check.sh
      - type: script
        run: tools/ci_link_integrity.sh
```

- **`name`**: Unique string identifier of the class (e.g. `docs`, `test`). Duplicate names across entries are rejected by the loader.
- **`status`**: Verification enforcement mode: `advisory` or `hard`.
- **`founding_catch`**: Mandatory citation string documenting the real defect, regression, or drift incident that justifies machine verification. Pattern must match `#<digits>` or `incident:<slug>` (enforced by regex).
- **`priority`**: Non-negative integer (lower value = matched first). When multiple classes match a task's paths, the lowest priority number wins. Ties between classes with identical priority are broken alphabetically by name.
- **`paths`**: List of path glob patterns matched against the task's write delegation scope. An entry matches if and only if **all** touched paths fall inside its glob patterns.
- **`checks`**: Bundle of verification checks executed for the class:
  - **`type: script`**: Executes a tracked repository script (`run: <path>`) with a 5-minute timeout.
  - **`type: go-test`**: Executes `go test -count=1` with a 5-minute timeout (`packages: [...]`). An empty package list (`packages: []`) dynamically derives target Go packages from the touched Go source files (preferring non-test `.go` files when present, falling back to `_test.go` files for test-only changes).

### The Two Seeded Classes

Two classes are seeded out of the box, both initially set to `advisory`:

1. **`docs`** (Priority 10):
   - **Paths**: `*.md`, `docs/**`, `plans/**`, `skills/**`, `assets/**`, `offer/**` (mirrors the [ADR-0031](../decisions/0031-ci-lanes.md) DOCS lane prose set).
   - **Founding catch**: `#208 (docs drifted from code until the doc-contract gate existed)`.
   - **Checks**:
     - `tools/ci_doc_contract_check.sh`: Verifies documentation contracts, FSM state synchronizations, and structure trees.
     - `tools/ci_link_integrity.sh`: Validates relative link targets across all markdown files.
2. **`test`** (Priority 20):
   - **Paths**: `*_test.go`, `**/*_test.go`, `tools/**`, `plans/**`.
   - **Founding catch**: `#510 (the red-team suite's own vacuous seeds F1-F3, fixed by #512)`.
   - **Checks**:
     - `type: go-test`, `packages: []`: Automatically executes unit tests for packages affected by the diff.

### How a Class Earns Hard Status

Classes earn hard status by citing real catches.

The registry loader (`internal/verifier/verifier.go`) enforces a strict fail-closed parsing rule:
- If an entry specifies `status: hard` but its `founding_catch` lacks a valid citation pattern (`#<digits>` or `incident:<slug>`), the loader refuses the entry and drops it.
- A dropped class is treated as UNREGISTERED (deny-by-default). The parser never panics and never silently upgrades a class to hard status without citation evidence.

---

## 3. Running Verification (`g8s verify`)

The `g8s verify` command inspects a task's write scope, resolves its matching verifier class, executes the check bundle, and emits a structured verdict.

```sh
# Verify by flag
g8s verify --task <task-id> [--as-task <caller-task-id>]

# Verify by positional argument
g8s verify <task-id>
```

### Path Extraction from Receipts

Verifier evaluations are grounded in cryptographically and structurally tracked write receipts:

1. `g8s verify` looks up the task record in the controlplane SQLite database.
2. It extracts `receipt_id` and `allowed_paths` from the task's request payload.
3. If `allowed_paths` is not embedded in the request payload, it queries the `write_receipts` table for `allowed_paths_json` using the task's `receipt_id`.
4. If the task has no receipt or empty allowed paths (e.g. read-only tasks), it evaluates to an `unregistered` verdict.

### Verdict Fields & Envelope

The verifier outputs a unified JSON envelope containing the evaluation verdict:

```json
{
  "v": 1,
  "kind": "verdict",
  "cmd": "verify",
  "sub": "task-8f92a1",
  "trace_id": "tr-0199",
  "data": {
    "class": "docs",
    "registered": true,
    "status": "advisory",
    "outcome": "pass",
    "checks": [
      {
        "type": "script",
        "target": "tools/ci_doc_contract_check.sh",
        "ok": true,
        "detail": "ok"
      },
      {
        "type": "script",
        "target": "tools/ci_link_integrity.sh",
        "ok": true,
        "detail": "ok"
      }
    ],
    "scope": true,
    "recorded_at": "2026-10-03T10:00:00Z"
  }
}
```

- **`class`**: Resolved verifier class (`docs`, `test`, or `unregistered`).
- **`registered`**: Boolean indicating whether a matching class was found in `.g8s/verifier-classes.yml`.
- **`status`**: Enforcement mode of the resolved class (`advisory` or `hard`).
- **`outcome`**: Overall verification outcome:
  - `pass`: All checks ran and succeeded (`ok: true`).
  - `fail`: One or more checks failed (`ok: false`).
  - `not-run`: Infrastructure runner error occurred during execution.
  - `unregistered`: No registered class matched the task's paths.
- **`checks`**: Detailed array of individual check results, including `type`, `target`, `ok`, and `detail` (output status or error messages).
- **`scope`**: Boolean confirming whether all write-scope paths matched the class path globs.
- **`recorded_at`**: UTC timestamp recording when verification completed.

### The Self-Grade Rule

Under issue #515: **"no verifier grades its own class"**.

When `--as-task <id>` is provided, the engine compares the caller task ID against the target task ID:
- If `caller.ID == target.ID`, verification is refused with a typed `SelfGradeError` (`no verifier grades its own class`).
- The CLI prints the error and exits with code 1 (`cli.CodeInvalid`). A worker task can never grade its own output.

### Honest Results & Runner Error Semantics

The verifier strictly avoids false reporting:
- **Runner errors are `not-run`**: If a check fails to run due to an environmental or infrastructure failure (e.g. missing executable, script file missing from disk), the check result records `runner error: <detail>`. Under the fail-open rule for infrastructure errors, overall verdict `outcome` becomes `not-run`. The verifier never emits a fake pass or falsely attributes runner outages to task code failures.
- **Check failures are `fail`**: When a script or test executes and returns a non-zero exit code, `ok` is `false`, stderr/exit code is captured in `detail`, and `outcome` becomes `fail`.
- **Exit codes**:
  - `exit 0`: Returned for unregistered classes, advisory outcomes (pass/fail/not-run), and hard passes.
  - `exit 1`: Returned strictly when `status == "hard"` AND `outcome == "fail"`, or upon self-grade guard violations.

### Telemetry Event (`verifier_verdict`)

Every verification evaluation emits a structured trace event to the telemetry engine:
- **Event type**: `verifier_verdict` (`TraceEventVerifierVerdict`).
- **Payload**:
  ```json
  {
    "class": "docs",
    "status": "advisory",
    "outcome": "pass",
    "checks_run": 2
  }
  ```

---

## 4. The Autonomy Ladder (`autonomy_level`)

Autonomy in `g8s` is controlled by the persistent configuration key `autonomy_level` managed through `internal/settings`.

```sh
# Check current autonomy posture
g8s config get autonomy_level

# Update autonomy posture
g8s config set autonomy_level 1
```

### Levels & Posture

Valid values for `autonomy_level` are strictly `{0, 1}`:

- **Level 0 (Default)**: Manual posture. Automated merging is disabled. If `tools/merger.sh` is invoked, it refuses to act and terminates with exit code 3 before running any other checks.
- **Level 1**: Docs-lane automated merge posture. The merger tool is authorized to squash-merge pull requests whose changes are confined entirely to the docs lane (`lane=docs`) and whose full gate battery passes.
- **Levels above 1 are reserved and unratified**: Attempting to set `autonomy_level` to 2 or higher is refused by configuration validation:
  ```text
  autonomy_level must be 0 or 1 (higher levels are reserved and unratified), got 2
  ```

### The Operator Flip Decision

The transition from Level 0 to Level 1 is an **explicit operator decision recorded on the round**, never a default or implicit system escalation ([ADR-0029](../decisions/0029-budgeted-auto-retry.md), [ADR-0024](../decisions/0024-gate-lane-routing.md)). Autonomous merging requires prior human consent for the specific campaign or operating window.

---

## 5. The Merger (`tools/merger.sh`)

The gated auto-merge executor [tools/merger.sh](../../tools/merger.sh) executes unattended PR merges when all safety criteria are satisfied.

```sh
# Live gated merge
tools/merger.sh --pr <num> --task <task-id>

# Dry-run gate evaluation (evaluates all gates without merging)
tools/merger.sh --pr <num> --task <task-id> --dry-run
```

### Options

- **`--pr <num>`**: GitHub pull request number (positive integer).
- **`--task <task-id>`**: Task ID used to evaluate the verifier acceptance verdict.
- **`--dry-run`**: Evaluates all four gates in sequence and prints planned actions without invoking `gh pr merge` or deleting branches.

### The Four Gates in Order

The merger evaluates four gates in strict fail-closed sequence. Every gate prints a standardized line prefixed with `GATE <name>: pass|fail — <detail>` so unattended round logs can quote results verbatim:

1. **Gate a: Autonomy Gate**
   - Evaluates `g8s config get autonomy_level`.
   - **Failure (`autonomy_level != 1`)**: Terminates with exit code 3 before performing any other work:
     ```text
     GATE autonomy: fail — autonomy_level=0: the merger refuses to act (operator flip required, ADR-0029/0024 posture)
     ```
   - **Pass (`autonomy_level == 1`)**:
     ```text
     GATE autonomy: pass — autonomy_level=1 (operator flip recorded)
     ```

2. **Gate b: Lane Gate**
   - Fetches the PR ref into a temporary detached worktree and runs `bash tools/ci_lane_detect.sh origin/main`.
   - In accordance with [ADR-0031](../decisions/0031-ci-lanes.md), a single code file outside the docs prose set classifies the diff as `lane=build`.
   - **Failure (`lane != docs`)**: Exits with code 1:
     ```text
     GATE lane: fail — lane=build (non-docs files detected or empty diff)
     ```
   - **Pass (`lane == docs`)**:
     ```text
     GATE lane: pass — lane=docs (docs-only PR)
     ```

3. **Gate c: Verifier Gate**
   - Invokes `g8s verify --task <task-id>` and parses the JSON verdict envelope.
   - Requires `registered=true`, `class=docs`, `outcome=pass`, and `status=advisory|hard`.
   - **Advisory acceptance**: Advisory verdicts are accepted because the operator flip to Level 1 serves as the recorded human decision for the round:
     ```text
     GATE verifier: pass — class=docs registered=true outcome=pass status=advisory (advisory verdicts are ACCEPTED here because the operator flip to level 1 IS the recorded human decision for this round)
     ```
   - **Hard acceptance**:
     ```text
     GATE verifier: pass — class=docs registered=true outcome=pass status=hard
     ```
   - **Failure**: Any non-docs class, unregistered status, check failure, or unparseable output terminates with exit code 1.

4. **Gate d: CI Gate**
   - Queries `gh pr checks <num>` to evaluate GitHub Actions check suites.
   - Requires `pending=0` AND `failure=0`.
   - **Failure (pending or failed checks)**: Exits with code 1:
     ```text
     GATE ci: fail — checks pending (pending=1, failure=0)
     GATE ci: fail — checks failed (pending=0, failure=1)
     ```
   - **Pass**:
     ```text
     GATE ci: pass — checks green (pass=5, pending=0, failure=0)
     ```

### Execution & The D3 Boundary

When all four gates pass:
- Under `--dry-run`, the tool reports planned execution and exits 0:
  ```text
  DRY-RUN: would execute: gh pr merge <num> --squash --delete-branch
  ```
- Under live execution, it runs `gh pr merge <num> --squash --delete-branch` and outputs the merge confirmation with an ISO 8601 UTC timestamp:
  ```text
  MERGED <num> at 2026-10-03T10:00:00Z
  ```

#### The D3 Boundary (Hard)

> **The merger never mints receipts, never submits tasks, never touches controlplane state — it reads config and verdicts, runs read-only checks, and merges a PR whose checks are already green.**

By enforcing this strict separation of concerns ([ADR-0029](../decisions/0029-budgeted-auto-retry.md) D3), the merger cannot create autonomous recursion loops or bypass receipt boundaries.
