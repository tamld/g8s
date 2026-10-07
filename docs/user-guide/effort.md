# Task Effort Configuration & Quality Ladder

This guide covers configuring task-level reasoning effort in `g8s`, understanding how effort is decided across write scopes and declared signals, working with baked-name platform models, inspecting cost telemetry, operating the quality ladder (`g8s ladder status`, `advance`, `gauges`), and maintaining the effort registries.

---

## The Knob: `g8s submit --effort <level>`

`g8s submit` provides the `--effort` flag to declare worker reasoning depth:

```sh
# Explicit effort submission
g8s submit \
  --idempotency-key "task-effort-001" \
  --prompt "Refactor internal/routing error handling" \
  --effort high
```

### The 7-Level Effort Ladder

Reasoning effort follows a canonical 7-level ordered scale:

$$\text{none} < \text{minimal} < \text{low} < \text{medium} < \text{high} < \text{xhigh} < \text{max}$$

- **Default**: `medium` when `--effort` is omitted.
- **Parse-Time Validation**: The flag value is strictly checked against the canonical ladder. Specifying any value outside the ladder exits immediately with a usage error naming the allowed ladder (`invalid --effort "<val>": allowed ladder is [none, minimal, low, medium, high, xhigh, max]`).
- **Hard-Refuse Case (`none` on Mandatory Reasoning Models)**: Models with mandatory reasoning (e.g. models under providers with `mandatory: true`, such as xAI models in `.g8s/agent-models.yml`) strictly refuse effort `none`. Attempting to submit or route a task with `--effort none` against a mandatory reasoning model produces a typed error (`MandatoryEffortError`) and exits with a usage error (`Reasoning cannot be disabled for this model`).

---

## How Effort Is Decided

When a task is submitted, `g8s` determines the target effort level using a deterministic precedence chain:

$$\text{Explicit Flag (--effort)} > \text{Declared Signals (--blast-radius, --loc-estimate)} > \text{Path Class (.g8s/effort-classes.yml)} > \text{Fail-Open Floor (medium)}$$

```
                           +------------------------+
                           |  --effort flag passed? |
                           +-----------+------------+
                                      / \
                               (yes) /   \ (no)
                                    v     v
              +-----------------------+  +-------------------------------+
              | Explicit Flag Wins    |  | Declared Signals Passed?      |
              | (marks effort_override|  | (--blast-radius/--loc-estimate|
              +-----------------------+  +---------------+---------------+
                                                        / \
                                                 (yes) /   \ (no)
                                                      v     v
                           +---------------------------+   +---------------------------+
                           | Evaluate Matrix v1.1      |   | Match Path Class in       |
                           | blast=high & loc>200->high|   | .g8s/effort-classes.yml   |
                           | blast=high -> >=medium    |   | (first match wins)        |
                           | blast=low  -> low         |   +-------------+-------------+
                           +-------------+-------------+                 |
                                         |                         (no match)
                                         v                               v
                           +---------------------------+   +---------------------------+
                           | Path class demoted/elevated   | Fail-Open Floor: medium   |
                           +---------------------------+   +---------------------------+
```

### 1. Explicit Flag Override (`--effort`)
Passing `--effort <level>` explicitly on the CLI overrides any class default or signal recommendation. The payload records `effort_override: true`. If the requested level is lower on the ladder than the baseline prior, `effort_override_down: true` is also recorded for auditing.

### 2. Declared Signals Demotion/Elevation (Matrix v1.1)
Operators or callers can declare task risk signals via `--blast-radius low|medium|high` and `--loc-estimate <int>`:
- **`blast=high` and `loc>200`**: Promotes target effort to `high`.
- **`blast=high` (loc $\le 200$)**: Sets effort to at least `medium` (elevates registry classes that default below `medium` to `medium`; registry classes at `medium` or higher stand).
- **`blast=low` (Matrix v1.1 Rule)**: Declares `low` effort at **any LOC estimate**. Low-blast modifications are cheap to verify and revert, and multi-file audit evidence demonstrates scan scale does not require deep reasoning.
- **Source Auditing**: When signals are provided, the task payload records `effort_source: "declared"`, and nests the `effort_signals` object detailing `blast_radius`, `loc_estimate`, `declared_suggestion`, `registry_suggestion`, and `override_down`.

