#!/usr/bin/env bash
#
# tools/claims_check.sh — Quantitative Claims Registry Checker (#447)
#
# Verifies docs/claims.yml against live code and artifacts:
#   1. For each bound claim, 'test:' must match a 'func <Name>(' in *_test.go.
#   2. For each bound claim, 'source:' must resolve to an existing file.
#   3. Claims under 'unbound:' are tracked as aspirations.
#
# Usage:
#   tools/claims_check.sh [--file <path>] [--root <dir>]
#
# Exit codes:
#   0: all bound claims verified
#   1: broken claim bindings found
#   2: usage / file error

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CLAIMS_FILE=""
ROOT_DIR=""

while [ $# -gt 0 ]; do
    case "$1" in
        --file)
            if [ $# -lt 2 ]; then
                echo "ERROR: --file requires a file path" >&2
                exit 2
            fi
            CLAIMS_FILE="$2"
            shift 2
            ;;
        --root)
            if [ $# -lt 2 ]; then
                echo "ERROR: --root requires a directory path" >&2
                exit 2
            fi
            ROOT_DIR="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [--file <path>] [--root <dir>]"
            exit 0
            ;;
        -*)
            echo "ERROR: unknown argument: $1" >&2
            exit 2
            ;;
        *)
            if [ -z "$CLAIMS_FILE" ]; then
                CLAIMS_FILE="$1"
                shift
            else
                echo "ERROR: unexpected extra argument: $1" >&2
                exit 2
            fi
            ;;
    esac
done

if [ -z "$ROOT_DIR" ]; then
    ROOT_DIR="$REPO_ROOT"
fi

if [ -z "$CLAIMS_FILE" ]; then
    CLAIMS_FILE="$ROOT_DIR/docs/claims.yml"
fi

if [ ! -f "$CLAIMS_FILE" ]; then
    echo "ERROR: claims registry file not found: $CLAIMS_FILE" >&2
    exit 2
fi

clean_val() {
    printf '%s\n' "$1" | sed -e 's/[[:space:]]*#.*$//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//"
}

BOUND_COUNT=0
UNBOUND_COUNT=0
BROKEN_COUNT=0

current_id=""
current_claim=""
current_test=""
current_eval=""
current_source=""
current_unbound=0
in_unbound=0

BROKEN_MESSAGES=()

validate_claim() {
    [ -z "$current_id" ] && return 0

    if [ "$current_unbound" -eq 1 ]; then
        UNBOUND_COUNT=$((UNBOUND_COUNT + 1))
        return 0
    fi

    local claim_broken=0

    # 1. Verify test binding
    if [ -n "$current_test" ]; then
        if ! grep -rn --exclude-dir=.git --exclude-dir=dist --include="*_test.go" "func ${current_test}(" "$ROOT_DIR" >/dev/null 2>&1; then
            BROKEN_MESSAGES+=("claim ${current_id}: test '${current_test}' not found (missing 'func ${current_test}(' in *_test.go)")
            claim_broken=1
        fi
    elif [ -z "$current_eval" ]; then
        BROKEN_MESSAGES+=("claim ${current_id}: neither 'test' nor 'eval' binding specified")
        claim_broken=1
    fi

    # 2. Verify source file binding
    if [ -n "$current_source" ]; then
        if [ ! -f "$ROOT_DIR/$current_source" ] && [ ! -f "$current_source" ]; then
            BROKEN_MESSAGES+=("claim ${current_id}: source file '${current_source}' not found")
            claim_broken=1
        fi
    fi

    if [ "$claim_broken" -ne 0 ]; then
        BROKEN_COUNT=$((BROKEN_COUNT + 1))
    else
        BOUND_COUNT=$((BOUND_COUNT + 1))
    fi
}

while IFS= read -r line || [ -n "$line" ]; do
    trimmed=$(printf '%s\n' "$line" | sed 's/^[[:space:]]*//')
    [ -z "$trimmed" ] && continue

    case "$trimmed" in
        "#"*)
            continue
            ;;
        "- id:"*)
            validate_claim
            current_id=$(clean_val "${trimmed#- id:}")
            current_claim=""
            current_test=""
            current_eval=""
            current_source=""
            current_unbound="$in_unbound"
            ;;
        "claim:"*)
            current_claim=$(clean_val "${trimmed#claim:}")
            ;;
        "test:"*)
            current_test=$(clean_val "${trimmed#test:}")
            ;;
        "eval:"*)
            current_eval=$(clean_val "${trimmed#eval:}")
            ;;
        "source:"*)
            current_source=$(clean_val "${trimmed#source:}")
            ;;
        "unbound:"*)
            in_unbound=1
            ;;
    esac
done < "$CLAIMS_FILE"

# Validate final claim
validate_claim

TOTAL_CLAIMS=$((BOUND_COUNT + UNBOUND_COUNT + BROKEN_COUNT))

if [ ${#BROKEN_MESSAGES[@]} -gt 0 ]; then
    echo "Broken claim bindings found in $CLAIMS_FILE:" >&2
    for msg in "${BROKEN_MESSAGES[@]}"; do
        echo "  - $msg" >&2
    done
fi

echo "SCORECARD: ${BOUND_COUNT} bound, ${UNBOUND_COUNT} unbound, ${BROKEN_COUNT} broken (total ${TOTAL_CLAIMS} claims)"

if [ "$BROKEN_COUNT" -gt 0 ]; then
    exit 1
fi

exit 0
