# Plan: Release Pipeline Hardening & v0.9.2 Release Recovery

**Plan ID:** release-pipeline-hardening
**Created:** 2026-09-18
**Status:** Draft → Active
**Target:** g8s v0.9.2 release + pipeline hardening

---

## Problem Statement

### Current State
- Local code: v0.9.2 (commit a5b9830)
- Remote tag: v0.9.2 exists but Release workflow **failed** (Go 1.26 runner issue, coverage extraction, Windows flakiness)
- GitHub Release: **v0.3.0 marked "Latest"** because v0.9.2 release never completed
- 3 draft releases pollute history: v0.2.0, v0.3.0, v0.7.0
- ~538 agy/sup-* orphan branches (130 remote, 408 local) from supervisor sub-tasks

### Root Causes (Anti-Patterns Identified)
1. **Push-before-verify**: Tags pushed without pre-flight validation
2. **No pre-tag gate**: No verification that CI passes before tagging
3. **Silent draft accumulation**: Failed releases leave draft artifacts
4. **Single-point failure**: One workflow failure breaks entire release
5. **No rollback on failure**: Failed releases not auto-cleaned
6. **Version drift**: Code version (0.9.2) ≠ GitHub Release "Latest" (0.3.0)

---

## ADLC Compliance Requirements

### FSM States (ADLC)
```
RELEASE_PIPELINE:
  DRAFT → PRE_TAG_VALIDATION → TAG_CREATED → CI_VERIFICATION → RELEASE_CREATED → LATEST_PUBLISHED
                    ↓               ↓              ↓                  ↓
               FAILED          FAILED        FAILED              FAILED
```

### Red Test Proof (Mandatory)
Each gate MUST have executable verification:
- `pre_tag.sh` returns exit code 0 before tag push
- CI workflow passes on tag push
- Release workflow completes successfully
- GitHub Release "Latest" points to correct version

---

## Phase 1: Pre-Tag Validation Gate (Blocking)

### Task 1.1: Create `tools/pre_tag.sh`
**Goal:** Comprehensive validation before any tag push
**Inputs:** Version from `cmd/g8s/version.go`, CHANGELOG.md, git state
**Outputs:** Exit 0 (ready) or 1 (blocked with reasons)

**Validation Checks:**
1. Version sync: `cmd/g8s/version.go` matches CHANGELOG [Unreleased] header
2. CHANGELOG has content under [Unreleased] (not empty)
3. All CI workflows pass on current HEAD (CI, Quality, Crash-Survival, Dist-Validation)
4. No uncommitted changes
5. Current branch is main
6. Tag doesn't already exist remotely
7. Go version in workflows matches `go.mod` requirement

**Deliverable:** `tools/pre_tag.sh` executable, integrated into Makefile `make tag`

### Task 1.2: Add Makefile target
```makefile
tag: pre-tag
	git tag v$(VERSION) && git push origin v$(VERSION)

pre-tag:
	./tools/pre_tag.sh
```

---

## Phase 2: Workflow Hardening (Already Partially Done)

### Completed (Verified)
- ✅ All 8 workflows: `go-version: '1.26.0'` → `'1.26'`
- ✅ Quality gate: coverage extraction uses temp file + `-count=1` + PIPESTATUS
- ✅ Coverage threshold: 83.51% (32 packages, cmd/g8s excluded) > 80%

### Task 2.1: Add Release workflow pre-checks
**File:** `.github/workflows/release.yml`
**Add at start of job:**
```yaml
- name: Pre-release validation
  run: |
    ./tools/pre_tag.sh || exit 1
```

### Task 2.2: Add Release workflow failure cleanup
**On failure:** Auto-delete draft release via GitHub API

---

## Phase 3: Draft Cleanup Automation

### Task 3.1: Create `tools/cleanup_drafts.sh`
**Goal:** Remove draft releases older than 7 days
**Trigger:** Cron (weekly) + on Release workflow failure
**Logic:** `gh release list --json name,isDraft,createdAt | filter draft & age > 7d | gh release delete`

