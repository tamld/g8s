# Phase 4 — CI Wiring, Docs & PR to Remote

## Context Links
- `.github/workflows/quality.yml`, `version-sync.yml`, `hygiene-guard.yml` (existing gate patterns)
- `docs/CHANGELOG.md`, `docs/RELEASE_SOP.md`, `AGENTS.md` (§ reading order)

## Overview
- **Priority**: P1 | **Status**: Pending
- Make the guard a hard CI gate, finish the docs, and open the PR for main-agent review/merge.

## Key Insights
- 11 workflows already exist; a speccheck step inside `quality.yml` (or a sibling `spec-registry.yml`) follows the established gate pattern.
- The repo is in **detached HEAD** today — create `feat/speckit-integration` from `origin/main` before any commit. Untracked `cmd/jevtriage/` is pre-existing work-in-progress: **leave untouched, do not commit**.

## Requirements
- CI: run `go run ./cmd/speccheck` as a step in `quality.yml` (fail fast, before the test matrix).
- Docs: CHANGELOG entry (Added/Changed/Fixed); one line added to AGENTS.md's reading order pointing net-new features at the `/speckit.*` chain; optional `uvx` install note.

## Related Code Files
- **Modify**: `.github/workflows/quality.yml`, `docs/CHANGELOG.md`, `AGENTS.md` (one line)
- **Create**: PR description (motivation, drift fixed, routing rule, dogfood evidence)
- **Delete**: none

## Implementation Steps
1. Add the speccheck step to `quality.yml` after lint, before tests.
2. Verify locally: full dual-pass (`CGO_ENABLED=0 go vet ./... && go test -count=1 ./...`; `CGO_ENABLED=1 go test -race -count=1 ./...`) + `go run ./cmd/speccheck` green.
3. `git switch -c feat/speckit-integration origin/main`; conventional commits (`feat(sdd): scaffold spec-kit workflow engine`, `fix(sdd): repair openspec registry drift`, `feat(sdd): add cmd/speccheck sync guard`, `ci: enforce spec registry sync`).
4. Push + `gh pr create` with the summary above; hand to main agent for review/merge.
5. Post-merge: update plan status → completed; `/ck:plan archive` + journal.

## Todo List
- [ ] CI gate added and required
- [ ] dual-pass + speccheck green locally
- [ ] branch + conventional commits + PR created
- [ ] post-merge: plan archived, journal written

## Success Criteria
- PR open with required check green; diff scoped to docs/tooling/speccheck/CI; main agent can review without cross-referencing untracked WIP.

## Risk Assessment
| Risk | Mitigation |
|------|-----------|
| Branch protection forbids adding required checks | Ship as non-blocking check first; promote to required in a follow-up |
| Merge conflicts with concurrent PRs | Rebase onto `origin/main` immediately before push |

## Security Considerations
- PR is outward-facing: CI green before push; `.specify/` scripts reviewed in Phase 1; no secrets or local paths in committed files.

## Next Steps
- Post-merge: `/ck:plan archive` + `/ck:journal`; future features start with `/speckit.specify`.
