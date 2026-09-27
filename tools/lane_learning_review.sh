#!/usr/bin/env bash
#
# lane_learning_review.sh — Learning-loop review for P0 escalations (S6-4, #420, ADR-0024 Layer 3)
#
# Analyzes recorded P0 escalations to identify repeated patterns (>= min-count)
# eligible for promotion into .g8s/trust-boundaries.yml.
#
# Usage:
#   lane_learning_review.sh [--log .g8s/escalations.jsonl] [--min-count 2]
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

LOG_FILE=""
MIN_COUNT=2

usage() {
    echo "Usage: lane_learning_review.sh [--log .g8s/escalations.jsonl] [--min-count 2]" >&2
}

while [ $# -gt 0 ]; do
    case "$1" in
        --log)
            if [ $# -lt 2 ]; then
                echo "error: --log requires an argument" >&2
                usage
                exit 2
            fi
            LOG_FILE="$2"
            shift 2
            ;;
        --min-count)
            if [ $# -lt 2 ]; then
                echo "error: --min-count requires an argument" >&2
                usage
                exit 2
            fi
            MIN_COUNT="$2"
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

# Validate min-count
case "$MIN_COUNT" in
    ''|*[!0-9]*)
        echo "error: --min-count must be a positive integer, got: '$MIN_COUNT'" >&2
        exit 2
        ;;
esac

if [ "$MIN_COUNT" -le 0 ]; then
    echo "error: --min-count must be greater than 0, got: '$MIN_COUNT'" >&2
    exit 2
fi

# Locate default escalations log file if not provided
if [ -z "$LOG_FILE" ]; then
    if git rev-parse --show-toplevel >/dev/null 2>&1; then
        LOG_FILE="$(git rev-parse --show-toplevel)/.g8s/escalations.jsonl"
    else
        LOG_FILE=".g8s/escalations.jsonl"
    fi
fi

# If log file doesn't exist or is empty
if [ ! -f "$LOG_FILE" ] || [ ! -s "$LOG_FILE" ]; then
    echo "no escalations recorded"
    exit 0
fi

# Check if log file contains non-whitespace content
non_blank=$(grep -c '[^[:space:]]' "$LOG_FILE" || true)
if [ "$non_blank" -eq 0 ]; then
    echo "no escalations recorded"
    exit 0
fi

# Process log file and generate report
awk -v min_count="$MIN_COUNT" '
function extract_glob(file,   sub_f, idx, i, d) {
    sub_f = file
    sub(/^\.\//, "", sub_f)
    idx = 0
    for (i = length(sub_f); i >= 1; i--) {
        if (substr(sub_f, i, 1) == "/") {
            idx = i
            break
        }
    }
    if (idx > 0) {
        d = substr(sub_f, 1, idx - 1)
        return d "/**"
    } else {
        return "**"
    }
}

BEGIN {
    total_records = 0
}

{
    # Match the files array
    if (match($0, /"files"[[:space:]]*:[[:space:]]*\[[^]]*\]/)) {
        files_part = substr($0, RSTART, RLENGTH)
        delete seen_in_line
        has_file = 0
        while (match(files_part, /"([^"\\]|\\.)*"/)) {
            token = substr(files_part, RSTART + 1, RLENGTH - 2)
            if (token != "files") {
                gsub(/\\"/, "\"", token)
                gsub(/\\\\/, "\\", token)
                g = extract_glob(token)
                seen_in_line[g] = 1
                has_file = 1
            }
            files_part = substr(files_part, RSTART + RLENGTH)
        }
        if (has_file) {
            total_records++
            for (g in seen_in_line) {
                counts[g]++
            }
        }
    }
}

END {
    if (total_records == 0) {
        print "no escalations recorded"
        exit 0
    }

    n_cand = 0
    n_below = 0

    for (g in counts) {
        if (counts[g] >= min_count) {
            candidates[n_cand++] = g
        } else {
            below[n_below++] = g
        }
    }

    # Sort candidates
    for (i = 0; i < n_cand - 1; i++) {
        for (j = i + 1; j < n_cand; j++) {
            if (candidates[i] > candidates[j]) {
                tmp = candidates[i]
                candidates[i] = candidates[j]
                candidates[j] = tmp
            }
        }
    }

    # Sort below threshold
    for (i = 0; i < n_below - 1; i++) {
        for (j = i + 1; j < n_below; j++) {
            if (below[i] > below[j]) {
                tmp = below[i]
                below[i] = below[j]
                below[j] = tmp
            }
        }
    }

    # Print candidates
    for (i = 0; i < n_cand; i++) {
        g = candidates[i]
        printf "PROMOTE CANDIDATE: %s (%d escalations) — add under trust_boundaries:\n", g, counts[g]
    }

    # Print below threshold
    for (i = 0; i < n_below; i++) {
        g = below[i]
        printf "%s — below threshold (%d)\n", g, counts[g]
    }
}
' "$LOG_FILE"
