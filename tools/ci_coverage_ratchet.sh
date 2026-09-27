#!/usr/bin/env bash
#
# ci_coverage_ratchet.sh — G4 coverage ratchet gate (S6-2b, #420, ADR-0024 G4)
#
# Prevents silent test coverage regressions. Fails if current aggregate
# coverage drops more than 0.25% below the committed baseline. Emits an
# upgrade suggestion when current coverage exceeds the baseline.
#
# Usage:
#   ci_coverage_ratchet.sh --baseline <file> --current <number>
#
# Exit codes:
#   0: coverage within ratchet tolerance (or higher)
#   1: coverage dropped below baseline - 0.25%
#   2: malformed inputs, missing arguments, or invalid numbers

set -euo pipefail

BASELINE_FILE=""
CURRENT_VAL=""
TOLERANCE="0.25"

while [ $# -gt 0 ]; do
    case "$1" in
        --baseline)
            if [ $# -lt 2 ]; then
                echo "ERROR: --baseline requires a file path" >&2
                exit 2
            fi
            BASELINE_FILE="$2"
            shift 2
            ;;
        --current)
            if [ $# -lt 2 ]; then
                echo "ERROR: --current requires a coverage value" >&2
                exit 2
            fi
            CURRENT_VAL="$2"
            shift 2
            ;;
        --tolerance)
            if [ $# -lt 2 ]; then
                echo "ERROR: --tolerance requires a value" >&2
                exit 2
            fi
            TOLERANCE="$2"
            shift 2
            ;;
        *)
            echo "ERROR: unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [ -z "$BASELINE_FILE" ] || [ -z "$CURRENT_VAL" ]; then
    echo "ERROR: usage: ci_coverage_ratchet.sh --baseline <file> --current <number>" >&2
    exit 2
fi

if [ ! -f "$BASELINE_FILE" ]; then
    echo "ERROR: baseline file not found: $BASELINE_FILE" >&2
    exit 2
fi

# Read baseline content, trim whitespace
BASELINE_RAW=$(sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' "$BASELINE_FILE" | tr -d '\r\n')
CURRENT_VAL=$(echo "$CURRENT_VAL" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')

is_number() {
    local val="$1"
    [ -n "$val" ] && [[ "$val" =~ ^[0-9]+(\.[0-9]+)?$ ]]
}

if ! is_number "$BASELINE_RAW"; then
    echo "ERROR: malformed baseline value in '$BASELINE_FILE': '$BASELINE_RAW'" >&2
    exit 2
fi

if ! is_number "$CURRENT_VAL"; then
    echo "ERROR: malformed current coverage value: '$CURRENT_VAL'" >&2
    exit 2
fi

if ! is_number "$TOLERANCE"; then
    echo "ERROR: malformed tolerance value: '$TOLERANCE'" >&2
    exit 2
fi

awk -v b="$BASELINE_RAW" -v c="$CURRENT_VAL" -v tol="$TOLERANCE" 'BEGIN {
    b_val = b + 0
    c_val = c + 0
    tol_val = tol + 0
    min_allowed = b_val - tol_val

    # Float epsilon for comparison
    eps = 1e-9

    if (c_val < min_allowed - eps) {
        printf "::error::coverage ratchet: current coverage %s%% is below baseline %s%% (tolerance %s%%, min %0.2f%%) (#420 G4)\n", c, b, tol, min_allowed > "/dev/stderr"
        exit 1
    } else if (c_val > b_val + eps) {
        printf "coverage ratchet: current coverage %s%% exceeds baseline %s%%; consider raising baseline to %s\n", c, b, c
        exit 0
    } else {
        printf "coverage ratchet: OK (current %s%% >= threshold %0.2f%% against baseline %s%%)\n", c, min_allowed, b
        exit 0
    }
}'
