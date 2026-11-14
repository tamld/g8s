# Provider Configuration and Multi-Provider Fleets

This guide covers configuring multi-provider execution in `g8s`, managing provider declarations, and operating mixed worker fleets against a shared durable task queue.

---

## Overview

`g8s` is an **adapter-neutral control plane** — it orchestrates workers across heterogeneous backends without privileging any single provider. Supported adapter classes:

- **`api_call`** (HTTP gateways): `9router`, `openai`, `anthropic`, `google`, `openrouter`, `cliproxyapi`
- **`platform_dispatch`** (CLI binaries): `opencode`, `codex`, `claude`, `cursor`, `commandcode`, `qwen`, `agy`

All adapters are peers. `agy` (Google Antigravity) is one `platform_dispatch` adapter among many — not a privileged default.

Under **DELTA-10**, `providers.json` serves as the Single Source of Truth (SSoT) provider manifest. No secondary manifest or format is required. Both the task submission pipeline (`g8s submit`) and the worker daemon (`g8s worker`) use this manifest alongside task payload routing to execute provider-affinity matching and command-template argument substitution.

**DELTA-48** extends the manifest with declarative model adoption metadata (`family`, `cost_tier`, `status`, `effort_style`) and a 4-gate protocol (`Discover -> Declare -> Evaluate -> Blind-Verify`) that applies symmetrically to all adapters.

---

## File Location & Precedence

The provider registry file is evaluated with the following precedence:

1. **Environment Variable Override**: `G8S_PROVIDERS` (points to an explicit file path).
2. **Default Location**: `~/.config/g8s/providers.json` (canonical operator configuration).

The configuration file is read at startup by:
- `g8s worker`: Loads provider declarations and command templates for task execution.
- `g8s providers`: Inspects and displays configured provider entries and probing availability.

> **Note**: Repo-level configuration layering is **out of scope**. `g8s` does not load repository-local provider files (e.g., `.g8s/providers.json`); all provider configuration is resolved through the canonical user path or `G8S_PROVIDERS`.

---

## Provider Schema Reference

The root of `providers.json` contains a `providers` array:

```json
{
  "providers": [
    ...
  ]
}
```

Each element in the `providers` array is a `ProviderEntry` conforming to the two-class taxonomy (`platform_dispatch` or `api_call`).

### ProviderEntry Fields

| Field | Type | Required For | Description | Validation Rules (`internal/config/config.go`) |
| :--- | :--- | :--- | :--- | :--- |
| `class` | `string` | All | Provider class taxonomy. Must be `"platform_dispatch"` or `"api_call"`. | Rejects any value other than `"platform_dispatch"` or `"api_call"`. |
| `name` | `string` | `platform_dispatch` | Unique provider identifier (e.g., `"agy"`, `"codex"`, `"opencode"`). Used for task routing, worker claim affinity, and result envelopes. | Required (non-empty) for `platform_dispatch`. Optional identifier for `api_call`. |
| `base_url` | `string` | `api_call` | Base HTTP/REST API endpoint URL for remote model proxy calls. | Required (non-empty) for `api_call`. Ignored for `platform_dispatch`. |
| `auth_env` | `string` | Optional | Name of the environment variable containing authentication credentials (e.g. `OPENAI_API_KEY`). | Emptiness is not rejected during validation; entries with missing env vars degrade to `UNAVAILABLE` during health probes without issuing HTTP requests. |
| `models` | `array` | All | List of model descriptors supported by this provider. | Must contain at least one model (`len(models) >= 1`). Each item requires an `id` string; `context_window` (integer) is optional. |
| `slots` | `int` | `api_call` | Maximum concurrent execution capacity for proxy calls. | Must be `>= 1` for `api_call`. Ignored for `platform_dispatch`. |
| `args` | `array` | Optional | CLI invocation template for `platform_dispatch` binaries that do not conform to native `g8s` flags. | Array of string tokens. Placeholders `{prompt}`, `{model}`, and `{timeout}` are substituted verbatim into the execution argv. Templates never originate from task payloads. |

