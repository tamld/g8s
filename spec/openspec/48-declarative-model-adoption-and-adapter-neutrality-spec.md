# OpenSpec DELTA-48: Declarative Model Adoption & Adapter-Neutral Control Plane

* **Specification ID**: `DELTA-48`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/config`, `internal/routing`, `internal/provider`, `internal/provider/conformance`, `internal/harness/probe`, `internal/verifier`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.23.0`

---

## 1. Problem Statement

1. **Implicit/opaque model onboarding and missing declarative lifecycle metadata**: Model adoption lacked explicit `family`, `cost_tier`, `status`, and `effort_style: none` fields. Fixed-reasoning orchestrators (`gemini-pro-orchestrator*`) had no `effort_style: none` support, and element-variant clamping was absent for the Gemini 4 series (`gemini-4-hydrogen-{low,medium,high,xhigh}` vs `gemini-4-carbon-{low,medium,high}` / `gemini-4-argon-{low,medium,high}`).

2. **Adapter asymmetry**: Legacy CLI defaults (`gemini-3.8-flash-high`), `providers discover`, `providers evaluate`, `eval run`, `blind-verify`, and conformance policy treated `agy` differently from peer `api_call` (`9router`, `openai`, `anthropic`, `google`) and `platform_dispatch` (`opencode`, `codex`, `claude`, `cursor`, `commandcode`, `qwen`) adapters. Legacy docs framed `g8s` around `agy` exclusively.

---

## 2. Normative Requirements

### R1 — Declarative Model Adoption Metadata & 4-Gate Protocol (`Discover -> Declare -> Evaluate -> Blind-Verify`)

Every active model in `.g8s/agent-models.yml` MUST declare the metadata below (enforced mechanically by Check 16 in `tools/ci_doc_contract_check.sh`); transitional/deprecated catalog entries declare `status` explicitly, and operator `providers.json` entries MAY declare them directly or inherit defaults from `.g8s/agent-models.yml` via `Catalog.MergeInto`:

- `family`: Lowercase vendor-disjoint slug (e.g., `google`, `anthropic`, `openai`, `qwen`, `deepseek`, `nvidia`, `xai`, `mistral`, `multi`, `moonshot`, `zhipu`) used for `S3-A` disjoint-family blind verification (`--author-family`).
- `cost_tier`: One of `free`, `economy`, `standard`, `frontier`.
- `status`: One of `active`, `transitional`, `deprecated`.
- `effort_style`: Supports `none` for fixed-reasoning orchestrators (`gemini-pro-orchestrator*`) that omit `--effort` on `BuildCommand`.

The 4-gate adoption lifecycle:

1. **Discover**: `g8s providers discover --provider <name>` enumerates live models from the provider (supports both `api_call` and `platform_dispatch` adapters via `AgyProvider.DiscoverModels`).
2. **Declare**: Operator adds model entries with `family`, `cost_tier`, `status`, `effort_style` to `providers.json` or `.g8s/agent-models.yml`. `catalog_version: 3` onboards Gemini 4 Hydrogen/Argon/Carbon + Orchestrator + `9router/*` under `opencode` while retaining `gemini-3.8-*` as `deprecated`.
3. **Evaluate**: `g8s providers evaluate --provider <name> --audit-catalog` and `g8s eval run` (`ProviderWorkerAdapter` + `WrapProbeContractPrompt`) verify capability parity across adapters.
4. **Blind-Verify**: `g8s blind-verify` (`SetupToolPolicyNoneWorkspace` + `--sandbox` for CLI adapters) executes disjoint-family `S3-A` verification for both `api_call` and `platform_dispatch` adapters symmetrically.

`ResolveProviderDefaultModel(manifest, cat, providerName)` selects the default model per provider using declared metadata.

---

### R2 — Baked-Family Effort Clamping, `effort_style: none` & Element Cost Ranking

- **`BakedFamilyEfforts` clamps unsupported levels per sub-family**:
  - `carbon` + `xhigh` → `high` with `Mismatch: true`
  - `hydrogen` + `xhigh` → `xhigh` with `Mismatch: false`
  - `argon` + `xhigh` → `high` with `Mismatch: true`
  - `orchestrator` + any effort → `none` with `Mismatch: true`
- **`effort_style: none` yields empty applied effort and omits `--effort` on `BuildCommand`** for fixed-reasoning orchestrators.
- **`isBakedNameProvider` honors model-level `effort_style` before provider-level style**.
- **`modelCostRank` orders tiers**: `free (5)` < `economy hydrogen (30..40)` < `standard argon (70..75)` < `frontier carbon/orchestrator (95)`.

---

### R3 — Multi-Adapter Symmetry Across Discovery, Conformance, Evaluation & `S3-A` Blind Verification

