# v0.16.0 cut plan — "the self-measuring release" (RATIFIED direction, operator 2026-10-06)

**Status**: plan approved for execution ("lên plan để cut off version
0.16"). **Session type**: T2 (one focused g8s session, ~half day).
**Content parent**: the effort wave, #550 (plans/261004-effort-
optimization/, all merged #556–#565).

## The one-line mission

Cut the release that MEASURES ITS OWN WORK: every dispatch carries
(class, effort_requested, effort_applied, tokens); the cost-per-class
row ships as data, not prose; the ladder and gauges are live organs.
v0.14 = feature release, v0.15 = integrity release, **v0.16 =
self-measuring release**.

## Scope (merged, main = #565 era)

- Effort manifest + catalog loader (#556), dispatch adapter +
  `submit --effort` (#557), effort-classes + token telemetry +
  submit wiring (#558/#559), ladder machinery + CLI + gauges (#561),
  sibling classes + offer seed (#562), ground-truth fixes (#563),
  pass-3 signal model (#564), matrix v1.1 (#565).
- Ship-blocker bugs fixed on contact with reality: delivery-loss
  regression #551/#553 (pre-v0.15), redacted-prompt replay +
  RequestHash-as-Timeout (#563), matrix hole (#565).

## Pre-cut gates (each an artifact)

- **G1 — Red Cell HARD (D-01) on the new surface**: red-team suites
  over internal/lane/{effortclass,effortsignal}, internal/routing/
  effort.go, internal/ladder/, cmd/g8s submit+ladder (verification
  vocabulary, worker-dispatched, parallel disjoint receipts). v0.14
  precedent: 24 findings found BEFORE the tag is cheaper than after.
- **G2 — patrol residue fixed**: the g8s self-audit (task f7d6e5c2)
  class-1 findings — VERSIONING.md still claims "v0.1.0-alpha
  (Current)" at tag v0.15.0, VERIFICATION_GUIDE stale versions, plus
  whatever the claims gate surfaces. A release that ships 0.16 while
  its own docs claim v0.1 fails its standard.
- **G3 — claims gate green**: every new quantitative claim (cost-per-
  class numbers, low-tier 0-thinking, 3/5 M1) bound to telemetry
  evidence or a demo command per #548 — claims_check FAILS proof-less.
- **G4 — docs**: docs/user-guide/effort.md (knob, declared signals,
  registries, ladder, realignment), README/README.vi roadmap row flip,
  structure-sync green.
- **G5 — the 8-gate release flow** (proven twice): write CHANGELOG
  [Unreleased] → 0.16.0 section (currently EMPTY — write it), pre-bump
  version.go + manifest.version + packaging templates, keep the
  `[Unreleased]` heading, explicit `bash tools/release.sh 0.16.0`
  (NEVER `minor` — double-bump), rerun raced CI jobs (known
  2-release pattern), tag push, binary smoke `g8s version 0.16.0`.

## Sequencing

Cut BEFORE the aegis mercenary session starts (it should run on the
fresh v0.16 binary; its returns land in v0.17). The mercenary session
does NOT block the cut — different repos.

## Explicitly out (ride v0.17)

Jev per-class criteria · M1 4–5 (homelab-proxmox, hash-checker,
tuneflow) · blast→check-intensity wiring · C5 cadence→autopilot
self-schedule · patrol-accretion improvements.

## Falsification

The cost-per-class row ships with ONLY numbers that cite their
telemetry evidence (task ids on #550). A number that cannot cite does
not ship. If the red cell finds a finding class the effort machinery
itself should have caught, that is a release blocker, not a footnote.