### Model Descriptor Fields (DELTA-48)

| Field | Type | Description |
| :--- | :--- | :--- |
| `id` | `string` | Model identifier (e.g., `gemini-4-hydrogen-high`, `reasoning-high`, `gpt-5.4`). |
| `context_window` | `integer` | Maximum context tokens. |
| `family` | `string` | Model family (e.g., `gemini-4-hydrogen`, `gemini-4-carbon`, `gemini-pro-orchestrator`, `gpt-5`). Required for `status: active`. |
| `cost_tier` | `string` | One of `free`, `economy`, `standard`, `frontier`. Required for `status: active`. |
| `status` | `string` | One of `active`, `transitional`, `deprecated`. Default: `active`. |
| `effort_style` | `string` | Adaptation style: `named`, `budget`, `baked-name`, `toggle`, `none`. `none` for fixed-reasoning orchestrators. |

---

## Worked Example: `providers.json`

The following configuration defines multiple `platform_dispatch` and `api_call` providers with declarative model metadata:

```json
{
  "providers": [
    {
      "class": "api_call",
      "name": "9router",
      "protocol": "9router",
      "base_url": "http://127.0.0.1:20128/v1",
      "base_url_env": "NINEROUTER_URL",
      "auth_env": "OPENAI_API_KEY",
      "default_model": "reasoning-high",
      "allow_unlisted_models": true,
      "slots": 6,
      "models": [
        {
          "id": "reasoning-high",
          "context_window": 200000,
          "family": "9router",
          "cost_tier": "standard",
          "status": "active",
          "effort_style": "named",
          "supported_efforts": ["low", "medium", "high", "xhigh", "max"],
          "default_effort": "high"
        },
        {
          "id": "reasoning-free",
          "context_window": 128000,
          "family": "9router",
          "cost_tier": "free",
          "status": "active",
          "effort_style": "named",
          "supported_efforts": ["low", "medium", "high"],
          "default_effort": "high"
        }
      ]
    },
    {
      "class": "platform_dispatch",
      "name": "opencode",
      "default_model": "9router/reasoning-high",
      "allow_unlisted_models": true,
      "models": [
        {
          "id": "9router/reasoning-high",
          "context_window": 200000,
          "family": "9router",
          "cost_tier": "standard",
          "status": "active",
          "effort_style": "named"
        }
      ]
    },
    {
      "class": "platform_dispatch",
      "name": "codex",
      "default_model": "gpt-5.4",
      "allow_unlisted_models": true,
      "models": [
        {
          "id": "gpt-5.4",
          "context_window": 200000,
          "family": "gpt-5",
          "cost_tier": "frontier",
          "status": "active",
          "effort_style": "named",
          "supported_efforts": ["none", "minimal", "low", "medium", "high", "xhigh", "max"],
          "default_effort": "medium"
        }
      ]
    },
    {
      "class": "platform_dispatch",
      "name": "claude",
      "default_model": "claude-sonnet-5-5",
      "allow_unlisted_models": true,
      "models": [
        {
          "id": "claude-sonnet-5-5",
          "context_window": 200000,
          "family": "claude-5",
          "cost_tier": "frontier",
          "status": "active",
          "effort_style": "named",
          "supported_efforts": ["low", "medium", "high", "xhigh"],
          "default_effort": "high"
        }
      ]
    },
    {
      "class": "platform_dispatch",
      "name": "agy",
      "default_model": "gemini-4-hydrogen-high",
      "models": [
        {
          "id": "gemini-4-hydrogen-high",
          "context_window": 1000000,
          "family": "gemini-4-hydrogen",
          "cost_tier": "economy",
          "status": "active",
          "effort_style": "baked-name",
          "supported_efforts": ["high"],
          "default_effort": "high"
        },
        {
          "id": "gemini-4-argon-high",
          "context_window": 1000000,
          "family": "gemini-4-argon",
          "cost_tier": "standard",
          "status": "active",
          "effort_style": "baked-name",
          "supported_efforts": ["high"],
          "default_effort": "high"
        },
        {
          "id": "gemini-4-carbon-high",
          "context_window": 1000000,
          "family": "gemini-4-carbon",
          "cost_tier": "frontier",
          "status": "active",
          "effort_style": "baked-name",
          "supported_efforts": ["high"],
          "default_effort": "high"
        },
        {
          "id": "gemini-pro-orchestrator",
          "context_window": 1000000,
          "family": "gemini-pro-orchestrator",
          "cost_tier": "frontier",
          "status": "active",
          "effort_style": "none",
          "supported_efforts": [],
          "default_effort": "none"
        }
      ]
    }
  ]
}
```

