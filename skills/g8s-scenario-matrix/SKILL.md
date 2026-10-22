---
name: g8s-scenario-matrix
description: "12-dimension edge-case, concurrency, and failure-injection generator and holdout verifier defeating test suite overfitting and AI blindspots."
domain: verification
version: 1.0.0
last_updated: "2026-10-22"
risk_level: medium
evidence_level: runtime_required
args_syntax: "g8s matrix [gen|verify|inspect] --spec <spec_path> [--holdout-dir <dir>] [--dimensions <d1,d2...>] [--seed <seed>] [--json]"
---

# g8s Scenario Matrix Verification Skill

The `g8s-scenario-matrix` skill is an automated 12-dimension test synthesis and blind holdout execution engine. It generates edge cases, state machine permutations, concurrency stress tests, and failure injection scenarios to rigorously evaluate system implementations against their behavioral specifications.

---

## 1. Purpose & Threat Defeated

### Threats Defeated
- **Test Suite Overfitting**: Autonomous AI agents frequently craft tests tailored exclusively to pass their own code implementations, asserting incorrect assumptions or creating circular validation loops.
- **Happy-Path AI Blindspots**: LLM-driven development exhibits a systematic cognitive bias toward nominal "happy path" scenarios, missing boundary regressions, malformed payloads, and pathological input strings.
- **Untested Concurrency & Boundary Failures**: Multi-threaded race conditions, deadlocks, goroutine leaks, process signal aborts, and partial I/O failures remain undetectable under typical unit testing.

### Mechanism & Out-of-Worktree Holdouts
The scenario matrix splits generated test suites into two distinct tiers:
1. **Visible In-Tree Tests**: Placed directly in the repository/worktree (e.g. `tests/generated/`) to provide workers with baseline edge-case coverage and immediate feedback during implementation.
2. **Blind Out-of-Worktree Holdout Suites**: Stored outside the worker's git worktree at `$G8S_STATE_DIR/holdout/<feature-id>`. This suite is withheld from worker context during the generation phase and executed by the supervisor (F0) or dedicated verifiers during acceptance testing. If an agent overfits to in-tree tests, the blind holdout suite catches divergence immediately.

---

## 2. Deterministic Inputs

The scenario generator relies strictly on reproducible, deterministic inputs:

| Input | Description | Format / Source |
|---|---|---|
| **Feature Spec Contract** | Behavioral specification or OpenSpec delta detailing endpoints, schemas, and contracts. | Markdown / YAML / JSON Schema (`spec/openspec/` or path) |
| **State Machine Transitions** | Explicit graph of states, legal transitions, trigger events, and invariant assertions. | State transition table or Mermaid stateDiagram |
| **I/O Boundary Definitions** | Min/max bounds, array cardinality, regex patterns, enum sets, and type constraints. | JSON Schema / Go structs |
| **Concurrency Limits** | Channel buffers, worker pool boundaries, mutex locks, and timeout thresholds. | Config manifest (`.g8s/` or spec parameters) |
| **Generation Seed** | Deterministic integer or hash seed ensuring reproducible test case generation across runs. | `--seed <uint64>` (default: SHA-256 of spec file) |

---

## 3. Execution Commands

### Generate Matrix Scenarios & Holdout Suite
Generate both visible in-tree test scaffolding and blind holdout test suites from a specification contract:
```bash
g8s matrix gen \
  --spec spec/openspec/DELTA-26.md \
  --holdout-dir "$G8S_STATE_DIR/holdout/feature-delta26" \
  --dimensions all \
  --seed 4242 \
  --json
```

### Run Holdout Verification
Evaluate the current worktree implementation against the sealed out-of-worktree holdout suite:
```bash
g8s verify \
  --holdout-dir "$G8S_STATE_DIR/holdout/feature-delta26" \
  --task task-9821 \
  --json
```

### Inspect Matrix Coverage
Inspect which of the 12 dimensions have been synthesized and analyze failure distributions:
```bash
g8s matrix inspect \
  --holdout-dir "$G8S_STATE_DIR/holdout/feature-delta26" \
  --json
```

---

## 4. JSON Verdict Schema

