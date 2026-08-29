#!/usr/bin/env bash
# tools/reap-ghost-agy.sh
# One-shot sweeper: prune orphan worktree refs, delete orphan dirs,
# and reclaim ghost processes and stale receipts.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

echo "==> Pruning stale git worktree administrative refs..."
git worktree prune --expire=now

echo "==> Cleaning orphan worktree temporary directories..."
for base in "${TMPDIR:-/tmp}/g8s-worktrees" "/tmp/g8s-worktrees"; do
    if [ -d "${base}" ]; then
        rm -rf "${base}"
    fi
done

echo "OK: Worktree refs and directories pruned."
echo "Run 'go run ./cmd/g8s cleanup --force' to reclaim ghost processes and stale receipts."
