# AI Factory Scorecard

Ground truth for the factory program (plans/261002-factory/plan.md).
A row is PROVEN only with an artifact: a merged test/eval, or a
recorded live round. **A slogan without a row here is a bug.**

| # | Milestone | Metric | Baseline | Current | Artifact |
|---|-----------|--------|----------|---------|----------|
| S-1 | Safety floor: budgeted auto-retry live (ADR-0029) | retry works end-to-end w/ caps | 0 | Wave F in flight | (pending) |
| S-2 | Tenancy posture ratified (ADR-0028) | doc accepted + containment live | partial | #468/#491 live; ADR accepted 2026-10-02 | docs/decisions/0028 |
| S-3 | Deterministic router exists (ADR-0030) | RT-F green (no-network routing + fallback parity) | 0 | Wave F in flight | (pending) |
| S-4 | Routing benchmark: Jev vs deterministic | M3 delta recorded | n/a | Wave F seeds it | (pending) |
| S-5 | Submit-time routing integration | `--route auto` lands, default byte-identical | 0 | Wave G | (pending) |
| S-6 | Lane router in code (ADR-0024 S6-3) | table-driven lanes + Jev-suggest telemetry | 0 | Wave G | (pending) |
| S-7 | Verifier-class registry | classes with cited catches | 0 | Wave H | (pending) |
| S-8 | **First unattended closed round (docs-class)** | **M1: 0→1** | **0** | Wave H | (pending) |
| S-9 | Retrospective-as-task | lesson cites verifiable catch | 0 | Wave I | (pending) |

## Live rounds log (M1 evidence)

| Round | Date | Goal | Human actions after goal | Outcome |
|-------|------|------|--------------------------|---------|
| (none yet — Wave H target) | | | | |