### 3. Path Class Registry (`.g8s/effort-classes.yml`)
If no signals are declared, the target paths specified in `--add-dir` (or the working directory) are evaluated against the write-scope glob rules in `.g8s/effort-classes.yml`.
- **Priority Order**: Evaluated by ascending `priority` integer (first match wins). For example, `red-cell` (priority 10, `high`) takes precedence over `docs` (priority 30, `low`).
- **Fail-Open Default**: Any unclassified or unregistered file paths fall open safely to class `unregistered` with a floor of `medium` effort.
- **Known Limitation (issue #570)**: patterns are matched with `filepath.Match`, whose `*` does not cross `/` — `docs/**` and `*.md` never match nested paths, so in practice most tasks classify as `unregistered` and the declared signals (blast radius + LOC estimate) are what resolve their effort. Registry pattern semantics are scheduled for rework in v0.17.

---

## Baked-Name Platform Models

Certain platforms encode reasoning effort directly into the model identifier suffix (e.g. `gemini-3.8-flash-low`, `gemini-3.8-flash-medium`, `gemini-3.8-flash-high`).

### Resolution Order

To prevent mismatched `(model, effort)` pairs from being rejected at worker execution time, `cmd/g8s/submit.go` evaluates whether a model is baked using this resolution order:

1. **Platform Manifest (`providers.json`)**: If the provider and model are declared in the active manifest:
   - If the model entry specifies an explicit `effort_style: "baked-name"`, it is treated as baked.
   - If the entry specifies another style or leaves `effort_style: ""` (style-less), the manifest has declared the pairing as non-baked, stopping further resolution.
2. **Effort Catalog (`.g8s/agent-models.yml`)**: If the manifest does not declare an effort style for the model (or knows the provider but not that specific variant, or if `--provider` was omitted on a manual submit):
   - The default catalog decides. In `.g8s/agent-models.yml`, `agy` is registered as `effort_style: baked-name` across its variants (`gemini-3.8-flash-low`, `gemini-3.8-flash-medium`, `gemini-3.8-flash-high`).

### Model Realignment at Submit

When a model is identified as `baked-name` and the decided effort level (from `--effort`, signals, or path class) differs from the model's suffix:

- **The Model Is Realigned**: The model name is rewritten to match the applied effort variant (e.g. requested model `gemini-3.8-flash-high` with class `docs` defaulting to `low` is realigned to `gemini-3.8-flash-low`).
- **Effort Passes Through Unchanged**: The applied effort level passes through without modification, preserving per-class effort differentiation for accurate telemetry and execution semantics (e.g. docs-class tasks run at `low` on `flash-low`, verified live in task `5c44b300`).
- **Durable Payload Audit Fields**:
  - `model_requested`: The original model requested by the operator or routing default (e.g. `gemini-3.8-flash-high`).
  - `model_realigned`: Boolean flag set to `true` when realignment took place.
  - `model`: The effective realigned model passed to the worker dispatch (e.g. `gemini-3.8-flash-low`).

### Inspecting Task Payloads

Every submission persists its resolved effort and model metadata into the task payload in SQLite WAL storage (`g8s.db`). Users can inspect these fields with `g8s get <task-id>`:

```json
{
  "actor": "tamld",
  "add_dirs": ["docs/user-guide"],
  "effort": "low",
  "effort_applied": "low",
  "effort_budget_tokens": 0,
  "effort_class": "docs",
  "effort_mismatch": false,
  "effort_override": false,
  "effort_requested": "low",
  "model": "gemini-3.8-flash-low",
  "model_realigned": true,
  "model_requested": "gemini-3.8-flash-high",
  "permission": "read_only",
  "prompt": "Update documentation",
  "role": "summarizer"
}
```

| Payload Field | Purpose |
| :--- | :--- |
| `effort_requested` | Effort level requested by CLI flag, declared signal, or class default. |
| `effort_applied` | Effort level applied after model adapter normalization. |
| `effort_class` | Matched task class name from `.g8s/effort-classes.yml` (`red-cell`, `discovery`, `feature`, `test`, `docs`, or `unregistered`). |
| `effort_budget_tokens`| Reasoning token budget when using `budget` effort styles (e.g. Anthropic legacy or OpenRouter). |
| `effort_mismatch` | Boolean flag indicating whether the requested level had to be adapted to the nearest supported level. |
| `effort_override` | Boolean flag indicating whether `--effort` was explicitly passed. |
| `effort_override_down`| Boolean flag indicating whether an explicit `--effort` downgraded the class prior. |
| `effort_source` | Source of effort resolution when signals were used (`explicit`, `declared`, `registry`, or `floor`). |
| `effort_signals` | Nested object with declared signal parameters and suggestions. |
| `model_requested` | Model name before baked-name realignment. |
| `model_realigned` | `true` if the model identifier was rewritten to match the applied effort level. |

---

## Telemetry: Cost per Class

Per-class token and cost telemetry is captured from worker usage records into `telemetry.TraceEvent` rows (`internal/worker/worker.go`, opt-in via `G8S_TELEMETRY=1` or `G8S_TELEMETRY_DB=<path>`); `g8s ladder gauges` aggregates the ingested events per class and effort. Note: `g8s supervisor-metrics` reports a different table (the orchestrate self-test loop), not dispatch telemetry. (Issue #570 documents the current capture gaps.)

---

## The Quality Ladder: `g8s ladder`

The quality ladder manages failure classification, automated multi-rung escalation, and human-in-the-loop (HITL) handoff when worker tasks fail.

```sh
# Inspect ladder lineage, cumulative token consumption, and next plan
g8s ladder status <task-id>

# Advance task along the quality ladder (escalate effort, try alternative model, or route to HITL)
g8s ladder advance <task-id> --prompt "Refined instructions for retry"

# Advance and export HITL evidence packet if automated options are exhausted
g8s ladder advance <task-id> --out ./hitl-packet.json

# Report pass rates, escalation rates, and HITL metrics
g8s ladder gauges
g8s ladder gauges docs
```

### Subcommands

1. **`g8s ladder status <task-id>`**: Traverses task lineage from parent to root, aggregates cumulative token usage against `ladder_token_budget`, inspects the latest failure shape, and displays the planned next action without executing it.
2. **`g8s ladder advance <task-id>`**: Executes the planned ladder rung. Submits an escalation child task linked via `parent_task_id`. If advancing requires workspace mutation (`workspace_write`), a fresh write receipt must be supplied via `--receipt-id`. Escalation re-dos (rungs 1..5) require explicit prompt instructions via `--prompt` or `--prompt-file`. If automated options are exhausted or refused, it emits an HITL evidence packet and can save it to disk via `--out <path>`.
3. **`g8s ladder gauges [class]`**: Analyzes telemetry events and completed tasks across classes to report pass rates per `(class, effort)` pair, escalation rates per class (rungs fired per task), and the overall HITL system rate. These gauges provide the empirical measurements used to tune escalation policies and default class efforts.

### Escalation Policy

Ladder evaluation follows a strict automated progression (`internal/ladder/policy.go`):
- **Success / Early Termination**: If the latest attempt passed class checks, the ladder terminates with `action: done`.
- **Rung 0 (Diagnosis)**: When an initial task fails, Rung 0 dispatches a read-only diagnosis worker at `low` effort to classify failure shape (`effort-shaped`, `brief-shaped`, or `env-shaped`) and identify failing checks.
- **Rungs 1..3 (Effort Escalation)**: If failure is `effort-shaped`, the ladder steps reasoning effort up by +1 along the model's `supported_efforts` subset.
- **Rungs 4..5 (Alternative Models)**: If the model's effort ladder is exhausted, the ladder falls back to alternative models declared in the manifest that serve the task class.
- **Refusals and Rung 6 (Mandatory HITL)**: Non-effort shapes (e.g. environment breakages or ambiguous briefs), token budget overruns (`cumulative_tokens >= ladder_token_budget`), or reaching the 6-rung ceiling immediately refuse further automated execution and route to human review (`action: hitl`).

---

## Registries Reference

The effort system is configured through two declarative files under `.g8s/`:

### 1. Write-Scope Class Registry (`.g8s/effort-classes.yml`)

Maps target write-scope file paths to reasoning effort defaults and optional ladder token budgets.

| Field | Type | Description |
| :--- | :--- | :--- |
| `schema_version` | `string` | Must be `effort-classes.v1`. |
| `classes` | `list` | List of effort class definitions. |
| `classes[].name` | `string` | Unique class identifier (`red-cell`, `discovery`, `feature`, `test`, `docs`). |
| `classes[].default_effort`| `string` | Default effort level from canonical ladder (`low`, `medium`, `high`, etc.). |
| `classes[].priority` | `int` | Evaluation order; lower number evaluated first (first matching glob wins). |
| `classes[].paths` | `list` | List of glob patterns matched against task write paths. |
| `classes[].ladder_token_budget`| `int` | Optional maximum cumulative token ceiling before ladder forces HITL review. |

### 2. Supported-Models Effort Catalog (`.g8s/agent-models.yml`)

The research-verified Single Source of Truth (SSoT) for provider effort capabilities across major LLM platforms.

| Field | Type | Description |
| :--- | :--- | :--- |
| `schema_version` | `string` | Catalog schema version (e.g. `agent-models.v1`). |
| `catalog_version` | `int` | Incremental catalog revision integer. |
| `verified_at` | `string` | ISO date of latest research verification against official provider documentation. |
| `sources` | `list` | Upstream official documentation URLs consulted for verification. |
| `providers.<name>.effort_style` | `string` | Adaptation style: `named`, `budget`, `baked-name`, or `toggle`. |
| `providers.<name>.field` | `string` | Wire protocol parameter name (e.g. `reasoning_effort`, `thinking_level`). |
| `providers.<name>.default_effort`| `string`| Provider-wide default effort posture (`medium`, `high`, `adaptive`, `dynamic`). |
| `providers.<name>.mandatory` | `bool` | If `true`, reasoning cannot be disabled; `none` is rejected. |
| `models[].id` | `string` | Model identifier string (or prefix pattern). |
| `models[].supported_efforts`| `list` | Subset of the canonical ladder supported by this model. Empty list indicates no effort control. |
| `models[].default_effort` | `string` | Model-specific default effort if different from provider default. |
| `models[].mandatory` | `bool` | Model-level mandatory reasoning override. |
| `models[].effort_budget_map`| `map` | Explicit token budget integers for `budget` style models. |

> [!NOTE]
> The model catalog is strictly research-verified against official vendor API documentation and carries `verified_at` timestamps. Any entries whose provider API semantics drift should be updated through verifiable research benchmarks.
