---
handoff-version: 1
generated: 2026-10-07T00:30:00+07:00
generator: handoff@1.0.0
focus: "DEPLOYMENT — the two-project stress test begins. This doc routes Session A (cut v0.16.0) and Session B (aegis mercenary). Pass criteria pre-registered. Launch from the g8s workspace."
workspace: github.com/tamld/g8s
branch: main
head: 5b378e0
---

# DEPLOYMENT HANDOFF: stress test — two projects, one factory

**Session type**: this is a ROUTING doc. Do NOT execute the whole test
from the session that reads it — pick YOUR session (A or B), read only
your entry doc, close a handoff before touching the other project.

## Mission (the one line)

Prove the factory runs two heterogeneous projects with zero context
interleaving and zero goal drift — **and "test passed" is defined
structurally: v0.16.0 tagged + Returns Ledger 5/5 + patrol green.**

## Current state (verified 2026-10-07, main 5b378e0, CI green)

- v0.15.0 released; the ENTIRE effort wave merged (#556–#565): loader,
  adapter + `submit --effort`, classes + token telemetry, ladder +
  gauges, sibling registries, ground-truth fixes, pass-3 signal model,
  matrix v1.1.
- Patrol live: automation-87f06732, weekly Mondays 09:30, THREE
  targets (g8s self + defense-in-depth + aegis), read_only only, wiki
  excluded (hardgate — another agent owns it).
- M1 3/5 (wiki, defense-in-depth, aegis) — falsification target HIT.
- Strategy docs: plans/261006-v016-cut/plan.md (Session A entry),
  plans/handoffs/execution-handoff-20261006-aegis-mercenary.md
  (Session B entry), plans/261006-two-project-ops/brainstorm.md
  (operating model + clerk pattern).

## SESSION A — cut v0.16.0 ("the self-measuring release")

- **Entry doc**: plans/261006-v016-cut/plan.md — gates G1–G5 are the
  whole job; anything else found on the way goes to the ledger, not
  into your hands.
- Order that works: G1 red-cell workers ∥ G2 residue fixes (disjoint
  scopes, parallel receipts) → G3 claims + G4 docs → G5 flow
  (pre-bump version.go + manifest + packaging; CHANGELOG [Unreleased]
  is EMPTY — write it; keep the heading; explicit
  `bash tools/release.sh 0.16.0`, NEVER `minor`; rerun raced CI jobs
  — 2-release known pattern; binary smoke `g8s version 0.16.0`).
- Done = tag pushed + main CI 5/5 + release notes carry only
  citation-backed numbers.

## SESSION B — aegis mercenary

- **Entry doc**: plans/handoffs/execution-handoff-20261006-aegis-
  mercenary.md — the two-sided contract (aegis fixed ∥ Returns Ledger
  home) + the full dogfood protocol. Own state dir
  `g8s-sess-aegis`. Debt seeds: docs/audits pass-2 + 5 red files
  (#2354 aegis tracker) + wave-3 findings.
- Done = Returns Ledger 5/5 WITH citations (issues, lessons, infra-
  review-class telemetry, ladder traversals, end handoff).

## Operating rules (the stress-test frame, pre-registered)

1. One project per session; close a handoff before opening the other.
2. Workers of BOTH projects may share the queue timeline (disjoint
   receipts; stagger spawns 3s; never reap worktrees while RUNNING).
3. Distraction → ledger, not → hands. Supervisor digests residue only.
4. Every "done" cites an artifact (tag / merged PR / ledger row /
   task id) — artifact-or-it-didn't-happen.
5. Failure signatures the operator watches: wrong-repo paths in a
   prompt (V1) · one session holding both handoffs open (V2) · a
   dispatch >50 min unharvested (V3) · "fixed lots of stuff" with an
   empty ledger (V4).
6. Boundaries: tamld-llm-wiki hands-off; aegis `records/metrics/`
   untouched; Session B never patches g8s main (issues/lessons only);
   rebuild `bin/g8s` from main before trusting classifications.

## After both sessions pass

- tuneflow onboarding (wave 4, SCORECARD row exists; profile TBD).
- F-recursion clerk probe (read_only, the first F1→F2 exercise —
  plans/261006-two-project-ops/brainstorm.md addendum).
- v0.17 ingests the mercenary returns.

## Source pointers

- Operating model + scaling law: plans/261006-two-project-ops/
  brainstorm.md
- Effort wave: #550 (comments tail), plans/261004-effort-optimization/
- Patrol: automation-87f06732; SCORECARD = the project board
- Memory: g8s-effort-optimization, g8s-dispatch-mechanics-v012,
  g8s-f0-f1-subagent-routing, feedback-jev-escalation-autonomy
