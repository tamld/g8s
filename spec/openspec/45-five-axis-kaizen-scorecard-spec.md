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

#### Scenario: Worker attempt completion and verifier gate-outcome ledger feed D-09 sense
<!-- tests: TestWorkerRunOnceRecordsModelOutcome, TestRecordModelOutcomeDeduplicatesLegacyCLIWrapper, TestRecordGateOutcomesAndFlakeSummary -->
- **GIVEN** a worker task executed via `Supervisor.RunOnce` and a verification run evaluated via `Verifier.Verify`
- **WHEN** the attempt finishes and verification checks complete
- **THEN** `model_quality_events.jsonl` records one `ModelOutcomeRecord` per attempt (without duplicate CLI wrapper rows) and `gate_outcome_events.jsonl` records per-check outcomes and flake transitions

## D9-S2 — Evidence-bound telemetry JUDGE (#714)

`internal/reflection` defines a separate telemetry JUDGE contract from the older
Jev-specific `internal/reflex` receipt. It does not change that receipt or wire
ACT, PRUNE, scheduling, or workflows.

The strict Draft 2020-12 receipt requires `receipt_id`, `cycle` (RFC3339 timestamp
and nightly/release/manual trigger), `judge` (name/version), `verdicts`, path-only
`add_list` and `prune_list`, computed `prune_ratio`, and positive integer
`self_tombstone_after`. Every verdict requires a portable repo-relative
`artifact_path`, a staleness/drift/theater dimension, healthy/stale/drift/theater
verdict, and nonempty `evidence_refs`. Unknown fields fail at every object level.
The ratio is prune count divided by add count, or prune count with no additions;
the Go validator rejects a supplied ratio that differs from this computation.

References are shape-checked offline: `file:<repo-relative-path>@<sha-prefix>`,
`run:<gate-history-id>`, or `event:<model-quality-row-id>`. Ledger citations also
accept `file:<ledger>@<row-offset>`, where the offset is a zero-based decimal
byte offset in the physical JSONL file, including CRLF and blank-line bytes.
Shape acceptance does not claim the referenced evidence exists.

`TelemetryJudge` uses routing's resolved S1 model-quality and CI-gate paths and
record types, read through `telemetry.ReadLedger`. Both paths must be beneath
the supplied repository root. Missing ledgers yield no verdicts; malformed rows,
invalid observation timestamps, external paths, and I/O failures return errors.
Each observation gets staleness (default 30 days) and drift verdicts; model rows
with verifier results also get a theater verdict when claimed success conflicts
with failed verification. Healthy means no corresponding signal in that row,
not a claim about unobserved artifacts or overall system health.

#### Scenario: Strict telemetry reflection receipt rejects unsupported judgments
<!-- tests: TestReceiptSchemaSelfConformance, TestReceiptHostileFields, TestEvidenceRefShapes, TestComputedPruneRatio -->
- **GIVEN** the embedded schema and a golden receipt
- **WHEN** a receipt is validated
- **THEN** golden passes; absent, null, or empty evidence returns typed `E_REFLEX_VERDICT_UNEVIDENCED`; malformed refs, unknown fields, and incorrect ratios fail

#### Scenario: Telemetry JUDGE preserves ledger evidence and prompt provenance
<!-- tests: TestTelemetryJudgeEvidencedVerdicts, TestLedgerOffsetsAndFailureBoundaries, TestTelemetryJudgeEmptyAndOutsideRoot, TestPromptManifestCoverage -->
- **GIVEN** synthetic S1 model and CI-gate rows and the versioned prompt directory
- **WHEN** JUDGE runs and the manifest is audited
- **THEN** verdict refs identify actual ledger row offsets, corruption fails closed, and pinned hashes cover every Markdown prompt without stale entries
