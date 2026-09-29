#!/usr/bin/env bash
#
# Test suite for tools/claims_check.sh (#447)
# Validates quantitative claims registry checker:
#   (a) registry with valid bindings -> exit 0
#   (b) registry with a broken test name -> exit 1 + offending id printed
#   (c) registry with a broken source path -> exit 1 + offending id printed
#   (d) unbound aspirations are counted without failing
#   (e) committed docs/claims.yml registry passes cleanly
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CHECK_SCRIPT="${SCRIPT_DIR}/claims_check.sh"

if [ ! -f "$CHECK_SCRIPT" ]; then
    echo "ERROR: claims checker script not found at $CHECK_SCRIPT"
    exit 1
fi

echo "==> Running Claims Check Gate Test Suite..."

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
        echo "  ok (exit $exit_code, pattern matched: '$pattern')"
    fi
}

# --- fixtures -------------------------------------------------------------

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

# Fixture (a): valid bindings
cat > "$FIXTURE_ROOT/valid.yml" <<'EOF'
- id: claim-valid-attempt
  claim: "Valid test binding"
  evidence:
    test: TestReleasePreservesDirtyWorktree
    eval: ""
    source: docs/OPERATIONS.md
EOF

# Fixture (b): broken test name
cat > "$FIXTURE_ROOT/broken_test.yml" <<'EOF'
- id: claim-broken-test-id
  claim: "Broken test binding"
  evidence:
    test: TestDoesNotExistInRepoAnywhere
    eval: ""
    source: docs/OPERATIONS.md
EOF

# Fixture (c): broken source path
cat > "$FIXTURE_ROOT/broken_source.yml" <<'EOF'
- id: claim-broken-source-id
  claim: "Broken source binding"
  evidence:
    test: TestReleasePreservesDirtyWorktree
    eval: ""
    source: docs/non_existent_doc_file.md
EOF

# Fixture (d): mixed bound and unbound aspirations
cat > "$FIXTURE_ROOT/with_unbound.yml" <<'EOF'
- id: claim-bound-item
  claim: "Bound claim"
  evidence:
    test: TestReleasePreservesDirtyWorktree
    eval: ""
    source: docs/OPERATIONS.md

unbound:
  - id: claim-aspiration-item
    claim: "Aspirational unbound claim"
    evidence:
      test: ""
      eval: ""
      source: docs/OPERATIONS.md
EOF

# --- test cases -----------------------------------------------------------

# (a) registry with valid bindings -> exit 0
run_test_with_output \
    "(a) registry with valid bindings passes and outputs SCORECARD" 0 \
    "SCORECARD: 1 bound, 0 unbound, 0 broken (total 1 claims)" \
    bash "$CHECK_SCRIPT" --file "$FIXTURE_ROOT/valid.yml" --root "$REPO_ROOT"

# (b) registry with a broken test name -> exit 1 + the offending id printed
run_test_with_output \
    "(b) registry with broken test name exits 1 and prints offending id" 1 \
    "claim-broken-test-id" \
    bash "$CHECK_SCRIPT" --file "$FIXTURE_ROOT/broken_test.yml" --root "$REPO_ROOT"

# (c) registry with a broken source path -> exit 1 + the offending id printed
run_test_with_output \
    "(c) registry with broken source path exits 1 and prints offending id" 1 \
    "claim-broken-source-id" \
    bash "$CHECK_SCRIPT" --file "$FIXTURE_ROOT/broken_source.yml" --root "$REPO_ROOT"

# (d) registry with unbound aspirations -> exit 0 and counts unbound correctly
run_test_with_output \
    "(d) registry with unbound aspirations passes with correct counts" 0 \
    "SCORECARD: 1 bound, 1 unbound, 0 broken (total 2 claims)" \
    bash "$CHECK_SCRIPT" --file "$FIXTURE_ROOT/with_unbound.yml" --root "$REPO_ROOT"

# (e) committed docs/claims.yml registry passes cleanly
run_test_with_output \
    "(e) committed docs/claims.yml passes cleanly" 0 \
    "SCORECARD: 7 bound, 3 unbound, 0 broken (total 10 claims)" \
    bash "$CHECK_SCRIPT"

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "CLAIMS CHECK TEST SUITE: $FAILURES failure(s)."
    exit 1
fi

echo ""
echo "CLAIMS CHECK TEST SUITE: all tests passed."
exit 0
