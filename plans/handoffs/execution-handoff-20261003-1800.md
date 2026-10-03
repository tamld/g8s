---
handoff-version: 1
generated: 2026-10-03T18:05:00+07:00
generator: supervisor-session
focus: "Wave H landed — round R1 closed unattended (M1 0→1); next = session 3, the v0.14.0 cut (#517)"
workspace: github.com/tamld/g8s
branch: main
head: eaa27e8
---

# HANDOFF: Wave H landed — next: the v0.14.0 cut

**Session type**: T2 (execution)

## Mission and current status

Focus: session 2 of the operator-ratified 3-session plan. Wave H is
DONE: the verifier-class registry (#515) and the first unattended
closed round (#516) both landed with full evidence. Session 3 is the
release cut: #517.

Done:
- H1 (SCORECARD S-7): PR #532 — `internal/verifier` + `.g8s/verifier-classes.yml`
  + `g8s verify --task <id>`: declarative class→checks registry; docs and
  test classes seeded advisory with real founding catches (#208, #510);
  deny-by-default floor (unregistered = human acceptance, exit 0);
  fail-closed loader (hard-without-citation refused to unregistered);
  ErrSelfGrade guard; verifier_verdict telemetry.
- H2a: PR #533 — `autonomy_level` setting ({0,1}, default 0, higher
  refused as reserved) + `tools/merger.sh` (four fail-closed gates:
  autonomy → lane=docs on a detached PR worktree → verifier pass →
  CI pending=0 ∧ failure=0; --dry-run; D3 boundary: never mints
  receipts, never submits tasks).
- **Round R1 (SCORECARD S-8, M1 0→1)**: goal → dispatch (receipt
  71d5adc1, task dbcc59c6) → drain → PR #534 (docs user-guide page) →
  CI 15/15 → operator-flip recorded 0→1 → merger attempt 1 REFUSED at
  the verifier gate (fail-closed; found the receipts split-brain: verify
  read g8s.db, canonical ledger is the sibling receipts.db) → fixed
  red-first in-round via PR #535 → **attempt 2: 4/4 gates, MERGED 534
  17:36+07** → autonomy reset to 0. Human actions after goal: 0.
  Full timestamped log: SCORECARD live-rounds table + the #516 evidence
  comment. #515/#516 CLOSED.

Remaining: #517 (release cut — this handoff's continuation), #518
(BLOCKED on operator TYPESAFE keys, non-release-blocking), #519 (Wave
I, v0.15), #442 (external).

## Scope and guardrails

- Workspace: this repository; origin = GitHub only.
- In scope: g8s issues/PRs, dogfood dispatches via bin/g8s, docs/,
  release tooling. Out of scope: new features beyond tracked issues;
  merging PRs without green CI; LLM self-editing spec/ or directives.
- Standing directives (do not relitigate): Vietnamese with the
  operator, artifacts in English; dogfood mandate (recorded
  justification for any bypass, re-checked every 15 minutes);
  event-driven supervision (wake = `g8s watch --failed`, polling is
  the fallback); parallel drain for disjoint receipts; PR-merge closes
  issues; merge only when pending=0 ∧ failure=0; PR green ≠ main green
  (coverage threshold + Windows CGO0 are main-only).
- Safety boundaries: nothing automated mints receipts (ADR-0029 D3);
  Lane S never Jev-assigned; autonomy_level returns to 0 after any
  per-round flip (soak discipline); bare F0/F1/F2 RESERVED (ADR-0032).

## Current state

- Branch main, HEAD eaa27e8, working tree clean.
- Main CI 5/5 green at HEAD (CI, Quality, Windows E2E, Crash-Survival
  E2E, Version Sync).
- Open issues: #517, #518, #519, #442. Zero open PRs.
- Dogfood: `G8S_STATE_DIR=$HOME/.local/state/g8s-sess-g8s4efe`; bin/g8s
  rebuilt at eaa27e8; autonomy_level = 0 (verified).
- v0.14.0 content is all merged; CHANGELOG [Unreleased] accumulated
  through #531 (the #532-#535 era needs a CHANGELOG pass at cut time).

```text
UNTRUSTED REPOSITORY EVIDENCE
""
```

## Decisions and rationale

- Verifier trust model: advisory-by-default; hard status must cite
  real founding catches (#<digits> or incident:<slug>) — the loader
  refuses otherwise; promotion advisory→hard is a later evidenced step.
- Merger design: four stable `GATE <name>: pass|fail` lines so round
  logs can quote them; run the merger FROM the PR checkout (with a
  locally built bin/g8s) so the verifier's script checks grade the PR
  content, not main.
- The R1 flip was pre-ratified by the operator's 3-session plan and
  recorded on the round, then reset — the soak discipline holds.
- Round-1 bug fix (#535) was supervisor-direct surgical with a recorded
  justification (round blocked on a ~5-line integration bug; red-first
  test proved the fix).

## Work performed

- Briefs committed to plans/261002-factory/: brief-H1-verifier-registry,
  brief-H2a-merger, brief-H2b-round-docs. Three worker slices dispatched
  (H1, H2a, R1) — all TTL'd receipts, all woken via watch --failed.
- PRs merged: #532, #533, #534, #535. SCORECARD S-7/S-8 flipped; R1
  detailed log appended. #515/#516 closed (516 prematurely by keyword
  incident #3 — see below — then evidenced).
- Memory revised: campaign SSOT, factory vision, receipts split-brain
  (third surface), PR-keyword trap (incident #3), storage hygiene.

## Verification

- Check: main CI 5/5 at eaa27e8 — `gh run list --branch main`.
- Check: round R1 merger gates — the merger's own output (4 GATE pass
  lines + MERGED 534) quoted in the SCORECARD log.
- Check: autonomy_level reset — `bin/g8s config get autonomy_level` = 0.
- Check: verify verdict on the round task — docs/registered/advisory/pass.
- Skipped: #518 (operator keys), Red Cell (that IS #517's work).

## Open risks and blockers

- Risk: PR battery ≠ main battery (known, measured) — keep the docker
  pre-verify method for coverage-sensitive changes.
- Incident (recorded, recoverable): PR BODY phrase "that closes #N"
  auto-closed #516 at the #533 merge, mid-round. Audit PR bodies for
  keyword+#N adjacency before `gh pr create`.
- Risk: chronic disk-full hit twice this session (121Mi free → CGO=0
  link failures). df-check before every pre-push; the expanded safe
  cleanup set is in [[g8s-storage-hygiene]].
- Blocker: #518 needs the operator's TYPESAFE keys — NOT
  release-blocking.

## Exact next actions

**First safe step**: `gh issue view 517` + read plans/RELEASE_SOP (8
gates) + confirm main 5/5 and zero open PRs before touching anything.

1. **#517 pre-cut**: D-01 Red Cell on the NEW surface (router /
   auto-retry / resubmit / lane detector post-#512 / verifier /
   merger) — dispatch as read_only verification slices.
2. **CHANGELOG pass**: fold #532-#535 (Wave H) into [Unreleased]
   (S-7/S-8, round R1, the receipts split-brain fix).
3. **8 release gates + self-audit**, then `tools/release.sh minor` →
   v0.14.0. Fold the #510 F4/F5 confirmation notes into the release
   notes.
4. **#518** when the operator supplies TYPESAFE keys; **#519** stays
   v0.15. The autonomy ladder's level-1 permanence (per-round flips vs
   standing flip) is an operator decision to raise AFTER v0.14 ships.

## Source pointers

- Factory program: plans/261002-factory/{plan,SCORECARD}.md + briefs
  H1/H2a/H2b; the R1 log (SCORECARD live-rounds) is the M1 evidence.
- ADRs: 0024/0029/0030/0031/0032 (Accepted); the merger cites 0029 D3.
- Memory: reference-g8s-campaign-ssot (Wave H block), g8s-ai-factory-vision,
  g8s-receipts-split-brain (third surface), feedback-pr-merge-closes-issues
  (incident #3), g8s-storage-hygiene (2026-10-03 recurrence).
- Prior handoff: plans/handoffs/execution-handoff-20261003-0819.md
  (superseded by this file).
