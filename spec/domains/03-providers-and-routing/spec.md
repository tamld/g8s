# Living Domain Spec 03 — Universal LLM Providers, Routing & 5-Axis Scorecard

- **Domain ID**: `03-providers-and-routing`
- **Collision Domains**: `llm-provider`, `routing-registry`
- **Feature IDs**: `FEAT-LLMCALL-01`, `FEAT-ROUTING-LANE-01`
- **Constitutional Laws**: Law 1 (Zero-Trust Delegation), Law 5 (Self-Describing Surface)

---

### R1 — Fail-Closed CLI Agent Adapter & Conformance Gating {#R1-cli-agent-fail-closed}

<!-- inherited-from: DELTA-25, DELTA-32, DELTA-38, DELTA-43 -->
<!-- feature_id: FEAT-LLMCALL-01 -->

Universal CLI agent adapters (`opencode`, `commandcode`, `qwen`, `codex`, `claude`) MUST fail closed (`Status: "FAILED"`, `ExitCode: 1`) whenever `stderr` or `stdout` contains `[API Error:` and no valid deliverable was emitted on `stdout`.

#### Scenario: CLI agent exit-0 with API Error on stderr fails closed
<!-- tests: TestUniversalCLIAgentAdapters -->
- **GIVEN** a CLI agent process that exits `0` with empty `stdout` and `[API Error: 404` on `stderr`
- **WHEN** `execHandle.Wait` harvests the process result
- **THEN** the result status is `FAILED` with non-zero exit code and `CLI_AGENT_API_ERROR` metadata

### R4 — DELTA-45 5-Axis Cross-Evaluation & Kaizen Scorecard {#R4-five-axis-scorecard}

<!-- inherited-from: DELTA-05, DELTA-19, DELTA-45 -->
<!-- feature_id: FEAT-ROUTING-LANE-01 -->

The routing and telemetry subsystem MUST expose `g8s scorecard [status|audit|kaizen] --json` evaluating all 5 orthogonal axes (`f1_f2_worker_yield`, `write_receipt_tightness`, `tool_mcp_friction_index`, `skill_cross_audit`, `jev_l1_l3_calibration`).

#### Scenario: 5-axis scorecard envelope emits all five axes and kaizen recommendations
<!-- tests: TestFiveAxisScorecardAndF0Gate -->
- **GIVEN** an initialized `G8S_STATE_DIR`
- **WHEN** `g8s scorecard status --json` is executed
- **THEN** it returns a `scorecard_5axis` envelope containing all 5 axes and `kaizen_recommendations`

#### Scenario: Worker attempt completion and verifier gate-outcome ledger feed D-09 sense
<!-- tests: TestWorkerRunOnceRecordsModelOutcome, TestRecordModelOutcomeDeduplicatesLegacyCLIWrapper, TestRecordGateOutcomesAndFlakeSummary -->
- **GIVEN** a worker task executed via `Supervisor.RunOnce` and a verification run evaluated via `Verifier.Verify`
- **WHEN** the attempt finishes and verification checks complete
- **THEN** `model_quality_events.jsonl` records one `ModelOutcomeRecord` per attempt (without duplicate CLI wrapper rows) and `gate_outcome_events.jsonl` records per-check outcomes and flake transitions
