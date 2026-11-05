# g8s product site — hero isometric engine (F1-GP3)

**Status:** shipped in `assets/site/hero-engine.js`  
**Reference pattern:** FlowAxon `rig3.js` (Canvas2D `project3D`, anchored packet, lease countdown) — engine mechanics only, g8s palette and vocabulary.

## Problem

The hero explained containment in prose (`write receipt`, `CAS lease`, `gate lane`) and JSON terminal panes, but had no spatial model of the **task loop** operators actually run. Visitors could read terms without seeing ordering or single-winner semantics.

## Design

### Scene graph

Six isometric pads on a ground grid, depth-sorted (painter’s algorithm). One amber **packet** travels pad-to-pad on a fixed 14s loop (single `requestAnimationFrame` chain). Stage accent colors: green (default active), amber (CAS lease), blue (write receipt). Verify pad renders **gate lane** chips `0`, `D`, `R`, `F`, `S` with one lane highlighted per beat.

### Stage map (G1 — mechanism truth)

| Order | Engine id   | On-page vocabulary                         | Subcaption                          |
|------:|-------------|--------------------------------------------|-------------------------------------|
| 1     | `submit`    | g8s submit (quickstart / terminal)         | task prefix from captured JSON      |
| 2     | `queue`     | SQLite WAL · single winner (bridges copy)  | queue row semantics                 |
| 3     | `lease`     | **CAS lease** (bridge term)                | countdown + compare-and-swap claim  |
| 4     | `worktree`  | isolated git sandbox (hero subhead)        | private worktree                    |
| 5     | `receipt`   | **write receipt** (bridge term)            | path-scoped seal                    |
| 6     | `verify`    | **gate lane** + spec **0/D/R/F/S**         | verify gate                         |

No extra stages (no “merge”, “deliver”, or F0/F1 rings in this figure — those live in architecture sections).

### Captured values (not fabricated outcomes)

From `index.html` hero terminal JSON panes:

- Task id prefix `015ea318` (`g8s submit` pane)
- Receipt id prefix `67992e6a` (`g8s receipt issue` pane)
- Lease display uses **60s** countdown label (CAS lease window; aligns with harness lease UX, not a fabricated verify result)

### Integration

- Markup: `<figure id="g8s-hero-engine">` inserted **below terminology bridges**, copy unchanged.
- Script: `assets/site/hero-engine.js` (defer, no inline handlers).
- Styles: `.hero-engine-*` tokens only; `width: 100%`, `min-width: 0`, no fixed width &gt;390px.

### Motion & a11y

- **Lazy init:** `IntersectionObserver` starts/stops rAF when fig enters/leaves viewport.
- **`prefers-reduced-motion`:** static end-frame at verify gate; readout announces static mode; no animation loop.
- Live region: `[data-hero-readout]` with `aria-live="polite"`.
- Canvas decorative (`aria-hidden`); figure carries `aria-label` for the loop.

### Performance (G5)

- One rAF loop when visible and motion allowed.
- Canvas draw only (no CSS animation on engine nodes).
- DPR capped at 2.

## Gates evidence

| Gate | Evidence |
|------|----------|
| G1 | Stage ids/labels match bridge + spec vocabulary; sequence submit→queue→lease→worktree→receipt→verify |
| G2 | `drawStatic()` + `matchMedia('prefers-reduced-motion: reduce')` skips rAF |
| G3 | Engine frame `width:100%`, flex wrap caption, hero grid `min-width:0` inherited |
| G4 | INSPECT baseline vs post-change: same FAIL ids (`few-pictures`, `broken-image`), no new banned tells |
| G5 | Single rAF; observer pause offscreen |
| G6 | Commit includes `hero-engine.js`, `index.html`, `styles.css`, this doc |

## Files

- `assets/site/hero-engine.js` — engine
- `assets/site/styles.css` — frame + readout
- `index.html` — wiring + updated CSS SRI
- `docs/research/31-g8s-site-hero-engine.md` — this note

## Operator notes

Re-run `scripts/verify-sri.sh` after any edit to `assets/site/styles.css`. `hero-engine.js` is not in the SRI gate (only `main.js` + `styles.css` on `index.html`).
