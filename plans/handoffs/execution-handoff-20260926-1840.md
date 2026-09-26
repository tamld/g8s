# HANDOFF: g8s T2 Campaign — S1 Execution Start (ALDC Stage 3 Ready)

**Date**: 2026-09-26 ~18:35 · **Branch**: main @ 007e463 · **Written by**: T1→T2 transition session (ZCode)

## Mission and current status

Strategy session (T1) completed and operator-ratified. ADR-0022 (Session-Type
Protocol) **Accepted**. Campaign issues opened. S1 plan written to full ALDC
contract. The next session's job: run ALDC stages 3–9 for issue **#393**
(implementation → ship → learn), then proceed down the campaign order.

## What is done (this session)

1. **Docs (uncommitted on working tree — ship them in the first PR)**:
   - `docs/decisions/0022-strategic-session-protocol.md` — **Accepted**, operator-ratified 2026-09-26
   - `plans/260926-strategy-t1-session/plan.md` — T1 ledger incl. self-reflection revisions + sharing topology + ratified campaign order
   - `plans/260926-s1-session-state/plan.md` — S1 plan (frontmatter `issue: 393`), State Diagram + Red Test Proof contract-complete
2. **Issues opened**: #393 S1 session state (P0, plan handoff comment posted) ·
   #394 S2 concurrent dispatch (hard-gated on #393) · #395 S3a memory
   promotion gate (ADR-0023 candidate, must precede #396) · #396 S3b Context
   Broker (fail-open + degraded telemetry) · #397 doc-contract session-type
   marker · #398 S5 L2 floor + L4 advisory. DECIDEd semantics commented on
   #379 (option 1+2, LLM-judge killed) and #383 (schema first, content = L3
   extension).
3. **S0 hygiene**: reaped 18 `agy/sup-*` merged branches + 4 orphan `/tmp`
   blind-converge worktrees + 4 `blind/*` branches. Repo now: 12 local
   branches, 1 worktree, 0 PRs — matches handoff claims.
4. **Jev triages**: grant_receipt ×2 (risk 0.01, 0.09), trace `01a0dd42…`.
5. **Skill registry fixed** (session-tooling, outside repo): 58 SKILL.md files
   in `~/.agents/skills/` had JSON-quoted frontmatter keys (`"name":`) which
   the ZCode loader drops (missing name/description → fails-to-load tier).
   Fixed to flat YAML (`name:`). Backup (moved out of the `~/.agents` git
   repo): `~/.agents-backups/skills-frontmatter-backup-20260926.tar.gz`.
   Post-fix: **133/134 dirs pass the documented loader rules** (the 1
   non-skill dir is `_shared`). Known remaining exception: `skill-creator`
   has a quoted `"metadata":` key (skipped line — harmless; plugin twin
   works).

## Cross-check reconciliation (T1 ZCode session ↔ operator's ALDC audit session, 2026-09-26)

Both investigations agree on the phenomenon (backbone claudekit skills on
disk, absent from ZCode registry) and on "no data loss". They diverged on
root-cause depth — reconciled as follows:

1. **No contradiction between "validators 0 errors" (their side) and "loader
   drops the files" (ZCode side)**: their `layer-manifest.json` records
   `cook → name: "ck:cook"` — their tooling parses BOTH frontmatter dialects
   (lenient); the ZCode loader parses flat `key: value` YAML only (strict,
   per `zcode-guide:diagnosing-skills` two-tier model). Quoted keys parse on
   one side, drop on the other.
2. **Manifest is now stale** (their "in-sync" was true at generation
   18:18:59+0700, before the 18:32 sed): 58 `skill_md_sha16` entries no
   longer match the edited files. **Action for this session or the audit
   session: regenerate `~/.agents/layer-manifest.json`** with their own
   generator after deciding the fate of the 58 edits.
3. **The `~/.agents` git repo carries exactly 58 modified SKILL.md files** —
   that is the frontmatter fix, intentional, nothing else dirty. Decide:
   commit the fix (recommended — aligns files with what ZCode loads) or
   restore from the backup tarball, then regenerate the manifest. Either way
   manifest and files must move together.
4. **Discriminating test at restart** (settles both theories in one shot):
   - Theory A (frontmatter filter): after the fix, registry from this layer
     jumps to ≈133 skills.
   - Theory B (platform cap/filter independent of frontmatter): registry
     stays ≈50 even with fixed frontmatter.
   If B wins: the frontmatter fix was necessary-but-not-sufficient — their
   remaining hypothesis (registry build mechanism) takes over.
5. **Workaround endorsed regardless of A/B** (audit session's point 5,
   consistent with ALDC's "absent skill" intent): a skill on disk but not in
   the registry is NOT an absent skill — read its `SKILL.md` and execute the
   contract inline, journaling `invoked-by-file`. This unblocks ALDC Stage 3
   for #393 even before any registry fix lands.
6. **Registry verification offered**: after the fix + restart, if the ZCode
   side should be re-verified against `layer-manifest.json`, the audit
   session asked to be notified.

## Exact next actions (new session, after restart)

1. Verify ALDC stage skills now invocable (`ck:cook` etc. in skill list).
2. Resume **ALDC at stage 3 (--from 3)** for #393 per
   `plans/260926-s1-session-state/plan.md`: worktree sandbox (≥5 files →
   `ck:worktree` mandatory), implement Phases 1–5 (R6 precondition first,
   separate small PR), dual-pass CI, Red Test Proof
   (`TestConcurrentSessionPromotion` FAIL→PASS), `ck:code-review` +
   `autoreview`, `code-simplification`, `ck:ship` → PR (include the three
   uncommitted docs from this session in the docs-touching PR, plus #397's
   gate extension per ADR-0022 §6).
3. Then #394 (S2) after #393 merges; #395 before #396; #379/#383 as
   independent fillers.

## Guardrails (do not relitigate)

- ADR-0021 SOM FSM + ADR-0022 session types are canonical.
- Ratified designs: flock-gated conditional isolation (in-place when lock
  acquired, auto-promote to pool worktree on contention); sessions registry
  (status+heartbeat) as zombie-vs-live truth; composite index
  `(session_id, status)` + partial index `is_active=1` ONLY (per-session
  partial index is impossible in SQLite); provenance stamp on every shared
  write; `--concurrency` default 1 with hard-error when unisolated; broker
  fail-open + `broker_failure` telemetry; LLM-judge scoring killed.
- DEBT-34 layer ownership; dual-pass CI; pre-push 12 gates; Zero garbage
  policy; Jev triage before every mutation.

## Source pointers

| Artifact | Path |
|----------|------|
| S1 plan (execute this) | plans/260926-s1-session-state/plan.md |
| T1 ledger (rationale) | plans/260926-strategy-t1-session/plan.md |
| ADR-0022 | docs/decisions/0022-strategic-session-protocol.md |
| Prior strategic handoff | plans/handoffs/strategic-handoff-20260926-1030.md |
| Skills fix backup | ~/.agents/skills-frontmatter-backup-20260926.tar.gz |
