#!/usr/bin/env bash
# tools/cleanup_blind_worktrees.sh
# Removes blind/* worktrees (DEBT-48 dual-blind design leftovers)
# Each blind worktree has 2 wt-N dirs + 1 branch ref.
# Strategy: for each g8s-blind-* dir, prune its refs, rm its wt-N dirs.

set -euo pipefail

WT_BASE="/private/var/folders/vj/2xz4y7td4pv95q4jn4zfpkyr0000gn/T"
WT_PARENT="/Users/tamld/Documents/github/g8s"

# List all g8s-blind-* directories
shopt -s nullglob
BLIND_DIRS=("$WT_BASE"/g8s-blind-*)
shopt -u nullglob

if [ ${#BLIND_DIRS[@]} -eq 0 ]; then
    echo "No blind worktree dirs found."
    exit 0
fi

echo "Found ${#BLIND_DIRS[@]} blind worktree directories to clean."

# For each blind worktree, also delete the related branches
for d in "${BLIND_DIRS[@]}"; do
    name=$(basename "$d")
    # Extract the hash suffix
    # name pattern: g8s-blind-<uuid>
    echo "Cleaning: $name"
    # Delete related branches (the wt-N branches)
    for branch in $(git -C "$WT_PARENT" branch --list 'blind/*' 2>/dev/null); do
        git -C "$WT_PARENT" branch -D "$branch" 2>&1 | head -1 || true
    done
done

# Prune to clear refs (now branches are gone)
git -C "$WT_PARENT" worktree prune --expire=now 2>&1 | head -3

# Now remove the actual directories (these are inside the var/folders
# TempDir which is in cwd of the script context)
for d in "${BLIND_DIRS[@]}"; do
    if [ -d "$d" ]; then
        rm -rf "$d" && echo "Removed: $d" || echo "Failed: $d"
    fi
done

# Also remove /tmp/g8s* dirs
for d in /tmp/g8s*; do
    if [ -d "$d" ] && [ "$d" != "/tmp/g8s.worktrees" ]; then
        rm -rf "$d" && echo "Removed: $d" || echo "Failed: $d"
    fi
done

# Final prune
git -C "$WT_PARENT" worktree prune --expire=now 2>&1 | head -3

echo "=== Final state ==="
git -C "$WT_PARENT" worktree list 2>&1 | wc -l
git -C "$WT_PARENT" branch -l 2>&1 | wc -l
du -sh "$WT_BASE"/g8s* 2>/dev/null | head -5 || echo "No more g8s dirs in $WT_BASE"
