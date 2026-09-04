# Dynamic Provider Manifest & Multi-Provider Concept (9router)

**Date**: 2026-09-04
**Status**: Draft
**Author**: Sisyphus
**Supersedes**: Hardcoded model defaults in `internal/dispatch/dispatch.go`, `cmd/g8s/{submit,orchestrate,orchestrate_aic}.go`, `internal/conv/runner.go`

## 1. Problem

g8s currently hardcodes "Gemini 3.8 Flash (High)" as the default model in five places. This proves version compatibility with agy but does not prove the multi-provider concept. If agy changes model IDs, or a user wants a different provider (9router, local llama, OpenRouter, etc.), g8s is locked.

### 1.1 Hardcode locations

| File | Line | What |
|---|---|---|
| `internal/dispatch/dispatch.go` | 23 | `DefaultModel = "Gemini 3.8 Flash (High)"` |
| `internal/dispatch/dispatch.go` | 35 | `DefaultEffort = "high"` |
| `internal/conv/runner.go` | 48 | `req.Model = "gemini-3.8-flash-high"` |
| `cmd/g8s/submit.go` | 25 | CLI `--model` default |
| `cmd/g8s/orchestrate.go` | 210, 378 | CLI `--model` defaults |
| `cmd/g8s/orchestrate_aic.go` | 35 | CLI `--model` default |

### 1.2 Goals

1. Model manifest is data, not code. Change without recompiling.
2. New provider onboarding is one YAML entry, not a PR.
3. Built-in **recommend catalog** + **auto-detect** so users discover what works.
4. Add 9router as proof of the multi-provider concept (resource-pool HTTP API).

### 1.3 Non-goals

- Streaming responses. Sync blocking HTTP only in v1.
- Anthropic Messages API. OpenAI Chat Completions only in v1.
- Per-project config layering. User-level only in v1.

## 2. Architecture

### 2.1 Three-tier provider resolution

```
Built-in catalog (compiled, read-only)  ← discovery & recommend
        │
        ▼  merge at startup
Auto-detect (PATH scan, localhost probes)  ← opportunistic enable
        │
        ▼  merge
User providers.yaml (real config)  ← user explicit choice
        │
        ▼
Resolved ProviderRegistry (immutable, used by dispatch)
```

Layering is **additive**: catalog lists known providers, auto-detect marks which are present, user config fills in auth + selection. Final registry has all providers with their resolved state (`available`, `configured`, `selected`).

### 2.2 Components

**`internal/provider/registry.go`** (existing, extend)
- Add `LoadFromYAML(path string) error` to overlay user config on top of catalog.
- Add `Recommend() []ProviderInfo` returning catalog with install hints.
- Add `AutoDetect(ctx) error` running scan + probes, setting `Available` flags.
- Existing `NewRegistry()` becomes `NewRegistryFromCatalog()` (no behavior change).

**`internal/provider/catalog.go`** (new)
- Static catalog: agy, codex, claude, ollama, 9router.
- Each entry: `{Name, Class, Binary, BaseURL, Probe, Models[], DefaultModel, Slots, InstallHint}`.
- Compiled into binary (read-only), printed by `g8s providers recommend`.

**`internal/provider/openai.go`** (new)
- OpenAI Chat Completions HTTP provider.
- Implements `Provider` interface: `Name, Version, Available, Spawn`.
- Spawn signature returns an `execHandle` wrapper that does HTTP POST → read body → write receipt.
- Used by 9router and any other OpenAI-compatible endpoint.

**`internal/config/providers.go`** (new)
- Schema: `[]ProviderConfig{Name, Enabled, BaseURL, AuthEnv, APIKey, DefaultModel, Slots, Models[]}`.
- JSON loader + validator (`encoding/json` stdlib, no new dep).
- File: `~/.config/g8s/providers.json` (matches existing `providers.json` pattern in `internal/config/config.go:35`).

**`internal/dispatch/dispatch.go`** (modify)
- Remove `DefaultModel` constant.
- Change `DefaultEffort` to empty string.
- `Run()` resolves model from registry: `if req.Model == "" → registry.DefaultModel()`.
- If no provider configured, return error: `"no model resolved; run 'g8s providers recommend'"`.

**`cmd/g8s/{submit,orchestrate,orchestrate_aic}.go`** (modify)
- Remove hardcoded `--model` defaults.
- Flag value empty by default; resolution happens in dispatch layer.

**`internal/conv/runner.go`** (modify)
- Remove `req.Model = "gemini-3.8-flash-high"` assignment.
- Let dispatch layer fill the default.

