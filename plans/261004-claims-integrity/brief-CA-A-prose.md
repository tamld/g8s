# Task: Claims-integrity audit CA-A — prose surface (v0.15 prep, operator directive 2026-10-04)

Repo: g8s @ main. The operator's release directive: every claim must be
TRUE + OPERATIONAL (live-demonstrable), weaknesses get fix-priority, no
ambiguity ships. You AUDIT the prose surface and produce a findings
ledger — you fix nothing.

## Delivery protocol

Read-only task. You write NO files — the findings ledger is your final
report text (structured exactly as specified below). Read-only means
read-only: do not write, do not dispatch, do not modify anything.

## Audit surface

- README.md + README.vi.md (every roadmap row, feature bullet, number)
- CHANGELOG.md section [0.14.0] (every Added/Fixed/Changed bullet)
- docs/user-guide/*.md (every command example, flag, and behavioral claim)
- docs/directives.md (directive statements — do the enforcement points exist?)

## Classification per claim (be exhaustive — every sentence that asserts behavior or a number)

- **BOUND**: in docs/claims.yml with a passing test binding (cross-check
  docs/claims.yml; run nothing — report the binding name).
- **OPERATIONAL-UNBOUND**: true and live-demonstrable, but no claims.yml
  binding — give the exact demo command that proves it (e.g.
  `bin/g8s config get auto_retry_enabled` → "false").
- **AMBIGUOUS**: hedged, vague, or unmeasurable as written ("fast",
  "robust", "seamless", "up to", "improved") — rewrite each as a
  testable statement or mark for deletion.
- **FALSE/STALE**: contradicts the code on main (check the actual flag
  names, defaults, command surfaces, config keys against the source).
  Cite file:line for the contradiction.

## Report format (your final message, exactly this shape)

```
CLAIMS-AUDIT-A
BOUND: <n> (list ids)
OPERATIONAL-UNBOUND: <n>
  - <claim> | demo: <command>
AMBIGUOUS: <n>
  - <file:line> | <quote ≤80 chars> | <testable rewrite>
FALSE/STALE: <n>
  - <file:line> | <quote ≤80 chars> | <contradiction: file:line>
```

Constraints: verification vocabulary only; no fixes; cite file:line for
every non-BOUND finding; ≤ 60 findings total (prioritize the
operator-facing surface — if over, cut the least user-visible).
