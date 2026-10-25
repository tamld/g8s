---
title: "v0.19.0 — Universal Config-Driven LLM Call Consumer & 9Router Multi-Model Integration"
status: in_progress
priority: P1
created: 2026-10-21
adr: docs/decisions/0034-config-driven-llm-consumer-9router.md
openspec: spec/openspec/25-config-driven-llm-consumer-spec.md
---

# Plan: `v0.19.0` — Universal Config-Driven LLM Call Consumer & `9router` Multi-Model Integration

**Session type**: T2 (execution)
**Phase / Milestone**: `v0.19.0` (`DELTA-25`)

## 1. Objective
Enable `g8s` to act as a **universal, config-driven LLM call consumer** capable of consuming any LLM model or virtual combo declared in config (`providers.json` and `.g8s/agent-models.yml`) — with first-class support for **`9router`** (`http://127.0.0.1:20128/v1` / `$G8S_9ROUTER_BASE_URL`, combos `reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`, and prefixes `nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`), OpenAI `/v1/chat/completions`, and Anthropic `/v1/messages`.

---

## 2. Slice Decomposition (Respecting DEBT-34 Layer Boundaries)

| Slice | Layer | Scope (`AllowedPaths`) | Description |
| :--- | :--- | :--- | :--- |
| **WF1 (`v019-p1`)** | `config` + `provider` + `.g8s` | `internal/config/**`, `internal/provider/**`, `.g8s/agent-models.yml` | Extend `config.ProviderEntry` (`protocol`, `base_url_env`, `headers`, `allow_unlisted_models`, `disable_queue_exec`, prefix matching); upgrade `internal/provider` (`OpenAIProvider` + `Anthropic` `/v1/messages` + `9router` `/api/health` & `/v1/models` live discovery + `9router` catalog entry); register `9router` combos in `.g8s/agent-models.yml`. |
| **WF2 (`v019-p2`)** | `cmd/g8s` + `docs` + `README` + `version` | `cmd/g8s/**`, `docs/**`, `README.md`, `README.vi.md`, `CHANGELOG.md`, `manifest.json`, `packaging/**` | Add `g8s llm-call` and `g8s providers discover`; wire `api_call` providers into `runWorker`, `runProviders`, and `runSubmit`; update `PRD.md`, `SRS.md`, `DOD_DOR.md`, `README.md`, `README.vi.md`, `CHANGELOG.md`, and bump version to `v0.19.0`. |

---

## 3. Definition of Ready (DoR) for `v0.19.0`
- [x] **DoR-1 (Knowledge Grounding)**: `9router` architecture, combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`), prefixes (`nvidia/`, `openrouter/`, `cc/`, `cx/`), and endpoints (`/v1/chat/completions`, `/v1/messages`, `/v1/models`, `/api/health` on `127.0.0.1:20128` / `$G8S_9ROUTER_BASE_URL`) verified via `wiki.py` and endpoint probe (`{"ok":true}`).
- [x] **DoR-2 (ADR & OpenSpec Ratified)**: Codified in [`docs/decisions/0034-config-driven-llm-consumer-9router.md`](../../docs/decisions/0034-config-driven-llm-consumer-9router.md) and [`spec/openspec/25-config-driven-llm-consumer-spec.md`](../../spec/openspec/25-config-driven-llm-consumer-spec.md).
- [x] **DoR-3 (DEBT-34 Layer Isolation)**: Zero changes to `internal/worker/**` (`W=0`) by bridging `api_call` providers through `g8s llm-call --worker-envelope` in `cmd/g8s/main.go`.
- [x] **DoR-4 (Prerequisite `v0.18.0` Complete)**: `v0.18.0` released with all prior GitHub issues and PRs closed before merging `v0.19.0`.

---

## 4. Definition of Done (DoD) for `v0.19.0`
- [ ] **DoD-1 (Config-Driven Multi-Protocol Support)**: Any provider declared in `providers.json` with `class: "api_call"` and `protocol: "openai" | "anthropic" | "9router"` loads, probes, and executes LLM calls without hardcoded provider logic.
- [ ] **DoD-2 (`9router` Multi-Model & Combo Support)**: `.g8s/agent-models.yml` and `internal/config` support `9router` combos (`reasoning-free`, `reasoning-high`, `deep-coding`, `fast-agent`, `docs-retriever`) and prefix wildcard models (`nvidia/*`, `openrouter/*`, `cc/*`, `cx/*`).
- [ ] **DoD-3 (End-to-End CLI & Queue Execution)**: Both `g8s llm-call --provider 9router --model reasoning-free` and `g8s submit --provider 9router --model reasoning-free` + `g8s worker --once` execute end-to-end against both unit-test `httptest.Server` and gateway (`http://127.0.0.1:20128/v1`).
- [ ] **DoD-4 (Zero-CGO Tests & Gates)**: `CGO_ENABLED=0 go test ./...`, `./tools/pre_push.sh --fast`, `tools/ci_version_sync_check.sh`, `tools/ci_doc_contract_check.sh`, and `tools/ci_link_integrity.sh` pass 100%.
- [ ] **DoD-5 (Release & Cross-Project Dogfooding)**: `v0.19.0` tagged and released on GitHub, and dogfooded across `adopter-utility-01`, `defense-in-depth`, `aegis`, and `hash-checker`.
