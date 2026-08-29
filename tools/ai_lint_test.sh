#!/usr/bin/env bash
#
# Test suite for tools/ai_lint.sh (DEBT-21 / Issue #87)
# Validates that all 5 AI anti-pattern checks trigger on violations
# and pass on compliant Go code.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/ai_lint.sh"

if [ ! -f "$LINT_SCRIPT" ]; then
    echo "ERROR: Linter script not found at $LINT_SCRIPT"
    exit 1
fi

TMP_DIR="$(mktemp -d -t ai_lint_test_XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

echo "==> Setting up test fixtures in $TMP_DIR..."

mkdir -p "$TMP_DIR/internal/badpkg"
mkdir -p "$TMP_DIR/internal/goodpkg"

# 1. Create a fixture containing all 5 anti-patterns
cat << 'EOF' > "$TMP_DIR/internal/badpkg/bad.go"
package badpkg

import (
	"fmt"
	"os"
)

// Certainly! I am an AI language model generated helper.
func AntiPatternExamples(raw any) {
	// 1. check_no_panic
	panic("panic used for control flow")

	// 2. check_no_ignored_errors
	f, _ := os.Open("file.txt")
	_ = f.Close()

	defer func() { _ = f.Close() }()

	// 3. check_no_type_assertion_in_library (unchecked downcast)
	strVal := raw.(string)
	fmt.Println(strVal)

	// 4. check_todo_owner (TODO without OWNER=)
	// TODO: fix this unassigned debt later

	// 5. check_no_ai_artifacts
	// I hope this helps!
}
EOF

# 2. Create a clean fixture following all idioms and constitution rules
cat << 'EOF' > "$TMP_DIR/internal/goodpkg/good.go"
package goodpkg

import (
	"errors"
	"fmt"
	"os"
)

// Valid code following g8s standards.
func GoodExamples(raw any) error {
	// 1. Explicit error return instead of panic
	if raw == nil {
		return errors.New("raw cannot be nil")
	}

	// 2. Clean defer without error swallowing
	f, err := os.Open("file.txt")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	// 3. Checked type assertion with comma-ok idiom & type switch
	if strVal, ok := raw.(string); ok {
		_ = strVal
	}

	switch v := raw.(type) {
	case int:
		_ = v
	default:
	}

	// 4. TODO with explicit OWNER annotation
	// TODO(OWNER=tamld): add metric instrumentation

	return nil
}
EOF

echo "==> Test 1: Assert linter fails on dirty fixture and catches all 5 anti-patterns..."
output=""
exit_code=0
output=$(bash "$LINT_SCRIPT" "$TMP_DIR/internal/badpkg" 2>&1) || exit_code=$?

if [ "$exit_code" -eq 0 ]; then
    echo "FAIL: Expected linter to fail on bad fixture, but it exited with 0."
    echo "$output"
    exit 1
fi

# Assert all 5 checks were triggered
assert_check_triggered() {
    local check_name="$1"
    if echo "$output" | grep -q "$check_name"; then
        echo "  [PASS] $check_name detected violation"
    else
        echo "  [FAIL] $check_name failed to detect violation in bad fixture!"
        echo "Full output:"
        echo "$output"
        exit 1
    fi
}

assert_check_triggered "check_no_panic"
assert_check_triggered "check_no_ignored_errors"
assert_check_triggered "check_no_type_assertion_in_library"
assert_check_triggered "check_todo_owner"
assert_check_triggered "check_no_ai_artifacts"

echo "==> Test 2: Assert linter passes cleanly on compliant fixture..."
clean_output=""
clean_exit=0
clean_output=$(bash "$LINT_SCRIPT" "$TMP_DIR/internal/goodpkg" 2>&1) || clean_exit=$?

if [ "$clean_exit" -ne 0 ]; then
    echo "FAIL: Expected clean fixture to pass, but got exit code $clean_exit"
    echo "$clean_output"
    exit 1
fi
echo "  [PASS] Compliant fixture passed with exit code 0"

echo "==> Test 3: Assert test files (*_test.go) are exempt from linter checks..."
cat << 'EOF' > "$TMP_DIR/internal/goodpkg/exempt_test.go"
package goodpkg

import "testing"

func TestExemptions(t *testing.T) {
	// Test files are permitted to panic / assert / ignore errors
	panic("test panic")
	_ = t
}
EOF

test_exempt_output=""
test_exempt_exit=0
test_exempt_output=$(bash "$LINT_SCRIPT" "$TMP_DIR/internal/goodpkg" 2>&1) || test_exempt_exit=$?

if [ "$test_exempt_exit" -ne 0 ]; then
    echo "FAIL: Test files should be exempt from linter, but got exit code $test_exempt_exit"
    echo "$test_exempt_output"
    exit 1
fi
echo "  [PASS] Test file exemptions honored"

echo ""
echo "[ALL SELF-TESTS PASSED]"
exit 0
