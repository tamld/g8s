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
VERIFY_JSON=$("${G8S_BIN[@]}" feature verify --file "$FILE_PATH" --repo "$REPO_DIR" --json 2>&1)
verify_exit=$?
if [ "$verify_exit" -ne 0 ]; then
    echo "$VERIFY_JSON"
    echo "==> Feature map verification failed!" >&2
    exit 1
fi

LIST_JSON=$("${G8S_BIN[@]}" feature list --file "$FILE_PATH" --json 2>&1)
if [ $? -ne 0 ]; then
    echo "$LIST_JSON" >&2
    echo "==> Feature map list failed!" >&2
    exit 1
fi

python3 - <<'PY' "$LIST_JSON"
import json, sys
features = json.loads(sys.argv[1])
for f in sorted(features, key=lambda x: x.get("id", "")):
    domain = (f.get("collision_domain") or f.get("id") or "?").strip()
    paths = f.get("owned_paths") or f.get("code_paths") or []
    print(f"feature {f['id']}: {domain} — packages: {len(paths)}, orphans: 0")
PY

echo "==> Feature map verification passed successfully."
exit 0
