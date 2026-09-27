#!/usr/bin/env bash
#
# Test suite for tools/lane_escalate.sh (S6-4 / #420, ADR-0024 Layer 3)
# Validates P0 escalation recording and machine-match deny-all guard:
#   (a) Machine match against trust-boundaries -> exit 1 'already P0 by machine match — deny-all'
#   (b) Non-matching files -> append JSON line to log & exit 0 'escalated to P0 (recorded for learning loop)'
#   (c) Argument validation and exit 2 on bad args
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ESCALATE_SCRIPT="${SCRIPT_DIR}/lane_escalate.sh"

if [ ! -f "$ESCALATE_SCRIPT" ]; then
    echo "ERROR: lane escalate script not found at $ESCALATE_SCRIPT"
    exit 1
fi

echo "==> Running Lane Escalate Gate Test Suite..."

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
    elif [ -n "$expect_output_pattern" ] && ! echo "$output" | grep -q "$expect_output_pattern"; then
        echo "  FAIL: expected output to match pattern '$expect_output_pattern', got '$output'"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok"
    fi
}

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

# Setup fixture trust-boundaries.yml
mkdir -p "$FIXTURE_ROOT/.g8s"
TB_FILE="$FIXTURE_ROOT/.g8s/trust-boundaries.yml"
cat > "$TB_FILE" <<'EOF'
trust_boundaries:
  - "internal/receipt/*"
  - "internal/worker/proc_*"
  - "internal/server/*"
  - "internal/harness/*"
  - "internal/memory/*"
  - "internal/dispatch/*"
EOF

LOG_FILE="$FIXTURE_ROOT/.g8s/escalations.jsonl"

# --- 1. Argument validation ---

run_test "missing all args fails with code 2" 2 "Usage:" \
    "$ESCALATE_SCRIPT"

run_test "missing --files fails with code 2" 2 "error: missing required argument --files" \
    "$ESCALATE_SCRIPT" --reason "test reason"

run_test "missing --reason fails with code 2" 2 "error: missing required argument --reason" \
    "$ESCALATE_SCRIPT" --files "tools/x.go"

run_test "unknown flag fails with code 2" 2 "error: unknown argument" \
    "$ESCALATE_SCRIPT" --files "tools/x.go" --reason "foo" --invalid-flag

# --- 2. Machine match deny-all (already P0) ---

run_test "exact directory glob match denied (receipt)" 1 "already P0 by machine match — deny-all" \
    "$ESCALATE_SCRIPT" --files "internal/receipt/receipt.go" --reason "manual check" --registry "$TB_FILE" --log "$LOG_FILE"

run_test "prefix wildcard glob match denied (worker/proc_*)" 1 "already P0 by machine match — deny-all" \
    "$ESCALATE_SCRIPT" --files "internal/worker/proc_linux.go" --reason "manual check" --registry "$TB_FILE" --log "$LOG_FILE"

run_test "mixed files where one matches denied" 1 "already P0 by machine match — deny-all" \
    "$ESCALATE_SCRIPT" --files "tools/x.go,internal/server/handler.go" --reason "manual check" --registry "$TB_FILE" --log "$LOG_FILE"

run_test "leading ./ normalized and denied" 1 "already P0 by machine match — deny-all" \
    "$ESCALATE_SCRIPT" --files "./internal/memory/vector.go" --reason "manual check" --registry "$TB_FILE" --log "$LOG_FILE"

# Verify that log file was NOT created or written during deny-all runs
if [ -f "$LOG_FILE" ]; then
    echo "  FAIL: log file should not exist after deny-all runs"
    FAILURES=$((FAILURES + 1))
fi

# --- 3. Successful escalation recording ---

run_test "single file non-match escalates to P0" 0 "escalated to P0 (recorded for learning loop)" \
    "$ESCALATE_SCRIPT" --files "tools/custom.go" --reason "critical new tool" --registry "$TB_FILE" --log "$LOG_FILE"

if [ ! -f "$LOG_FILE" ]; then
    echo "  FAIL: log file was not created"
    FAILURES=$((FAILURES + 1))
else
    line_count=$(wc -l < "$LOG_FILE" | tr -d ' ')
    if [ "$line_count" -ne 1 ]; then
        echo "  FAIL: expected 1 log line, got $line_count"
        FAILURES=$((FAILURES + 1))
    fi
    if ! grep -q '"files":\["tools/custom.go"\]' "$LOG_FILE"; then
        echo "  FAIL: log line does not contain expected files array"
        FAILURES=$((FAILURES + 1))
    fi
    if ! grep -q '"reason":"critical new tool"' "$LOG_FILE"; then
        echo "  FAIL: log line does not contain expected reason"
        FAILURES=$((FAILURES + 1))
    fi
    if ! grep -q '"date":' "$LOG_FILE"; then
        echo "  FAIL: log line does not contain date field"
        FAILURES=$((FAILURES + 1))
    fi
fi

run_test "multiple files non-match escalates to P0 and appends" 0 "escalated to P0 (recorded for learning loop)" \
    "$ESCALATE_SCRIPT" --files "internal/foo/a.go, internal/foo/b.go" --reason "subsystem escalation" --registry "$TB_FILE" --log "$LOG_FILE"

if [ -f "$LOG_FILE" ]; then
    line_count=$(wc -l < "$LOG_FILE" | tr -d ' ')
    if [ "$line_count" -ne 2 ]; then
        echo "  FAIL: expected 2 log lines, got $line_count"
        FAILURES=$((FAILURES + 1))
    fi
    second_line=$(sed -n '2p' "$LOG_FILE")
    if ! echo "$second_line" | grep -q '"files":\["internal/foo/a.go","internal/foo/b.go"\]'; then
        echo "  FAIL: second line files JSON format incorrect: $second_line"
        FAILURES=$((FAILURES + 1))
    fi
fi

# Test special character escaping in reason
run_test "reason with special characters escapes cleanly" 0 "escalated to P0 (recorded for learning loop)" \
    "$ESCALATE_SCRIPT" --files "pkg/util/util.go" --reason 'quotes "test" and \slashes\ and $symbols' --registry "$TB_FILE" --log "$LOG_FILE"

# Test creating nested log directory
NESTED_LOG="$FIXTURE_ROOT/nested/dir/escalations.jsonl"
run_test "nested log directory is created automatically" 0 "escalated to P0 (recorded for learning loop)" \
    "$ESCALATE_SCRIPT" --files "cmd/app/main.go" --reason "root escalation" --registry "$TB_FILE" --log "$NESTED_LOG"

if [ ! -f "$NESTED_LOG" ]; then
    echo "  FAIL: nested log file was not created"
    FAILURES=$((FAILURES + 1))
fi

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "LANE ESCALATE GATE TEST SUITE: $FAILURES failure(s)."
    exit 1
fi
echo ""
echo "LANE ESCALATE GATE TEST SUITE: all tests passed."
