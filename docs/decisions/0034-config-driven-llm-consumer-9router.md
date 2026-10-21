# ADR-0034: Universal Config-Driven LLM Call Consumer & 9Router Multi-Model Gateway Integration

- **Status**: Accepted
- **Date**: 2026-10-21
- **Deciders**: Operator (PiC), F0 Supervisor
- **Target Release**: `v0.19.0`
- **Related**: ADR-0005 (`05-provider-and-resource-pool-spec.md`), ADR-0010 (`10-two-class-provider-pool-spec.md`), ADR-0020 (System-1 Jev Reflex), ADR-0033 (F0 Verified Delegation)

---

## 1. Context & Problem Statement

Prior to `v0.19.0`, `g8s` declared a two-class provider pool in `providers.json` (`DELTA-10`):
1. `platform_dispatch`: local CLI binaries (`agy`, `claude`, `codex`, `ollama`) spawned via `os/exec`.
2. `api_call`: remote HTTP gateways (`9router`, OpenAI-compatible endpoints, OpenRouter, NVIDIA NIM, Anthropic Messages API).

However, in `cmd/g8s/main.go` (`runWorker`), `api_call` providers were registered only in `apiCallNames` and rejected on the worker queue execution path (`"provider %q is an api_call provider and is not executable on the queue path"`), and `internal/provider/openai.go` lacked:
- Direct CLI and queue-path LLM call consumption (`g8s llm-call` + `g8s worker`),
- `g8s submit` validation for `supplied_text` (`tool_policy=none`) on `api_call` gateways,
- Multi-protocol HTTP gateways (`9router` on `http://127.0.0.1:20128/v1` or `$G8S_9ROUTER_BASE_URL`) that expose both OpenAI `/v1/chat/completions` and Anthropic `/v1/messages` plus virtual model combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`) and upstream provider prefixes (`nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`).

The operator's core architectural requirement for `v0.19.0` is:
> **`g8s` must act as a universal, config-driven LLM call consumer**: any external, LAN, or local LLM source (`9router` multi-model combos, OpenAI-compatible `/v1/chat/completions`, Anthropic `/v1/messages`, Ollama HTTP) declared in config (`providers.json` / `.g8s/agent-models.yml`) must be directly consumable across CLI invocation (`g8s llm-call`), worker queue execution (`g8s submit` + `g8s worker`), and provider discovery (`g8s providers discover`) without code changes or external CLI binary wrappers.

---

## 2. Decision

We adopt a **Unified Config-Driven LLM Consumer Architecture (`DELTA-25`)** with four pillars:

### 2.1. Multi-Protocol `api_call` HTTP Client (`internal/provider/openai.go` & `internal/config/config.go`)
Extend `internal/provider` and `internal/config` so any `api_call` provider entry in `providers.json` can declare:
- `protocol`: `"openai"` (default, `POST /chat/completions`), `"anthropic"` (`POST /messages` with `anthropic-version: 2023-06-01` and `x-api-key` / `Bearer`), or `"9router"` (supports both `/v1/chat/completions` and `/v1/messages`, `/api/health` + `/v1/models` health probing, and combo metadata).
- `base_url`: e.g., `"http://127.0.0.1:20128/v1"` or `$G8S_9ROUTER_BASE_URL`, with optional `base_url_env` override (e.g., `NINEROUTER_URL`).
- `auth_env`: e.g., `"NINEROUTER_API_KEY"` or `"OPENAI_API_KEY"` (optional when gateway permits unauthenticated calls or when key is resolved from env).
- `headers`: optional map of static HTTP headers.
- `models`: explicit model and combo declarations (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`, `nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`), plus wildcard/prefix matching (`allow_unlisted_models: true` or prefix globs like `nvidia/*`, `openrouter/*`, `cc/*`) so `9router`'s dynamic upstream models can be consumed as long as the gateway is declared in config.

### 2.2. `g8s llm-call` & Native `api_call` Execution on the `g8s worker` Queue Path
- Add `g8s llm-call` (`cmd/g8s/llm_call.go`) to invoke any declared `api_call` provider synchronously from the CLI or inside a supervised worker attempt.
- In `cmd/g8s/main.go` (`runWorker`), wire `api_call` entries (unless `disable_queue_exec: true`) to `g8s llm-call --worker-envelope --provider <name> --model {model} --timeout {timeout} --prompt {prompt}`, enabling full queue execution (`g8s submit --provider 9router --model reasoning-free` + `g8s worker --once`) with timeout, cancellation, secret sanitization, token usage accounting, and `WriteReceipt`-enforced file block application (`--apply-files`).

### 2.3. First-Class `9router` Catalog & Combo Registry (`.g8s/agent-models.yml` & `internal/provider/catalog.go`)
Register `9router` in `.g8s/agent-models.yml` and `internal/provider/catalog.go` with its standardized SWE combos:
- `reasoning-free` (`fallback`: GLM-free -> NVIDIA Nemotron Ultra -> OpenRouter free)
- `reasoning-high` (`fallback`: NVIDIA Nemotron Ultra/Super -> GPT-OSS-20B -> OpenRouter)
- `deep-coding` (`fallback`: NVIDIA Nemotron Ultra/Super -> GPT-OSS-20B -> North-Mini-Code)
- `fast-agent` (`round-robin`: MiniMax-M3 / GPT-OSS-20B / North-Mini-Code / Gemini-3-Flash)
- `docs-retriever` (`fallback`: 1M-context Nemotron Ultra/Super -> Lightning)

### 2.4. Live Model Discovery (`g8s providers discover`)
Add `g8s providers discover [--provider <name>] [--json]` to query `GET <base_url>/models` on configured `api_call` gateways and return live model and combo IDs.

### 2.5. Two-Layer Separation Rule (Public Repo Mechanism vs. Local Untracked Operator Policy)
To prevent environment coupling and preserve zero-leakage boundaries:
- **Public Repo Mechanism (Layer 1)**: Tracked files declare only generic protocols (`openai`, `anthropic`, `9router`), standard SWE combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`), standard wildcards (`nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`), and localhost endpoints (`http://127.0.0.1:20128/v1`).
- **Local Untracked Operator Policy (Layer 2)**: Specific LAN IPs, custom tokens, and proprietary homelab routing combos reside strictly in untracked local configurations (`~/.config/g8s/providers.json` and `~/.agents/skills/`). No homelab IP or private cluster topology is ever committed to the repository.

---

## 3. Consequences

### Positive
- **Zero-Vendor Lock-In**: Operators can point `g8s` at `9router` (`http://127.0.0.1:20128/v1`), OpenRouter, NVIDIA NIM, Anthropic, OpenAI, or local Ollama/llama.cpp simply by declaring the endpoint in `providers.json`.
- **Zero-Cost 24/7 Autonomy**: `g8s` workers, verifiers, and auditors can consume the `9router` multi-model resource pool (`fast-agent`, `deep-coding`, `reasoning-high`, `reasoning-free`) with automatic upstream failover.
- **T3 Independent Verification (`#628`)**: F0 can use `9router` (`deep-coding` or `reasoning-high`) as a genuinely independent second verifier (`!= agy`) on `T3` trust-boundary changes.

### Negative / Mitigations
- **HTTP Model Mutation Safety**: Raw HTTP chat completions do not have a built-in CLI tool loop by default. Mitigation: `read_only` / `supplied_text` tasks consume responses directly (`tool_policy: "none"` by construction), and `workspace_write` tasks apply structured file blocks strictly through `WriteReceipt.AllowedPaths`.
