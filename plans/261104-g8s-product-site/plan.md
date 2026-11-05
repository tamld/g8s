---
title: "g8s product site — rebuild to flowaxon standard, product-first"
issues: []
related-plans: ["261104-film-v5-and-publish"]
status: in_progress
priority: P1
created: 2026-11-04
mode: auto

**Session type**: T2 (execution)
---

# g8s Product Site

## Summary
Rebuild the g8s site (repo-root `index.html`, replacing the v3 portal) as a PRODUCT website: visitors must leave knowing what g8s is, seeing capabilities demonstrated, and able to install + run it in ≤60s. NOT a portfolio. Research base: `docs/research/28` (IA from 6 OSS product sites: oxide/temporal/dagger/tailscale/...) + `docs/research/29` (repo-grounded inventory: install paths incl. verified `scripts/install.sh`, `go install`, `make local`; 60s quickstart; feature table with proof artifacts; docs gaps).

## Design law (own style — NOT a flowaxon clone)
"Steel & Signal": dark terminal-native ground (the product IS a CLI), ONE signal accent (verified-green), mono for UI/code, sans for body copy (inverse of flowaxon). Same CRAFT gates as flowaxon: taste INSPECT as mechanical gate, B7 motion law (1 curve `cubic-bezier(0.22,1,0.36,1)`, transform+opacity, bands 500/300/600-800ms), stop-slop copy, zero deps, CSP-clean, SRI, WCAG, 390px, reduced-motion.

## IA — 9 sections (from docs/research/28, verified against 29)
nav (version tag → releases, star badge, Install button) · hero (category badge + headline + DUAL-ACTION: curl pipe AND the 2026 agent copy-prompt — Dagger pattern) · capabilities (receipts/gates/fleet/mesh — each with a REAL terminal capture, no mockups) · quickstart (60s: install → doctor → submit → worker, commands verbatim from docs/quickstart.md) · architecture (F0/F1/F2 diagram — isometric, own engine or SVG) · docs surface (quickstart.md, AGENTS.md, constitution, ADRs, user-guide) · proof (CI 18-check battery, coverage 82.5%, SLSA, dogfood: g8s builds g8s) · community/contribute · footer.

## Phases
1. **Build** (F1 worker): `index.html` + `assets/site/{styles.css,main.js}` + retarget `scripts/verify-sri.sh` to the new asset set. Gates: node checks · SRI pass · taste INSPECT (small-text ≤3, body 16px, no banned tells) · 390px no overflow · reduced-motion.
2. **CI retarget check** (F0): engine boot test still passes (sites/aegis.html untouched — aegis site keeps v3 until its own round); pre-commit gates pass; PR → CI.
3. **Publish**: merge → engine serves new index → curl verify + INSPECT on the served origin.
4. **Aegis site** (separate round, after g8s proves the pattern): same pipeline for sites/aegis.html — needs Aegis product inventory (Aegis is private: site content limited to what's public).

## Eval gates
- G1: install command in hero verbatim-matches `scripts/install.sh` usage
- G2: quickstart section runnable as written (read_only, no keys)
- G3: every feature claim links a proof artifact (file/ADR/CI run)
- G4: taste INSPECT on the built page: no banned tells, small-text ≤3, body ≥16px
- G5: SRI + node + 390px + reduced-motion pass
- G6: engine boot test (3 vhosts DOCTYPE) green in CI

## Non-goals
No framework. No build step (static, engine-served). No marketing fluff (stop-slop). Aegis site not in this round. Backend/API docs beyond linking existing files.
