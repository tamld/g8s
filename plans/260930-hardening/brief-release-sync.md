# Task: release.sh must sync manifest.json and packaging templates (close the #461 gap family)

Repo: g8s @ main. Tracking: #461 follow-up (the gap survived the #471 fix:
the guard now CATCHES the drift, but release.sh still CREATES it — v0.13.0
proved it live: the release push was blocked twice by version-sync until
manifest + packaging were synced by hand).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- tools/release.sh
- tools/release_test.sh (new file — follow tools/claims_check_test.sh style)

Do NOT run git commit. Do NOT modify any other file. Another worker owns
internal/ — touching anything outside the list corrupts their delivery.

## Context (verified at main)

`tools/release.sh` bumps `cmd/g8s/version.go` and inserts the CHANGELOG
section, but does NOT touch:
- `manifest.json` → `version`, `latest_release.tag` (the guard added by
  #471 then BLOCKS the release push until someone edits it by hand —
  happened live during v0.13.0);
- `packaging/windows/g8s.nsi`, `packaging/windows/g8s.wxs`,
  `packaging/chocolatey/tools/chocolateyinstall.ps1` (the version-sync
  packaging-drift check then fails — also happened live during v0.13.0).

Note the `latest_release.commit` field is intentionally NOT synced by the
script (self-reference circularity: the release commit sha is only final
after the commit exists; the guard does not check it). Document that in a
comment.

## Required implementation

1. In release.sh, after the version.go bump and CHANGELOG handling:
   - Update `manifest.json`: `"version": "X.Y.Z"` and
     `latest_release.tag: "vX.Y.Z"` (only these two fields; commit/date
     left alone).
   - Update `packaging/windows/g8s.nsi`, `packaging/windows/g8s.wxs`:
     their version literal → X.Y.Z.
   - Update `packaging/chocolatey/tools/chocolateyinstall.ps1`: both
     `vX.Y.Z` occurrences in the download URL → vX.Y.Z.
   - All edits idempotent (already-correct files pass through unchanged).
   - Stage every modified file together with version.go + CHANGELOG.md in
     the release commit (extend the existing `git add` list).
2. Keep `--dry-run` behavior: print what would change, modify nothing.
3. The manifest version-sync guard and the packaging-drift check must both
   PASS on the tree release.sh produces (that is the acceptance — run
   `bash tools/ci_version_sync_check.sh` after a dry-run on a scratch copy
   if possible, or assert the sed replacements in the test).

## Tests — release_test.sh (red first)

Bash test in the claims_check_test.sh style, hermetic (temp clone/copy of
the minimal file set — version.go, CHANGELOG.md, manifest.json, the three
packaging files — NOT a full repo clone):

1. Dry-run on files pinned at 0.12.0 → prints intended changes, files
   byte-identical afterwards.
2. Real run bumping 0.12.0 → 0.13.0 → version.go, manifest.json
   (version + latest_release.tag), nsi, wxs, chocolatey URL all read
   0.13.0/v0.13.0; `latest_release.commit` UNCHANGED.
3. Idempotency: running again on already-0.13.0 files changes nothing and
   still exits 0.
4. Missing packaging files (fixture omits one) → script warns but does not
   abort the version bump (packaging is best-effort with a loud warning),
   exit still 0.

## Constraints

- Pure bash stdlib; match the script's existing style (colors, checks).
- Do not change the guard scripts (ci_version_sync_check.sh) — they are
  the acceptance oracle, not the thing under test.
- `bash -n tools/release.sh` clean; `bash tools/release_test.sh` green.