> **Note**: `gemini-3.8-*` models are retained in `.g8s/agent-models.yml` at `catalog_version: 3` with `status: deprecated` for backward compatibility but should not be used for new tasks.

---

## Declarative Model Adoption Lifecycle (`Discover -> Declare -> Evaluate -> Blind-Verify`)

**DELTA-48** introduces a 4-gate protocol for onboarding any model on any adapter:

### 1. Discover
Enumerate live models from the provider (works for both `api_call` and `platform_dispatch`):

```sh
g8s providers discover --provider 9router --json
g8s providers discover --provider agy --json
g8s providers discover --provider opencode --json
```

### 2. Declare
Add model entries to `providers.json` or `.g8s/agent-models.yml` with required metadata:
- `family`: Model family identifier
- `cost_tier`: `free` | `economy` | `standard` | `frontier`
- `status`: `active` | `transitional` | `deprecated`
- `effort_style`: `named` | `budget` | `baked-name` | `toggle` | `none`

`catalog_version: 3` onboards Gemini 4 element series + Orchestrator + `9router/*` under `opencode` while retaining `gemini-3.8-*` as `deprecated`.

### 3. Evaluate
Verify capability parity across adapters:

```sh
# Audit catalog metadata
g8s providers evaluate --provider agy --audit-catalog --json

# Run evaluation probes (ProviderWorkerAdapter + WrapProbeContractPrompt)
g8s eval run --provider agy --model gemini-4-hydrogen-high --json
g8s eval run --provider 9router --model reasoning-high --json
g8s eval run --provider opencode --model 9router/reasoning-high --json
```

### 4. Blind-Verify
Disjoint-family `S3-A` verification for all adapters symmetrically:

```sh
g8s blind-verify --spec SPEC.md --invariants INVARIANTS.md --diff changes.diff \
  --model deep-coding --provider 9router --json
```

---

## Submitting Tasks with Provider Affinity

When submitting tasks via `g8s submit`, specify the target provider with the `--provider` flag:

```sh
# Submit a task targeting the '9router' provider (api_call)
g8s submit \
  --idempotency-key "task-9router-001" \
  --provider "9router" \
  --model "reasoning-high" \
  --prompt "Analyze memory footprint of internal/controlplane"

# Submit a task targeting the 'opencode' provider (platform_dispatch)
g8s submit \
  --idempotency-key "task-opencode-001" \
  --provider "opencode" \
  --model "9router/reasoning-high" \
  --prompt "Refactor error handling in internal/worker"

# Submit a task targeting the 'codex' provider (platform_dispatch)
g8s submit \
  --idempotency-key "task-codex-001" \
  --provider "codex" \
  --model "gpt-5.4" \
  --prompt "Implement feature X"

# Submit a task targeting the 'agy' provider (platform_dispatch)
g8s submit \
  --idempotency-key "task-agy-001" \
  --provider "agy" \
  --model "gemini-4-hydrogen-high" \
  --prompt "Analyze project architecture"
```

