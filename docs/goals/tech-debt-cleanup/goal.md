# Goal: Technical Debt Cleanup - g8s

## Owner Intent
Clean up all accumulated technical debt in the g8s repository:
1. Remove 20 orphan worktrees with uncommitted changes
2. Fix release history: add missing v0.6.1, correct order (semver), make v0.9.2 "Latest"
3. Address 19 open issues (group into focused PRs)
4. Review/merge 1 open PR (#299 Bolt optimization)
5. Implement anti-pattern guards to prevent recurrence

## Authority & Proof
- **Authority**: Full repo owner (tamld)
- **Proof required**: 
  - `git worktree list` shows only main
  - `gh release list` shows 11 releases in semver order, v0.9.2 is Latest
  - Issues closed/PRs merged with verification
  - Anti-pattern guards in CI (version-sync, pre_tag.sh as required check)

## Diagnostic Ladder (WHAT→HOW→WHY)
- **WHAT**: 20 orphan worktrees, 11 releases out of order, 19 open issues, 1 draft PR
- **HOW**: Worktrees created by g8s dogfood/self-test; releases pushed out of order; issues accumulated without triage
- **WHY**: No pre-tag gate, no version sync CI, no draft cleanup automation, no worktree TTL, no issue triage process

## Likely Misfire
- Force-removing worktrees without checking for valuable changes
- Recreating releases in wrong order again
- Gomming unrelated issues into un-reviewable PRs
- Missing edge cases in anti-pattern guards

## Blind Spot
- Some worktrees might have uncommitted work worth saving
- Release recreation loses download counts/artifacts
- Some issues may be stale/duplicates

## Completion Criteria
- [ ] `git worktree list` = 1 entry (main only)
- [ ] `gh release list` = 11 releases, chronological semver order, v0.9.2 Latest
- [ ] All 19 issues: closed, or in focused PRs with verification
- [ ] PR #299 merged
- [ ] Anti-pattern guards enforced in CI (version-sync.yml required, pre_tag.sh as gate)

## Tranche Plan
1. **T1**: Worktree cleanup (manual --force required)
2. **T2**: Release recreation (v0.6.1, re-order v0.2.0/v0.3.0, v0.9.2 last)
3. **T3**: Issue triage & grouping into focused PRs
4. **T4**: PR #299 review & merge
5. **T5**: Anti-pattern guard enforcement (CI required checks)
6. **T6**: Final audit