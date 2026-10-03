# Task: Debt D2 — CI lane follow-ups: Test-matrix guard + main-push detection (ADR-0031 tail)

Repo: g8s @ main. Two edge cases the S-10 probes recorded, one slice.

## Delivery protocol

Scratch worktree. Write ONLY to:

- .github/workflows/ (the workflow file(s) that own the
  `Test macos-latest` / `Test windows-latest` jobs — find them; do
  NOT rename any job)
- tools/ci_lane_detect.sh (main-push detection support)
- tools/ci_lane_detect_test.sh (new fixtures for both fixes)

Do NOT run git commit. Do NOT touch other workflows.

## Fix 1 — Test-matrix lane guard

The docs-lane probe (PR #508) showed `Test macos-latest` /
`Test windows-latest` still ran go test on a docs-only diff (ubuntu's
Quality was guarded; the test-matrix workflow was missed). Apply the
same pattern as quality.yml's Detect CI Lane step: detect once, set
CI_LANE, guard the go-test/race steps with
`if: env.CI_LANE != 'docs'` so docs PRs skip the matrix early while
job names stay identical (branch protection). Verify the workflow
file YAML-parses.

## Fix 2 — main-push detection

On a direct main push, `git diff origin/main...HEAD` is EMPTY
(origin/main == HEAD post-push) → deny-by-default resolves BUILD even
for docs commits — the S-10 recorded edge. Extend
tools/ci_lane_detect.sh: accept an optional second arg (a base SHA).
When running in GitHub Actions on push events, the caller passes
`${{ github.event.before }}` (when it is a real SHA — 0000000-ish
values mean "new branch", fall back to current behavior). Locally
(pre-push), behavior is unchanged. Update the test fixtures: docs-only
commit between before-SHA and HEAD → docs; empty before-range → build
(deny-by-default preserved).

## Constraints

- bash 3.2 compatible (the #466 idiom); detector exits 0 always.
- Job/step names that branch protection or the Verify aggregate
  references stay EXACT.
- New/changed YAML must parse (python3 yaml.safe_load check in your
  report).
- Report: the workflow files + step names guarded, the detect
  fixtures matrix, and one honest risk you see in the approach.
