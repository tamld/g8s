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
2. **tests** — plan/validate passes; no apply-path change without a
   dry-run test pinned.
3. **docs-freshness** — runbooks rot fastest in infra; link integrity +
   containment-table truth.

## Worker recommendation

- `read_only` verifiers for drift audits; `workspace_write` only for
  test-only changes (receipt-gated); apply commands are always
  human-executed.
- Model: flash for scans; human for anything that mutates hosts.

## Contribute back

Drift-detection recall, containment-model divergences found, flaky
infra-test patterns.
