# Task: #476 P2.6 — release.sh must run the pre-tag gates and refuse non-main cuts

Repo: g8s @ main. Tracking: tamld/g8s#476 (P2.6) + #485 stage-guard spirit.

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- tools/release.sh
- tools/release_test.sh (extend the existing suite)

Do NOT run git commit. Do NOT modify any other file.

## Context (verified at main)

tools/release.sh commits, tags, and pushes WITHOUT:
- invoking `tools/pre_tag.sh` (which owns the version-sync + claims checks
  — the release tag bypasses every pre-tag gate; remote release.yml only
  catches problems after the tag is public), and
- any branch guard: `git push origin main` is hardcoded, so running
  release.sh from a worktree/feature branch publishes a tag on an
  unmerged commit.

## Required implementation

1. **Branch guard** (before any mutation): refuse unless
   `git branch --show-current` == `main` AND the local main is not behind
   origin (`git rev-parse main origin/main` equal after a fetch — the
   fetch may fail offline; if it fails, warn and continue on the local
   check). Error message names the fix ("run from an up-to-date main").
2. **Pre-tag gates**: invoke `bash tools/pre_tag.sh` after the branch
   guard and BEFORE the version bump; abort on its non-zero exit. If
   pre_tag.sh is missing, warn loudly and continue (do not hard-fail
   environments that vendored release.sh without the full tools/ tree).
3. Extend `--dry-run` to print the would-run gates.

## Tests — extend release_test.sh (red first)

- Fixture branch ≠ main → release aborts with the branch-guard message,
  no version bump.
- Fixture main behind origin (simulate: a marker file/second fixture
  repo) → aborts with the up-to-date message.
- pre_tag.sh present and failing → aborts before version bump; missing →
  warns and proceeds.
- Existing suite stays green (idempotency, dry-run, packaging sync).

## Constraints

- Pure bash; match style; `bash -n` clean; full release_test.sh green
  locally.
