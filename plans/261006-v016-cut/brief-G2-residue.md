# Task: G2 — patrol residue fixes in version docs (v0.16 gate G2)

Repo: g8s @ main. The weekly patrol (automation-87f06732, g8s
self-audit) flagged stale version claims: the repo is at tag v0.15.0
(tagged 2026-10-05, release commit 2ea9546) while docs still present
v0.1.0-alpha as current. A release that ships 0.16 while its own
docs claim v0.1 fails its standard. Fix the residue, minimally and
truthfully.

## Delivery protocol

Scratch worktree. Write ONLY to these three files:

- docs/VERSIONING.md
- docs/security/VERIFICATION_GUIDE.md
- docs/CLI_AND_EXPERIENCE_DESIGN.md

Do NOT run git commit. Do NOT touch any other file. Do NOT claim
v0.16.0 as released anywhere — it is a target, not a fact.

## Required fixes

1. docs/VERSIONING.md
   - Section 2 cadence diagram (line ~19) still reads
     `v0.1.0-alpha (Current)` — update the diagram to reflect the
     actual position: v0.14.0 feature release (2026-10-03), v0.15.0
     integrity release (2026-10-05, current), v0.16.0 self-measuring
     release (in preparation). Keep the diagram one line if it
     stays a diagram.
   - Milestone table (section 2) stops at the v0.1.x milestones —
     keep the historical rows, add a short "Delivered" row set for
     v0.14.0 / v0.15.0 and a "In preparation" row for v0.16.0 with
     one-sentence scope each (feature wave / integrity hardening /
     self-measuring effort telemetry). Do not invent dates beyond
     the two given above.
   - Leave semantic-versioning definitions (section 1) untouched.
2. docs/security/VERIFICATION_GUIDE.md (~line 87): sample
   certificate-identity references `@refs/tags/v0.1.0` — update the
   sample to a current-style tag reference (`@refs/tags/v0.15.0`)
   and confirm no other stale `v0.1.0` claims in that file without
   touching unrelated prose.
3. docs/CLI_AND_EXPERIENCE_DESIGN.md (~line 65): sample binary
   banner reads `v0.1.0-alpha, darwin/arm64` — update the sample
   output to `v0.15.0, darwin/arm64`.

## Constraints

- Truthful minimal edits; no marketing language; no reformatting
  beyond the edited lines.
- All version references you write must match: v0.14.0 = feature
  release, v0.15.0 = integrity release (current), v0.16.0 =
  self-measuring release (in preparation).
- grep the three files after editing to confirm no remaining
  "(Current)" marker on v0.1.0-alpha.

## Report

- Per file: lines changed, before/after for each edited claim.
- Confirmation grep output (no stale current-marker left).
