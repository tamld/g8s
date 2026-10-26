# OpenSpec DELTA-43: Provider Conformance Ground-Truth Matrix & Role-Tier Gating

* **Specification ID**: `DELTA-43`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/provider/conformance`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **Unverified Provider Capabilities**: `g8s` integrated CLI agents (`opencode`, `claude`, `codex`, `gemini`, `agy`, `commandcode`) and HTTP LLM gateways (`9router`, `openai`, `anthropic`, `ollama`), but lacked mechanical ground truth proving which capabilities each provider actually honors (`P0..P8` for CLI agents, `H1..H3` for HTTP gateways).
2. **Quota & Role Misalignment**: Without explicit live-probe policies (`free`, `paid`, `forbidden`) and role-tier eligibility (`brain`, `worker`, `scout`, `compatibility-only`), an operator or supervisor could accidentally burn frontier Brain quota on F1 worker probes or attempt to use local offline models (`ollama`) for heavy SWE coding despite SSD storage and capability constraints.

---

## 2. Normative Requirements

### R1 — Probe Suite (`internal/provider/conformance/`)
- **CLI Agent Probes (`P0..P8`)**:
  - `P0` (`presence_and_version`, required): binary discovery + `--version` exit 0.
  - `P1` (`non_interactive_one_shot`, required): non-interactive one-shot execution exits cleanly.
  - `P2` (`structured_json_output`): JSON output flag advertised / parseable.
  - `P3` (`failure_exit_semantics`, required): invalid model or invocation fails with non-zero exit or structured error.
  - `P4` (`timeout_process_group_kill`, required): honors deadline and `Setpgid` process-group termination without orphan processes.
  - `P5` (`worktree_confinement`, required): writes remain confined to the target worktree.
  - `P6` (`file_edit_lands`, required): file modification lands deterministically on disk.
  - `P7` (`output_schema_respected`): honors `--output-schema` / `--json-schema`.
  - `P8` (`usage_token_accounting`): reports prompt/completion token usage.
- **HTTP LLM Gateway Probes (`P0`, `H1..H3`, `P1..P8`)**:
  - `P0` & `H1` (`model_listing`): `GET /models` reachability and OpenAI model list parsing.
  - `H2` (`tool_calling`): forced `tool_choice` produces a valid `tool_calls` entry.
  - `H3` (`context_window_claim_vs_observed`): compares `/models` declared context window against observed prompt token acceptance.

### R2 — Quota Protection & Role-Tier Profile (`policy.go`, `types.go`)
- Static mode (`default`) MUST consume zero paid tokens (`--version` / `--help` for CLI agents, `GET /models` for HTTP gateways).
- Live mode (`--live`) enforces `DecideLive`:
  - `agy` is `PolicyForbidden` (`E_LIVE_FORBIDDEN`, protecting F0 Brain quota per Lesson #23).
  - Paid providers (`claude`, `codex`, `gemini`, `commandcode`) require explicit `--allow-paid <provider>`.
  - `9router` and `opencode` only run live probes without `--allow-paid` when using cheapest/free-tier models (`fast-agent`, `reasoning-free`, `docs-retriever`, `*free*`).
- Every `ProviderReport` carries `roles_eligible` (`brain` | `worker` | `scout` | `compatibility-only`), `recommended_for`, and `note`. `ollama` is classified `compatibility-only` (`recommended_for: ["tts", "voice", "light-offline"]`).

### R3 — Ground-Truth Persistence & Pre-Dispatch Gate (`store.go`, `cmd/g8s/submit.go`)
- `g8s providers conformance [--provider <name>] [--live] [--allow-paid <csv>] [--write] [--json]`
- `--write` atomically updates `.g8s/provider-conformance.yml` (`schema_version: "g8s.provider-conformance/v1"`) with sanitized evidence (never recording private LAN IPs or secrets) and renders `docs/user-guide/provider-matrix.md`.
- `g8s submit` consults `.g8s/provider-conformance.yml` via `CheckDispatchFromDir`: if a provider has a `FAIL` on a required probe, `g8s submit` emits a warning by default and refuses dispatch (`E_PROVIDER_CONFORMANCE_FAIL`, exit code `3`) when `--enforce-conformance` is passed.

---

## 3. Verification Scenarios

#### Scenario: Static and live conformance probing across CLI and HTTP providers
<!-- tests: TestProbeHTTPStaticAndLive, TestStoreRunnerAndDispatchGate -->
- **GIVEN** CLI agent profiles and OpenAI-compatible HTTP endpoints
- **WHEN** `ProbeCLI`, `ProbeHTTP`, and `WriteMatrix` execute
- **THEN** probe verdicts (`PASS`, `FAIL`, `UNSUPPORTED`, `UNVERIFIED`), `roles_eligible` (`brain`, `worker`, `scout`, `compatibility-only`), and sanitized SHA-256 evidence are persisted and enforced by `CheckDispatchFromDir`.

