#!/usr/bin/env bash
#
# ci_feature_map_sync.sh — Declarative Feature Map validation gate (Issue #649)
#
# Enforces that .g8s/feature-map.yml exists, satisfies schema 1.0, is acyclic,
# has zero orphaned internal/ packages, and aligns with docs/SRS.md and manifest.json.
#
# Usage:
#   tools/ci_feature_map_sync.sh [--file <path>] [--repo <dir>]

set -euo pipefail

FILE_PATH=".g8s/feature-map.yml"
REPO_DIR="."

while [ $# -gt 0 ]; do
    case "$1" in
        --file) FILE_PATH="$2"; shift 2 ;;
        --repo) REPO_DIR="$2"; shift 2 ;;
        -h|--help)
            echo "Usage: tools/ci_feature_map_sync.sh [--file <path>] [--repo <dir>]"
            exit 0
            ;;
        *)
            if [ -f "$1" ]; then
                FILE_PATH="$1"
                shift
            else
                echo "Unknown option: $1" >&2
                exit 1
            fi
            ;;
    esac
done

if [ -x "./bin/g8s" ] && ./bin/g8s feature help >/dev/null 2>&1; then
    G8S_BIN=("./bin/g8s")
else
    G8S_BIN=("go" "run" "./cmd/g8s")
fi

echo "==> Verifying feature map at $FILE_PATH against repo $REPO_DIR..."
if ! "${G8S_BIN[@]}" feature verify --file "$FILE_PATH" --repo "$REPO_DIR" --json; then
    echo "==> Feature map verification failed!" >&2
    exit 1
fi

echo "==> Feature map verification passed successfully."
exit 0
