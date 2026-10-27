# OpenSpec DELTA-45: 5-Axis F1/F2/Tool/Skill/Receipt Cross-Evaluation & Kaizen Engine

* **Specification ID**: `DELTA-45`
* **Status**: `APPLIED` (Status: implemented)
* **Implementation Status**: `IMPLEMENTED`
* **Target Packages**: `internal/routing`, `cmd/g8s`
* **Milestone**: `M6 (LLM Consumer & Fleet)`
* **Issue**: #680
* **domain_href**: `spec/domains/03-providers-and-routing/spec.md#R4-five-axis-scorecard`
* **feature_id**: `FEAT-ROUTING-LANE-01`
* **Date**: 2026-10-27

---

## 1. Context & Problem Statement

Evaluating worker models in isolation masks systemic friction caused by over-broad `WriteReceipt` scopes, CLI/MCP flag mismatches, or uncalibrated System-1 `Jev` triage. `DELTA-45` introduces a 5-Axis Cross-Evaluation & Kaizen Engine exposed via `./bin/g8s scorecard [status|audit|kaizen] --json`.

## 2. Normative Requirements

1. **Axis 1 — F1/F2 Worker Yield (`f1_f2_worker_yield`)**: Tracks terminal task counts, first-pass yield, CLI agent API-error count, and rolling per-model composite scores.
2. **Axis 2 — WriteReceipt Tightness (`write_receipt_tightness`)**: Audits total vs consumed receipts and flags wildcard (`**`, `*`, `.`) scope over-provisioning.
3. **Axis 3 — Tool/MCP Friction Index (`tool_mcp_friction_index`)**: Tracks canonical CLI command count, flag alias coverage, and `E_UNBOUND_CLI_CLAIM` count.
4. **Axis 4 — Skill-vs-Skill Cross-Audit (`skill_cross_audit`)**: Verifies coverage of canonical supervisor and verifier skills (`g8s-supervisor`, `g8s-blind-verifier`, `g8s-red-prove`, `g8s-sec-gate`, `g8s-epistemic-audit`, `g8s-scenario-matrix`, `g8s-ux-visual-gate`).
5. **Axis 5 — Jev L1-vs-L3 Calibration (`jev_l1_l3_calibration`)**: Reports active System-1 operating tier and L3 post-run verification gate (`g8s reflex verify`).

#### Scenario: Execute 5-axis scorecard and F0 gate check
<!-- tests: TestFiveAxisScorecardAndF0Gate -->
- **GIVEN** an active `G8S_STATE_DIR`
- **WHEN** `g8s scorecard status --json` and `g8s f0 gate check --json` are executed
- **THEN** `scorecard_5axis` returns all 5 evaluation axes with `kaizen_recommendations` and `f0_gate_check` returns `verdict: "PASS"`