**`cmd/g8s/providers.go`** (new)
- Subcommands: `list`, `recommend`, `enable`, `disable`, `init`.
- `g8s providers init` writes a starter `~/.config/g8s/providers.yaml` with 9router example.

### 2.3 Data flow (g8s submit --task "...")

1. CLI parses args, `--model` may be empty.
2. `dispatch.Run(ctx, RunOptions{Model: optModel})` called.
3. Run resolves model via `registry.DefaultModel()` if empty.
4. Registry returns `(provider, model)` tuple based on user config + auto-detect.
5. `provider.Spawn(ctx, spec)` returns handle (exec for local, HTTP for remote).
6. Handle.Wait() returns receipt.

### 2.4 Error handling

| Condition | Behavior |
|---|---|
| No providers.yaml + no auto-detected provider | `g8s providers recommend` hint + exit 3 |
| `--model X` but no provider has model X | exit 4 with list of available models |
| HTTP 401/403 | exit 5 with auth env hint |
| HTTP 429/5xx | retry 2x with 2s backoff, then exit 6 |
| Connection refused | exit 7 with endpoint hint |

### 2.5 Security

- API keys read from env only, never YAML inline.
- `~/.config/g8s/providers.yaml` mode 0600 recommended (documented, not enforced).
- HTTP provider validates URL scheme (`https://` or `http://localhost` only).
- No telemetry, no auto-update, no remote calls beyond user-configured endpoint.

## 3. Built-in Catalog (v1)

| Name | Class | Detection | Default model | Models |
|---|---|---|---|---|
| `agy` | platform_dispatch | `which agy` | (none — agy picks from `--model` flag) | catalog only |
| `codex` | platform_dispatch | `which codex` | (none) | catalog only |
| `claude` | platform_dispatch | `which claude` | (none) | catalog only |
| `ollama` | api_call | `http://localhost:11434/api/tags` | (user picks) | from probe |
| `9router` | api_call | `https://api.9router.dev/v1/models` probe | `auto` | from probe |

`agy/codex/claude` get their model list from the binary itself (`--list-models` or similar); g8s does not bake model names.

## 4. Removed (moved below)

## 5. Testing

### 5.1 Unit
- `provider/catalog_test.go`: catalog loads, recommend returns 5 entries.
- `provider/openai_test.go`: httptest mock for chat completions, verify request shape + receipt parsing.
- `config/providers_test.go`: YAML parse, validation, env expansion.
- `dispatch/dispatch_test.go`: empty Model → registry resolution; missing provider → error.

### 5.2 Integration
- Spin up local httptest server, register as 9router-compatible, run `g8s submit` against it.

### 5.3 Existing tests
- All 30 packages must still pass.
- Test fixtures referencing `gemini-3.8-flash-high` need `default_provider: agy` in fixture YAML or explicit `--model` flag.

## 6. Migration

- v0.6.1 → v0.7.0 (semver minor: new config layer, no breaking CLI).
- Users on agy: ship `g8s providers init` output as default `~/.config/g8s/providers.yaml`.
- Users on other providers: must add to YAML manually or run `g8s providers init` and edit.
- CHANGELOG: note removal of hardcoded defaults, point to `g8s providers recommend`.

## 7. Rollout

1. Add catalog + auto-detect + YAML loader (additive, no behavior change).
2. Add `g8s providers` subcommand.
## 4. User config example (`~/.config/g8s/providers.json`)

```json
{
  "providers": [
    {
      "name": "agy",
      "enabled": true,
      "default_model": "Gemini 3.8 Flash (High)",
      "slots": 4
    },
    {
      "name": "9router",
      "enabled": true,
      "base_url": "https://api.9router.dev/v1",
      "auth_env": "OPENAI_API_KEY",
      "default_model": "auto",
      "slots": 8,
      "models": [
        {"id": "gpt-4o-mini"},
        {"id": "claude-3.5-sonnet"},
        {"id": "gemini-2.0-flash"}
      ]
    }
  ],
  "default_provider": "agy",
  "default_effort": "high"
}
```
4. Remove hardcoded defaults (breaking if no config exists — warn in CHANGELOG).
5. Update docs (README, RELEASE_STRATEGY.md, integrations/antigravity.md, integrations/9router.md new).
6. Tag v0.7.0.

## 8. Open questions

- None blocking. 9router base URL is assumed `https://api.9router.dev/v1` — confirmed in user message.
- Catalog model lists for agy/codex/claude deferred (probe binary at runtime).
