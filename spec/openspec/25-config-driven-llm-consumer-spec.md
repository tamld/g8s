# OpenSpec DELTA-25: Universal Config-Driven LLM Call Consumer & Multi-Model Gateway

* **Specification ID**: `DELTA-25`
* **Title**: Universal Config-Driven LLM Call Consumer & Multi-Model Gateway
* **Status**: `APPLIED`
* **Milestone**: `M6` (LLM Consumer & Fleet)
* **ADR**: [`docs/decisions/0034-config-driven-llm-consumer-9router.md`](../../docs/decisions/0034-config-driven-llm-consumer-9router.md)
* **Supersedes / Extends**: `DELTA-05` (`05-provider-and-resource-pool-spec.md`), `DELTA-10` (`10-two-class-provider-pool-spec.md`)

---

## 1. Normative Requirements

### R1 — Declarative Provider Configuration (`providers.json`)
1. Every `api_call` provider entry in `providers.json` MUST support:
   - `class`: `"api_call"`
   - `name`: unique provider identifier (e.g., `"9router"`, `"openrouter"`, `"anthropic-gateway"`, `"local-vllm"`)
   - `protocol`: optional protocol selector: `"openai"` (default), `"anthropic"`, or `"9router"`
   - `base_url`: HTTP(S) base URL (e.g., `"http://127.0.0.1:20128/v1"` or `$G8S_9ROUTER_BASE_URL`)
   - `base_url_env`: optional environment variable overriding `base_url` at runtime (e.g., `"NINEROUTER_URL"`)
   - `auth_env`: optional environment variable carrying the API key/token
   - `headers`: optional `map[string]string` of custom HTTP headers
   - `slots`: concurrency limit (`>= 1`)
   - `allow_unlisted_models`: optional boolean allowing arbitrary upstream model IDs (e.g., `nvidia/minimaxai/minimax-m3`, `cc/claude-sonnet-5`, `cx/gpt-5.6-sol-xhigh`) when the task explicitly specifies `--provider <name>`
   - `disable_queue_exec`: optional boolean to disable queue-path execution for an `api_call` entry
   - `models`: array of `ModelEntry` descriptors (including `9router` combos `reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`, or prefix patterns like `nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`).

### R2 — Multi-Protocol HTTP Execution (`internal/provider`)
1. **`openai` / `9router` Chat Completions Protocol**:
   - Sends `POST <base_url>/chat/completions` with JSON body `{"model": "<model>", "messages": [...]}`.
   - Extracts `choices[0].message.content`, `model` (actual upstream model resolved by `9router` combo failover), and `usage` (`prompt_tokens`, `completion_tokens`, `total_tokens`).
2. **`anthropic` Messages Protocol**:
   - Sends `POST <base_url>/messages` (or `<base_url>/v1/messages` if `base_url` does not end with `/v1`) with headers `anthropic-version: 2023-06-01`, `x-api-key: <token>`, `Authorization: Bearer <token>`, and JSON body `{"model": "<model>", "max_tokens": <max>, "system": "<sys>", "messages": [{"role": "user", "content": "<brief>"}]}`.
   - Extracts text blocks from `content[]`.
3. **Health & Live Model Discovery**:
   - `Available(ctx)` probes `<base_url>/models` (with `/api/health` fallback for `9router`).
   - `DiscoverModels(ctx)` fetches `GET <base_url>/models` and parses the OpenAI-compatible `{"data": [{"id": "..."}]}` list.

### R3 — Direct CLI (`g8s llm-call`) & Queue Execution (`g8s worker`)
1. `g8s llm-call` MUST allow direct synchronous LLM calls against any configured `api_call` provider (`--provider`, `--model`, `--prompt`, `--prompt-file`, `--system`, `--effort`, `--json`).
2. `g8s worker` MUST wire `api_call` providers (unless `disable_queue_exec: true`) to `g8s llm-call --worker-envelope`, capturing response text, resolved model, and token usage in the task result envelope.
3. Because HTTP `api_call` requests do not expose local shell/filesystem tools to the remote model, `supplied_text` (`tool_policy: "none"`) and `read_only` contracts are enforced by construction.

### R4 — Catalog & Effort Registration (`.g8s/agent-models.yml`)
1. `.g8s/agent-models.yml` MUST include provider `9router` with `effort_style: named`, `field: reasoning_effort`, `default_effort: high`, and the 5 canonical combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`).
2. Two-Layer Separation Rule: Public repo files declare generic protocol mechanisms and standard combos only; private LAN endpoints or custom homelab combos reside strictly in untracked local configurations (`~/.config/g8s/providers.json`).

---

## 2. Verification Scenarios

#### Scenario: multi-protocol chat completions and anthropic messages execution
<!-- tests: TestOpenAIProvider_SpawnSuccess, TestOpenAIProvider_Spawn_AnthropicProtocol -->
- `OpenAIProvider.Spawn` executes chat completions with `stream: false`, maps reasoning effort levels, wraps context with timeout without putting timeout in JSON body, and supports Anthropic Messages endpoint.

#### Scenario: 9router health fallback and live model discovery
<!-- tests: TestOpenAIProvider_Available_9RouterFallback, TestOpenAIProvider_DiscoverModels -->
- `OpenAIProvider.Available` falls back to `/api/health` when `/models` returns 404 on 9router, and `DiscoverModels` queries `/models` and extracts model IDs.

#### Scenario: direct CLI and queue worker envelope execution
<!-- tests: TestLLMCallDirect, TestLLMCallQueueWorkerExecution -->
- `g8s llm-call` executes direct synchronous LLM calls against configured providers, and `g8s worker` bridges `api_call` providers via `--worker-envelope`.

#### Scenario: MCP server exposes g8s_llm_call tool
<!-- tests: TestMCPLLMCallTool -->
- Stdio MCP server registers `g8s_llm_call` and executes direct LLM calls returning output text and token accounting.