- **Discovery**: `AgyProvider.DiscoverModels` + `g8s providers discover --provider agy` enables model enumeration for `platform_dispatch` adapters on par with `api_call` gateways.
- **Conformance**: `conformance` unifies `agy` under `PolicyPaid` (`DefaultModel: gemini-4-hydrogen-low`, `RolesEligible: ["brain", "worker", "scout"]`) alongside `claude` and `codex`, while `PolicyForbidden` (`E_LIVE_FORBIDDEN`) remains supported for operator-locked adapters.
- **Evaluation**: `g8s providers evaluate`, `g8s eval run` (`ProviderWorkerAdapter` + `WrapProbeContractPrompt`) support both `api_call` and `platform_dispatch` adapters symmetrically.
- **Blind Verification**: `g8s blind-verify` (`SetupToolPolicyNoneWorkspace` + `--sandbox` for CLI adapters) supports both adapter classes with disjoint-family verifier selection.

---

### R4 — Adapter-Neutral Documentation & Anti-Exclusivity CI Gate

All docs and `offer/` templates present `g8s` as an **adapter-neutral control plane** (`agy` is strictly one `platform_dispatch` adapter among peers). Enforced by **Check 16** in `tools/ci_doc_contract_check.sh`:

1. Verify every profile in `profiles:` declares a `provider` present under `providers:` and a `model` present under that provider's `models:` list.
2. Verify every model with `status: active` in `.g8s/agent-models.yml` also declares non-empty `family:` and `cost_tier:`.
3. Verify active docs and templates (`docs/integrations/antigravity.md`, `docs/quickstart.md`, `docs/user-guide/providers.md`, `docs/user-guide/effort.md`, `docs/user-guide/cli-reference.md`, `offer/ONBOARDING.md`, `offer/templates/providers.example.json`) contain ZERO `agy`-exclusivity phrases (`natively designed to supervise and isolate`, `agy subdispatch`, `AGY_MCP_ALLOW_WORKSPACE_WRITE=1`) and ZERO active `gemini-3.8-flash` recommendations.

---

## 3. Verification Scenarios

#### Scenario: Declarative model catalog metadata, Gemini 4 onboarding, and provider default resolution
<!-- tests: TestCatalogDeclarativeMetadataAndValidation, TestDefaultCatalogGemini4AndAdapterParity, TestResolveProviderDefaultModel -->
- **GIVEN** `.g8s/agent-models.yml` at `catalog_version: 3` with Gemini 4 element series and orchestrator entries
- **WHEN** `ResolveProviderDefaultModel` resolves defaults for `9router`, `opencode`, `agy`, `codex`, `claude`
- **THEN** each provider returns a model with declared `family`, `cost_tier`, `status: active`, and correct `effort_style` (`none` for orchestrator)

#### Scenario: Baked-family effort clamping, none-style orchestrator handling, and element cost ranking
<!-- tests: TestResolveEffort_Gemini4BakedVariantClampingAndNoneStyle, TestBuildCommand_OmitsNoneEffortAndSupportsXHigh, TestModelCostRank_Gemini4ElementTiers -->
- **GIVEN** Tasks submitted with `gemini-4-carbon-high` at `xhigh`, `gemini-4-hydrogen-medium` at `xhigh`, `gemini-pro-orchestrator` at `high`
- **WHEN** `ResolveEffort` and `BuildCommand` execute
- **THEN** `carbon` clamps `xhigh` to `high` with `Mismatch: true`; `hydrogen` permits `xhigh` with `Mismatch: false`; `orchestrator` omits `--effort`; `modelCostRank` orders `free < hydrogen < argon < carbon/orchestrator`

#### Scenario: Provider-neutral CLI defaults and symmetric multi-adapter evaluation and blind verification
<!-- tests: TestSubmitProviderNeutralDefaultModelAndOrchestratorNoneEffort, TestResolveCallableProviderAndCatalogMetadata, TestEvalRun_MultiAdapterResolutionAndContractPrompt, TestBlindVerify_PlatformDispatchAdapter, TestValidateDisjointVerifierFamily, TestExecuteBlindVerify_LargeMultiFileDiffShardsFailClosed, TestSanitizeDiffForBlindReview_MultiFamilyRedaction, TestAgyProvider_DiscoverModelsAndSpawnFlags -->
- **GIVEN** `providers.json` or catalog entries for `9router`, `opencode`, `codex`, `claude`, `agy`
- **WHEN** `g8s submit`, `g8s providers discover`, `g8s providers evaluate`, `g8s eval run`, `g8s blind-verify` execute
- **THEN** all commands operate symmetrically across `api_call` and `platform_dispatch` adapters; `agy` is `PolicyPaid` with `gemini-4-hydrogen-low`; blind verify uses disjoint-family verifiers for all adapters