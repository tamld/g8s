# Phase 3 (WF3): Sovereign HITL Calibration & Hypothesis Falsification Engine (`#596` — Hypothesis H3)

## 1. Objective
Implement the 3-level watchman hierarchy anchored by **Sovereign HITL Ground Truth**, Sine-Wave rule weight regression $W_{t+1}$, and shadow-mode empirical falsification metrics for **Hypotheses `H1`, `H2`, and `H3`**.

## 2. Target Files
- `internal/telemetry/governance.go` (new)
- `internal/telemetry/governance_test.go` (new)
- `internal/telemetry/engine.go`
- `cmd/g8s/feedback.go` (new)
- `cmd/g8s/feedback_test.go` (new)
- `cmd/g8s/main.go`
- `spec/openspec/24-differential-governance-hitl-spec.md` (new)
- `manifest.json`

## 3. Implementation Details
1. **SQLite Schema Extensions in `internal/telemetry`**:
   - `rule_weights`: `rule_id TEXT PRIMARY KEY, package TEXT, weight REAL, thermal_state TEXT, tp_count INTEGER, fp_count INTEGER, hitl_tp INTEGER, hitl_fp INTEGER, hitl_veto INTEGER, dormant_triggers INTEGER, dormant_escapes INTEGER, updated_at INTEGER`
   - `override_receipts`: `id TEXT PRIMARY KEY, task_id TEXT, rule_id TEXT, actor TEXT, reason TEXT, auto_outcome TEXT, hitl_verdict TEXT, created_at INTEGER`
   - `hitl_feedback`: `id TEXT PRIMARY KEY, rule_id TEXT, task_id TEXT, verdict TEXT, actor TEXT, note TEXT, created_at INTEGER`
   - `k_yield_samples`: `id TEXT PRIMARY KEY, task_id TEXT, package TEXT, active_k INTEGER, first_pass_ok INTEGER, created_at INTEGER`
2. **Sine-Wave Weight Regression & Sovereign HITL Precedence**:
   - Constants: $\alpha_{\text{auto}} = 0.10$, $\beta_{\text{auto}} = 0.15$, $\alpha_{\text{HITL}} = 0.50 \gg \alpha_{\text{auto}}$, decay $\lambda = 0.05/\text{day}$.
   - Thermal thresholds: `Hot` ($W_t \ge 0.65$), `Warm` ($0.30 \le W_t < 0.65$), `Cold` ($W_t < 0.30$, dormant in SQLite = 0 context tokens).
   - Carrying Capacity `EnforceCarryingCapacity(ctx, pkg, maxK)`: keeps at most `maxK` rules in `Hot` state per package (demoting the lowest-weight rule to `Warm`/`Cold` on "one-in, one-out").
   - HITL `veto` or `tp` immediately promotes a rule to `Hot` ($W_t = \max(W_t + \alpha_{\text{HITL}}, 0.85)$); HITL `fp` immediately penalizes $W_t = \max(0.0, W_t - \alpha_{\text{HITL}})$.
3. **Hypothesis Falsification Engine (`EvaluateHypotheses`)**:
   - Computes empirical status (`hypothesis` / `validated` / `falsified` / `insufficient_data`) for:
     - **`H1` (`CarryingCapacityKMax`)**: First-Pass Yield (`FPY`) bucketed by $K \le 3$ vs $K > 3$.
     - **`H2` (`SineWaveDormancy`)**: `DormantEscapeRate = dormant_escapes / max(1, dormant_triggers)` (target $< 0.05$).
     - **`H3` (`HITLAnchoredRegression`)**: `HITLDisagreementRate` and mean `RuleF1Precision`.
4. **CLI Surface (`g8s feedback`)**:
   - `g8s feedback record --rule <id> --verdict <tp|fp|veto> [--task <id>] [--package <pkg>] [--note "..."]`
   - `g8s feedback override --task <id> --rule <id> --reason "..."`
   - `g8s feedback report [--max-k 3]` (emits JSON envelope with `HypothesisReport` for `H1`, `H2`, `H3` + active rule weights).
