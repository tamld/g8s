#!/usr/bin/env bash
set -euo pipefail

# tools/drop_worktree.sh
# Drops a git worktree safely.

usage() {
    echo "Usage: $0 <path> [--force]" >&2
    exit 1
}

TARGET_PATH=""
FORCE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --force|-f)
            FORCE=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 <path> [--force]"
            exit 0
            ;;
        *)
            if [ -z "$TARGET_PATH" ]; then
                TARGET_PATH="$1"
            else
                echo "Error: Unexpected argument '$1'." >&2
                usage
            fi
            shift
            ;;
    esac
done

if [ -z "$TARGET_PATH" ]; then
    echo "Error: Worktree path is required." >&2
    usage
fi

CMD_ARGS=("git" "worktree" "remove")
if [ "$FORCE" = true ]; then
    CMD_ARGS+=("--force")
fi
CMD_ARGS+=("$TARGET_PATH")

if ! "${CMD_ARGS[@]}" 2>/dev/null; then
    ERR_OUTPUT=$("${CMD_ARGS[@]}" 2>&1 || true)
    echo "Error: Failed to remove worktree at '$TARGET_PATH': $ERR_OUTPUT" >&2
    if [ "$FORCE" != true ]; then
        echo "Hint: If the worktree contains uncommitted or untracked changes, try '$0 $TARGET_PATH --force'." >&2
    else
        echo "Owner instruction: To force remove manually, execute:" >&2
        echo "  git worktree remove --force \"$TARGET_PATH\"" >&2
        echo "  rm -rf \"$TARGET_PATH\" && git worktree prune" >&2
    fi
    exit 1
fi

git worktree prune >/dev/null 2>&1 || true
exit 0
