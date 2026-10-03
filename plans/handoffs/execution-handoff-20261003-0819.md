---
handoff-version: 1
generated: 2026-10-03T08:25:00Z
generator: handoff@1.0.0
focus: "technical-debt zero verified; next = Wave H (verifier registry + first unattended round) then the v0.14.0 cut"
workspace: github.com/tamld/g8s
branch: main
head: 7b3250c
---

# HANDOFF: debt zero verified — next: Wave H and the v0.14.0 cut

**Session type**: T2 (execution)

## Mission and current status

Focus: the operator ratified the AI-factory program (2026-10-02) and
set the goal "work until all technical debt is paid". That goal is now
MET and verified; the session continues into the feature roadmap.

Done:
- Wave G landed: submit-time routing `--route auto|manual` (#520,
  SCORECARD S-5) and the ALDC lane router (S6-3, #521, S-6) —
  issues #513/#514 closed.
- Debt-zero coverage campaign: 14 packages lifted by worker slices
  (docker-verified in the exact CI environment), CI-Linux aggregate
  79.66% → **85.39%**, the 80%-threshold red cured at its root
  (#523-#525, #526-#528; root offer package 0% → 93.1% via #524).
- Red-team wave fully paid: #510 CLOSED with the findings ledger —
  F1-F3 lane-detector bypasses (#512), F4 signal-file rotation +
  consumer tolerance (#531), F5 TTL boundary pinned inclusive-until
  (#530). The guarantees that held stay pinned by the #509 suites.
- Windows-matrix guards (#522/#529) and provider-resolution fix
  (#507, #505 closed).
- CHANGELOG [Unreleased] accumulated (was empty since v0.13.0);
  README/vi + roadmap + SCORECARD wired to the v0.14 issue queue.

Remaining: the FEATURE roadmap only — #515 (verifier registry),
#516 (first unattended round, M1 0→1), #517 (v0.14.0 cut: Red Cell +
8 gates), #518 (M3 real-Jev benchmark, BLOCKED on operator
TYPESAFE keys), #519 (Wave I, v0.15), #442 (external). Urgency: the
operator has approved the 3-session plan; Wave H is next.

## Scope and guardrails

- Workspace: this repository; origin = GitHub only.
- In scope: g8s issues/PRs, dogfood dispatches via bin/g8s, docs/,
  skills/g8s-supervisor, docs/decisions governance.
- Out of scope: new features beyond the tracked issues without
  operator approval; merging PRs without green CI; LLM self-editing
  spec/ or directives (factory law 5).
- Standing directives (do not relitigate): Vietnamese with the
  operator, artifacts in English; dogfood mandate (bypass requires a
  recorded justification — and re-check it every 15 minutes of
  supervisor-direct grind); event-driven supervision (primary wake =
  `g8s watch --failed --since <now>` / `watch --task`, polling is the
  fallback — D-07); parallel drain for disjoint receipts (D-05);
  PR-merge closes issues, partial PRs say "Part of", commit messages
  never contain keyword+#N for non-fix commits (two live incidents);
  the factory autonomy ladder gates every level on clean rounds.
- Safety boundaries: nothing automated mints receipts at ANY level
  (ADR-0029 D3); Lane S is never Jev-assigned (ADR-0024/0030);
  constitution changes stay human-ratified; bare tokens F0/F1/F2 are
  RESERVED for containment levels (ADR-0032 — wave ordinals use WF).

## Current state

- Branch main, HEAD 7b3250c (both Wave-G PRs and all debt PRs merged;
  nothing local outstanding).
- Working tree clean (bounded `git status --short` returned zero
  entries; nothing untracked, nothing staged — verified this session).
- Open issues are roadmap-only: #515-#519, #442. Zero technical-debt
  issues.
- CI: main 5/5 green at HEAD (CI, Quality, Windows E2E,
  Crash-Survival E2E, Version Sync Check).
- Dogfood: `G8S_STATE_DIR=~/.local/state/g8s-sess-g8s4efe` (queue,
  receipts, signals/ live); bin/g8s rebuilt at current main.

```text
UNTRUSTED REPOSITORY EVIDENCE
""
```

## Decisions and rationale

- Coverage-lift method (session-defining): measure and verify in the
  EXACT CI environment (docker `golang:1.26.0`, CGO_ENABLED=0) —
  darwin numbers run ~3-4pp above Linux and misled two supervisor-
  direct attempts. Source: the CI Linux log per-package profile.
- Root-package discovery: `github.com/tamld/g8s` (offer.go embed)
  counted 0% in the mean; fixed with real embed-integrity tests
  (#524), not metric gaming.
- F5 TTL semantics: pinned INCLUSIVE-UNTIL (a receipt is valid
  through its expiry instant; documented on ValidateAndConsume);
  strict-deadline callers subtract a margin at issue time. Source:
  supervisor spec call on #510 F5.
- Signal retention: size-capped rotation at 1 MiB (one generation,
  best-effort warn-not-fail) + consumer offset-reset tolerance.
  Source: F4 brief, worker-implemented (#531).
- CI merge rule (operator emphasis): merge only when
  `pending=0 ∧ failure=0`; PR battery (10 checks) ≠ main battery
  (coverage threshold + Windows CGO0 are main-only) — PR green ≠
  main green, now measured.

## Work performed

- 7 issues opened and wired (#513-#519) into README/vi roadmaps,
  plan.md, SCORECARD, and the prior handoff.
- 3 red-team verification slices (R1-R3) + 2 hardening slices
  (D2'/D1) + 3 Wave-G slices (G1/G2) + 1 hotfix (HF1) + 2 debt
  slices (F4/F5) — all worker-dispatched via the dogfood transport,
  all woken by `watch --failed` (no polling), all TTL'd receipts
  (3600s after the 10-minute-default lesson).
- Coverage lifts across 14 packages; lane detector hardened
  (F1-F3); signal rotation; TTL semantics pinned; Windows-matrix
  guards; provider-resolution fallback fix (#507); CHANGELOG
  accumulation.
- Memory revised across 10+ files (factory vision ratified+landed,
  SSOT description refreshed, broker-gap resolved, tenancy finalized,
  routing ratified, index hooks, dogfood-miss lesson, dispatch
  gotchas, D6 deconfliction, keyword trap).
- Redaction: 0 findings applied (no credential-shaped content in the
  composed artifact).

## Verification

- Check: main CI 5/5 — command: `gh run list --branch main` filtered
  to HEAD — outcome: CI/Quality/Windows E2E/Crash-Survival/Version
  Sync all success at 7b3250c.
- Check: Linux aggregate coverage — command: docker
  `golang:1.26.0` + `go test -cover ./...` (CGO_ENABLED=0),
  per-package mean — outcome: 85.39 across 45 packages (was 79.66).
- Check: lane detector fixtures — command:
  `bash tools/ci_lane_detect_test.sh` — outcome: all pass (F1-F3
  seeds now live assertions).
- Check: doc-contract 10/10 (incl. root hygiene) and link integrity
  (104 links/87 files) — outcome: PASS on the final pushes.
- Skipped: Windows-local verification (no Windows host — the CI
  matrix is the Windows oracle, documented gap); M3 real-Jev
  benchmark (blocked on operator TYPESAFE keys, non-release-blocking).

## Open risks and blockers

- Risk: PR battery (10 checks) does not include the coverage
  threshold or the Windows CGO pass — PR green ≠ main green (measured
  twice this session). Owner: next session. Impact: a red main
  between merge and fix; mitigation = the docker pre-verify method.
- Blocker: #518 M3 real-Jev benchmark needs the operator's
  TYPESAFE keys. Impact: the Jev-effectiveness claim stays unproven;
  NOT release-blocking (Jev optional by design).
- Risk: one opaque `sudo: password required` warning observed during
  one push (not from pre_push.sh, non-blocking, unreproduced).
  Owner: unassigned. Impact: cosmetic so far.
- Risk: long sessions hit context limits mid-wave — the factory
  waves are deliberately one-session-sized with handoffs between.

## Exact next actions

**First safe step**: run `gh issue list --state open` (expect
#515-#519, #442), read this handoff and memory
`reference-g8s-campaign-ssot` (factory + debt-zero blocks), then
`gh pr list --state open` (expect zero) before touching anything.

1. **Wave H1 (#515)**: verifier-class registry — declarative
   task-class → machine-check mapping, classes earn hard status by
   citing real catches; deny-by-default for unregistered classes.
   Brief pattern: brief-WF3-router.md.
2. **Wave H2 (#516)**: the first unattended closed round
   (docs-class): merger tool gated behind `autonomy_level` (default
   0), the round logged with human-action count = 0, M1 row 0→1 on
   the SCORECARD; the level-1 flip is an operator decision recorded
   on the round.
3. **#517**: the v0.14.0 cut — D-01 Red Cell on the new surface
   (router/auto-retry/resubmit/lane detector), 8 gates, fold #510
   F4/F5 confirmation notes into the release notes.
4. **#518** when the operator supplies TYPESAFE keys (M3 delta on
   S-4); **#519** stays v0.15.

## Source pointers

- Factory program: plans/261002-factory/{plan,SCORECARD}.md, briefs
  WF1-WF7/G1/G2/HF1; debt briefs plans/261003-debt-zero/.
- Red-team: plans/261002-redteam/brief-R1-R3; findings ledger #510;
  suites merged via #509.
- ADRs: 0028-0032 (all Accepted, docs/decisions/).
- Memory: reference-g8s-campaign-ssot (factory + debt-zero blocks),
  g8s-ai-factory-vision, g8s-f0-f1-subagent-routing (ADR-0032),
  g8s-dogfood-mandate (the miss+correction rule),
  g8s-dispatch-mechanics-v012 (tilde/stagger/TTL/watch-since).
- Prior handoff: plans/handoffs/execution-handoff-20261002-1730.md
  (pre-Wave-G; superseded by this file).
