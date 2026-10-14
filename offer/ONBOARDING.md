# Onboarding — self-service for a project's main agent

Pull-based adoption (ADR-0024 distribution model): the project's main agent
performs these steps itself, at its own pace. Nothing is pushed.

## Step 1 — Choose a profile

Read [`profiles/`](profiles/): `knowledge` (wiki/vault), `security`
(guard repos), `infra` (IaC/platform), `utility` (libs/tools). Pick the
closest; tune the seeds to the project.

## Step 2 — Scaffold `.g8s/`

Run `g8s offer init --profile <knowledge|security|infra|utility> --dir <repo>`
(or `--gates-only` to refresh `tools/ci_*.sh` without overwriting customized
`.g8s/*.yml` registries). This creates:

```text
.g8s/
├── trust-boundaries.yml   # seeded from the profile; tuned to the project
├── lane-bundles.yml       # lane → gate-bundle map from the profile
└── effort-classes.yml     # path/signal-driven effort-class defaults
```

Tuning is expected: the profile is the pattern, the project is the
instance. Any change to a seeded boundary follows the ADR-0024 lifecycle
(supervisor exception → recorded → promoted via PR when the pattern
repeats).

## Step 3 — Wire the three generic gates

Run `g8s offer check --dir <repo>` (executes `<repo>/tools/ci_*.sh` when
present, or falls back automatically to the embedded gate scripts in `g8s`
for L1-domain-locked repositories that forbid top-level `tools/*.sh`):

| Script | Enforces | Needs |
|---|---|---|
| `ci_spec_code_sync.sh` (+test) | Delta scenarios pinned to real tests (`func TestX(` or `tests/<file>`) | `spec/openspec/` or `docs/openspec/specs/` (`*.md` & `*/*.md`; skips gracefully if absent) |
| `ci_structure_sync.sh` (+test) | README structure truth (`go list` or `docs/README.md` taxonomy) | README markers + `go.mod` or `docs/README.md` (skips gracefully when neither is present) |
| `ci_link_integrity.sh` (+test) | No dead relative links | Nothing |

## Step 4 — Run the first brief & cross-repo remediation

Dispatch the first audit (`read_only`) or bounded fix (`workspace_write`) through `g8s`:

```bash
# Read-only first audit
g8s submit --idempotency-key <key> --prompt "..." --role verifier --scope-root <repo> --add-dir <repo>
g8s worker --once

# Delegated cross-repo write (isolated git worktree + single-use receipt)
RID=$(g8s receipt issue --path "docs/**" --json | jq -r '.data.receipt_id')
AGY_MCP_ALLOW_WORKSPACE_WRITE=1 g8s submit --idempotency-key <key> --prompt "..." \
  --permission workspace_write --receipt-id "$RID" --scope-root <repo> --add-dir <repo> --skip-permissions
g8s worker --once
g8s deliver <task-id>
```

Report the outcome — including failures — per the charter's Mode 3
(sanitized contribution packet → PR to g8s). That report is the learning
loop closing.

## Step 5 — Adopt the cadence

From the profile's audit-cadence recommendation. The deep-audit runner
(`tools/run_audit_fanout.sh` in the g8s repo) is the reference for
multi-dimension sweeps; adopt or adapt.

## Agent pull path (fastest — no git)

An autonomous agent pulls the offer without cloning:

```bash
# 1. the onboarding guide (entry point)
curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/offer/ONBOARDING.md
# 2. the profile for the project's nature
curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/offer/profiles/security.md
# 3. the three generic gate scripts (+ their test suites)
curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/tools/ci_link_integrity.sh
```

5 HTTP GET requests retrieve all bundle files with zero git clones. The
profile text is self-sufficient for the agent to scaffold `.g8s/` itself —
the offer's value is the knowledge, not the files.
