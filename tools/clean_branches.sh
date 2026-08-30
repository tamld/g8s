#!/usr/bin/env bash
set -euo pipefail

# tools/clean_branches.sh
# Safely bulk-deletes local orphan branches that are merged into main
# and not present on remote. Enforces `git branch -d` (never -D).

PATTERN="${1:-agy/*}"

REPO_DIR="$(git rev-parse --show-toplevel)"
cd "$REPO_DIR"

# 1. Fetch remote tracking branches with prune
git fetch --prune origin >/dev/null 2>&1 || true

# 2. Get list of remote branches
REMOTE_BRANCHES=$(git ls-remote --heads origin | awk '{print $2}' | sed 's#refs/heads/##')

# 3. Get active worktree branches to avoid deleting branches currently checked out
ACTIVE_WT_BRANCHES=$(git worktree list --porcelain | awk '/^branch / {sub("refs/heads/", "", $2); print $2}')

# 4. Find all local branches matching pattern
LOCAL_BRANCHES=()
while IFS= read -r b; do
  [ -z "$b" ] && continue
  LOCAL_BRANCHES+=("$b")
done < <(git branch --list "$PATTERN" --format='%(refname:short)' 2>/dev/null || true)

# Also check agy-sup-* if default agy/*
if [ "$PATTERN" = "agy/*" ]; then
  while IFS= read -r b; do
    [ -z "$b" ] && continue
    LOCAL_BRANCHES+=("$b")
  done < <(git branch --list 'agy-sup-*' --format='%(refname:short)' 2>/dev/null || true)
fi

if [ ${#LOCAL_BRANCHES[@]} -eq 0 ]; then
  echo "No local branches found matching '$PATTERN'."
  exit 0
fi

# 5. Filter out protected branches, active worktrees, and branches existing on remote
CANDIDATES=()
for b in "${LOCAL_BRANCHES[@]}"; do
  # Skip main/master
  if [ "$b" = "main" ] || [ "$b" = "master" ]; then
    continue
  fi
  # Skip active worktrees
  if echo "$ACTIVE_WT_BRANCHES" | grep -qx "$b"; then
    continue
  fi
  # Skip if present on remote
  if echo "$REMOTE_BRANCHES" | grep -qx "$b"; then
    continue
  fi
  CANDIDATES+=("$b")
done

if [ ${#CANDIDATES[@]} -eq 0 ]; then
  echo "No orphan branches to clean for '$PATTERN'."
  exit 0
fi

echo "Found ${#CANDIDATES[@]} orphan candidate branch(es) for cleanup."

# 6. Gather unique commit hashes of candidates to build a temporary merge base
# This allows `git branch -d` (safe-delete) to succeed even for squash-merged branch commits.
COMMITS=()
for b in "${CANDIDATES[@]}"; do
  commit=$(git rev-parse --verify "$b" 2>/dev/null || true)
  if [ -n "$commit" ]; then
    COMMITS+=("$commit")
  fi
done

UNIQUE_COMMITS=($(printf '%s\n' "${COMMITS[@]}" | sort -u))

# 7. Create temporary merge environment so `git branch -d` passes without -D
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
TMP_BRANCH="tmp-clean-base-$(date +%s%N 2>/dev/null || date +%s)"

# Create tmp branch from main
git checkout -q -b "$TMP_BRANCH" main

# Batch merge unique commits using -s ours so the working tree is unchanged
BATCH_SIZE=50
for ((i=0; i<${#UNIQUE_COMMITS[@]}; i+=BATCH_SIZE)); do
  BATCH=("${UNIQUE_COMMITS[@]:i:BATCH_SIZE}")
  git merge -q -s ours --no-ff -m "merge candidates ours" "${BATCH[@]}" >/dev/null 2>&1 || true
done

# 8. Delete candidate branches safely with `git branch -d`
DELETED_COUNT=0
for b in "${CANDIDATES[@]}"; do
  if git branch -d "$b" >/dev/null 2>&1; then
    DELETED_COUNT=$((DELETED_COUNT + 1))
  else
    echo "Warning: failed to safe-delete branch '$b' with -d" >&2
  fi
done

# 9. Return to previous branch and clean up tmp branch
git checkout -q "$CURRENT_BRANCH"
git branch -D "$TMP_BRANCH" >/dev/null 2>&1 || true

echo "Successfully deleted $DELETED_COUNT orphan branch(es)."
