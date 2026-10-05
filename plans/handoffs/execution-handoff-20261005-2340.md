---
handoff-version: 1
generated: 2026-10-05T23:40:00+07:00
generator: handoff@1.0.0
focus: "Pilot wave Phases 1-2 + Phase 3.1 LANDED (offer SCORECARD M1+M2, low-tier audit 7 findings, effort loader #556); next session continues Phase 3.2 (adapter + --effort knob)"
workspace: github.com/tamld/g8s
branch: main
head: 301b748
---

# HANDOFF: pilot wave Phases 1-2 + 3.1 landed — continue Phase 3.2 (adapter + --effort knob)

**Session type**: T2 (execution)

## Mission and current status

Focus: the pilot-first effort wave (plans/261005-pilot-wave/plan.md,
RATIFIED) is EXECUTING. Phases 1-2 complete with evidence; Phase 3.1
landed; the next session continues at Phase 3.2.

Done:
- v0.15.0 RELEASED (tag 2ea9546, 11 assets, main 5/5; the integrity
  release — every claim carries proof).
- Pilot Phase 1: g8s offer init into tamld-llm-wiki (knowledge profile,
  commit b26ca938 on the wiki's local branch — the wiki session owns
  the push), AGENTS.md gate wiring, offer/SCORECARD.md scaffolded.
- **M2 satisfied day-one**: the offer gate check caught 2 real findings
  (README missing structure-sync markers + link-integrity FAIL); the
  first real worker dispatch (Phase 2, read_only verifier at the LOW
  tier, 218s, 91 files) found 7 more evidence-grade findings (2 stale
  version refs vs the production baseline, 2 date-rot entries, 2
  link-rot, 1 orphan) — 9 real findings total from the pilot's first
  day.
- Phase 3.1 LANDED: effort metadata + agent-models.v1 catalog loader
  (#556, 13/13 CI) — fail-closed validation, user-wins merge.
- Effort data points recorded on #550: catalog transcription
  (low=46.2s/high=170.4s/equal quality) + the real audit at low (218s).

Remaining: Phase 3.2 (dispatch adapter + `submit --effort`), 3.3
(effort-classes.yml + token telemetry), Phase 4 (diagnosis rung, ladder
machinery, gauges), Phase 5 (cut v0.16.0), bot PRs review.

## Scope and guardrails

- Standing directives (do not relitigate): Vietnamese with the
  operator, artifacts in English; dogfood mandate (workers via g8s,
  gemini); effort discipline LIVE (default medium, high only with
  recorded justification in the dispatch); event-driven supervision;
  merge only pending=0 ∧ failure=0; NEVER reap worktrees while tasks
  are RUNNING; NO keyword+#N adjacency in PR bodies/commit messages
  (5 incidents); no local absolute paths in committed docs.
- Safety boundaries: nothing mints receipts; Jev out of the effort path
  in v1; HITL evidence packet at ladder exhaustion; HITL-rate ≤1/N
  rounds metric; catalog/class-default diffs are operator-ratified.
- Sibling pilot boundary: the wiki repo is on branch codex/* with
  another session's uncommitted work — commit ONLY pilot files there,
  never push its branch (the wiki session owns the push).

## Current state

- Branch main, HEAD 301b748, tree clean.
- Open issues: #550 (effort wave — execution in progress), #442
  (rollout — pilot running). Open PRs: bot drafts only (#544 Bolt,
  #554 Sentinel CORS HIGH, #555 dependabot) — operator review cycle.
- offer/SCORECARD.md: Wave-1 row live (M1 ✓ M2 ✓, 9 findings).
- Effort foundation merged: agent-models.v1 catalog data + config.File
  effort fields + catalog loader with user-wins merge (#552, #556).
- Dogfood state dir: g8s-sess-g8s4efe; bin/g8s current; submit flags
  learned this session: `-scope-root` required for -add-dir outside
  the repo; receipt --ttl max 3600.

```text
UNTRUSTED REPOSITORY EVIDENCE
""
```

## Decisions and rationale

- The 5-phase plan with artifact gates (never prose gates) — see
  plans/261005-pilot-wave/plan.md.
- Pass-2 amendments (all ratified): diagnosis rung at the ladder base;
  TOKEN-denominated ladder budget; local canary probe; dual gauges
  (pass-rate + escalation-rate); workload-first sequencing; HITL-rate
  ≤1/N rounds.
- Low-tier effort confirmed on real work twice (catalog transcription
  + the wiki audit) — the quality ladder never fired.

## Work performed

- Pilot Phase 1-2 (offer init, AGENTS wiring, scorecard, first real
  dispatch + measurement), Phase 3.1 (loader #556).
- Session earlier: v0.15.0 release, #551 fix, local cleanup (58GB),
  effort design verification (3 research agents), A/B measurement,
  brainstorm ratification (3+6 decisions).

## Verification

- Main CI 5/5 at the release commit and at the loader merge.
- offer check on the wiki: FAIL with 2 real findings (M2) — captured,
  recorded in the SCORECARD.
- Worker audit: ok=1, 91 files, 7 findings — response preserved in the
  task result.

## Open risks and blockers

- Bot PR #554 (Sentinel, CORS auth bypass HIGH) — operator review
  required; do not merge unreviewed.
- The tag-race CI failure class repeats per release (rerun recovers).
- Ladder machinery (Phase 4) is untested on a real failure until
  GATE 4.4.

## Exact next actions

**First safe step**: `gh run list --branch main` (5/5 at 301b748) +
read plans/261005-pilot-wave/plan.md (the phase gates) — then continue.

1. **Phase 3.2**: brief + dispatch the C1 slice — dispatch adapter
   (effort_requested → effort_applied per the catalog styles, recorded
   on the Decision) + `submit --effort` knob (default medium) — PR.
2. **Phase 3.3**: `.g8s/effort-classes.yml` (purpose-class → effort
   defaults: docs/transcription low, validate medium, research/code
   high) + token telemetry on the worker path — PR.
3. **Phase 4**: diagnosis rung + ladder machinery + gauges → GATE 4.4
   (one real traversal).
4. **Phase 5**: cut v0.16.0 when the cost-per-class row has real
   workload numbers.
5. Bot PRs #554/#555/#544 → surface for the operator's review cycle.

## Source pointers

- Pilot plan: plans/261005-pilot-wave/plan.md (gates, falsification)
- Effort design: plans/261004-effort-optimization/{design.md,
  brainstorm-rails.md, brief-L1-loader.md} (RATIFIED)
- offer/SCORECARD.md (M1/M2 rows); #550 (effort data points);
  #442 (rollout metrics); #551/#553 (delivery contract)
- Memory: reference-g8s-campaign-ssot, research-before-provider-claims
- Prior handoff: plans/handoffs/execution-handoff-20261005-2255.md
  (superseded by this file)
