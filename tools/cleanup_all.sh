#!/usr/bin/env bash
# tools/cleanup_all.sh - session-end cleanup for g8s work
# Idempotent. Reports total disk freed.

set -euo pipefail

DRY_RUN=0
if [ "${1:-}" = "--dry-run" ]; then
    DRY_RUN=1
elif [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    echo "Usage: $0 [--dry-run]"
    exit 0
fi

run() {
    if [ "$DRY_RUN" -eq 1 ]; then
        echo "  [dry-run] $*"
    else
        "$@"
    fi
}

echo "=== g8s session cleanup ==="

# Record available disk space before cleanup (KB)
BEFORE_KB=$(df -k / 2>/dev/null | awk 'NR>1 {print $(NF-2); exit}' || echo 0)

echo "1. Go build cache"
if command -v go >/dev/null 2>&1; then
    run go clean -cache 2>&1 | head -2 || true
    run go clean -modcache 2>&1 | head -2 || true
fi

echo "2. Worktree refs (expire=now)"
run git worktree prune --expire=now 2>&1 | head -3 || true

echo "3. Merged branches"
ACTIVE_WT_BRANCHES=$(git worktree list --porcelain 2>/dev/null | awk '/^branch / {sub("refs/heads/", "", $2); print $2}' || true)
for br in $(git branch --merged main --format='%(refname:short)' 2>/dev/null || true); do
    if [ -n "$br" ] && [ "$br" != "main" ] && [ "$br" != "master" ]; then
        # Skip branches checked out in active worktrees
        is_wt_branch=0
        for wt_br in $ACTIVE_WT_BRANCHES; do
            if [ "$br" = "$wt_br" ]; then
                is_wt_branch=1
                break
            fi
        done
        if [ "$is_wt_branch" -eq 0 ]; then
            run git branch -d "$br" 2>&1 | head -1 || true
        fi
    fi
done

echo "4. /tmp g8s dirs (90 MB)"
shopt -s nullglob
ACTIVE_WTS=$(git worktree list --porcelain 2>/dev/null | awk '/^worktree / {print substr($0, 10)}' || true)
for d in /tmp/g8s* /private/tmp/g8s*; do
    if [ -d "$d" ] && [ "$d" != "/tmp/g8s.worktrees" ]; then
        is_active=0
        for wt in $ACTIVE_WTS; do
            if [ "$d" = "$wt" ] || [ "/private$d" = "$wt" ] || [ "$d" = "/private$wt" ]; then
                is_active=1
                break
            fi
        done
        if [ "$is_active" -eq 0 ]; then
            run rm -rf "$d"
        fi
    fi
done
if [ -d "/private/var/folders" ]; then
    for d in /private/var/folders/*/*/*/g8s-blind-*; do
        if [ -d "$d" ]; then
            run rm -rf "$d"
        fi
    done
fi
shopt -u nullglob

echo "5. Package manager caches (in scope of g8s)"
if [ -d "$HOME/.bun/install/cache" ]; then
    run rm -rf "$HOME/.bun/install/cache"
fi
if command -v npm >/dev/null 2>&1; then
    run npm cache clean --force 2>&1 | head -2 || true
fi
if command -v pnpm >/dev/null 2>&1; then
    run pnpm store prune 2>&1 | head -3 || true
fi

echo "6. Report"
echo "Active worktrees: $(git worktree list 2>&1 | wc -l | tr -d ' ')"
echo "Local branches: $(git branch -l 2>&1 | wc -l | tr -d ' ')"
df -h / 2>&1 | head -2 || true

AFTER_KB=$(df -k / 2>/dev/null | awk 'NR>1 {print $(NF-2); exit}' || echo 0)

if [ "$DRY_RUN" -eq 1 ]; then
    echo "=== Done ==="
    echo "Cleaned"
else
    echo "=== Done ==="
    if [ -n "$BEFORE_KB" ] && [ -n "$AFTER_KB" ] && [ "$BEFORE_KB" -gt 0 ] && [ "$AFTER_KB" -gt "$BEFORE_KB" ]; then
        DIFF_KB=$((AFTER_KB - BEFORE_KB))
        if [ "$DIFF_KB" -ge 1048576 ]; then
            FREED_GB=$(awk -v kb="$DIFF_KB" 'BEGIN { printf "%.2f", kb / 1048576 }')
            echo "Freed ${FREED_GB} GB"
        elif [ "$DIFF_KB" -ge 1024 ]; then
            FREED_MB=$(awk -v kb="$DIFF_KB" 'BEGIN { printf "%.1f", kb / 1024 }')
            echo "Freed ${FREED_MB} MB"
        else
            echo "Freed ${DIFF_KB} KB"
        fi
    else
        echo "Cleaned"
    fi
fi
