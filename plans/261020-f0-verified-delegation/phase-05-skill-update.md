# Phase 05: Supervisor skill update (#631)

## Files
`skills/g8s-supervisor/SKILL.md`, `skills/g8s-supervisor/references/hard-boundary.md`, `skills/g8s-supervisor/references/operational-hard-lessons.md`. Then sync to `~/.agents/skills/g8s-supervisor` (F0 runs this sync after merge).

## Changes
- `hard-boundary.md`: replace the "Remote issue/PR publication, push, merge … PiC reviews first" row with the T0–T4 tier table from ADR-0033, and add the rule "commitment points require `f0 gate check` PASS; exploration is free".
- `SKILL.md`: add an "F0 evidence toolkit" section listing `f0 run`, `f0 status`, `f0 gate check`, `f0 waive`, `f0 manual`, `f0 mandate`, and state the axiom "freedom on the road, toll at the booth".
- `operational-hard-lessons.md`: add the 2026-10-18 lessons, each citing its evidence (corrections ledger IDs C1–C10, run IDs, PRs):
  1. Never state CI status without a run ID (C10).
  2. Never merge with checks pending or failing; a flake needs a waiver with an issue link (C1, C2).
  3. Never satisfy a guard by rewriting its matched tokens or inventing keys (C3, C4).
  4. Verify every F1 claim before accepting it (C5, C8).
  5. Compare delivered files with the final diff before squash (C6).
  6. `Closes #N` only when every acceptance criterion is evidenced (C7).
  7. Never `kill` a `g8s worker --concurrency` drain; let it drain or run `g8s cleanup` (#617).
  8. An instant verifier result means "check the item count" (vacuous pass).

## Success criteria
Link integrity and the doc-contract gate pass. The skill text matches ADR-0033.
