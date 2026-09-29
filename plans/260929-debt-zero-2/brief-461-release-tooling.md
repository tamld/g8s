# Task: Fix #461 — release tooling gaps: manifest version drift guard, SOP Gate 6, claims CI wiring

Repo: g8s @ main. Tracking: tamld/g8s#461.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- manifest.json
- tools/ci_version_sync_check.sh
- tools/pre_push.sh
- .github/workflows/version-sync.yml
- .github/workflows/quality.yml
- docs/RELEASE_SOP.md

Do NOT run git commit. Do NOT modify any other file. Other workers are
operating in parallel on unrelated files (docs/ENTERPRISE_LEDGER*,
docs/decisions/, docs/designs/, internal/worker/, internal/harness/) —
touching anything outside the list above corrupts their delivery. In
particular: do NOT touch docs/RELEASE_SOP.md's neighbors in docs/, and do
not edit CHANGELOG.md or cmd/g8s/version.go (version bumps go through
tools/release.sh on main).

## Context (verified at main)

- cmd/g8s/version.go:17 `Version = "0.12.0"`; latest tag v0.12.0 (commit
  a255bd5, 2026-09-27); CHANGELOG [0.12.0] - 2026-09-27; packaging in
  sync. manifest.json still says version 0.10.0 with latest_release
  pointing at v0.10.0/60905b4/2026-09-20 — and NOTHING guards it
  (release.sh never reads manifest.json; version-sync.yml never compares
  it).
- docs/RELEASE_SOP.md Gate 6 (line ~16) says bump the version in
  cmd/g8s/main.go — the constant lives in cmd/g8s/version.go.
- tools/ci_version_sync_check.sh (strict: version.go vs latest git tag)
  exists but NO workflow invokes it; .github/workflows/version-sync.yml
  does its own inline comparisons (version.go vs CHANGELOG header,
  packaging literals, go directive).
- tools/claims_check.sh (docs/claims.yml scorecard, exit 1 on broken
  claims) is wired into nothing.

## Required implementation

1. **manifest.json**: set `version` to `0.12.0` and `latest_release` to
   `{ "tag": "v0.12.0", "commit": "a255bd5", "date": "2026-09-27" }`
   (verify a255bd5 and the date with git before writing: `git log -1
   v0.12.0`). Change nothing else in the file.

2. **Guard manifest drift**: extend tools/ci_version_sync_check.sh with a
   manifest check — manifest.json `version` must equal the version in
   cmd/g8s/version.go, and manifest.json `latest_release.tag` must equal
   the latest git tag (v-prefixed). Failure messages name the mismatching
   field. Then wire the script into .github/workflows/version-sync.yml as
   an additional step (keep the existing inline checks; the script runs
   after them). The script must still exit 0 on a freshly synced tree.

3. **RELEASE_SOP Gate 6**: fix the file reference from cmd/g8s/main.go to
   cmd/g8s/version.go. While there, add one bullet to the 6-Gate Audit
   list: manifest.json version + latest_release are synced with
   version.go and the latest tag (enforced by the version-sync guard).

4. **claims_check CI wiring**:
   - tools/pre_push.sh: add a step running `bash tools/claims_check.sh`
     (after the AI-lint step is fine); it must fail the pre-push on
     nonzero exit.
   - .github/workflows/quality.yml: add a step running the same script
     on ubuntu-latest as part of the existing Quality job (follow the
     step style of neighboring steps; no new job needed).

## Tests / verification

- `bash tools/ci_version_sync_check.sh` exits 0 after your manifest fix.
- `bash tools/claims_check.sh` exits 0 (it already does on main; the
  wiring must not change its output contract — see its self-test).
- `bash tools/claims_check_test.sh` still passes.
- Shell scripts pass `bash -n` syntax check.
- YAML files: validate syntax (e.g. with python3 -c yaml or careful
  manual check matching the file's existing indentation style).

## Constraints

- Do not bump versions anywhere (this is a sync/guard fix, not a
  release). Do not touch release.sh.
- Keep workflow changes minimal — additive steps only.
