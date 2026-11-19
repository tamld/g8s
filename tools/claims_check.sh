#!/usr/bin/env bash
#
# tools/claims_check.sh — Quantitative Claims Registry Checker (#447, #547)
#
# Verifies docs/claims.yml against live code and artifacts:
#   1. For each bound claim, 'test:' must match a 'func <Name>(' in *_test.go.
#   2. With --test-report <file>, each bound test must also PASS in go test -v output.
#   3. For each claim, 'source:' must resolve to an existing file if specified.
#   4. Every claim MUST carry either a test binding or a demo command.
#      A claim with neither fails the check (the no-ambiguity standard).
#   5. Demo commands are recorded, not executed, by the checker.
#
# Usage:
#   tools/claims_check.sh [--file <path>] [--root <dir>] [--test-report <path>]
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
TEST_REPORT=""

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
        --test-report)
            if [ $# -lt 2 ]; then
                echo "ERROR: --test-report requires a file path" >&2
                exit 2
            fi
            TEST_REPORT="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [--file <path>] [--root <dir>] [--test-report <path>]"
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

if [ -n "$TEST_REPORT" ] && [ ! -f "$TEST_REPORT" ]; then
    echo "ERROR: test report file not found: $TEST_REPORT" >&2
    exit 2
fi

clean_val() {
    printf '%s\n' "$1" | sed -e 's/[[:space:]]*#.*$//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//"
}

BOUND_COUNT=0
UNBOUND_COUNT=0
BROKEN_COUNT=0
RUNTIME_PROVEN_COUNT=0

current_id=""
current_claim=""
current_tests=()
current_eval=""
current_demo=""
current_source=""
current_unbound=0
in_unbound=0

BROKEN_MESSAGES=()
RECORDED_DEMOS=()

validate_claim() {
    [ -z "$current_id" ] && return 0

    local claim_broken=0

    # Record demo commands (recorded, not executed)
    if [ -n "$current_demo" ]; then
        RECORDED_DEMOS+=("${current_id}: ${current_demo}")
    fi

    # 1. Verify test binding(s) or demo command (the no-ambiguity standard)
    if [ "${#current_tests[@]}" -gt 0 ]; then
        local t
        for t in "${current_tests[@]}"; do
            if ! grep -rn --exclude-dir=.git --exclude-dir=dist --include="*_test.go" "func ${t}(" "$ROOT_DIR" >/dev/null 2>&1; then
                BROKEN_MESSAGES+=("claim ${current_id}: test '${t}' not found (missing 'func ${t}(' in *_test.go)")
                claim_broken=1
            elif [ -n "$TEST_REPORT" ]; then
                if grep -Eq "^[[:space:]]*--- FAIL:[[:space:]]+${t}([[:space:]]|$)" "$TEST_REPORT" || ! grep -Eq "^[[:space:]]*--- PASS:[[:space:]]+${t}([[:space:]]|$)" "$TEST_REPORT"; then
                    BROKEN_MESSAGES+=("claim ${current_id}: test '${t}' did not PASS in test report '${TEST_REPORT}'")
                    claim_broken=1
                fi
            fi
        done
    elif [ -n "$current_eval" ]; then
        : # eval binding specified
    elif [ -n "$current_demo" ]; then
        : # demo command specified and recorded
    else
        BROKEN_MESSAGES+=("claim ${current_id}: neither test binding nor demo command specified")
        claim_broken=1
    fi

    # 2. Verify source file binding
    if [ -n "$current_source" ]; then
        local src_file="${current_source%%:*}"
        if [ ! -f "$ROOT_DIR/$src_file" ] && [ ! -f "$src_file" ]; then
            BROKEN_MESSAGES+=("claim ${current_id}: source file '${current_source}' not found")
            claim_broken=1
        fi
    fi

    if [ "$claim_broken" -ne 0 ]; then
        BROKEN_COUNT=$((BROKEN_COUNT + 1))
    elif [ "$current_unbound" -eq 1 ] || [ "${#current_tests[@]}" -eq 0 ]; then
        UNBOUND_COUNT=$((UNBOUND_COUNT + 1))
    else
        BOUND_COUNT=$((BOUND_COUNT + 1))
        if [ -n "$TEST_REPORT" ]; then
            RUNTIME_PROVEN_COUNT=$((RUNTIME_PROVEN_COUNT + 1))
        fi
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
            current_tests=()
            current_eval=""
            current_demo=""
            current_source=""
            current_unbound="$in_unbound"
            ;;
        "claim:"*)
            current_claim=$(clean_val "${trimmed#claim:}")
            ;;
        "test:"*)
            test_val=$(clean_val "${trimmed#test:}")
            if [ -n "$test_val" ]; then
                current_tests+=("$test_val")
            fi
            ;;
        "eval:"*)
            current_eval=$(clean_val "${trimmed#eval:}")
            ;;
        "demo:"*)
            current_demo=$(clean_val "${trimmed#demo:}")
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

if [ "${#BROKEN_MESSAGES[@]}" -gt 0 ]; then
    echo "Broken claim bindings found in $CLAIMS_FILE:" >&2
    for msg in "${BROKEN_MESSAGES[@]}"; do
        echo "  - $msg" >&2
    done
fi

echo "SCORECARD: ${BOUND_COUNT} bound, ${UNBOUND_COUNT} unbound, ${BROKEN_COUNT} broken (total ${TOTAL_CLAIMS} claims)"
if [ -n "$TEST_REPORT" ]; then
    echo "RUNTIME_PROVEN: ${RUNTIME_PROVEN_COUNT}/${BOUND_COUNT} bound claims verified against ${TEST_REPORT}"
fi

if [ "$BROKEN_COUNT" -gt 0 ]; then
    exit 1
fi

exit 0
