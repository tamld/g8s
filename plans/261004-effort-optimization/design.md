# Design: effort-optimization — provider-neutral effort manifest (roadmap wave)

**Status**: APPROVED direction (operator "Duyệt" 2026-10-04); design
proposed. **Session type**: T1 (strategy)

## Problem (operator's review finding)

Every worker dispatch this campaign ran at the DEFAULT effort (high,
baked into the agy model name). ~60-70% of the quota went to
mechanical/enum-driven work that needed low-medium. There is no effort
knob, no class→effort mapping, and no per-task token accounting to even
measure the waste.

## Cross-platform survey (2026-10-04) — how platforms declare effort

| Platform | Style | Declaration |
|---|---|---|
| OpenAI (GPT-5 family, o-series) | named | `reasoning_effort`: minimal \| low \| medium \| high (+ `none` on 5.1; adaptive default) |
| Anthropic Claude | named + budget | `effort` named levels AND/OR `thinking.budget_tokens` (≥1024, < max_tokens) — co-designed |
| Google Gemini 3.x | named | `thinking_level`: minimal \| low \| high (replaces the 2.x integer `thinkingBudget`) |
| agy CLI (local) | baked-name | effort encoded in the model name (`gemini-3.8-flash-high`) |
| DeepSeek hybrid / Ollama | toggle | thinking on/off only |
| xAI Grok | named | `reasoning_effort`: low \| high |

Three real styles: **named**, **budget**, **baked-name/toggle**.

## Design — g8s canonical effort dimension

1. **Canonical levels**: `low | medium | high` (deny-by-default maps
   unregistered requests to medium — NOT high; recorded, never silent).
2. **Per-model manifest declaration** (providers.json / config.File —
   the provider registry, not a new store):
   ```yaml
   models:
     - name: "gemini-3.8-flash-{effort}"     # baked-name style: {effort} placeholder
       effort_style: baked-name
       effort_levels: [low, medium, high]
     - name: "claude-opus-5"
       effort_style: named                    # provider-native named param
       effort_levels: [low, medium, high]
       effort_budget_map: {low: 2048, medium: 8192, high: 32768}   # when the provider wants tokens
     - name: "deepseek-v3.1"
       effort_style: toggle
       effort_levels: [medium, high]          # low unsupported -> nearest + recorded
   ```
3. **Translation at dispatch** (the adapter, one place):
   named → pass through; baked-name → substitute {effort}; budget →
   lookup the map; toggle → nearest-supported with the substitution
   RECORDED in the decision (`effort_applied` vs `effort_requested`) —
   no silent approximation (no-silent-fallback doctrine).
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
