# Onboarding — self-service for a project's main agent

Pull-based adoption (ADR-0024 distribution model): the project's main agent
performs these steps itself, at its own pace. Nothing is pushed.

## Step 1 — Choose a profile

Read [`profiles/`](profiles/): `knowledge` (wiki/vault), `security`
(guard repos), `infra` (IaC/platform), `utility` (libs/tools). Pick the
closest; tune the seeds to the project.

## Step 2 — Scaffold `.g8s/`

From the profile's trust-boundary + lane-bundle seeds, create:

```text
.g8s/
├── trust-boundaries.yml   # seeded from the profile; tuned to the project
└── lane-bundles.yml       # lane → gate-bundle map from the profile
```

Tuning is expected: the profile is the pattern, the project is the
instance. Any change to a seeded boundary follows the ADR-0024 lifecycle
(supervisor exception → recorded → promoted via PR when the pattern
repeats).

## Step 3 — Wire the three generic gates

Copy from `g8s/tools/` and wire into the project's CI (they are
project-agnostic — they take a repo root):

| Script | Enforces | Needs |
|---|---|---|
| `ci_spec_code_sync.sh` (+test) | Delta scenarios pinned to real tests | `spec/openspec/` conventions (skips gracefully if absent) |
| `ci_structure_sync.sh` (+test) | README structure truth | README markers + `go list` (Go projects; Markdown-only repos skip) |
| `ci_link_integrity.sh` (+test) | No dead relative links | Nothing |

## Step 4 — Run the first brief

Dispatch the first real task through g8s (this is the dogfood):

```bash
g8s brief-issue --title "..." --payload-file ... --dod-file ... --permission read_only
g8s submit --role verifier --receipt-id <id> ...
g8s worker --once=false --concurrency 2
```

Report the outcome — including failures — per the charter's Mode 3
(sanitized contribution packet → PR to g8s). That report is the learning
loop closing.

## Step 5 — Adopt the cadence

From the profile's audit-cadence recommendation. The deep-audit runner
(`tools/run_audit_fanout.sh` in the g8s repo) is the reference for
multi-dimension sweeps; adopt or adapt.
