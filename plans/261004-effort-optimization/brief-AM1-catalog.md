# Task: AM1 — agent-models catalog v1 (issue #550; feeds the versioned provider manifest)

Repo: g8s @ main. You author the SUPPORTED-MODELS CATALOG: a versioned,
declarative list of agent models and their effort capabilities, verified
against official docs on 2026-10-04 by three research agents. This
catalog becomes the seed of the g8s provider manifest (config.File
loader lands in a later slice — you produce the DATA + schema draft
only).

## Delivery protocol

Scratch worktree. Write ONLY to:

- .g8s/agent-models.yml (new — the catalog; note .g8s/ is gitignored,
  the file will be force-added at harvest, do not fight git)

Do NOT run git commit. Do NOT touch any other file.

## Target schema (agent-models.v1)

```yaml
schema_version: agent-models.v1
catalog_version: 1
verified_at: "2026-10-04"
sources:
  - https://developers.openai.com/api/docs/models
  - https://developers.openai.com/api/docs/guides/reasoning
  - https://platform.claude.com/docs/en/docs/build-with-claude/effort
  - https://ai.google.dev/gemini-api/docs/thinking
  - https://docs.x.ai/developers/model-capabilities/text/reasoning
  - https://api-docs.deepseek.com/guides/reasoning_model
  - https://docs.mistral.ai/capabilities/reasoning/
  - https://openrouter.ai/docs/use-cases/reasoning-tokens
providers:
  <provider-key>:
    effort_style: named | budget | baked-name | toggle
    field: <exact API field name or "model-name-suffix">
    default_effort: <level or "adaptive">
    mandatory: <bool — true when thinking cannot be disabled>
    notes: <provider-level caveats, one line>
    models:
      - id: <exact current model identifier>
        supported_efforts: [ ...ordered subset of none,minimal,low,medium,high,xhigh,max ]
        default_effort: <level or "adaptive">
        mandatory: <bool, optional override>
        notes: <one line, only when load-bearing>
```

## The verified data (from the 2026-10-04 research — transcribe faithfully)

**openai** (named; field `reasoning_effort` / `reasoning.effort` on
Responses; adaptive default, per-model):
- gpt-6-astra: [low, medium, high, xhigh, max] — no none; Chat
  Completions lacks function calling for this model
- gpt-6.1-sol: [low, medium, high, xhigh, max], default medium
- gpt-6-luna: [none, low, medium, high, xhigh, max] — only model with none
- gpt-5.5 / gpt-5.5-pro / gpt-5.6-sol / gpt-5.6-terra / gpt-5.6-luna /
  gpt-5.6-cyber (prior-gen, live): subsets of
  [none, minimal, low, medium, high, xhigh, max], default medium
- deprecated 2026-10-23: o3, o3-pro, o1, o4-mini (do NOT list as current;
  one provider-level note)

**anthropic** (named; field `output_config.effort`; mid-conversation
per-message variant exists):
- claude-opus-5-5: [low, medium, high, xhigh, max], default **medium**
- claude-sonnet-5-5: [low, medium, high, xhigh], default high
  (xhigh/max combo: thinking {"type": "between_tools"} 400s at xhigh+)
- claude-fable-5-1: [low, medium, high, xhigh, max], default high
- claude-haiku-4-5: NO effort support (record with supported_efforts: [])
- provider note: budget_tokens deprecated — rejected 400 on 4.7+ models;
  adaptive thinking mode on Opus 5.5 (cannot disable)

**google** (named; field `thinking_level`; dynamic default = model-decided):
- gemini-3.1-pro-preview: [minimal, low, medium, high], default dynamic
- gemini-3.8-flash: [low, medium, high], default on (medium)
- gemini-3.5-flash / 3.6-flash / 3.7-flash: [minimal, low, medium, high]
- gemini-3.5-flash-lite / 3.1-flash-lite: [minimal, high], default minimal

**xai** (named; field `reasoning_effort`; default high; thinking cannot
be disabled — mandatory: true):
- grok-4.7: [low, medium, high, xhigh]
- grok-4.6: [low, medium, high, xhigh]
- grok-4.5: [low, medium, high] (xhigh silently treated as high)

**deepseek** (named + toggle hybrid; field `reasoning_effort` +
`thinking: {type: "enabled"}`; thinking default ON):
- deepseek-flash: [medium, high] (non-thinking mode exists = low-equivalent
  via thinking omitted — record in notes, not as a level)
- deepseek-v4-pro: [medium, high] (same note)

**mistral** (named; field `reasoning_effort`):
- mistral-medium-3.5: [high, none]
- mistral-small-latest: [high, none]
- zai-glm-5-3 (hosted): [low, high, max]

**ollama** (toggle or model-defined strings; discovery via /api/show is
MANDATORY per model — record as provider note, no fixed model list):
- provider-level entry only: effort_style: toggle, note "string levels are
  model-defined; discovery via /api/show (e.g. gpt-oss: low/medium/high;
  qwen3/deepseek-r1: boolean)"

**agy** (baked-name; template `gemini-3.8-flash-{effort}`):
- gemini-3.8-flash: [low, medium, high] (the local wrapper's convention;
  mirrors google's gemini-3.8-flash levels)

## Rules

- Transcribe EXACTLY the data above — do not add models from your own
  memory (the catalog is research-verified only; models you know but that
  are absent here are out of scope by design).
- Ordered subsets only; default_effort must be a member of
  supported_efforts OR the literal "adaptive"/"dynamic" (record which).
- One-line notes only; YAML comments for anything that does not fit the
  schema.
- Validate: `python3 -c "import yaml,sys; yaml.safe_load(open('.g8s/agent-models.yml')); print('YAML_OK')"`
  must pass before you finish.
- Report: the provider/model counts, any schema field you found
  insufficient, and the YAML validation output.
