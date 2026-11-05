---
title: "Build Theater — the page assembles itself, live and scrub-able"
issues: []
related-plans: ["261103-f0-skill-vault-optimization", "261104-g8s-product-site"]
status: in_progress
priority: P1
created: 2026-11-05
mode: auto

**Session type**: T2 (execution)
---

# Build Theater

## Summary
"The site that assembled itself." is the strongest hook on flowaxon.com but its current
execution is a static table. This plan replaces it with a **self-assembly theater**: a scaled
live iframe of the page itself, revealed region-by-region in real commit order (from the
verified ledger.json — 9/9 hashes confirmed in git history), scrub-able and playable, ending
with a crossfade into the actual page. Zero fabrication: the miniature IS the page.

## Measured ground
- CSP currently `frame-ancestors 'none'` — blocks self-framing; must become `'self'`
  (external clickjacking protection preserved; the product's own use case justifies self-framing).
- ledger.json: 9 commit hashes, all verified in git history (git cat-file).
- Polish PR #768 (aegis) MERGED; #769 (g8s) open — merge before build to avoid conflicts.

## Phases

### Phase 1 — CSP + section scaffolding (F1 worker, index.html + _headers + assets/v5/theater.js)
- `_headers`: `frame-ancestors 'none'` → `'self'` (documented: the theater's own use case).
- New section markup: theater frame container (16:10, scaled 0.25 iframe, sandbox), scrub
  timeline, play/replay button, per-commit human-line strip, commit hash chips.
- Recursion guard: page JS checks `location.search` for `embed=1` → theater section renders
  nothing (the nested page never nests). ALSO the iframe src uses `?embed=1`.
- Lazy-init: theater constructs only when scrolled into view (IntersectionObserver);
  reduced-motion → static end-frame image instead of animation.

### Phase 2 — Assembly choreography (same worker)
- Reveal map: each ledger row → a clip-path polygon window over the iframe, in commit
  order, with human-line + receipt seal per row (command-truth law: every hash carries a
  plain sentence).
- Scrub: input range 0..1 drives reveal progress deterministically (same windows).
- Play: auto-advance 12s with the film's tick stem optional (no audio by default).
- End: crossfade mask away → the real page IS the theater (the section collapses, the
  visitor is inside the built page).

### Phase 3 — Publish (F0)
- PR → CI (7 required checks) → merge → deploy → prod verify (beacon/recursion/CSP probes).

## Eval gates
- BT-1: ledger↔reveal 1:1 — the reveal map in theater.js parses assets/v5/ledger.json and
  asserts window count == row count, order identical.
- BT-2: command_truth.py PASS (unchanged).
- BT-3: taste INSPECT on the built page: no new banned tells, small-text budget holds.
- BT-4: recursion — load `/?embed=1` → theater section absent (assert via DOM check).
- BT-5: SRI PASS · 390px no overflow · reduced-motion → static frame (no iframe animation).
- BT-6: CSP probe — response header contains `frame-ancestors 'self'`; external frame test
  (a data: or foreign-origin embed) still blocked.

## Non-goals
No audio in the theater (the film owns sound). No g8s/aegis changes. No analytics changes.
