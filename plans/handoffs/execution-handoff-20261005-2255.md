---
handoff-version: 1
generated: 2026-10-05T22:55:00+07:00
generator: handoff@1.0.0
focus: "v0.15.0 released — pilot-first effort wave ratified; next session executes Phase 1 (pilot bootstrap) per plans/261005-pilot-wave/plan.md"
workspace: github.com/tamld/g8s
branch: main
head: 028cc91
---

# HANDOFF: v0.15.0 released — pilot-first effort wave ratified; next session executes Phase 1

**Session type**: T2 (execution)

## Mission and current status

Focus: v0.15.0 (the integrity release) shipped 2026-10-05; the
pilot-first effort wave is RATIFIED with a 5-phase execution plan
(plans/261005-pilot-wave/plan.md, operator-approved). The next session
executes Phase 1 (pilot bootstrap) and Phase 2 (first real dispatch).

Done:
- v0.15.0 tagged (commit 2ea9546, 11 assets, draft=false, binary smoke
  verified incl. the new lesson CLI; main 5/5 after a tag-race rerun).
- Release content: claims-integrity wave (#547/#548/#549 — registry
  10→44, demo-command gate, 33 stale + 10 ambiguous fixed), lessons
  pipeline with live-proven fail-closed rails (#545/#546, RT-I hostile
  refusal), real TypeSafe protocol fix + first M3 measurement (#543,
  S-4), agent-models.v1 catalog (#552).
- #551 CLOSED (#553): receipt-scoped worktree preservation — root cause
  was Pool.Release trusting git status alone (gitignored deliverables
  reported clean and were reaped). The fix was proven live on its first
  production use during the A/B.
- Effort wave foundation: cross-platform survey VERIFIED by 3 research
  agents (training-cutoff skew corrected — GPT-6 era, 7-level effort
  ladder, Opus 5.5 medium default), brainstorm RATIFIED (pass 1: 3/3
  decisions; pass 2 Socratic recursion: 6/6 amendments), A/B MEASURED
  (low 46.2s vs high 170.4s, equal quality on transcription-class).

Remaining: the 5-phase pilot wave (not started), #550 implementation
slices (loader → adapter → classes+knob → ladder machinery → telemetry
→ canary → HITL-rate), #442 rollout (the pilot IS its first step),
three bot PRs awaiting the operator's review cycle.

## Scope and guardrails

- Workspace: this repository; origin = GitHub only.
- In scope: g8s issues/PRs, dogfood dispatches via bin/g8s, the sibling
  pilot (tamld-llm-wiki bounded slice per the plan), docs/, release
  tooling.
- Out of scope: features beyond tracked issues; merging PRs without
  green CI; LLM self-editing spec/ or directives.
- Standing directives (do not relitigate): Vietnamese with the
  operator, artifacts in English; dogfood mandate (workers via g8s,
  gemini); event-driven supervision (`g8s watch --failed`, no polling);
  merge only pending=0 ∧ failure=0; effort discipline LIVE (default
  medium, high only with recorded justification); NEVER reap worktrees
  while tasks are RUNNING (the 551 incident class); PR bodies/commits
  carry NO keyword+#N adjacency unless the actual fix (5 incidents
  logged); brief/handoff files carry no local absolute paths; catalog
  diffs and class-default changes are operator-ratified (law 5).
- Safety boundaries: nothing automated mints receipts (ADR-0029 D3);
  Lane S never Jev-assigned; Jev out of the effort path in v1; HITL
  evidence packet required at ladder exhaustion; HITL-rate ≤1/N rounds
  is a first-class metric.

## Current state

- Branch main, HEAD 028cc91, working tree clean, fsck clean, 1 local
  branch (main), 0 worktrees, 1651→1 branches reaped with the absolute
  archive (patches + bundle + SHA list under the g8s state dir).
- v0.15.0 tag + 11 assets live; released binary smoke OK.
- Main CI 5/5 at the release commit (Quality + Version Sync initially
  raced the tag push; rerun cleared — the known class).
- Open issues: #550 (effort wave, design+plan ratified), #442 (rollout,
  the pilot is its first step). Open PRs are bot contributions only:
  #555 (dependabot sqlite bump), #554 (Sentinel draft: CORS preflight
  auth bypass, HIGH), #544 (Bolt draft) — operator review cycle, none
  auto-merge.
