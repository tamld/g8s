# Profile: utility (libraries / small tools)

**Nature**: small deterministic repositories (libs, CLI utilities,
hash-checkers). Package has zero third-party dependencies and passes go test ./... at 100%.

## Trust boundaries (seed)

```yaml
trust_boundaries: []   # minimal — utility surfaces are their own boundary
```

## Lane bundle recommendation

| Lane | Usage |
|---|---|
| R/F | Dominant — deterministic tests are the oracle |
| D | README truth only |
| Cadence | Audit per PR; no standing audit needed |

## Audit dimensions (weight order)

1. **tests** — deterministic pass/fail is the whole story; keep the fixture
   suite exhaustive.
2. **docs-freshness** — README accuracy (structure + links).

## Worker recommendation

- `read_only` verifiers; `workspace_write` receipt-gated for test-only
  changes.
- Model: flash-tier sufficient.

## Contribute back

Fixture-suite patterns, gate-script portability findings (POSIX vs BSD vs
GNU edge cases), minimal-profile retention data.
