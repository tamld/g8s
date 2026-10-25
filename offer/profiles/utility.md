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
2. **ui-dom-invariants** (for GUI / Electron / Web-wrapped utility apps) —
   run `g8s-ux-visual-gate` to verify that DOM text mutations (`textContent`)
   never clobber child badge elements (`#queue-badge-count`, `#drawer-count`,
   `#fav-count`, `#downloaded-count`).
3. **sidecar-binary-manifest** (for desktop / cross-platform tools) — verify
   that bundled sidecars (`ffmpeg`, `ffprobe`, `yt-dlp`) exist across build,
   installer, and launcher manifests so post-processing pipelines never stall.
4. **docs-freshness** — README accuracy (structure + links) and PRD/SRS/AC/FSM
   alignment.

## Role & Skill Routing Matrix

| Task Phase | Recommended Role | Permission | Mandatory Skills |
|---|---|---|---|
| Requirement & State Modeling | `analyst` | `read_only` | `g8s-scenario-matrix`, `fable-thinking` |
| Upstream / Binary / API Research | `researcher` | `read_only` | `g8s-epistemic-audit`, `ck:xia` |
| Feature / Bugfix Implementation | `implementer` | `workspace_write` | `g8s-red-prove`, `ck:cook` |
| Test Execution & Regression | `test-runner` | `workspace_write` | `g8s-red-prove`, `ck:test` |
| Independent Blind Verification | `verifier` | `read_only` | `g8s-blind-verifier`, `g8s-ux-visual-gate` |

## Worker recommendation

- `read_only` (`researcher`, `analyst`, `verifier`) for discovery, spec decomposition, and verification; `workspace_write` (`implementer`, `test-runner`) receipt-gated for scoped code and test changes.
- Model: `deep-coding` (`HEALTHY`) for `implementer` / `test-runner`; `docs-retriever` for `researcher` / `summarizer`.

## Contribute back

Fixture-suite patterns, gate-script portability findings (POSIX vs BSD vs
GNU edge cases), minimal-profile retention data.