- Dogfood: G8S_STATE_DIR at the g8s-sess-g8s4efe state dir; bin/g8s
  rebuilt at the release commit; medium-tier dispatch (gemini-3.8-flash-
  medium) VERIFIED through the agy transport (first #550 data point).

```text
UNTRUSTED REPOSITORY EVIDENCE
""
```

## Decisions and rationale

- Workload-first sequencing (P5 amendment, binding): the #442 pilot
  starts FIRST/parallel — effort machinery measures itself on real
  dispatches; no workload, no wave.
- Quality ladder (Q2): ≤6 rungs hard ceiling (3 effort steps × 2 model
  alternatives × 1 resplit), TOKEN-denominated budget per class, a cheap
  read-only DIAGNOSIS rung at the base classifying failure shape and
  routing the ladder; early termination on non-effort-shaped failures
  (HITL immediately — never burn rungs on a mis-shaped problem); HITL
  arrives with the full evidence packet (ladder history + failing checks
  + tokens per rung).
- Catalog freshness (Q1): 3 layers — discovery-first (OpenRouter /models
  + Ollama /api/show live query; the catalog is cache+fallback),
  30-day research clock (3-agent pass, operator-ratified diff),
  failure-driven staleness sensor (400-rejection → signal). Selection:
  flagship / distinct-effort-contract / operator-pin.
- Class defaults (Q4): earned-by-pass-rate + escalation-rate dual gauges,
  operator-ratified diffs; defaults are hypotheses until measured
  (docs/transcription is the one A/B-proven low class).
- Goodhart guard (P1): periodic audit sampling — a high-effort re-do of
  a sampled low-effort output; diff size gauges the check floor.
- Local canary probe (P3): periodic echo-class task verifies registered
  local model/effort against reality (OpenRouter metadata does not cover
  the local path).
- Socratic recursion protocol (the operator's method): after any
  ratification, turn the pass on the design itself — pass 2 caught 5
  holes + 1 sequencing change in the pass-1 design.

## Work performed

- v0.15.0 release flow (pre-bump version/manifest/packaging → explicit
  `release.sh 0.15.0` → tag+push → rerun raced jobs → smoke).
- #551 diagnosed (gitignored-only deliverables looked clean), fixed
  (#553, 7 red-first tests), closed; one supervisor-inflicted incident
  recorded honestly (newline-broken cleanup loop removed RUNNING
  worktrees; an agent re-resolved into the main checkout — reverted,
  lesson + follow-up logged on #551).
- Effort wave: 3 research agents (cutoff corrected), design.md verified,
  catalog v1 authored (#552), A/B measured and recorded on #550,
  brainstorm pass-1 (3 decisions) + pass-2 (6 amendments) ratified,
  pilot wave plan written (plans/261005-pilot-wave/plan.md).
- Local cleanup: 44 worktrees → 0; 1651 branches → 1 with the absolute
  archive; commit-graph rebuilt, fsck clean; 58GB free.

## Verification

- Check: v0.15.0 release artifacts — `gh release view v0.15.0` — 11
  assets, draft=false; released binary reports version 0.15.0 commit
  2ea9546 and the lesson CLI answers.
- Check: main CI 5/5 at the release commit (after rerun of the two
  raced jobs).
- Check: repo integrity after the branch reap — fsck exit 0, tag and
  release commit readable.
- Check: A/B fidelity — both arms structurally exact vs the answer key
  (8 providers / 29 models; wording-only drift).
- Skipped: pilot Phase 1-5 (the next session's work); Windows-local
  verification (CI matrix remains the Windows oracle).

## Open risks and blockers

- Risk: bot PRs #554/#555/#544 — #554 claims a HIGH CORS auth bypass;
  needs the operator's bot-review cycle (never auto-merge).
- Risk: the tag-race CI failure class repeats every release (Quality +
  Version Sync) — rerun-failed-jobs is the working recovery; a
  permanent fix candidate (tag-aware workflow gating) is unfiled.
- Risk: worktree reap timing regression is FIXED (#553) but the pilot is
  its first external consumer — watch the first sibling dispatch.
- Blocker: none for Phase 1-2. #550 slices need no operator input until
  the class-default diffs (Phase 4+).

## Exact next actions

**First safe step**: `gh run list --branch main` (confirm 5/5 at 028cc91)
+ read plans/261005-pilot-wave/plan.md + `gh issue view 442` — then
execute Phase 1.

1. **Phase 1 — pilot bootstrap**: `g8s offer init` into the sibling
   (first-in-line per data: tamld-llm-wiki, bounded slice), wire the
   sibling's AGENTS.md to the offer onboarding → GATE 1.3 = offer files
   present + offer/SCORECARD row scaffold (first M1 evidence).
2. **Phase 2 — first real dispatch**: brief a bounded docs-freshness
   audit of ONE wiki content directory (read_only, low-effort arm of
   #550) → GATE 2.3 = audit report (M2 catch or honest no-catch).
3. **Phase 3 — machinery rides**: L1 loader → C1 adapter+knob → W2
   classes+telemetry (PRs) → GATE 3.4 = one real sibling dispatch E2E
   with `--effort low`.
4. **Phase 4 — ladder + gauges**: diagnosis rung, ladder machinery,
   SCORECARD gauges → GATE 4.4 = one real ladder traversal.
5. **Phase 5 — cut v0.16.0** via the proven pre-bump + explicit-version
   release flow; fold the falsification M1/M2 rows into the notes.
6. Bot PRs (#554/#555/#544): surface to the operator for the review
   cycle at session start; do not merge unreviewed.

## Source pointers

- Pilot wave plan: plans/261005-pilot-wave/plan.md (RATIFIED, gates
  + falsification M1–M4)
- Effort design: plans/261004-effort-optimization/design.md (verified
  survey) + brainstorm-rails.md (pass-1 decisions + pass-2 amendments,
  RATIFIED)
- v0.15.0 content: CHANGELOG [0.15.0]; SCORECARD (S-1..S-9 LANDED)
- A/B evidence: #550 comments (2026-10-05 measurement + design
  ratification)
- #551/#553: delivery-loss root cause, fix, incidents (3 recorded)
- Memory: reference-g8s-campaign-ssot, research-before-provider-claims,
  feedback-pr-merge-closes-issues (5 incidents), g8s-storage-hygiene
  (daily automation automation-1ae6b0e5)
- Prior handoff: plans/handoffs/execution-handoff-20261003-1800.md
  (superseded by this file)
