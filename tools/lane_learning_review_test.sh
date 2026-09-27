#!/usr/bin/env bash
#
# Test suite for tools/lane_learning_review.sh (S6-4 / #420, ADR-0024 Layer 3)
# Validates learning-loop review and promotion candidate reporting:
#   (a) No log / empty log -> 'no escalations recorded' exit 0
#   (b) Grouping by directory pattern (<glob>) and threshold promotion
#   (c) Candidate promotion format: 'PROMOTE CANDIDATE: <glob> (n escalations) — add under trust_boundaries:'
#   (d) Patterns below threshold: 'below threshold (n)'
#   (e) Multi-file single escalation grouping
#   (f) Custom --min-count argument
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REVIEW_SCRIPT="${SCRIPT_DIR}/lane_learning_review.sh"

if [ ! -f "$REVIEW_SCRIPT" ]; then
    echo "ERROR: lane learning review script not found at $REVIEW_SCRIPT"
    exit 1
fi

echo "==> Running Lane Learning Review Test Suite..."

FAILURES=0

run_test() {
    local desc="$1"
    local expect_code="$2"
    local expect_output_pattern="$3"
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit $expect_code, got $exit_code"
        echo "  Output: $output"
        FAILURES=$((FAILURES + 1))
    elif [ -n "$expect_output_pattern" ] && ! echo "$output" | grep -q -- "$expect_output_pattern"; then
        echo "  FAIL: expected output to match pattern '$expect_output_pattern', got '$output'"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok"
    fi
}

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

# --- 1. Empty or missing log file handling ---

NONEXISTENT_LOG="$FIXTURE_ROOT/nonexistent.jsonl"
run_test "nonexistent log -> 'no escalations recorded'" 0 "no escalations recorded" \
    "$REVIEW_SCRIPT" --log "$NONEXISTENT_LOG"

EMPTY_LOG="$FIXTURE_ROOT/empty.jsonl"
touch "$EMPTY_LOG"
run_test "empty log (0 bytes) -> 'no escalations recorded'" 0 "no escalations recorded" \
    "$REVIEW_SCRIPT" --log "$EMPTY_LOG"

BLANK_LOG="$FIXTURE_ROOT/blank.jsonl"
printf "\n  \n\t\n" > "$BLANK_LOG"
run_test "blank lines log -> 'no escalations recorded'" 0 "no escalations recorded" \
    "$REVIEW_SCRIPT" --log "$BLANK_LOG"

# --- 2. Argument validation ---

run_test "unknown flag fails with code 2" 2 "error:" \
    "$REVIEW_SCRIPT" --invalid-flag

run_test "non-numeric --min-count fails with code 2" 2 "error:" \
    "$REVIEW_SCRIPT" --log "$EMPTY_LOG" --min-count "two"

run_test "negative --min-count fails with code 2" 2 "error:" \
    "$REVIEW_SCRIPT" --log "$EMPTY_LOG" --min-count "-1"

# --- 3. Single escalation below default threshold (min-count 2) ---

SINGLE_LOG="$FIXTURE_ROOT/single.jsonl"
cat > "$SINGLE_LOG" <<'EOF'
{"date":"2026-09-27T10:00:00Z","files":["internal/foo/bar.go"],"reason":"isolated issue"}
EOF

run_test "single escalation shows below threshold (1)" 0 "below threshold (1)" \
    "$REVIEW_SCRIPT" --log "$SINGLE_LOG"

single_output=$("$REVIEW_SCRIPT" --log "$SINGLE_LOG")
if echo "$single_output" | grep -q "PROMOTE CANDIDATE"; then
    echo "  FAIL: single escalation should not emit PROMOTE CANDIDATE"
    FAILURES=$((FAILURES + 1))
fi

if ! echo "$single_output" | grep -q "internal/foo/\*\*"; then
    echo "  FAIL: output should mention pattern internal/foo/**"
    FAILURES=$((FAILURES + 1))
fi

# --- 4. Promotion candidates and below-threshold patterns (min-count 2) ---

MULTI_LOG="$FIXTURE_ROOT/multi.jsonl"
cat > "$MULTI_LOG" <<'EOF'
{"date":"2026-09-27T10:00:00Z","files":["internal/foo/a.go"],"reason":"escalation 1"}
{"date":"2026-09-27T11:00:00Z","files":["internal/foo/b.go"],"reason":"escalation 2"}
{"date":"2026-09-27T12:00:00Z","files":["internal/bar/c.go"],"reason":"escalation 3"}
EOF

run_test "multi log promotes internal/foo/** with 2 escalations" 0 "PROMOTE CANDIDATE: internal/foo/\*\* (2 escalations) — add under trust_boundaries:" \
    "$REVIEW_SCRIPT" --log "$MULTI_LOG"

multi_output=$("$REVIEW_SCRIPT" --log "$MULTI_LOG")
if ! echo "$multi_output" | grep -q "internal/bar/\*\*.*below threshold (1)"; then
    echo "  FAIL: multi log should show internal/bar/** below threshold (1)"
    echo "  Output: $multi_output"
    FAILURES=$((FAILURES + 1))
fi

# --- 5. Custom --min-count ---

run_test "with --min-count 1, all patterns promoted" 0 "PROMOTE CANDIDATE: internal/bar/\*\* (1 escalations) — add under trust_boundaries:" \
    "$REVIEW_SCRIPT" --log "$MULTI_LOG" --min-count 1

run_test "with --min-count 3, internal/foo/** is below threshold (2)" 0 "below threshold (2)" \
    "$REVIEW_SCRIPT" --log "$MULTI_LOG" --min-count 3

# --- 6. Multi-file escalations in single record ---

MULTI_FILE_LOG="$FIXTURE_ROOT/multi_file.jsonl"
cat > "$MULTI_FILE_LOG" <<'EOF'
{"date":"2026-09-27T10:00:00Z","files":["internal/auth/login.go","internal/auth/token.go"],"reason":"auth overhaul"}
{"date":"2026-09-27T11:00:00Z","files":["internal/auth/session.go","internal/audit/log.go"],"reason":"audit & session"}
EOF

run_test "multi-file record groups same dir as 1 escalation per record" 0 "PROMOTE CANDIDATE: internal/auth/\*\* (2 escalations) — add under trust_boundaries:" \
    "$REVIEW_SCRIPT" --log "$MULTI_FILE_LOG" --min-count 2

multi_file_output=$("$REVIEW_SCRIPT" --log "$MULTI_FILE_LOG" --min-count 2)
if ! echo "$multi_file_output" | grep -q "internal/audit/\*\*.*below threshold (1)"; then
    echo "  FAIL: internal/audit/** should be below threshold (1)"
    echo "  Output: $multi_file_output"
    FAILURES=$((FAILURES + 1))
fi

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "LANE LEARNING REVIEW TEST SUITE: $FAILURES failure(s)."
    exit 1
fi
echo ""
echo "LANE LEARNING REVIEW TEST SUITE: all tests passed."
