# Task: Claims-integrity audit CA-B — bundle, skill, ledger surface (v0.15 prep)

Repo: g8s @ main. The operator's release directive: every claim must be
TRUE + OPERATIONAL, weaknesses get fix-priority, no ambiguity ships. You
AUDIT the bundle/skill/ledger surface and produce a findings ledger —
you fix nothing.

## Delivery protocol

Read-only task. You write NO files — the findings ledger is your final
report text (structured exactly as specified below).

## Audit surface

- offer/** (the pull bundle: profiles, seeds, onboarding, gate scripts —
  every instruction a sibling project would follow must match the real
  g8s surface: command names, flags, file paths, config keys)
- skills/g8s-supervisor/** (the vendored supervisor skill — its
  contracts vs the shipped CLI: submit/worker/watch/receipt/lesson flags)
- docs/ENTERPRISE_LEDGER.md (every PROVEN/UNPROVEN/PARTIAL row — flip
  candidates given the v0.14 surface: verifier registry, merger,
  autonomy_level, lessons pipeline, budgeted retry are now SHIPPED and
  red-cell-verified; stale PROVEN rows referencing removed behavior)
- manifest.json (version/description claims vs reality)

## Classification per claim

- **BOUND**: in docs/claims.yml with a passing test.
- **OPERATIONAL-UNBOUND**: true + live-demonstrable — give the exact
  demo command.
- **AMBIGUOUS**: hedged/unmeasurable — rewrite as a testable statement.
- **FALSE/STALE**: contradicts main (cite file:line both sides). Pay
  special attention to: commands the bundle/skill teaches that were
  renamed or gained required flags (e.g. lesson subcommand exists now;
  watch flags; autonomy_level; verify --task), and ENTERPRISE_LEDGER
  rows the v0.14 surface upgrades.

## Report format (your final message, exactly this shape)

```
CLAIMS-AUDIT-B
BOUND: <n> (list ids)
OPERATIONAL-UNBOUND: <n>
  - <claim> | demo: <command>
AMBIGUOUS: <n>
  - <file:line> | <quote ≤80 chars> | <testable rewrite>
FALSE/STALE: <n>
  - <file:line> | <quote ≤80 chars> | <contradiction: file:line>
LEDGER-FLIP-CANDIDATES: <n>
  - <row id> | <from → to> | <evidence>
```

Constraints: verification vocabulary only; no fixes; cite file:line;
≤ 60 findings (prioritize user-visible).
