#!/usr/bin/env bash
#
# Test suite for tools/ci_pr_contract.sh (S6-3a / #420 G5+G6)
# Validates the PR-contract gates:
#   (a) 250-line feat without ledger ref -> fail
#   (b) 250-line feat with 'plans/260927...' in body -> pass
#   (c) 150-line feat without ledger -> pass (G6 exempt)
#   (d) feat body without issue link -> fail (G5 violation)
#   (e) docs kind without anything -> pass (exempt)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE_SCRIPT="${SCRIPT_DIR}/ci_pr_contract.sh"

if [ ! -f "$GATE_SCRIPT" ]; then
    echo "ERROR: pr-contract gate script not found at $GATE_SCRIPT"
    exit 1
fi

echo "==> Running PR Contract Gate Test Suite..."

FAILURES=0

run_test() {
    local desc="$1"
    local expect="$2" # pass | fail
    shift 2
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$expect" = "pass" ] && [ "$exit_code" -ne 0 ]; then
        echo "  FAIL: expected pass, got exit $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    elif [ "$expect" = "fail" ] && [ "$exit_code" -eq 0 ]; then
        echo "  FAIL: expected fail, got success"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok"
    fi
}

# --- fixture: temporary body files -----------------------------------------

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

# Fixture A: 250-line feat body with issue link but NO plan ledger
FEAT_NO_LEDGER="$FIXTURE_ROOT/feat_no_ledger.md"
cat > "$FEAT_NO_LEDGER" <<'EOF'
### Description
Implements awesome feature.

Closes #101
EOF

# Fixture B: 250-line feat body with issue link AND plan ledger
FEAT_WITH_LEDGER="$FIXTURE_ROOT/feat_with_ledger.md"
cat > "$FEAT_WITH_LEDGER" <<'EOF'
### Description
Implements large feature according to plan.

Tracked in plans/260927-s6-3a-pr-contract-gates.md.

Closes #102
EOF

# Fixture C: 150-line feat body with issue link (no ledger needed)
FEAT_SMALL="$FIXTURE_ROOT/feat_small.md"
cat > "$FEAT_SMALL" <<'EOF'
### Description
Minor feature update.

Fixes #103
EOF

# Fixture D: feat body with NO issue link (even if it has a ledger)
FEAT_NO_ISSUE="$FIXTURE_ROOT/feat_no_issue.md"
cat > "$FEAT_NO_ISSUE" <<'EOF'
### Description
Feature update without linking issue.

Tracked in plans/260927-s6-3a-pr-contract-gates.md.
EOF

# Fixture for fix kind with Part of #
FIX_WITH_LEDGER="$FIXTURE_ROOT/fix_with_ledger.md"
cat > "$FIX_WITH_LEDGER" <<'EOF'
### Bugfix
Fixes edge case in worker supervisor.

Part of #104
Ledger: plans/260927-fix-plan.md
EOF

# --- tests -----------------------------------------------------------------

# (a) 250-line feat without ledger ref -> fail (G6 violation)
run_test "(a) 250-line feat without ledger ref fails" fail \
    "$GATE_SCRIPT" --kind feat --changed-lines 250 --body-file "$FEAT_NO_LEDGER"

# Verify exact G6 error message on failure
output=""
exit_code=0
output=$("$GATE_SCRIPT" --kind feat --changed-lines 250 --body-file "$FEAT_NO_LEDGER" 2>&1) || exit_code=$?
echo "Testing: (a) 250-line feat emits G6 error prefix"
if [ "$exit_code" -ne 1 ]; then
    echo "  FAIL: expected exit 1, got $exit_code"
    FAILURES=$((FAILURES + 1))
elif ! echo "$output" | grep -q "::error::G6: large feat/fix PR must reference a plan ledger"; then
    echo "  FAIL: expected G6 error message in output"
    echo "$output" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    echo "  ok"
fi

# (b) 250-line feat with 'plans/260927...' in body -> pass
run_test "(b) 250-line feat with plans/ ledger ref passes" pass \
    "$GATE_SCRIPT" --kind feat --changed-lines 250 --body-file "$FEAT_WITH_LEDGER"

# (c) 150-line feat without ledger -> pass (G6 exempt)
run_test "(c) 150-line feat without ledger passes (G6 exempt)" pass \
    "$GATE_SCRIPT" --kind feat --changed-lines 150 --body-file "$FEAT_SMALL"

# (d) feat body without issue link -> fail (G5 violation)
run_test "(d) feat body without issue link fails" fail \
    "$GATE_SCRIPT" --kind feat --changed-lines 150 --body-file "$FEAT_NO_ISSUE"

# Verify exact G5 error message on failure
output=""
exit_code=0
output=$("$GATE_SCRIPT" --kind feat --changed-lines 150 --body-file "$FEAT_NO_ISSUE" 2>&1) || exit_code=$?
echo "Testing: (d) feat without issue link emits G5 error prefix"
if [ "$exit_code" -ne 1 ]; then
    echo "  FAIL: expected exit 1, got $exit_code"
    FAILURES=$((FAILURES + 1))
elif ! echo "$output" | grep -q "::error::G5: PR must link its tracking issue"; then
    echo "  FAIL: expected G5 error message in output"
    echo "$output" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    echo "  ok"
fi

# (e) docs kind without anything -> pass
run_test "(e) docs kind without anything passes" pass \
    "$GATE_SCRIPT" --kind docs

# (e2) chore kind without anything -> pass
run_test "(e2) chore kind without anything passes" pass \
    "$GATE_SCRIPT" --kind chore

# Additional: fix kind with Part of # and ledger passes
run_test "fix kind with Part of # and ledger passes" pass \
    "$GATE_SCRIPT" --kind fix --changed-lines 300 --body-file "$FIX_WITH_LEDGER"

# Boundary check: 200 lines feat without ledger passes (<= 200 is exempt)
run_test "boundary 200 lines feat without ledger passes" pass \
    "$GATE_SCRIPT" --kind feat --changed-lines 200 --body-file "$FEAT_NO_LEDGER"

# Boundary check: 201 lines feat without ledger fails (> 200 requires ledger)
run_test "boundary 201 lines feat without ledger fails" fail \
    "$GATE_SCRIPT" --kind feat --changed-lines 201 --body-file "$FEAT_NO_LEDGER"

# Unknown argument fails with exit code 2
run_test "unknown argument exits with code 2" fail \
    "$GATE_SCRIPT" --unknown-flag

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "PR CONTRACT GATE TEST SUITE: $FAILURES failure(s)."
    exit 1
fi
echo ""
echo "PR CONTRACT GATE TEST SUITE: all tests passed."
