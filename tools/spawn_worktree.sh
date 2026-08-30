#!/usr/bin/env bash
set -euo pipefail

# tools/spawn_worktree.sh
# Creates a git worktree with branch validation and predictable path defaults.

usage() {
    echo "Usage: $0 <branch-name> [base-ref=main] [--path <path>]" >&2
    exit 1
}

BRANCH=""
BASE_REF="main"
PATH_ARG=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --path)
            if [[ $# -lt 2 ]]; then
                echo "Error: --path requires a directory path argument." >&2
                exit 1
            fi
            PATH_ARG="$2"
            shift 2
            ;;
        --path=*)
            PATH_ARG="${1#*=}"
            shift
            ;;
        -h|--help)
            echo "Usage: $0 <branch-name> [base-ref=main] [--path <path>]"
            exit 0
            ;;
        *)
            if [ -z "$BRANCH" ]; then
                BRANCH="$1"
            elif [ "$BASE_REF" = "main" ]; then
                BASE_REF="$1"
            else
                echo "Error: Unexpected argument '$1'." >&2
                usage
            fi
            shift
            ;;
    esac
done

if [ -z "$BRANCH" ]; then
    echo "Error: Branch name is required." >&2
    usage
fi

# Validate branch name: ^[a-z0-9][a-z0-9-]{0,62}$
BRANCH_REGEX="^[a-z0-9][a-z0-9-]{0,62}$"
if [[ ! "$BRANCH" =~ $BRANCH_REGEX ]]; then
    echo "Error: Branch name '$BRANCH' is invalid. Must match regex: '^[a-z0-9][a-z0-9-]{0,62}$' (lowercase alphanumeric with hyphens, max 63 chars)." >&2
    exit 1
fi

# Default path if not specified
if [ -z "$PATH_ARG" ]; then
    PATH_ARG="/tmp/g8s-${BRANCH}"
fi

# Resolve base-ref
if ! git rev-parse --verify --quiet "$BASE_REF" >/dev/null 2>&1; then
    if git rev-parse --verify --quiet "origin/$BASE_REF" >/dev/null 2>&1; then
        BASE_REF="origin/$BASE_REF"
    else
        echo "Error: Base ref '$BASE_REF' does not exist." >&2
        exit 1
    fi
fi

# Check if worktree path is already registered or directory exists
EXISTING_WTS=$(git worktree list --porcelain | awk '/^worktree / {print substr($0, 10)}')
CANONICAL_TARGET=$(mkdir -p "$PATH_ARG" 2>/dev/null && cd "$PATH_ARG" && pwd -P 2>/dev/null || echo "$PATH_ARG")
rmdir "$PATH_ARG" 2>/dev/null || true

for wt in $EXISTING_WTS; do
    CANONICAL_WT=$(cd "$wt" 2>/dev/null && pwd -P 2>/dev/null || echo "$wt")
    if [ "$wt" = "$PATH_ARG" ] || [ "$CANONICAL_WT" = "$CANONICAL_TARGET" ] || [ "$wt" = "/private$PATH_ARG" ] || [ "/private$wt" = "$PATH_ARG" ]; then
        echo "Error: Worktree path '$PATH_ARG' is already in use by an active worktree." >&2
        echo "Hint: Use 'tools/drop_worktree.sh $PATH_ARG' to remove it or 'tools/cleanup_worktrees.sh' to prune." >&2
        exit 1
    fi
done

if [ -e "$PATH_ARG" ]; then
    echo "Error: Path '$PATH_ARG' already exists on disk." >&2
    echo "Hint: If this is an abandoned worktree, run 'tools/drop_worktree.sh $PATH_ARG' or 'tools/cleanup_worktrees.sh'." >&2
    exit 1
fi

# Create the worktree
if git rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null 2>&1; then
    # Branch already exists locally
    if ! git worktree add "$PATH_ARG" "$BRANCH" >/dev/null 2>&1; then
        echo "Error: Failed to add worktree for existing branch '$BRANCH' at '$PATH_ARG'." >&2
        echo "Hint: If worktree registration is stale, run 'tools/cleanup_worktrees.sh' or 'git worktree prune'." >&2
        exit 1
    fi
else
    # Create new branch from base ref
    if ! git worktree add -b "$BRANCH" "$PATH_ARG" "$BASE_REF" >/dev/null 2>&1; then
        echo "Error: Failed to create worktree '$PATH_ARG' on new branch '$BRANCH' from '$BASE_REF'." >&2
        echo "Hint: If worktree registration is stale, run 'tools/cleanup_worktrees.sh' or 'git worktree prune'." >&2
        exit 1
    fi
fi

# Output path to stdout on success
echo "$PATH_ARG"
exit 0
