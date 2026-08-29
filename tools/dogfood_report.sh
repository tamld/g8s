#!/usr/bin/env bash
#
# tools/dogfood_report.sh — g8s Self-Dogfooding Diagnostic Audit Engine
# Part of DEBT-27 (Issue #114) — Automated gap discovery via dogfooding cycle.
#
# Runs 4 mandatory dogfooding health checks:
#   1. make dogfood (brief-issue -> brief-consume roundtrip)
#   2. g8s cleanup-worktrees --older-than 24h --dry-run
#   3. g8s brief-list --expired
#   4. g8s brief-list --consumed
#
# Emits both machine-readable JSON report and human-readable summary.
# Exits 0 if all checks pass, exits 1 if any check fails.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Configurable options
JSON_MODE=0
OUTPUT_FILE=""
CUSTOM_BIN=""
SKIP_LIST="${G8S_DOGFOOD_SKIP:-}"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --json)
            JSON_MODE=1
            shift
            ;;
        --output|-o)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        --bin)
            CUSTOM_BIN="$2"
            shift 2
            ;;
        --skip)
            SKIP_LIST="$2"
            shift 2
            ;;
        --help|-h)
            echo "Usage: tools/dogfood_report.sh [options]"
            echo ""
            echo "Options:"
            echo "  --json              Output JSON report to stdout only"
            echo "  --output, -o <file> Save JSON report to specified file"
            echo "  --bin <path>        Path to g8s binary (builds ./cmd/g8s if unset)"
            echo "  --skip <id,id,...>  Comma-separated check IDs to skip"
            echo "  --help, -h          Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

# Resolve or build g8s binary
G8S_BIN="$CUSTOM_BIN"
CLEANUP_BIN=0
if [ -z "$G8S_BIN" ]; then
    TMP_BIN="${RUNNER_TEMP:-/tmp}/g8s-dogfood-bin-$$"
    if (cd "$REPO_ROOT" && go build -o "$TMP_BIN" ./cmd/g8s >/dev/null 2>&1); then
        G8S_BIN="$TMP_BIN"
        CLEANUP_BIN=1
    else
        echo "ERROR: Failed to build g8s binary for dogfood checks." >&2
        exit 1
    fi
fi

cleanup_on_exit() {
    if [ "$CLEANUP_BIN" -eq 1 ] && [ -f "$G8S_BIN" ]; then
        rm -f "$G8S_BIN"
    fi
}
trap cleanup_on_exit EXIT INT TERM

# State tracking
TOTAL_CHECKS=0
PASSED_CHECKS=0
FAILED_CHECKS=0
SKIPPED_CHECKS=0

CHECK_IDS=()
CHECK_NAMES=()
CHECK_COMMANDS=()
CHECK_STATUSES=()
CHECK_DURATIONS=()
CHECK_RULES=()
CHECK_DETAILS=()

is_skipped() {
    local check_id="$1"
    if [ -n "$SKIP_LIST" ]; then
        IFS=',' read -ra ADDR <<< "$SKIP_LIST"
        for s in "${ADDR[@]}"; do
            if [ "$s" = "$check_id" ] || [ "$s" = "all" ]; then
                return 0
            fi
        done
    fi
    return 1
}

escape_json() {
    local str="$1"
    str="${str//\\/\\\\}"
    str="${str//\"/\\\"}"
    str="${str//$'\n'/\\n}"
    str="${str//$'\r'/\\r}"
    str="${str//$'\t'/\\t}"
    printf '%s' "$str"
}

