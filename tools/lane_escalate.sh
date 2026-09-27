#!/usr/bin/env bash
#
# lane_escalate.sh — P0 escalation recording & machine-match deny-all guard (S6-4, #420, ADR-0024 Layer 3)
#
# Records supervisor/Jev manual escalations to P0 when changes fall outside
# declared trust boundaries.
#
# Behavior:
#   (1) If ANY changed file already matches a trust_boundaries glob -> exit 1 'already P0 by machine match — deny-all'
#   (2) Else append one JSON line to the log: {"date":"<iso>","files":[...],"reason":"..."}
#       and exit 0 printing 'escalated to P0 (recorded for learning loop)'
#
# Usage:
#   lane_escalate.sh --files <comma-separated-files> --reason <text> [--registry .g8s/trust-boundaries.yml --log .g8s/escalations.jsonl]
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FILES_ARG=""
HAS_FILES=0
REASON_ARG=""
HAS_REASON=0
REGISTRY_FILE=""
LOG_FILE=""

usage() {
    echo "Usage: lane_escalate.sh --files <comma-separated-files> --reason <text> [--registry .g8s/trust-boundaries.yml] [--log .g8s/escalations.jsonl]" >&2
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
        --reason)
            if [ $# -lt 2 ]; then
                echo "error: --reason requires an argument" >&2
                usage
                exit 2
            fi
            REASON_ARG="$2"
            HAS_REASON=1
            shift 2
            ;;
        --registry)
            if [ $# -lt 2 ]; then
                echo "error: --registry requires an argument" >&2
                usage
                exit 2
            fi
            REGISTRY_FILE="$2"
            shift 2
            ;;
        --log)
            if [ $# -lt 2 ]; then
                echo "error: --log requires an argument" >&2
                usage
                exit 2
            fi
            LOG_FILE="$2"
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

if [ "$HAS_REASON" -ne 1 ]; then
    echo "error: missing required argument --reason" >&2
    usage
    exit 2
fi

# Locate default trust boundaries registry if not provided
if [ -z "$REGISTRY_FILE" ]; then
    if [ -f ".g8s/trust-boundaries.yml" ]; then
        REGISTRY_FILE=".g8s/trust-boundaries.yml"
    elif git rev-parse --show-toplevel >/dev/null 2>&1 && [ -f "$(git rev-parse --show-toplevel)/.g8s/trust-boundaries.yml" ]; then
        REGISTRY_FILE="$(git rev-parse --show-toplevel)/.g8s/trust-boundaries.yml"
    elif [ -f "${SCRIPT_DIR}/../.g8s/trust-boundaries.yml" ]; then
        REGISTRY_FILE="${SCRIPT_DIR}/../.g8s/trust-boundaries.yml"
    fi
fi

# Locate default escalations log file if not provided
if [ -z "$LOG_FILE" ]; then
    if git rev-parse --show-toplevel >/dev/null 2>&1; then
        LOG_FILE="$(git rev-parse --show-toplevel)/.g8s/escalations.jsonl"
    else
        LOG_FILE=".g8s/escalations.jsonl"
    fi
fi

# Function to parse trust_boundaries list from YAML
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

# Collect trust boundary patterns
TRUST_PATTERNS=()
if [ -n "$REGISTRY_FILE" ] && [ -f "$REGISTRY_FILE" ]; then
    while IFS= read -r pat; do
        [ -n "$pat" ] && TRUST_PATTERNS+=("$pat")
    done < <(parse_trust_boundaries "$REGISTRY_FILE")
fi

# Parse changed files list
CHANGED_FILES=()
IFS=',' read -r -a raw_files <<< "$FILES_ARG"
for raw in "${raw_files[@]}"; do
    cleaned=$(printf '%s' "$raw" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
    [ -n "$cleaned" ] && CHANGED_FILES+=("$cleaned")
done

if [ "${#CHANGED_FILES[@]}" -eq 0 ]; then
    echo "error: --files cannot be empty" >&2
    exit 2
fi

# Guard (a): If ANY changed file already matches trust_boundaries glob -> deny-all exit 1
for file in "${CHANGED_FILES[@]}"; do
    norm_file="${file#./}"
    for pat in "${TRUST_PATTERNS[@]}"; do
        [ -n "$pat" ] || continue
        norm_pat="${pat#./}"
        case "$norm_file" in
            $norm_pat|$norm_pat/*)
                echo "already P0 by machine match — deny-all"
                exit 1
                ;;
        esac
    done
done

# Action (b): Non-matching files -> append JSON record to log and exit 0
mkdir -p "$(dirname "$LOG_FILE")"

ISO_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

files_json="["
first=1
for f in "${CHANGED_FILES[@]}"; do
    norm_f="${f#./}"
    f_esc=$(printf '%s' "$norm_f" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')
    if [ "$first" -eq 1 ]; then
        files_json="${files_json}\"${f_esc}\""
        first=0
    else
        files_json="${files_json},\"${f_esc}\""
    fi
done
files_json="${files_json}]"

reason_esc=$(printf '%s' "$REASON_ARG" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | tr '\n' ' ' | tr '\r' ' ' | tr '\t' ' ')

json_line="{\"date\":\"${ISO_DATE}\",\"files\":${files_json},\"reason\":\"${reason_esc}\"}"
printf '%s\n' "$json_line" >> "$LOG_FILE"

echo "escalated to P0 (recorded for learning loop)"
exit 0
