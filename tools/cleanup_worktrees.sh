#!/usr/bin/env bash
set -euo pipefail

# tools/cleanup_worktrees.sh
# Prunes stale worktree registrations and identifies orphan worktree directories on disk.
# NOTE: This script NEVER executes 'rm -rf' directly; it prints owner commands for manual removal.

echo "==> Pruning git worktree metadata (expire=now)..."
git worktree prune --expire=now

# Collect all currently active worktree paths
ACTIVE_WTS=()
while IFS= read -r line; do
    if [[ "$line" =~ ^worktree[[:space:]]+(.*)$ ]]; then
        wt="${BASH_REMATCH[1]}"
        ACTIVE_WTS+=("$wt")
        if [ -d "$wt" ]; then
            canon=$(cd "$wt" 2>/dev/null && pwd -P 2>/dev/null || echo "")
            if [ -n "$canon" ]; then
                ACTIVE_WTS+=("$canon")
            fi
        fi
    fi
done < <(git worktree list --porcelain)

is_active_worktree() {
    local target="$1"
    local canon_target
    canon_target=$(cd "$target" 2>/dev/null && pwd -P 2>/dev/null || echo "$target")

    if [ ${#ACTIVE_WTS[@]} -gt 0 ]; then
        for active in "${ACTIVE_WTS[@]}"; do
            if [ "$target" = "$active" ] || [ "$canon_target" = "$active" ] || [ "/private$target" = "$active" ] || [ "$target" = "/private$active" ]; then
                return 0
            fi
        done
    fi
    return 1
}

# Scan potential locations for orphan g8s worktree directories
SEARCH_DIRS=()
if [ -d "/tmp" ]; then
    SEARCH_DIRS+=("/tmp")
fi
if [ -d "/private/tmp" ]; then
    SEARCH_DIRS+=("/private/tmp")
fi
if [ -n "${TMPDIR:-}" ] && [ -d "$TMPDIR" ]; then
    SEARCH_DIRS+=("$TMPDIR")
fi
if [ -d "/private/var/folders" ]; then
    while IFS= read -r d; do
        if [ -n "$d" ]; then
            SEARCH_DIRS+=("$d")
        fi
    done < <(find /private/var/folders -maxdepth 4 -type d -name "g8s-worktrees" 2>/dev/null || true)
fi

ORPHAN_DIRS=()
SEEN=()

if [ ${#SEARCH_DIRS[@]} -gt 0 ]; then
    for sdir in "${SEARCH_DIRS[@]}"; do
        [ -d "$sdir" ] || continue
        candidates=()
        if [[ "$sdir" == *"g8s-worktrees"* ]]; then
            while IFS= read -r c; do
                if [ -n "$c" ]; then
                    candidates+=("$c")
                fi
            done < <(find "$sdir" -mindepth 1 -maxdepth 1 -type d 2>/dev/null || true)
        else
            while IFS= read -r c; do
                if [ -n "$c" ]; then
                    candidates+=("$c")
                fi
            done < <(find "$sdir" -mindepth 1 -maxdepth 1 -type d -name "g8s-*" 2>/dev/null || true)
        fi

        if [ ${#candidates[@]} -gt 0 ]; then
            for cand in "${candidates[@]}"; do
                [ -d "$cand" ] || continue
                canon_cand=$(cd "$cand" 2>/dev/null && pwd -P 2>/dev/null || echo "$cand")
                
                already_seen=false
                if [ ${#SEEN[@]} -gt 0 ]; then
                    for s in "${SEEN[@]}"; do
                        if [ "$s" = "$cand" ] || [ "$s" = "$canon_cand" ]; then
                            already_seen=true
                            break
                        fi
                    done
                fi
                if [ "$already_seen" = true ]; then
                    continue
                fi
                SEEN+=("$cand" "$canon_cand")

                if ! is_active_worktree "$cand"; then
                    ORPHAN_DIRS+=("$cand")
                fi
            done
        fi
    done
fi

echo ""
if [ ${#ORPHAN_DIRS[@]} -eq 0 ]; then
    echo "No orphan worktree directories found on disk."
    exit 0
fi

echo "Found ${#ORPHAN_DIRS[@]} orphan worktree directory/directories on disk:"
for o in "${ORPHAN_DIRS[@]}"; do
    echo "  - $o"
done

echo ""
echo "To safely remove these orphan directories, run the following command as repository owner:"
echo "  rm -rf ${ORPHAN_DIRS[*]}"
echo ""