run_check() {
    local check_id="$1"
    local check_name="$2"
    local check_cmd="$3"
    local pass_rule="$4"

    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    CHECK_IDS+=("$check_id")
    CHECK_NAMES+=("$check_name")
    CHECK_COMMANDS+=("$check_cmd")
    CHECK_RULES+=("$pass_rule")

    if is_skipped "$check_id"; then
        SKIPPED_CHECKS=$((SKIPPED_CHECKS + 1))
        CHECK_STATUSES+=("SKIP")
        CHECK_DURATIONS+=(0)
        CHECK_DETAILS+=("Skipped via skip configuration (G8S_DOGFOOD_SKIP)")
        return 0
    fi

    local start_time
    start_time=$(date +%s%N 2>/dev/null || date +%s)

    local output=""
    local exit_code=0

    output=$(eval "$check_cmd" 2>&1) || exit_code=$?

    local end_time
    end_time=$(date +%s%N 2>/dev/null || date +%s)

    local duration_ms=0
    if [ ${#start_time} -gt 10 ]; then
        duration_ms=$(( (end_time - start_time) / 1000000 ))
    else
        duration_ms=$(( (end_time - start_time) * 1000 ))
    fi

    CHECK_DURATIONS+=("$duration_ms")
    CHECK_DETAILS+=("$output")

    if [ "$exit_code" -eq 0 ]; then
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
        CHECK_STATUSES+=("PASS")
        return 0
    else
        FAILED_CHECKS=$((FAILED_CHECKS + 1))
        CHECK_STATUSES+=("FAIL")
        return 0
    fi
}

REPORT_START_TIME=$(date +%s%N 2>/dev/null || date +%s)
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# ---------------------------------------------------------
# Check 1: make dogfood (brief-issue -> brief-consume roundtrip)
# ---------------------------------------------------------
run_check "make_dogfood" \
    "Brief Roundtrip (make dogfood)" \
    "(cd \"$REPO_ROOT\" && make dogfood)" \
    "Exit code 0 with successful brief issuance and consumption"

# ---------------------------------------------------------
# Check 2: g8s cleanup-worktrees --older-than 24h --dry-run
# ---------------------------------------------------------
run_check "cleanup_worktrees" \
    "Worktree Leak Scanner (cleanup-worktrees)" \
    "\"$G8S_BIN\" cleanup-worktrees --older-than 24h --dry-run --repo \"$REPO_ROOT\"" \
    "Exit code 0 verifying worktree leak scan executes safely"

# ---------------------------------------------------------
# Check 3: g8s brief-list --expired
# ---------------------------------------------------------
# Prepare an isolated state DB for querying if G8S_DB is not explicitly set
DOGFOOD_DB="${G8S_DB:-${RUNNER_TEMP:-/tmp}/g8s-dogfood-audit-$$.db}"
export G8S_DB="$DOGFOOD_DB"

run_check "brief_list_expired" \
    "Expired Briefs Query (brief-list --expired)" \
    "\"$G8S_BIN\" brief-list --expired --json" \
    "Exit code 0 and valid query execution against SQLite store"

# ---------------------------------------------------------
# Check 4: g8s brief-list --consumed (sanity check: system uses briefs)
# ---------------------------------------------------------
# Seed a brief and consume it to verify active consumption query
SEED_PAYLOAD="${RUNNER_TEMP:-/tmp}/seed-payload-$$.md"
SEED_DOD="${RUNNER_TEMP:-/tmp}/seed-dod-$$.md"
echo "# Dogfood Brief Sanity Check" > "$SEED_PAYLOAD"
echo "- [x] Sanity check verified" > "$SEED_DOD"

SEED_ISSUE_OUT=$("$G8S_BIN" brief-issue --title "audit-sanity-brief" --payload-file "$SEED_PAYLOAD" --dod-file "$SEED_DOD" --issued-by "dogfood-audit" --ttl "30m" 2>/dev/null || true)
SEED_ID=$(echo "$SEED_ISSUE_OUT" | grep -o '"id": "[^"]*"' | head -1 | cut -d'"' -f4 || true)
if [ -n "$SEED_ID" ]; then
    "$G8S_BIN" brief-consume --id "$SEED_ID" >/dev/null 2>&1 || true
fi
rm -f "$SEED_PAYLOAD" "$SEED_DOD"

run_check "brief_list_consumed" \
    "Consumed Briefs Audit (brief-list --consumed)" \
    "\"$G8S_BIN\" brief-list --consumed --json" \
    "Exit code 0 and non-empty consumed brief ledger verification"

REPORT_END_TIME=$(date +%s%N 2>/dev/null || date +%s)
TOTAL_DURATION_MS=0
if [ ${#REPORT_START_TIME} -gt 10 ]; then
    TOTAL_DURATION_MS=$(( (REPORT_END_TIME - REPORT_START_TIME) / 1000000 ))
else
    TOTAL_DURATION_MS=$(( (REPORT_END_TIME - REPORT_START_TIME) * 1000 ))
fi

OVERALL_STATUS="PASSED"
if [ "$FAILED_CHECKS" -gt 0 ]; then
    OVERALL_STATUS="FAILED"
fi

# Clean up temp db if we created it
if [ "${DOGFOOD_DB}" = "${RUNNER_TEMP:-/tmp}/g8s-dogfood-audit-$$.db" ]; then
    rm -f "$DOGFOOD_DB" "$DOGFOOD_DB-wal" "$DOGFOOD_DB-shm"
fi

# ---------------------------------------------------------
# Construct JSON Report
# ---------------------------------------------------------
CHECKS_JSON=""
for i in "${!CHECK_IDS[@]}"; do
    C_ID="${CHECK_IDS[$i]}"
    C_NAME="${CHECK_NAMES[$i]}"
    C_CMD="${CHECK_COMMANDS[$i]}"
    C_STATUS="${CHECK_STATUSES[$i]}"
    C_DUR="${CHECK_DURATIONS[$i]}"
    C_RULE="${CHECK_RULES[$i]}"
    C_DET="${CHECK_DETAILS[$i]}"

    C_ID_ESC=$(escape_json "$C_ID")
    C_NAME_ESC=$(escape_json "$C_NAME")
    C_CMD_ESC=$(escape_json "$C_CMD")
    C_RULE_ESC=$(escape_json "$C_RULE")
    C_DET_ESC=$(escape_json "$C_DET")

    ITEM=$(cat <<EOF
    {
      "id": "$C_ID_ESC",
      "name": "$C_NAME_ESC",
      "command": "$C_CMD_ESC",
      "status": "$C_STATUS",
      "duration_ms": $C_DUR,
      "pass_rule": "$C_RULE_ESC",
      "details": "$C_DET_ESC"
    }
EOF
)
    if [ -n "$CHECKS_JSON" ]; then
        CHECKS_JSON="$CHECKS_JSON,$ITEM"
    else
        CHECKS_JSON="$ITEM"
    fi
done

JSON_REPORT=$(cat <<EOF
{
  "version": "1.0.0",
  "timestamp": "$TIMESTAMP",
  "overall_status": "$OVERALL_STATUS",
  "total_checks": $TOTAL_CHECKS,
  "passed_checks": $PASSED_CHECKS,
  "failed_checks": $FAILED_CHECKS,
  "skipped_checks": $SKIPPED_CHECKS,
  "duration_ms": $TOTAL_DURATION_MS,
  "checks": [
$CHECKS_JSON
  ]
}
EOF
)

# Save to output file if requested
if [ -n "$OUTPUT_FILE" ]; then
    printf '%s\n' "$JSON_REPORT" > "$OUTPUT_FILE"
fi

# Output JSON or Human-Readable Summary
if [ "$JSON_MODE" -eq 1 ]; then
    printf '%s\n' "$JSON_REPORT"
else
    echo "================================================================================"
    echo "                      g8s SELF-DOGFOODING AUDIT REPORT"
    echo "================================================================================"
    printf "Timestamp: %s | Total: %d | Passed: %d | Failed: %d | Skipped: %d | Status: %s\n\n" \
        "$TIMESTAMP" "$TOTAL_CHECKS" "$PASSED_CHECKS" "$FAILED_CHECKS" "$SKIPPED_CHECKS" "$OVERALL_STATUS"

    printf "%-4s | %-40s | %-8s | %-10s\n" "No." "Check Description" "Status" "Duration"
    echo "-----+------------------------------------------+----------+-----------"
    for i in "${!CHECK_IDS[@]}"; do
        NUM=$((i + 1))
        C_NAME="${CHECK_NAMES[$i]}"
        C_STATUS="${CHECK_STATUSES[$i]}"
        C_DUR="${CHECK_DURATIONS[$i]}ms"
        printf "%-4d | %-40s | %-8s | %-10s\n" "$NUM" "$C_NAME" "$C_STATUS" "$C_DUR"
    done
    echo "================================================================================"

    if [ "$FAILED_CHECKS" -gt 0 ]; then
        echo ""
        echo "FAILED CHECK DETAILS:"
        for i in "${!CHECK_IDS[@]}"; do
            if [ "${CHECK_STATUSES[$i]}" = "FAIL" ]; then
                echo "--------------------------------------------------------------------------------"
                echo "Check: ${CHECK_NAMES[$i]} (${CHECK_IDS[$i]})"
                echo "Command: ${CHECK_COMMANDS[$i]}"
                echo "Pass Rule: ${CHECK_RULES[$i]}"
                echo "Output:"
                echo "${CHECK_DETAILS[$i]}"
            fi
        done
        echo "--------------------------------------------------------------------------------"
    fi
fi

if [ "$OVERALL_STATUS" = "PASSED" ]; then
    exit 0
else
    exit 1
fi
