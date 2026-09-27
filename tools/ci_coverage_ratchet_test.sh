#!/usr/bin/env bash
#
# Test suite for tools/ci_coverage_ratchet.sh (S6-2b / #420 G4)
# Validates the coverage ratchet gate:
#   - (a) baseline 82.0, current 81.5 -> exit 1 (below ratchet 0.25 tolerance)
#   - (b) baseline 82.0, current 81.8 -> exit 0 (within tolerance)
#   - (c) baseline 82.0, current 83.0 -> exit 0 AND prints upgrade suggestion
#   - (d) malformed baseline file -> exit 2
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE_SCRIPT="${SCRIPT_DIR}/ci_coverage_ratchet.sh"

if [ ! -f "$GATE_SCRIPT" ]; then
    echo "ERROR: coverage ratchet gate script not found at $GATE_SCRIPT"
    exit 1
fi

echo "==> Running Coverage Ratchet Gate Test Suite..."

FAILURES=0

run_test() {
    local desc="$1"
    local expect_code="$2"
    shift 2
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit $exit_code)"
    fi
}

run_test_with_output() {
    local desc="$1"
    local expect_code="$2"
    local pattern="$3"
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    elif ! echo "$output" | grep -q "$pattern"; then
        echo "  FAIL: output did not match pattern '$pattern'"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit $exit_code, pattern matched)"
    fi
}

# --- fixtures -------------------------------------------------------------

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

echo "82.0" > "$FIXTURE_ROOT/baseline_82.txt"
echo "  82.0  " > "$FIXTURE_ROOT/baseline_whitespace.txt"
echo "not-a-number" > "$FIXTURE_ROOT/baseline_malformed.txt"
touch "$FIXTURE_ROOT/baseline_empty.txt"

# --- test cases -----------------------------------------------------------

# (a) baseline 82.0, current 81.5 -> exit 1 (below ratchet 0.25 tolerance)
run_test "(a) baseline 82.0, current 81.5 fails (below ratchet 0.25 tolerance)" 1 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_82.txt" --current 81.5

# (b) baseline 82.0, current 81.8 -> exit 0 (within tolerance)
run_test "(b) baseline 82.0, current 81.8 passes (within tolerance)" 0 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_82.txt" --current 81.8

# (c) baseline 82.0, current 83.0 -> exit 0 AND prints an upgrade suggestion
run_test_with_output "(c) baseline 82.0, current 83.0 passes and prints upgrade suggestion" 0 \
    "consider raising baseline to 83.0" \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_82.txt" --current 83.0

# (d) malformed baseline file -> exit 2
run_test "(d) malformed baseline file exits 2" 2 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_malformed.txt" --current 82.0

# (e) empty baseline file -> exit 2
run_test "empty baseline file exits 2" 2 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_empty.txt" --current 82.0

# (f) non-existent baseline file -> exit 2
run_test "non-existent baseline file exits 2" 2 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/does_not_exist.txt" --current 82.0

# (g) malformed current coverage value -> exit 2
run_test "malformed current coverage value exits 2" 2 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_82.txt" --current "invalid"

# (h) missing arguments -> exit 2
run_test "missing arguments exits 2" 2 \
    "$GATE_SCRIPT"

# (i) baseline file with surrounding whitespace is parsed cleanly
run_test "baseline file with whitespace passes" 0 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_whitespace.txt" --current 82.0

# (j) current exactly at boundary (82.0 - 0.25 = 81.75) -> exit 0
run_test "current exactly at boundary 81.75 passes" 0 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_82.txt" --current 81.75

# (k) current just below boundary (81.74) -> exit 1
run_test "current just below boundary 81.74 fails" 1 \
    "$GATE_SCRIPT" --baseline "$FIXTURE_ROOT/baseline_82.txt" --current 81.74

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "COVERAGE RATCHET GATE TEST SUITE: $FAILURES failure(s)."
    exit 1
fi

echo ""
echo "COVERAGE RATCHET GATE TEST SUITE: all tests passed."
