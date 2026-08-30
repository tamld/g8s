#!/usr/bin/env bash
#
# tools/brief_lint_test.sh — Test suite for tools/brief_lint.sh (DEBT-51)
# Validates detection for:
#   - Rule A: supervisor_thinks (polling loops / manual intervention)
#   - Rule B: directive_brief (directive template vs v2 open-question)
#   - Rule C: missing_dual_blind (complex task keywords vs --blind-converge)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/brief_lint.sh"

if [ ! -f "$LINT_SCRIPT" ]; then
    echo "ERROR: Linter script not found at $LINT_SCRIPT"
    exit 1
fi

TMP_DIR="$(mktemp -d -t brief_lint_test_XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

echo "==> Setting up test fixtures in $TMP_DIR..."

mkdir -p "$TMP_DIR/rule_b"
mkdir -p "$TMP_DIR/rule_c"
mkdir -p "$TMP_DIR/rule_a/bad_cmd"
mkdir -p "$TMP_DIR/rule_a/good_cmd"

# Fixture B1: Directive brief without open questions
cat << 'EOF_B1' > "$TMP_DIR/rule_b/bad_directive_brief.md"
# Brief — Implement In-Memory Cache

## Goal
Implement LRU cache in internal/cache package.

## DoD
- [ ] In-memory cache is thread-safe
- [ ] Unit tests pass

## Constraints
- Max 100 entries
- Zero external dependencies
EOF_B1

# Fixture B2: Compliant v2 brief with open questions
cat << 'EOF_B2' > "$TMP_DIR/rule_b/good_v2_brief.md"
# Brief — Implement In-Memory Cache (Brief v2)

## Intent
Provide fast in-memory caching.

## Open Questions to Answer Before Writing Code
### Question 1: What eviction policy is needed?
Answer: LRU with mutex lock.

## DoD
- [x] Cache operations verified thread-safe

## Constraints
- Zero external dependencies
EOF_B2

# Fixture C1: Complex task mentioning "state machine" without --blind-converge
cat << 'EOF_C1' > "$TMP_DIR/rule_c/bad_complex_brief.md"
# Brief — Implement Worker Consensus State Machine

## Intent
Build a state machine managing worker state transitions.

## Open Questions
- What states are valid?

## DoD
- [x] State machine transitions verified
EOF_C1

# Fixture C2: Complex task with --blind-converge flag
cat << 'EOF_C2' > "$TMP_DIR/rule_c/good_complex_brief.md"
# Brief — Implement Worker Consensus State Machine

## Intent
Build a state machine managing worker state transitions with --blind-converge 2.

## Open Questions
- What states are valid?

## DoD
- [x] State machine transitions verified
EOF_C2

# Fixture A1: Supervisor code containing polling loop with time.Sleep
cat << 'EOF_A1' > "$TMP_DIR/rule_a/bad_cmd/bad_poll.go"
package main

import "time"

func PollLoop() {
	for {
		time.Sleep(1 * time.Second)
	}
}
EOF_A1

# Fixture A2: Clean supervisor code without polling loop
cat << 'EOF_A2' > "$TMP_DIR/rule_a/good_cmd/good_trigger.go"
package main

func HandleTrigger(event string) error {
	return nil
}
EOF_A2

echo "==> Running Rule B Tests (directive_brief)..."
# Test B1: Should fail and warn on directive pattern without open questions
out_b1=""
exit_b1=0
out_b1=$(bash "$LINT_SCRIPT" "$TMP_DIR/rule_b/bad_directive_brief.md" 2>&1) || exit_b1=$?

if [ "$exit_b1" -eq 0 ]; then
    echo "FAIL: Expected linter to fail on directive brief, but exited with 0"
    echo "$out_b1"
    exit 1
fi
if ! echo "$out_b1" | grep -q "directive_brief"; then
    echo "FAIL: Expected 'directive_brief' violation in output"
    echo "$out_b1"
    exit 1
fi
echo "  [PASS] Rule B detected directive brief violation as expected"

# Test B2: Should pass on v2 brief with open questions
out_b2=""
exit_b2=0
out_b2=$(bash "$LINT_SCRIPT" "$TMP_DIR/rule_b/good_v2_brief.md" 2>&1) || exit_b2=$?

if [ "$exit_b2" -ne 0 ]; then
    echo "FAIL: Expected linter to pass on v2 brief, but got exit code $exit_b2"
    echo "$out_b2"
    exit 1
fi
echo "  [PASS] Rule B passed compliant v2 open-question brief"

echo "==> Running Rule C Tests (missing_dual_blind)..."
# Test C1: Should fail on complex task ("state machine") without --blind-converge
out_c1=""
exit_c1=0
out_c1=$(bash "$LINT_SCRIPT" "$TMP_DIR/rule_c/bad_complex_brief.md" 2>&1) || exit_c1=$?

if [ "$exit_c1" -eq 0 ]; then
    echo "FAIL: Expected linter to fail on complex brief without --blind-converge, but exited with 0"
    echo "$out_c1"
    exit 1
fi
if ! echo "$out_c1" | grep -q "missing_dual_blind"; then
    echo "FAIL: Expected 'missing_dual_blind' violation in output"
    echo "$out_c1"
    exit 1
fi
echo "  [PASS] Rule C detected missing dual-blind flag on complex task"

# Test C2: Should pass on complex task with --blind-converge
out_c2=""
exit_c2=0
out_c2=$(bash "$LINT_SCRIPT" "$TMP_DIR/rule_c/good_complex_brief.md" 2>&1) || exit_c2=$?

if [ "$exit_c2" -ne 0 ]; then
    echo "FAIL: Expected linter to pass on complex brief with --blind-converge, but got exit code $exit_c2"
    echo "$out_c2"
    exit 1
fi
echo "  [PASS] Rule C passed complex brief with --blind-converge"

echo "==> Running Rule A Tests (supervisor_thinks)..."
# Test A1: Should fail on supervisor code polling in a loop
out_a1=""
exit_a1=0
out_a1=$(bash "$LINT_SCRIPT" "$TMP_DIR/rule_a/bad_cmd" 2>&1) || exit_a1=$?

if [ "$exit_a1" -eq 0 ]; then
    echo "FAIL: Expected linter to fail on supervisor polling loop, but exited with 0"
    echo "$out_a1"
    exit 1
fi
if ! echo "$out_a1" | grep -q "supervisor_thinks"; then
    echo "FAIL: Expected 'supervisor_thinks' violation in output"
    echo "$out_a1"
    exit 1
fi
echo "  [PASS] Rule A detected supervisor polling loop"

# Test A2: Should pass on clean supervisor code
out_a2=""
exit_a2=0
out_a2=$(bash "$LINT_SCRIPT" "$TMP_DIR/rule_a/good_cmd" 2>&1) || exit_a2=$?

if [ "$exit_a2" -ne 0 ]; then
    echo "FAIL: Expected linter to pass on clean supervisor code, but got exit code $exit_a2"
    echo "$out_a2"
    exit 1
fi
echo "  [PASS] Rule A passed clean trigger-based code"

echo ""
echo "[ALL BRIEF-LINT SELF-TESTS PASSED]"
exit 0
