# OpenSpec DELTA-19: Stateful Worker Lego Mounts & System-1 Reflex Governance

* **Specification ID**: `DELTA-19`
* **Title**: Stateful Worker Lego Mounts (`SkillMount`, `HookMount`, `MemoryMount`), System-1 Reflex Triage & Declarative Governance Registries
* **Status**: `APPLIED`
* **Milestone**: `M5` (Lego Mounts, Reflex & Declarative Governance)
* **Target Packages**: `internal/orchestrator`, `internal/reflex`, `internal/routing`, `internal/verifier`, `internal/ladder`, `internal/lane`, `internal/registry`
* **Tracking Issues**: #87, #105, #457, #515, #516, #517, #523, #524

---

## 1. Context & Motivation

`DELTA-19` introduces composable non-invasive capability mounts (`SkillMount`, `HookMount`, `MemoryMount`) for stateful workers and pairs them with the **Sensor ≠ Decider** System-1 Reflex engine (`internal/reflex`), deterministic model/effort routing (`internal/routing`, `internal/ladder`), and declarative verification registries (`internal/verifier`, `internal/lane`, `.g8s/*.yml`).

---

## 2. ADDED Requirements

### Requirement: Composable Worker Lego Mounts (`internal/orchestrator`)

1. `SkillMount`, `HookMount` (`PreSpawn` / `PostWait`), and `MemoryMount` SHALL wrap any `orchestrator.Worker` without mutating core worker implementations (`WrapWithMounts`).
2. Mount chains SHALL execute in registration order, short-circuit on error, and remain concurrency-safe (`MountRegistry`).

#### Scenario: skill injection, pre-spawn/post-wait hooks, and memory mount delegation
<!-- tests: TestSkillMountRegisterTwoSkillsOrder, TestHookMountPreSpawnRewriteTask, TestHookMountPostWaitReceipt, TestAgyWorkerWithMounts_SkillInjection, TestAgyWorkerWithMounts_PreSpawnAndPostWaitHooks, TestMountRegistryConcurrent -->
- Mounted skills and hooks transform task specs before spawn and inspect receipts after wait, short-circuiting cleanly on error.

### Requirement: System-1 Reflex Triage & Zero-Leakage Key Pool (`internal/reflex`)

1. `internal/reflex` SHALL enforce **Sensor ≠ Decider**: L1 pre-mutation triage (`Triage`) and L3 output quality signals consume `ContextPacket` metadata, enforce weakest-link confidence calibration, fall back gracefully when offline, and redact raw API keys (`MaskKey`) across all logs and JSON envelopes.

#### Scenario: reflex triage, circuit breaker, scope boundary defense, and key redaction
<!-- tests: TestTriageWithMockJev, TestTriageCircuitBreakerInstantKill, TestScopeBoundarySiblingDirectoryDefense, TestOfflineGracefulFallback, TestMaskKeyRedactsRawSecret, TestTriageRequestV2RoundTrip -->
- Reflex triage evaluates risk with mock/live sensors, blocks sibling-directory scope escapes, degrades safely offline, and never leaks raw API keys.

### Requirement: Deterministic Model Routing, Effort Classes & Verifier Gate (`internal/routing`, `internal/verifier`)

1. `internal/routing` SHALL resolve provider, model, and effort class (`named`, `budget`, `baked-name`, `toggle`) from `.g8s/agent-models.yml` and `.g8s/effort-classes.yml`, refusing invalid suggestions and never touching the network when `G8S_ROUTER_MODE` is off.
2. `internal/verifier` (`g8s verify`) SHALL enforce `.g8s/verifier-classes.yml` acceptance checks and refuse self-grading (`--as-task` matching `--task`).

#### Scenario: deterministic routing, effort alignment, and anti-self-grade verifier enforcement
<!-- tests: TestRoute_DeterministicFixtures, TestRoute_EnvOff_NoNetwork, TestRedtest_Guarantee1_SuggestionRejectionMatrix, TestTableDrivenVerifier, TestRedtest_Guarantee4_SelfGradeRefusal_ExactMatch -->
- Routing resolves deterministically from declarative YAML catalogs without network I/O when disabled, and `g8s verify` enforces citation and anti-self-grade invariants.
