#!/usr/bin/env bash
#
# ci_lane_router.sh — Layer-1 deterministic lane router (S6-3b, #420, ADR-0024 Layer 1)
#
# Evaluates changed files and lines to deterministically assign a PR gate lane:
#   (1) Any changed file matches a trust-boundaries glob -> Lane S (security)
#   (2) All files under docs/ or *.md only               -> Lane D (docs)
#   (3) Changed lines > 200                              -> Lane F (feature)
#   (4) Default fallback                                 -> Lane R (refactor)
#
# Usage:
#   ci_lane_router.sh --files <comma-separated-changed-files> --lines <changed-line-count> [--overrides <file>] [--trust-boundaries <file>]
#
# Output:
#   The lane letter (0, D, R, F, S) on stdout (single char + newline).
#   Exit 0 on success, exit 2 on invalid arguments.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FILES_ARG=""
HAS_FILES=0
LINES_ARG=""
HAS_LINES=0
OVERRIDES_FILE=""
TB_FILE=""

usage() {
    echo "Usage: ci_lane_router.sh --files <comma-separated-changed-files> --lines <changed-line-count> [--overrides <file>] [--trust-boundaries <file>]" >&2
}

while [ $# -gt 0 ]; do
    case "$1" in
        --files)
            if [ $# -lt 2 ]; then
                echo "error: --files requires an argument" >&2
                usage
                exit 2
            fi
            FILES_ARG="$2"
            HAS_FILES=1
            shift 2
            ;;
        --lines)
            if [ $# -lt 2 ]; then
                echo "error: --lines requires an argument" >&2
                usage
                exit 2
            fi
            LINES_ARG="$2"
            HAS_LINES=1
            shift 2
            ;;
        --overrides)
            if [ $# -lt 2 ]; then
                echo "error: --overrides requires an argument" >&2
                usage
                exit 2
            fi
            OVERRIDES_FILE="$2"
            shift 2
            ;;
        --trust-boundaries)
            if [ $# -lt 2 ]; then
                echo "error: --trust-boundaries requires an argument" >&2
                usage
                exit 2
            fi
            TB_FILE="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "error: unknown argument: $1" >&2
            usage
            exit 2
            ;;
    esac
done

# Validate required arguments
if [ "$HAS_FILES" -ne 1 ]; then
    echo "error: missing required argument --files" >&2
    usage
    exit 2
fi

if [ "$HAS_LINES" -ne 1 ]; then
    echo "error: missing required argument --lines" >&2
    usage
    exit 2
fi

# Validate lines is a non-negative integer
case "$LINES_ARG" in
    ''|*[!0-9]*)
        echo "error: --lines must be a non-negative integer, got: '$LINES_ARG'" >&2
        exit 2
        ;;
esac

# Validate overrides file if provided
if [ -n "$OVERRIDES_FILE" ]; then
    if [ ! -f "$OVERRIDES_FILE" ]; then
        echo "error: overrides file not found: $OVERRIDES_FILE" >&2
        exit 2
    fi
fi

# Locate trust-boundaries.yml if not explicitly provided
if [ -z "$TB_FILE" ]; then
    if [ -f ".g8s/trust-boundaries.yml" ]; then
        TB_FILE=".g8s/trust-boundaries.yml"
    elif git rev-parse --show-toplevel >/dev/null 2>&1 && [ -f "$(git rev-parse --show-toplevel)/.g8s/trust-boundaries.yml" ]; then
        TB_FILE="$(git rev-parse --show-toplevel)/.g8s/trust-boundaries.yml"
    elif [ -f "${SCRIPT_DIR}/../.g8s/trust-boundaries.yml" ]; then
        TB_FILE="${SCRIPT_DIR}/../.g8s/trust-boundaries.yml"
    fi
fi

# Function to parse trust_boundaries list from a YAML file
parse_trust_boundaries() {
    local yml="$1"
    [ -f "$yml" ] || return 0
    awk '
        BEGIN { in_tb = 0 }
        /^[[:space:]]*trust_boundaries:[[:space:]]*$/ { in_tb = 1; next }
        /^[[:space:]]*[a-zA-Z0-9_]+:[[:space:]]*/ && !/^[[:space:]]*trust_boundaries:/ { in_tb = 0 }
        in_tb && /^[[:space:]]*-[[:space:]]*/ {
            val = $0
            sub(/^[[:space:]]*-[[:space:]]*/, "", val)
            sub(/[[:space:]]*#.*$/, "", val)
            gsub(/^["'\''[:space:]]+|["'\''[:space:]]+$/, "", val)
            if (val != "") print val
        }
    ' "$yml"
}

# 0. Check explicit lane override
if [ -n "$OVERRIDES_FILE" ]; then
    override_lane=$(awk -F: '
        /^[[:space:]]*(lane|override_lane)[[:space:]]*:/ {
            val = $2
            sub(/[[:space:]]*#.*$/, "", val)
            gsub(/^["'\''[:space:]]+|["'\''[:space:]]+$/, "", val)
            if (val ~ /^[0DRFS]$/) print val
        }
    ' "$OVERRIDES_FILE" | head -n 1)

    if [ -z "$override_lane" ]; then
        first_char=$(sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' "$OVERRIDES_FILE" | grep -v '^#' | grep -v '^$' | head -n 1 || true)
        case "$first_char" in
            0|D|R|F|S) override_lane="$first_char" ;;
        esac
    fi

    if [ -n "$override_lane" ]; then
        echo "$override_lane"
        exit 0
    fi
fi

# Collect trust boundary patterns
TRUST_PATTERNS=()
if [ -n "$TB_FILE" ] && [ -f "$TB_FILE" ]; then
    while IFS= read -r pat; do
        [ -n "$pat" ] && TRUST_PATTERNS+=("$pat")
    done < <(parse_trust_boundaries "$TB_FILE")
fi

if [ -n "$OVERRIDES_FILE" ]; then
    while IFS= read -r pat; do
        [ -n "$pat" ] && TRUST_PATTERNS+=("$pat")
    done < <(parse_trust_boundaries "$OVERRIDES_FILE")
fi

# Parse changed files list
CHANGED_FILES=()
IFS=',' read -r -a raw_files <<< "$FILES_ARG"
for raw in "${raw_files[@]}"; do
    cleaned=$(printf '%s' "$raw" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
    [ -n "$cleaned" ] && CHANGED_FILES+=("$cleaned")
done

# Rule 1: Any changed file matches a trust-boundaries glob -> lane S
for file in "${CHANGED_FILES[@]}"; do
    norm_file="${file#./}"
    for pat in "${TRUST_PATTERNS[@]}"; do
        [ -n "$pat" ] || continue
        norm_pat="${pat#./}"
        case "$norm_file" in
            $norm_pat|$norm_pat/*)
                echo "S"
                exit 0
                ;;
        esac
    done
done

# Rule 2: All files under docs/ or *.md only -> lane D
if [ "${#CHANGED_FILES[@]}" -gt 0 ]; then
    all_docs=1
    for file in "${CHANGED_FILES[@]}"; do
        norm_file="${file#./}"
        case "$norm_file" in
            docs/*|docs|*.md)
                ;;
            *)
                all_docs=0
                break
                ;;
        esac
    done

    if [ "$all_docs" -eq 1 ]; then
        echo "D"
        exit 0
    fi
fi

# Rule 3: Lines > 200 -> lane F
if [ "$LINES_ARG" -gt 200 ]; then
    echo "F"
    exit 0
fi

# Rule 4: Default -> lane R
echo "R"
exit 0
