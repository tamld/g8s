#!/usr/bin/env bash
#
# Test suite for tools/ci_lane_router.sh (S6-3b / #420, ADR-0024 Layer 1)
# Validates the Layer-1 deterministic lane router:
#   (a) internal/worker/proc_windows.go -> S
#   (b) docs/README.md only -> D
#   (c) tools/x.go 250 lines no trust files -> F
#   (d) tools/x.go 50 lines -> R
#   (e) mixed trust+docs -> S wins
#   (f) boundary at lines=200 -> R, lines=201 -> F
#   (g) root and spec *.md files -> D
#   (h) overrides file support (e.g. lane: 0)
#   (i) argument validation and exit code 2 on bad args
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROUTER_SCRIPT="${SCRIPT_DIR}/ci_lane_router.sh"

if [ ! -f "$ROUTER_SCRIPT" ]; then
    echo "ERROR: lane router script not found at $ROUTER_SCRIPT"
    exit 1
fi

echo "==> Running Lane Router Gate Test Suite..."

FAILURES=0

run_test() {
    local desc="$1"
    local expect_code="$2" # 0, 2, etc.
    local expect_output="$3" # expected stdout or pattern
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>/dev/null) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit $expect_code, got $exit_code"
        echo "  Output: $output"
        FAILURES=$((FAILURES + 1))
    elif [ -n "$expect_output" ] && [ "$output" != "$expect_output" ]; then
        echo "  FAIL: expected stdout '$expect_output', got '$output'"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok"
    fi
}

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

# Create a sample trust-boundaries.yml in fixture
mkdir -p "$FIXTURE_ROOT/.g8s"
cat > "$FIXTURE_ROOT/.g8s/trust-boundaries.yml" <<'EOF'
trust_boundaries:
  - "internal/receipt/*"
  - "internal/worker/proc_*"
  - "internal/server/*"
  - "internal/harness/*"
  - "internal/memory/*"
  - "internal/dispatch/*"
EOF

TB_FLAG="--trust-boundaries $FIXTURE_ROOT/.g8s/trust-boundaries.yml"

# --- 1. Required fixture suite test cases (a) - (e) ---

# (a) internal/worker/proc_windows.go -> S
run_test "fixture (a): internal/worker/proc_windows.go -> S" 0 "S" \
    "$ROUTER_SCRIPT" --files "internal/worker/proc_windows.go" --lines 50 $TB_FLAG

# (b) docs/README.md only -> D
run_test "fixture (b): docs/README.md only -> D" 0 "D" \
    "$ROUTER_SCRIPT" --files "docs/README.md" --lines 10 $TB_FLAG

# (c) tools/x.go 250 lines no trust files -> F
run_test "fixture (c): tools/x.go 250 lines no trust files -> F" 0 "F" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines 250 $TB_FLAG

# (d) tools/x.go 50 lines -> R
run_test "fixture (d): tools/x.go 50 lines -> R" 0 "R" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines 50 $TB_FLAG

# (e) mixed trust+docs -> S wins
run_test "fixture (e): mixed trust+docs -> S wins" 0 "S" \
    "$ROUTER_SCRIPT" --files "internal/worker/proc_windows.go,docs/README.md" --lines 10 $TB_FLAG

# --- 2. Additional trust boundary coverage ---

run_test "receipt trust boundary -> S" 0 "S" \
    "$ROUTER_SCRIPT" --files "internal/receipt/receipt.go" --lines 10 $TB_FLAG

run_test "server auth surface trust boundary -> S" 0 "S" \
    "$ROUTER_SCRIPT" --files "internal/server/server.go" --lines 10 $TB_FLAG

run_test "harness trust boundary -> S" 0 "S" \
    "$ROUTER_SCRIPT" --files "internal/harness/roles.go" --lines 10 $TB_FLAG

run_test "memory trust boundary -> S" 0 "S" \
    "$ROUTER_SCRIPT" --files "internal/memory/vector.go" --lines 10 $TB_FLAG

run_test "dispatch sanitizer trust boundary -> S" 0 "S" \
    "$ROUTER_SCRIPT" --files "internal/dispatch/verify.go" --lines 10 $TB_FLAG

# --- 3. Additional doc coverage ---

run_test "root README.md -> D" 0 "D" \
    "$ROUTER_SCRIPT" --files "README.md" --lines 10 $TB_FLAG

run_test "spec delta markdown -> D" 0 "D" \
    "$ROUTER_SCRIPT" --files "spec/openspec/01-receipt.md" --lines 10 $TB_FLAG

run_test "multiple docs files -> D" 0 "D" \
    "$ROUTER_SCRIPT" --files "docs/guide.md,docs/sub/topic.md,AGENTS.md" --lines 100 $TB_FLAG

# --- 4. Line count boundary conditions ---

run_test "boundary lines=200 -> R" 0 "R" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines 200 $TB_FLAG

run_test "boundary lines=201 -> F" 0 "F" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines 201 $TB_FLAG

run_test "zero lines non-doc -> R" 0 "R" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines 0 $TB_FLAG

# --- 5. Overrides file support ---

HOTFIX_OVERRIDE="$FIXTURE_ROOT/hotfix_override.yml"
cat > "$HOTFIX_OVERRIDE" <<'EOF'
lane: 0
EOF
run_test "override lane 0 (hotfix)" 0 "0" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines 500 --overrides "$HOTFIX_OVERRIDE" $TB_FLAG

SECURITY_OVERRIDE="$FIXTURE_ROOT/sec_override.yml"
cat > "$SECURITY_OVERRIDE" <<'EOF'
lane: S
EOF
run_test "override lane S" 0 "S" \
    "$ROUTER_SCRIPT" --files "docs/README.md" --lines 5 --overrides "$SECURITY_OVERRIDE" $TB_FLAG

# --- 6. Argument validation and error handling ---

run_test "missing --files fails with code 2" 2 "" \
    "$ROUTER_SCRIPT" --lines 50

run_test "missing --lines fails with code 2" 2 "" \
    "$ROUTER_SCRIPT" --files "tools/x.go"

run_test "non-numeric --lines fails with code 2" 2 "" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines "two-hundred"

run_test "negative --lines fails with code 2" 2 "" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines "-5"

run_test "unknown flag fails with code 2" 2 "" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines 10 --invalid-flag

run_test "nonexistent overrides file fails with code 2" 2 "" \
    "$ROUTER_SCRIPT" --files "tools/x.go" --lines 10 --overrides "$FIXTURE_ROOT/nonexistent.yml"

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "LANE ROUTER GATE TEST SUITE: $FAILURES failure(s)."
    exit 1
fi
echo ""
echo "LANE ROUTER GATE TEST SUITE: all tests passed."
