#!/usr/bin/env bash
#
# tools/pre_push_test.sh — Test suite for tools/pre_push.sh
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRE_PUSH="${REPO_ROOT}/tools/pre_push.sh"

echo "==> Testing tools/pre_push.sh harness..."

# Test 1: Help flag
echo "--> Test 1: Verify --help flag works..."
if ! "$PRE_PUSH" --help >/dev/null; then
    echo "FAIL: --help flag exited with non-zero"
    exit 1
fi
echo "  [PASS] --help flag works"

# Test 2: Fast mode on current codebase
echo "--> Test 2: Verify --fast mode passes on current clean codebase..."
if ! "$PRE_PUSH" --fast; then
    echo "FAIL: --fast mode failed on clean codebase"
    exit 1
fi
echo "  [PASS] --fast mode passed"

echo ""
echo "[ALL PRE-PUSH HARNESS TESTS PASSED]"
exit 0
