# OpenSpec DELTA-39: Universal CLI Agent Adapter Suite (`opencode`, `commandcode`, `codex`) & Free-Model Tiered Routing

* **Specification ID**: `DELTA-39`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/provider`, `internal/worker`, `internal/routing`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Release**: `v0.20.0`

---

## 1. Problem Statement

1. **Fragmented External CLI Agent Integration**: Operators run heterogeneous coding CLI agents (`opencode`, `commandcode`, `codex`, `claude`, `agy`). Previously, `codex` was a stub and `opencode`/`commandcode` were unregistered in the built-in provider registry and worker command resolver.
2. **Post-Run Worktree Mutation Scope Verification**: External CLI agents that mutate files directly in their isolated git worktree (instead of calling `g8s edit`) require a deterministic post-run worktree diff firewall verifying `git status --porcelain` against the task's `permission` and `WriteReceipt.AllowedPaths`.
3. **Zero-Cost Reconnaissance Routing**: High-volume `read_only` reconnaissance tasks (`scout`, `collector`, `researcher`, `analyst`, `sec-auditor`, `summarizer`) should automatically route to healthy free-tier models (`reasoning-free`, `docs-retriever`, `opencode/free`, `commandcode/free`) when present in the active manifest.

---

## 2. Normative Requirements

### R1 — Universal CLI Agent Providers (`internal/provider/cli_agents.go`, `internal/provider/codex.go`)
- Register `OpenCodeProvider` (`opencode`, override `OPENCODE_BIN`, `opencode run [--model <m>] <brief>`), `CommandCodeProvider` (`commandcode`, override `COMMANDCODE_BIN`, `commandcode run [--model <m>] <brief>`), and full `CodexProvider.Spawn` (`codex exec [--model <m>] <brief>`) in `provider.NewRegistry()` and `provider.Catalog()`.
- Expose `worker.DefaultCLIProviderTemplates()` so `NewProviderCommandResolver` resolves non-interactive CLI invocations for `opencode`, `commandcode`, and `codex` without requiring boilerplate in `providers.json`.

### R2 — Post-Run Worktree Diff Firewall (`internal/worker/diff_firewall.go`)
- `VerifyWorktreeDiffScope(worktreeDir, permission string, allowedPaths []string)` MUST inspect `git -C <worktreeDir> status --porcelain --untracked-files=all` after worker execution.
- Under `read_only`, `automation_read`, or `supplied_text` permissions, any modified or untracked file MUST fail closed with `E_DIFF_FIREWALL_READ_ONLY_MUTATION`.
- Under `workspace_write` with non-empty `allowedPaths`, any modified or untracked file outside `allowedPaths` MUST fail closed with `E_DIFF_FIREWALL_OUT_OF_SCOPE` and mark the attempt as a non-retryable `contract_violation`.

### R3 — Free-Model Tiered Routing (`internal/routing/router.go`)
- `IsFreeTierModel(modelID)` MUST classify `reasoning-free`, `docs-retriever`, and `*free*` models (`opencode/free`, `commandcode/free`) at the lowest cost rank (`5`).
- `RouteDeterministic` MUST route `read_only` reconnaissance tasks (`scout`, `collector`, `researcher`, `analyst`, `sec-auditor`, `summarizer`) to healthy free-tier candidates when available in the manifest.

---

## 3. Verification Scenarios

#### Scenario: Universal CLI Agent Adapters and Built-In Worker Templates
<!-- tests: TestUniversalCLIAgentAdapters -->
- **GIVEN** mock CLI binaries for `opencode`, `commandcode`, and `codex`
- **WHEN** `Available`, `Version`, `Spawn`, and `NewProviderCommandResolver` are invoked
- **THEN** each provider resolves its version, spawns in its isolated process group, and `NewProviderCommandResolver` expands the default non-interactive template.

#### Scenario: Post-Run Worktree Diff Firewall Rejects Out-Of-Scope and Read-Only Mutations
<!-- tests: TestWorktreeDiffFirewallScopeEnforcement -->
- **GIVEN** an isolated git worktree where an external CLI agent modifies files
- **WHEN** `VerifyWorktreeDiffScope` runs under `read_only` or under `workspace_write` with `allowedPaths`
- **THEN** in-scope edits pass, `read_only` mutations fail with `E_DIFF_FIREWALL_READ_ONLY_MUTATION`, and out-of-scope edits fail with `E_DIFF_FIREWALL_OUT_OF_SCOPE`.

#### Scenario: Free-Tier Reconnaissance Model Routing
<!-- tests: TestFreeTierReconRouting -->
- **GIVEN** a manifest containing paid models alongside free-tier models (`9router/reasoning-free`, `opencode/opencode/free`)
- **WHEN** `RouteDeterministic` evaluates a `read_only` or reconnaissance-role request
- **THEN** the router selects the healthy free-tier candidate and falls back to the paid tier only when trust-boundary rules or quarantine apply.