The verification output produces a machine-readable JSON verdict complying with the following schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "ScenarioMatrixVerdict",
  "type": "object",
  "required": [
    "verdict",
    "pass_rate",
    "generated_scenarios_count",
    "holdout_path",
    "dimensions_covered",
    "failing_scenarios"
  ],
  "properties": {
    "verdict": {
      "type": "string",
      "enum": ["PASS", "FAIL"]
    },
    "pass_rate": {
      "type": "number",
      "minimum": 0.0,
      "maximum": 1.0,
      "description": "Ratio of passing scenarios over total generated scenarios (1.0 = 100%)"
    },
    "generated_scenarios_count": {
      "type": "integer",
      "minimum": 0,
      "description": "Total number of test scenarios executed"
    },
    "holdout_path": {
      "type": "string",
      "description": "Absolute filesystem path to the sealed holdout suite directory"
    },
    "dimensions_covered": {
      "type": "array",
      "items": {
        "type": "string",
        "enum": [
          "Boundary Values",
          "Empty/Nil Inputs",
          "Type Mutations",
          "Network Latency/Partition",
          "Concurrency Race/Deadlock",
          "Process Interruption/SIGTERM",
          "Disk Full/RO FS",
          "Out-of-Order Events",
          "State Machine Desync",
          "Large Payloads",
          "Unicode/Encoding Edge Cases",
          "Resource Exhaustion/Leaks"
        ]
      },
      "description": "List of the 12 scenario matrix dimensions covered by the test run"
    },
    "failing_scenarios": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["scenario_id", "dimension", "error_message"],
        "properties": {
          "scenario_id": { "type": "string" },
          "dimension": { "type": "string" },
          "error_message": { "type": "string" },
          "reproduction_seed": { "type": "integer" }
        }
      }
    }
  }
}
```

### Example Verdict Output
```json
{
  "verdict": "PASS",
  "pass_rate": 1.0,
  "generated_scenarios_count": 96,
  "holdout_path": "/Users/tamld/.local/state/g8s-sess-v019-blind-verify/holdout/feature-delta26",
  "dimensions_covered": [
    "Boundary Values",
    "Empty/Nil Inputs",
    "Type Mutations",
    "Network Latency/Partition",
    "Concurrency Race/Deadlock",
    "Process Interruption/SIGTERM",
    "Disk Full/RO FS",
    "Out-of-Order Events",
    "State Machine Desync",
    "Large Payloads",
    "Unicode/Encoding Edge Cases",
    "Resource Exhaustion/Leaks"
  ],
  "failing_scenarios": []
}
```

---

## 5. F0 -> F1 -> F2 Delegation Contract

In accordance with Zero-Trust Capability Delegation and ADR-0032 Containment Tiers:

```
[ F0 Supervisor ]
       |
       | 1. Generates holdout suite in $G8S_STATE_DIR/holdout/ (outside worktree)
       | 2. Issues WriteReceipt scoped strictly to worktree (excludes holdout)
       v
[ F1 Worker (wt-xxx) ]
       |
       | 1. Receives spec and visible in-tree scaffolding
       | 2. Implements feature code and passes in-tree checks
       | 3. May spawn F2 Subagents for isolated subtasks
       v
[ F2 Subagent ]
       | (Inherits F1 sandbox and path restrictions; NO holdout access)
       | Reports completed code back to F1
       v
[ F0 Supervisor ]
       | Runs `g8s verify --holdout-dir ...` against F1 worktree
       | If pass_rate == 1.0, accepts commit and promotes code
```

- **F0 Supervisor Authority**: Exclusive holder of holdout generation and verification authority. F0 writes the holdout suite to `$G8S_STATE_DIR/holdout/<feature-id>` which is strictly outside the F1 git worktree.
- **F1 Worker Boundary**: F1 receives a path-scoped `WriteReceipt` strictly limited to the worktree path. F1 has zero read or write access to `$G8S_STATE_DIR/holdout/`. F1 executes visible tests, authors implementation code, and seals evidence.
- **F2 Subagent Containment**: Any F2 subagents spawned by F1 operate strictly within F1's receipt boundary (`F2 ⊆ F1 ⊆ F0`). F2 cannot interact with F0 directly and cannot observe holdout data.
- **Verification Gate**: Holdout verification must execute in an isolated environment against the sealed worktree state before F0 signs off on merge or commit.
