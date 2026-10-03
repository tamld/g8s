# AI Factory Scorecard

Ground truth for the factory program (plans/261002-factory/plan.md).
A row is PROVEN only with an artifact: a merged test/eval, or a
recorded live round. **A slogan without a row here is a bug.**

| # | Milestone | Metric | Baseline | Current | Artifact |
|---|-----------|--------|----------|---------|----------|
| S-1 | Safety floor: budgeted auto-retry (ADR-0029) | classify+budget+resubmit primitives + resubmit cmd + retry tick | 0 | **LANDED** #501/#502 (flag default OFF by design; first live auto-retry round = Wave H probe) | PRs #501/#502: 15/15 CI, red tests for classification/budget/backoff/idempotency |
| S-2 | Tenancy posture ratified (ADR-0028) | doc accepted + containment live | partial | #468/#491 live; ADR accepted 2026-10-02 | docs/decisions/0028 |
| S-3 | Deterministic router exists (ADR-0030) | RT-F green (no-network routing + fallback parity) | 0 | **LANDED** #503 (deterministic layer always-on; Jev layer env-gated, never mandatory) | PR #503: 15/15 CI, router_test fallback-parity suite |
| S-4 | Routing benchmark: Jev vs deterministic | M3 delta recorded | n/a | **SEEDED** #503: eval category `task_routing`, 6+ fixtures, mocked-Jev comparison, CI-runnable | internal/harness/probe/routing_suite.go; real-Jev M3 delta = later wave |
| S-5 | Submit-time routing integration | `--route auto` lands, default byte-identical | 0 | **LANDED** #520 (golden byte-identity test; route_source/reason in request_json; auto-undecidable = surfaced E_*, no silent fallback) | PR #520 10/10 CI |
| S-6 | Lane router in code (ADR-0024 S6-3) | table-driven lanes + Jev-suggest telemetry | 0 | **LANDED** #521 (deterministic hot path; Lane-S-suggestion refusal tested; degraded-mode trust floor tested) | PR #521 10/10 CI |
| S-10 | CI lanes: build vs docs (ADR-0031) | docs PR clears gates in ~2-3 min | ~12-15 min | **LANDED** #506 (build lane proved on itself 15/15). Probe finding: the lane split is live for PRs; direct-to-main docs pushes still run the full battery (post-push `origin/main...HEAD` diff is empty → deny-default → build) — fix candidate: push-event `github.event.before` SHA detection | PR #506 (15/15 build lane); PR #508 docs-lane probe: LANE gates green in **~3 min** (was 12-15), residual tail = Test-matrix (macos/windows) not yet lane-guarded — fix candidate recorded (ADR-0031 follow-up); direct-to-main docs pushes stay full-battery (empty-diff edge) |
| S-7 | Verifier-class registry | classes with cited catches | 0 | Wave H — #515 | (pending) |
| S-8 | **First unattended closed round (docs-class)** | **M1: 0→1** | **0** | Wave H — #516 | (pending) |
| S-9 | Retrospective-as-task | lesson cites verifiable catch | 0 | Wave I (v0.15) — #519 | (pending) |

## Live rounds log (M1 evidence)

| Round | Date | Goal | Human actions after goal | Outcome |
|-------|------|------|--------------------------|---------|
| (none yet — Wave H target) | | | | |
