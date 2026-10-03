# Context-Based Task Routing

This guide covers submit-time task routing in `g8s`, configuring `--route auto|manual`, operating the two-layer routing architecture, and tuning provider models for context-aware task distribution.

---

## Overview

Under **ADR-0030**, the router acts as the factory's distributor: orchestrators plan, workers execute, and the router assigns tasks to the best-fit provider, model, and role based on task context (file paths, prompt, blast radius, and provider availability).

By default, task submission operates in `manual` mode for strict backward compatibility. Specifying `--route auto` enables the context-aware routing pipeline at task submission time.

---

## The `--route` Flag

`g8s submit` supports the `--route` flag with two modes:

```sh
# Manual routing (default: identical to legacy submit behavior)
g8s submit \
  --idempotency-key "task-manual-001" \
  --prompt "General maintenance task" \
  --route manual

# Auto routing (context-driven provider, model, and role placement)
g8s submit \
  --idempotency-key "task-auto-001" \
  --prompt "Update API documentation" \
  --add-dir "docs/user-guide" \
  --route auto
```

### Semantics

- **`manual` (default)**: The exact existing submission path. Uses the `--model`, `--provider`, and `--role` provided on the CLI (or their configured defaults). Task payloads omit router metadata. The canonical request JSON bytes are guaranteed to be byte-identical whether `--route manual` is specified or omitted entirely.
- **`auto`**: Evaluates task context (prompt, target paths from `--add-dir`, timeout hints, blast radius) against active provider manifests. Sets `provider`, `model`, and `role` in the task payload, and attaches `route_source` and `route_reason` audit fields.

---

## Two-Layer Routing Architecture

The router follows a two-tier design ensuring that installations without external LLM advisory services execute safely and deterministically with zero network overhead.

```
                           +----------------------+
                           |      g8s submit      |
                           +----------+-----------+
                                      |
                              --route auto ?
                             /              \
                        (no) /                \ (yes)
                            v                  v
                   +----------------+   +-------------------------------+
                   |  Manual Route  |   | Layer 1: Deterministic Policy |
                   |  CLI defaults  |   | (Always active, zero network) |
                   +----------------+   +---------------+---------------+
                                                        |
                                            G8S_ROUTER_MODE=jev_assisted?
                                            and valid API keys present?
                                               /                 \
                                         (yes)/                   \(no)
                                             v                     v
                                    +-----------------+    +---------------+
                                    | Layer 2: Jev    |    | Final Layer 1 |
                                    | Suggestion      |    | Decision      |
                                    +--------+--------+    +---------------+
                                             |
                                    Validated against
                                    manifest & roles?
                                       /           \
                                 (yes)/             \(no: fallback)
                                     v               v
                             +-------------+  +---------------+
                             | Jev Decision|  | Layer 1 with  |
                             | Source: jev |  | IsFallback: t |
                             +-------------+  +---------------+
```

### Layer 1: Deterministic Policy (Always Active, Zero Network)

Layer 1 executes locally with **zero network requests**. It applies prioritized rules over task parameters:

1. **Trust Boundary Paths (Highest Governance Priority)**:
   - If target paths or prompt touch security-sensitive modules (`internal/harness`, `internal/receipt`, `internal/controlplane`), the router assigns the **`test-runner`** role profile and the model with the largest context window in the manifest.
2. **Documentation-Only Paths**:
   - If all paths resolve to markdown or text documents (`.md`, `.txt`, `.rst`, or under `docs/`), the router assigns the **`summarizer`** role and selects the cheapest/lightweight model in the manifest (e.g. `claude-haiku-4-5`).
3. **High Blast Radius Impact**:
   - If existing target files indicate `HIGH` or `CRITICAL` risk according to `analyzer.AnalyzeFileImpact`, the task is routed to a large-capacity model with the **`collector`** role.
4. **Extended Execution Windows**:
   - Tasks with timeout hints $\ge 30\text{m}$ route to high-capacity models with the **`collector`** role.
5. **Manifest Default Fallback**:
   - Tasks that do not trigger specialized rules route to the manifest default worker (`agy` / `gemini-3.8-flash-high` / `collector`).

### Layer 2: Jev-Assisted Routing (Optional, Benchmark-Gated)

Layer 2 provides LLM-based placement suggestions using typed questions.

- **Optional Posture**: Jev is never mandatory. When absent or unconfigured, the router stays completely within Layer 1.
- **Deterministic Validation**: Jev only suggests. Deterministic policy validates that the suggested provider, model, and role exist in the local manifest and harness role registry. Any invalid suggestion, HTTP error, rate limit, or timeout ($\le 2.5\text{s}$) cleanly discards the suggestion and falls back to the Layer 1 decision with `is_fallback: true` and `source: "deterministic"`.

---

## Provider Manifest Source

Auto-routing resolves candidate models and providers from the same Single Source of Truth (SSoT) used by worker daemons (`main.go:1488-1546`):

1. **`G8S_PROVIDERS`**: If set, loads provider definitions from this path.
2. **Canonical Config**: If `G8S_PROVIDERS` is unset, checks `~/.config/g8s/providers.json`.
3. **Built-in Defaults**: If neither file exists, the router falls back to catalog defaults (`agy` with `gemini-3.8-flash-high`, `claude` with `claude-haiku-4-5`, `ollama` with `llama3.1`).

If a provider config file exists at the specified location but fails validation (e.g. invalid JSON syntax), submission exits with a runtime error (`cli.CodeRuntime`) before queueing the task.

---

## Exit and Error Behavior

The router adheres to clean CLI error envelope semantics:

- **Invalid Flag Value**: If `--route` is given any value other than `auto` or `manual`, submission immediately exits with code `2` and surfaces an `E_USAGE` envelope:
  ```json
  {
    "v": 1,
    "kind": "error",
    "cmd": "submit",
    "sub": "route",
    "error": {
      "code": "E_USAGE",
      "message": "invalid --route \"custom\": must be 'auto' or 'manual'",
      "hint": "Specify --route=auto or --route=manual"
    }
  }
  ```
- **Routing Failure**: If auto-routing cannot decide (e.g. an empty candidate catalog or unresolvable routing context), submission surfaces an `E_USAGE` error envelope and exits. It **never silently falls back to manual mode**.

---

## Environment Variables

| Variable | Purpose | Default |
| :--- | :--- | :--- |
| `G8S_ROUTER_MODE` | Set to `jev_assisted` to enable Layer 2 LLM routing suggestions. Unset or any other value stays purely deterministic (Layer 1). | Unset (deterministic) |
| `G8S_PROVIDERS` | Path to custom `providers.json` registry file. | `~/.config/g8s/providers.json` |
| `TYPESAFE_API_KEYS` | Comma-separated API keys for Jev assistance service. | Unset |
| `TYPESAFE_ENDPOINT` | HTTP endpoint URL for Jev System 1 typed advice service. | `https://api.typesafe.ai/v1/systemone` |

---

## Durable Request Fields

When tasks are submitted with `--route auto`, the stored `request_json` includes the following metadata:

| Key | Type | Example | Description |
| :--- | :--- | :--- | :--- |
| `provider` | `string` | `"claude"` | Provider selected by the router. |
| `model` | `string` | `"claude-haiku-4-5"` | Model assigned to execute the task. |
| `role` | `string` | `"summarizer"` | Harness role profile contract. |
| `route_source` | `string` | `"deterministic"` | Decision source: `"deterministic"` or `"jev"`. |
| `route_reason` | `string` | `"documentation paths route to summarizer role with lightweight model"` | Human-readable audit explanation of the routing decision. |
