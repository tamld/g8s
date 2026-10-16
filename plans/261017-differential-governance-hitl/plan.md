---
title: "Differential Governance, Self-Silencing Rules, and Sovereign HITL Calibration"
issues: [594, 595, 596]
status: in_progress
created: 2026-10-17
epistemic_status: hypothesis
---

# Plan: Differential Governance, Self-Silencing Rules, and Sovereign HITL Calibration (`#594`, `#595`, `#596`)

- **Session type**: T1 (strategy + implementation)
- **Correlated Issues**: `tamld/g8s#594`, `tamld/g8s#595`, `tamld/g8s#596`
- **Second Brain Hypothesis Note**: `2-Insights/I-Differential_Governance_Self-Silencing_Rules_and_HITL_Ground_Truth.md` (`epistemic_status: hypothesis`)
- **OpenSpec Delta**: [`spec/openspec/24-differential-governance-hitl-spec.md`](../../spec/openspec/24-differential-governance-hitl-spec.md)
- **Status**: `IN_PROGRESS`

---

## 1. Executive Summary & Architectural Axioms

This plan implements **Differential Governance ("Trust, But Verify")** natively in `g8s` while treating dynamic rule theories (`H1`, `H2`, `H3`) as **falsifiable hypotheses** anchored by **Sovereign Human-In-The-Loop (HITL) Ground Truth**:

1. **Zero Provider Lock-in (`Jev` Optional)**: 100% Pure-Go implementation (`modernc.org/sqlite` FTS5 + git Net-Delta diff parser). Works offline without `Jev`.
2. **Never Strangle LLM Intelligence (True JIT, Zero In-Flight Blocking)**:
   - Agents inside an isolated `WriteReceipt` worktree are never blocked mid-thought by mechanical linters on `g8s edit`.
   - Historical failure anchors (`SpatialAnchor`) are delivered strictly as a non-blocking 1-line `jit_whisper` on `g8s read` / `g8s edit` and scoped pre-flight context, capped by a configurable carrying capacity $K_{\max}$ (default `3`, configurable via `G8S_JIT_MAX_RULES` to test **Hypothesis `H1`**).
3. **Net-Delta Handoff Verification (`CheckTypeDiffGuard = "diff-guard"`)**:
   - Evaluates only added lines (`+`) in the Net-Delta ($\Delta = \text{After} - \text{Before}$) at `g8s verify`.
   - Separates non-overridable `Hard Invariants` (hardcoded secrets in $\Delta$) from `Advisory Signals` (`SpatialAnchor.DeniedFragments` and Tier-0 silent error swallowing `_ =` on errors), and records dormant rule re-awakening to test **Hypothesis `H2`**.
4. **3-Level Watchman Hierarchy & Sovereign HITL Ground Truth (`g8s feedback`)**:
   - **Level 1 (`Guard <-> F0`)**: `F0` can override an `Advisory Signal` by recording a signed `OverrideReceipt`.
   - **Level 2 (`Delayed Reality Proxy`)**: Automated test/verification outcomes adjust rule weights ($W_t$) with modest step $\alpha_{\text{auto}}, \beta$.
   - **Level 3 (`Sovereign HITL Ground Truth`)**: Human operator verdicts (`g8s feedback record --rule <id> --verdict tp|fp|veto`) carry sovereign weight ($\alpha_{\text{HITL}} \gg \alpha_{\text{auto}}$), immediately anchoring rule weight $W_t$, thermal state (`hot`/`warm`/`cold`), and computing the falsification metrics for **Hypotheses `H1`, `H2`, and `H3`** (`g8s feedback report`).

---

## 2. Phase Breakdown & Wave Routing (`F0 -> F1`)

| Phase | Spec File | Issue | Scope (`AllowedPaths`) | Hypothesis | Dependencies |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Phase 1 (WF1)** | [`phase-01-spatial-jit-whisper-and-broker.md`](phase-01-spatial-jit-whisper-and-broker.md) | `#594` | `internal/vault/**`, `internal/context/**`, `internal/fsops/**`, `internal/worker/**`, `cmd/g8s/brief_issue.go` | `H1` ($K_{\max} \le 3$ Carrying Capacity) | None |
| **Phase 2 (WF2)** | [`phase-02-pure-go-net-delta-diff-guard.md`](phase-02-pure-go-net-delta-diff-guard.md) | `#595` | `internal/verifier/**`, `cmd/g8s/verify.go` | `H2` (Sine-Wave Dormant Re-Awakening) | Phase 1 |
| **Phase 3 (WF3)** | [`phase-03-hitl-calibration-and-hypothesis-engine.md`](phase-03-hitl-calibration-and-hypothesis-engine.md) | `#596` | `internal/telemetry/**`, `cmd/g8s/feedback*.go`, `cmd/g8s/main.go`, `spec/openspec/**`, `manifest.json` | `H3` (Sovereign HITL Calibration & Falsification) | Phase 1, Phase 2 |

---

## 3. Definition of Done (DoD)

- [x] **Phase 1 (`#594`)**: `Vault.MatchSpatial(ctx, targetPath, maxK)` returns active (`APPLIED`/`ACCEPTED`) records matching file or package; `fsops.Read` and `fsops.Edit` attach non-blocking `JITWhisper` (`jit_whisper,omitempty`) when `G8S_VAULT_DB` has matching anchors; `cmd/g8s/brief_issue.go` uses scoped `GetRelevantPatterns` + `QueryTopPatterns` fallback capped by `K_max`; `internal/worker/worker.go` wires `VaultNotes` into `ContextBroker`.
- [x] **Phase 2 (`#595`)**: `internal/verifier` supports `CheckTypeDiffGuard = "diff-guard"` in `ParseRegistry` and `VerifyContext`; `diff_guard.go` evaluates only added lines (`+`) in git Net-Delta against `Hard Invariants` (secrets) and `Advisory Signals` (`DeniedFragments` + swallowed error patterns), supporting `OverrideReceipt` and dormant rule re-awakening.
- [x] **Phase 3 (`#596`)**: `internal/telemetry` implements `rule_weights`, `override_receipts`, and `hitl_feedback` tables, Sine-Wave weight regression $W_{t+1}$ with $\alpha_{\text{HITL}} \gg \alpha_{\text{auto}}$, thermal state transitions (`hot`/`warm`/`cold`), and `EvaluateHypotheses(ctx)` for `H1`, `H2`, and `H3`; `g8s feedback <record|override|report>` CLI subcommand is wired and tested.
- [x] OpenSpec `DELTA-24` (`spec/openspec/24-differential-governance-hitl-spec.md`) and `manifest.json` are synced and pass `tools/ci_spec_code_sync.sh`, `tools/ci_structure_sync.sh`, `tools/ci_link_integrity.sh`, `golangci-lint`, and `go test -race ./...`.