### Provider Precedence

When resolving the provider for a submitted task, `g8s` evaluates:

1. **CLI Flag**: `--provider <name>` (highest precedence).
2. **Settings Key**: `default_provider` configured via `g8s config set default_provider <name>`.
3. **Absent / Legacy**: Default empty string `""` (legacy submission without provider affinity).

When a non-empty provider is resolved, it is persisted durably in the task payload as `"provider": "<name>"` alongside `"model"`.

### Configuring a Default Provider

You can set a default provider using `g8s config`:

```sh
# Set default provider
g8s config set default_provider 9router

# Inspect default provider
g8s config get default_provider

# Remove default provider
g8s config unset default_provider
```

---

## Running Mixed Fleets & Claim Affinity

Operators can run dedicated workers side-by-side for each provider, or run general-purpose workers:

```sh
# Start worker dedicated exclusively to '9router' tasks
g8s worker --provider 9router

# Start worker dedicated exclusively to 'opencode' tasks
g8s worker --provider opencode

# Start worker dedicated exclusively to 'codex' tasks
g8s worker --provider codex

# Start worker dedicated exclusively to 'agy' tasks
g8s worker --provider agy

# Start an unfiltered worker (claims any queued task)
g8s worker
```

### Claim Affinity Rules

1. **Strict Equality**: A worker started with `--provider <name>` claims **only** tasks whose payload `provider` matches `<name>` exactly.
2. **No Legacy Cross-Claiming**: A provider-filtered worker **never** claims legacy tasks (tasks submitted without a provider key).
3. **Unfiltered Workers (Backward Compatibility)**: A worker started without `--provider` claims any queued task regardless of the task's provider field or absence thereof.

---

## Argv Resolution Order & Failure Modes

When a worker claims a task, it resolves the executable command line from `providers.json` using the following deterministic sequence:

### 1. Legacy Tasks (No Provider Specified)
- If the task payload has no `provider` (or `provider` is empty `""`):
  1. The worker checks `providers.json` for a `platform_dispatch` args template whose model ID matches the task's `model`.
  2. If no model match exists, it falls back to the legacy default (`agy` binary with default model `gemini-3.8-flash-high`).

### 2. Provider-Targeted Tasks (Provider `P` Specified)
- If the task payload specifies provider `P`:
  1. The worker looks up the `platform_dispatch` entry in `providers.json` where `name == P`.
  2. If found, the worker instantiates the provider's `args` template by replacing `{prompt}`, `{model}`, and `{timeout}` with the task's parameters.
  3. **No Silent Fallback**: If provider `P` is not found among `platform_dispatch` entries, the attempt **fails immediately** with an error listing the available `platform_dispatch` providers from `providers.json`. Under no circumstances will `g8s` fall back to `agy` or another default when a provider was explicitly requested.
  4. **Queue Path Restriction**: If provider `P` matches an `api_call` entry, the attempt fails with a distinct error stating that `api_call` providers are not executable on the worker queue path.

---

## Result Envelopes

Task results recorded by the worker capture provider provenance:

- Result envelopes contain a top-level `provider` field (`"provider": "<name>"`) reflecting the provider that executed the task (omitted when empty).
- The legacy `agy_bin` field remains preserved for backward compatibility.

---

## Scope & Non-Goals

- **`api_call` on the Queue Path**: `api_call` entries in `providers.json` are designed exclusively for direct HTTP/proxy orchestration calls (via `g8s orchestrate` or REST HTTP endpoints) and cannot be claimed or executed by `g8s worker` subprocess loops.
- **Repository-Level Config Layering**: Configuration is strictly user-scoped (`~/.config/g8s/providers.json`) or environment-scoped (`G8S_PROVIDERS`). Repository-level overrides (such as `.g8s/providers.json` in project directories) are intentionally unsupported to prevent supply-chain security risks from untrusted repository configuration.