# Task: #476 wave B3 — repo hygiene + version-sync tooling polish

Repo: g8s @ main. Tracking: tamld/g8s#476 (P2 cleanup mismatch, P3 junk,
P3 version-sync noise/prerelease compare, P3 orchestrator test litter).

## Delivery protocol (read carefully)

Your current working directory is a scratch worktree. Write your changes
DIRECTLY to these receipt-scoped repository files (paths relative to
$G8S_REPO_ROOT):

- tools/ci_version_sync_check.sh
- tools/ci_version_sync_check_test.sh (new file — follow
  tools/claims_check_test.sh style)
- internal/orchestrator/mount_test.go (ONLY if it is the writer of
  internal/orchestrator/.heartbeat/ — trace it first; otherwise the test
  file that creates that path)
- .gitignore (add the in-tree test-state patterns)

NOTE: internal/cleanup/cleanup.go is OUT OF SCOPE for this task (another
worker owns it this wave) — the `--target scratch-branch` honesty fix is
deferred to wave C. Do not touch that file.

Do NOT run git commit. Do NOT modify any other file. Other workers own
cmd/g8s/, internal/doctor/, Makefile, internal/worker/, internal/settings/
in parallel.

## Required fixes

1. **Delete tracked junk at repo root** (git rm via the receipt — the
   files are tracked): `commit_message.txt`, `debug_jobs.py`,
   `parse_issues.py`, `delete_releases.sh`. Verify with `git ls-files`
   that each is actually tracked before removing; note anything already
   gone.
2. **ci_version_sync_check.sh polish**:
   - Delete the leftover `DEBUG: VERSION_GO=...` / `DEBUG: LATEST_TAG=...`
     / `DEBUG: CMP=...` echoes (they run on every pre-push).
   - Fix the prerelease tag comparison so `rc.10` > `rc.9` (numeric
     segment compare instead of alphabetical sort) — unit-testable via the
     new test file.
3. **Orchestrator test litter**: trace which test writes
   `internal/orchestrator/.heartbeat/agy` and `internal/orchestrator/.g8s`
   into the source tree (start at mount_test.go); redirect it to
   t.TempDir(). Add the in-tree patterns (`.heartbeat/`, `.g8s/`) to
   .gitignore as a backstop.

## Tests — red first

- ci_version_sync_check_test.sh: fixture-based cases for the DEBUG
  removal (no DEBUG lines in output), the 0.13.0-vs-0.13.0 match, and the
  prerelease ordering (rc.10 > rc.9 must not report drift).
- Manual verification in the report: `git ls-files | grep -E "commit_message|debug_jobs|parse_issues|delete_releases"` empty after the fix;
  `bash tools/ci_version_sync_check.sh` exits 0 with no DEBUG output.

## Constraints

- bash stdlib + Go test style as found; comments only for non-obvious
  constraints. `bash -n` on touched scripts; `go test ./internal/
  orchestrator/ ./internal/cleanup/` locally green.
