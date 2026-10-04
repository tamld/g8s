# Design: effort-optimization — provider-neutral effort manifest (roadmap wave)

**Status**: APPROVED direction (operator "Duyệt" 2026-10-04); design
proposed. **Session type**: T1 (strategy)

## Problem (operator's review finding)

Every worker dispatch this campaign ran at the DEFAULT effort (high,
baked into the agy model name). ~60-70% of the quota went to
mechanical/enum-driven work that needed low-medium. There is no effort
knob, no class→effort mapping, and no per-task token accounting to even
measure the waste.

## Cross-platform survey (VERIFIED 2026-10-04 via 3 research agents against official docs — supersedes the training-cutoff draft)

| Platform | Current flagship (Oct 2026) | Effort field | Values | Default |
|---|---|---|---|---|
| OpenAI | gpt-6-astra / gpt-6.1-sol / gpt-6-luna (GPT-5.x + o-series DEPRECATED; o-series shutdown 2026-10-23) | reasoning.effort (Responses) / reasoning_effort (Chat) | none, minimal, low, medium, high, xhigh, max (7-value enum; per-model subsets) | adaptive, per-model |
| Anthropic | claude-opus-5-5 (default MEDIUM), sonnet-5-5, fable-5-1, mythos-5.x | output_config.effort (+ mid-conversation per-message variant) | low, medium, high, xhigh, max | medium (Opus 5.5), high (others) |
| Google | gemini-3.1-pro-preview / gemini-3.8-flash / 3.5-flash-lite | thinking_level (replaces thinkingBudget) | minimal, low, medium, high (per-model subsets; lite = minimal+high only) | dynamic (model-decided) |
| xAI | grok-4.7 | reasoning_effort | low, medium, high, xhigh (cannot disable) | high |
| DeepSeek V4 | deepseek-flash / deepseek-v4-pro | thinking:{type:"enabled"} + reasoning_effort | parameter-based now (the old chat/reasoner NAME split is retired) | thinking default ON |
| Mistral | mistral-medium-3.5 (+ hosted zai-glm-5-3: low/high/max) | reasoning_effort | high, none (glm: low/high/max) | — |
| Ollama | local | think (bool OR model-defined strings) | model-defined; hard 400 on non-thinking models | model default |
| agy (local wrapper) | gemini-3.8-flash-{effort} | baked-name suffix | low/medium/high | baked |

2026 mechanisms the manifest must know about: adaptive reasoning as DEFAULT (OpenAI + Anthropic Opus 5.5 always-on), reasoning.mode standard|pro (OpenAI), xhigh/max tiers, mid-conversation effort updates (OpenAI configuration_update; Anthropic per-message output_config beta), budget_tokens DEPRECATED (Claude 4.7+ rejects with 400), multi-agent effort (grok-4.20: effort = agent count, not depth).

**Aggregator prior art (the schema to mirror):** OpenRouter exposes per-model reasoning metadata — supported_efforts[], default_effort, mandatory — via GET /models, translates effort per provider (Gemini xhigh→high mapped down; Anthropic budget = max_tokens × ratio 0.95/0.8/0.5/0.2/0.1, floor 1024), and uses silent-nearest mapping with hard-400 exceptions (mid-conversation changes, mandatory-reasoning models asked to disable). LiteLLM ships explicit per-provider translation tables. Two industry fallback conventions exist: silent-nearest (OpenRouter/LiteLLM) vs hard-400 (Ollama non-thinking models, mandatory-reasoning disable).

## Design — g8s canonical effort dimension

1. **Canonical scale**: an ORDERED 7-level ladder — `none < minimal < low
   < medium < high < xhigh < max` — matching the industry enum; each model
   declares its supported subset. The dispatch DEFAULT is medium (the
   industry median: Opus 5.5 ships medium; adaptive models decide below
   it — never default to high).
2. **Per-model manifest declaration** (providers.json / config.File —
   the provider registry), mirroring the OpenRouter discovery schema:
   ```yaml
   models:
     - name: "gemini-3.8-flash-{effort}"
       effort_style: baked-name          # baked-name | named | budget | toggle
       supported_efforts: [low, medium, high]
       default_effort: medium
       mandatory: false                  # true = thinking cannot be disabled (grok-4.7, mandatory-reasoning models)
     - name: "claude-opus-5-5"
       effort_style: named               # output_config.effort
       supported_efforts: [low, medium, high, xhigh, max]
       default_effort: medium
       effort_budget_map: {low: 2048, medium: 8192, high: 32768}   # only for legacy budget-style providers
     - name: "ollama:qwen3"
       effort_style: toggle
       supported_efforts: [medium, high] # low unsupported -> nearest + RECORDED
   ```
3. **Translation at dispatch** (the adapter, one place): named →
   pass-through the level if supported; baked-name → substitute {effort};
   budget → effort_budget_map lookup; toggle → nearest-supported.
   **Fallback convention (g8s chooses a hybrid of the two industry
   conventions)**: unsupported level ⇒ nearest-supported is APPLIED and
   RECORDED (`effort_requested` vs `effort_applied` on the Decision —
   an advisory mismatch signal, never silent); hard-REFUSE only when
   `mandatory: true` and the request is `none` (the provider rejects it
   anyway — refuse locally with the typed error instead of paying the
   round-trip).
4. **Task-class mapping** (`.g8s/effort-classes.yml`, mirrors the
   verifier-classes pattern): echo/mechanical→low, docs/test→medium,
   feature/red-cell/discovery→high, unregistered→medium. The brief may
   override with `--effort`; an override DOWN for a high-class task is
   recorded as an advisory signal.
5. **Telemetry + honesty**: per-task (class, effort_requested,
   effort_applied, tokens_in/out) into the telemetry events; SCORECARD
   row cost-per-class; one A/B measurement (a representative mechanical
   slice at low vs high — outcome-quality delta is a recorded number,
   not a guess) before trusting the mapping.
6. **Roadmap placement**: the wave lands as v0.16.0 (target) — v0.15.0
   is the integrity release and stays scoped; the effort wave is its own
   falsifiable step.

## Non-goals

No LLM choosing its own effort (the mapping is declarative + the
brief declares; the router may SUGGEST, never raise). No silent
downgrades. No per-prompt dynamic effort (YAGNI until the A/B data
demands it).
