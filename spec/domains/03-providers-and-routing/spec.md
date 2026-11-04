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

#### Scenario: Jev v2 Wave 2 joint route_target, retry, and memory promotion noul gate
<!-- tests: TestJevAssistChoicePermutationEval, TestParseRouteTargetChoice, TestEvaluateMemoryEntryOnlineAndOffline -->
- **GIVEN** Jev assist routing with manifest-legal candidates and declarative `memory_promotion` noul questions in `.g8s/jev-prompts.yml`
- **WHEN** `jevAssist` posts a single `route_target` `<provider>/<model>` choice (model pin `jev-1.14.0`) or `EvaluateMemoryEntry` inspects a memory note online
- **THEN** unbound provider/model pairs are structurally impossible, HTTP `429`/`529` retries rotate keys with bounded backoff, and memory promotion blocks when either noul ≥ `memory_block_breach_prob`

### R5 — Submit dispatch profiles (`-profile`) {#R5-submit-dispatch-profiles}

<!-- inherited-from: issue #725 -->
<!-- feature_id: FEAT-LLMCALL-01 -->

Named presets in `.g8s/agent-models.yml` under `profiles:` supply the explicit dispatch triple (`provider`, `model`, `effort`) plus `timeout`, `role`, and `permission`. `g8s submit -profile <name>` applies each preset field unless the operator passed the corresponding flag (`fs.Visit` precedence). Unknown profile names fail at parse time with `E_USAGE` and a sorted hint of available names. Profile timeouts use the same `ParseTimeoutDuration` and receipt TTL cross-check as explicit `-timeout`. Profile `workspace_write` still requires an active receipt and harness gates; `-profile` never bypasses isolation.

#### Scenario: Catalog profile fills dispatch fields and records provenance
<!-- tests: TestSubmitProfile_FillsFields, TestParseCatalogProfilesValid -->
- **GIVEN** a valid `profiles.scout-fast` entry in `.g8s/agent-models.yml`
- **WHEN** `g8s submit --profile scout-fast --idempotency-key k --prompt p` succeeds
- **THEN** the queued request JSON carries the preset provider, model, effort, role, permission, timeout, and `"profile":"scout-fast"`

#### Scenario: Unknown submit profile fails closed before queue write
<!-- tests: TestSubmitProfile_UnknownName -->
- **GIVEN** no matching preset name
- **WHEN** `g8s submit --profile no-such --idempotency-key k --prompt p` runs
- **THEN** the CLI exits `2` with `E_USAGE`, lists available profile names, and does not insert a task row

#### Scenario: Audited Jev v2 prompt registry, batch cache, and D-09 ProcessReflectionReceipt judge enforce evidence refs
<!-- tests: TestJevPromptsRegistryParityAndModelPinning, TestBatchEvaluateItemInQuestionAndCache, TestProcessReflectionReceiptSchemaAndEvidenceRefGate, TestJudgeProcessArtifactsPromptManifestAudit -->
- **GIVEN** `.g8s/jev-prompts.yml` pinned to `jev-1.14.0` and `schemas/process-reflection-receipt-v1.schema.json`
- **WHEN** `ReflexGate.JudgeProcessArtifacts` evaluates sense artifacts or `ValidateProcessReflectionReceipt` inspects a receipt
- **THEN** item payloads are packed per-question (`<= 50k` tokens) with SHA-256 caching, `.g8s/jev-prompts.yml` SHA-256 matches the audit manifest, and any verdict lacking verifiable `evidence_refs` is rejected with `E_REFLECTION_UNVERIFIED_VERDICT`
