# Profile: security (security tools / guard repos)

**Nature**: repositories whose deliverable IS protection (git guards, policy
enforcers, scanners). Every change is trust-boundary adjacent by definition.

## Trust boundaries (seed — maximal by default)

```yaml
trust_boundaries:
  - "**"                 # deny-all, allow-some: everything is P0-adjacent;
  # add explicit allow-lanes only after a review cycle proves the surface.
```

## Lane bundle recommendation

| Lane | Usage |
|---|---|
| S (security) | Default — redaction scan mandatory on every PR, independent review mandatory |
| F | Tooling only, still redaction-scanned |
| Cadence | Audit per PR + full deep-audit per release |

## Audit dimensions (weight order)

1. **security-redaction** (mandatory every PR) — leak scan per the
   g8s-supervisor security playbook: home paths, key shapes, raw secrets,
   first-person leaks in reports.
2. **tests** — deterministic behavior is the product; coverage ratchet on.
3. **lifecycle** — escalation history, gate falsification review (guard
   rules that never fire are guard rot).

## Worker recommendation

- `read_only` for all audits. `workspace_write` only for test-only changes,
  receipt-gated, never for guard logic itself (human-authored).
- Model: strongest available for review lanes; flash acceptable for
  mechanical scans.

## Contribute back

Guard-evasion attempts caught by tests, redaction-scan recall improvements,
false-positive rates of policy patterns.