### Task 3.2: Add to Release workflow
```yaml
- name: Cleanup failed draft on failure
  if: failure()
  run: ./tools/cleanup_drafts.sh --failed-tag=${{ github.ref_name }}
```

---

## Phase 4: Version Sync Enforcement

### Task 4.1: Add CI check for version consistency
**File:** `.github/workflows/quality.yml` (new step)
```yaml
- name: Version consistency check
  run: |
    VERSION_CODE=$(grep 'Version =' cmd/g8s/version.go | cut -d'"' -f2)
    VERSION_CHANGELOG=$(grep -A1 '## \[Unreleased\]' CHANGELOG.md | tail -1 | sed 's/## \[//;s/\].*//')
    if [ "$VERSION_CODE" != "$VERSION_CHANGELOG" ]; then
      echo "::error::Version mismatch: code=$VERSION_CODE changelog=$VERSION_CHANGELOG"
      exit 1
    fi
```

### Task 4.2: Add pre-push hook
**File:** `tools/pre_push.sh` (extend)
- Run version consistency check
- Run `pre_tag.sh` if commit message contains "release" or "chore: release"

---

## Phase 5: v0.9.2 Release Recovery

### Task 5.1: Re-trigger Release workflow
**Commands (manual - safety net blocks):**
```bash
git tag -d v0.9.2
git push origin :refs/tags/v0.9.2
git tag v0.9.2
git push origin v0.9.2
```

### Task 5.2: Verify Release completion
- Check Release workflow passes
- Verify GitHub Release shows v0.9.2 as "Latest"
- Verify artifacts uploaded (binaries, checksums, sbom)

---

## Phase 6: Orphan Branch Cleanup

### Task 6.1: Remote branches (130 merged)
```bash
for b in $(git branch -r | grep 'agy/sup-'); do
  git merge-base --is-ancestor $b main 2>/dev/null && \
  git push origin --delete ${b#origin/} && echo "Deleted $b"
done
```

### Task 6.2: Local branches (408 merged)
```bash
for b in $(git branch | grep 'agy/sup-'); do
  git merge-base --is-ancestor $b main 2>/dev/null && \
  git branch -d $b && echo "Deleted local $b"
done
```

### Task 6.3: Prune
```bash
git fetch -p
```

---

## Success Criteria (DoD)

| Gate | Verification Command | Expected |
|------|---------------------|----------|
| Pre-tag validation | `./tools/pre_tag.sh` | Exit 0 |
| CI on tag | `gh run list --workflow=CI --limit=1` | Success |
| Release workflow | `gh run list --workflow=Release --limit=1` | Success |
| GitHub Release | `gh release view v0.9.2 --json isLatest` | `isLatest: true` |
| No drafts | `gh release list --json isDraft | jq '.[] | select(.isDraft)'` | Empty |
| Orphan branches | `git branch -r | grep agy/sup- | wc -l` | 0 |

---

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Windows CI flaky | High | Medium | Add retry logic, investigate runner |
| Safety net blocks tag delete | Certain | High | Manual execution required |
| Draft cleanup deletes wrong release | Low | High | Filter by age + tag name match |
| Version drift recurs | Medium | Medium | CI gate + pre-push hook |

---

## Dependencies

```
Phase 1 (Pre-tag) → Phase 2 (Workflow) → Phase 5 (Release Recovery)
Phase 3 (Draft cleanup) → Phase 5 (Release Recovery)
Phase 4 (Version sync) → Phase 1 (Pre-tag)
Phase 6 (Orphan cleanup) → Independent, can parallel
```

---

## Execution Order

1. **Create plan** (this document) ✓
2. **Implement pre_tag.sh** (Phase 1)
3. **Integrate into Release workflow** (Phase 2)
4. **Create draft cleanup** (Phase 3)
5. **Add version sync CI check** (Phase 4)
6. **Manual: Re-trigger v0.9.2 release** (Phase 5)
7. **Clean orphan branches** (Phase 6)

---

## Notes

- All scripts must be POSIX sh compatible (run in GitHub Actions ubuntu-latest)
- Use `set -euo pipefail` in all scripts
- Log output to stdout for CI visibility
- No external dependencies beyond gh, git, go, standard unix tools