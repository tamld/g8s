# Profile: infra (infrastructure / IaC / platform tooling)

**Nature**: repositories that provision or operate systems (IaC, infra
tooling, daemons). Host mutation commands execute without container/virtualization boundaries.

## Trust boundaries (seed)

```yaml
trust_boundaries:
  - "infrastructure/**"  # apply paths — the containment surface
  - "bin/**"             # built tooling that touches real hosts
```

## Lane bundle recommendation

| Lane | Usage |
|---|---|
| R/F | Deterministic tests dominant (plan/validate cycles) |
| S | Apply-path changes (anything that mutates real hosts) |
| Cadence | Drift-detection audit weekly; containment review per release |

## Audit dimensions (weight order)

1. **containment** — runbook truth: does the documented containment model
   match what the entry points actually spawn? (Orphan/zombie diagnostics.)
2. **security-and-boundary-verification** — run `g8s-sec-gate` and
   `g8s-scenario-matrix` to validate blast-radius tiering, capability
   boundaries, and rollback safety before any infrastructure mutation.
3. **tests** — plan/validate passes; no apply-path change without a
   dry-run test pinned.
4. **docs-freshness** — runbooks rot fastest in infra; link integrity +
   containment-table truth.

## Role & Skill Routing Matrix

| Task Phase | Recommended Role | Permission | Mandatory Skills |
|---|---|---|---|
| Blast-Radius & Failure Design | `analyst` | `read_only` | `g8s-scenario-matrix`, `wiki-failure-design` |
| Upstream Version & CVE Lookup | `researcher` | `read_only` | `g8s-epistemic-audit`, `ck:xia` |
| Boundary & Secret Leak Audit | `sec-auditor` | `read_only` | `g8s-sec-gate`, `ck:security` |
| Scoped Tooling / Test Mutation | `implementer` | `workspace_write` | `g8s-red-prove`, `ck:cook` |
| Plan / Dry-Run Verification | `verifier` / `test-runner` | `read_only` / `workspace_write` | `g8s-blind-verifier`, `g8s-red-prove` |

## Worker recommendation

- `read_only` (`analyst`, `researcher`, `sec-auditor`, `verifier`) for drift and boundary audits; `workspace_write` (`implementer`, `test-runner`) strictly receipt-gated; live host apply commands remain human-gated.
- Model: `docs-retriever` for inventory scans; `deep-coding` for IaC validation and dry-run verification.

## Contribute back

Drift-detection recall, containment-model divergences found, flaky
infra-test patterns.
