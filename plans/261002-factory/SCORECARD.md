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
| S-7 | Verifier-class registry | classes with cited catches | 0 | **LANDED** #532 (declarative .g8s/verifier-classes.yml; docs+test seeded advisory with founding catches #208/#510; deny-by-default floor; ErrSelfGrade; verifier_verdict telemetry; g8s verify CLI) | PR #532 15/15 CI |
| S-8 | **First unattended closed round (docs-class)** | **M1: 0→1** | **1** | **LANDED** round R1 2026-10-03 (#534 auto-merged by tools/merger.sh, 4/4 gates, at the recorded operator flip; the round's verifier gate caught the receipts split-brain bug fail-closed on attempt 1 — fixed via #535 inside the round, 0 human actions) | PR #534/#535 15/15 CI |
| S-9 | Retrospective-as-task | lesson cites verifiable catch | 0 | Wave I (v0.15) — #519 | (pending) |

## Live rounds log (M1 evidence)

| Round | Date | Goal | Human actions after goal | Outcome |
|-------|------|------|--------------------------|---------|
| R1 | 2026-10-03 | docs user-guide page: verifier classes & automated autonomy (brief-H2b) | **0** | **CLOSED-UNATTENDED** — brief→dispatch→drain→harvest→CI→verify→auto-merge (#534 via merger 4/4 gates); attempt 1 verifier-gate refusal (receipts split-brain, #535) recovered in-round |

### R1 detailed log (times +07:00; the honesty rail)

- 16:53:56 GOAL stated — brief-H2b committed (1e6075d); operator pre-ratified the round + the level-1 flip in the 3-session plan
- 17:03:37 DISPATCH — receipt 71d5adc1 (TTL 3600s), task dbcc59c6 submitted, detached worker claimed immediately
- 17:08:07 DRAIN — WORKER_COMPLETED (wake: g8s watch --failed, no polling)
- 17:10 HARVEST (agent, mechanical) — branch docs/verifier-and-autonomy → PR #534; docs battery green in worktree
- 17:12 CI green — 15/15 checks
- 17:15:00 FLIP — autonomy_level 0→1 (operator decision, recorded here; merger run from the PR checkout so the verifier script checks grade the PR content)
- 17:15:07 MERGER attempt 1 — autonomy ✓, lane=docs ✓, **verifier ✗ unregistered** → exit 1, NO merge (fail-closed; root cause: verify read g8s.db for write_receipts — the canonical ledger is the sibling receipts.db)
- 17:16–17:31 FIX in-round — red-first fixtures in the real receipts.db layout + 5-line fix → PR #535, 15/15, merged (supervisor-direct surgical, justification recorded in the PR)
- 17:35:56 MERGER attempt 2 — 4/4 gates pass → **MERGED 534 at 10:36:03Z**
- 17:36:25 RESET — autonomy_level 1→0 (soak discipline; the flip was per-round, recorded)
- **Human actions after goal: 0**
