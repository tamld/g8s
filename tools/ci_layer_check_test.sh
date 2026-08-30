#!/usr/bin/env bash
#
# Test suite for tools/ci_layer_check.sh (DEBT-34 / Issue #126)
# Validates layer ownership rules and disjoint layer violation detection.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LAYER_SCRIPT="${SCRIPT_DIR}/ci_layer_check.sh"

if [ ! -f "$LAYER_SCRIPT" ]; then
    echo "ERROR: Layer check script not found at $LAYER_SCRIPT"
    exit 1
fi

echo "==> Running Layer Ownership Gate Test Suite..."

run_test() {
    local desc="$1"
    local changed_files="$2"
    local expect_pass="$3"
    local expected_substr="$4"

    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$(CHANGED_FILES="$changed_files" bash "$LAYER_SCRIPT" 2>&1) || exit_code=$?

    if [ "$expect_pass" = "true" ]; then
        if [ "$exit_code" -ne 0 ]; then
            echo "  [FAIL] Expected test to pass, but got exit code $exit_code"
            echo "  Output: $output"
            exit 1
        fi
        if [ -n "$expected_substr" ] && ! echo "$output" | grep -q "$expected_substr"; then
            echo "  [FAIL] Expected output to contain '$expected_substr'"
            echo "  Output: $output"
            exit 1
        fi
        echo "  [PASS] Passed as expected ($output)"
    else
        if [ "$exit_code" -eq 0 ]; then
            echo "  [FAIL] Expected test to fail, but got exit code 0"
            echo "  Output: $output"
            exit 1
        fi
        if [ -n "$expected_substr" ] && ! echo "$output" | grep -q "$expected_substr"; then
            echo "  [FAIL] Expected output to contain '$expected_substr'"
            echo "  Output: $output"
            exit 1
        fi
        echo "  [PASS] Failed as expected ($output)"
    fi
}

# 1. Standalone single layer PRs
run_test "Single layer: orchestrator only" \
    "internal/orchestrator/service.go" \
    "true" \
    "Layer check: OK (S=1 W=0 C=0)"

run_test "Single layer: worker only" \
    "internal/worker/runner.go" \
    "true" \
    "Layer check: OK (S=0 W=1 C=0)"

run_test "Single layer: CLI only" \
    "cmd/g8s/main.go" \
    "true" \
    "Layer check: OK (S=0 W=0 C=1)"

run_test "Single layer: controlplane only (shared layer)" \
    "internal/controlplane/db.go" \
    "true" \
    "Layer check: OK (S=0 W=0 C=0)"

run_test "Docs / tools only (no restricted layer touched)" \
    "docs/ARCHITECTURE_ROADMAP.md
tools/ci_layer_check.sh" \
    "true" \
    "Layer check: OK (S=0 W=0 C=0)"

# 2. Allowed multi-layer PRs (False positive prevention)
run_test "Allowed: orchestrator + controlplane (shared DB layer)" \
    "internal/orchestrator/service.go
internal/controlplane/db.go" \
    "true" \
    "Layer check: OK (S=1 W=0 C=0)"

run_test "Allowed: worker + controlplane (shared DB layer)" \
    "internal/worker/runner.go
internal/controlplane/db.go" \
    "true" \
    "Layer check: OK (S=0 W=1 C=0)"

run_test "Allowed: orchestrator + cmd/g8s (supervisor can edit CLI)" \
    "internal/orchestrator/service.go
cmd/g8s/main.go" \
    "true" \
    "Layer check: OK (S=1 W=0 C=1)"

run_test "Allowed: orchestrator + controlplane + cmd/g8s" \
    "internal/orchestrator/service.go
internal/controlplane/db.go
cmd/g8s/main.go" \
    "true" \
    "Layer check: OK (S=1 W=0 C=1)"

# 3. Disjoint layer violations (True positives)
run_test "Disjoint: orchestrator + worker" \
    "internal/orchestrator/service.go
internal/worker/runner.go" \
    "false" \
    "PR touches both internal/orchestrator/ and internal/worker/"

run_test "Disjoint: worker + cmd/g8s" \
    "internal/worker/runner.go
cmd/g8s/main.go" \
    "false" \
    "PR touches both internal/worker/ and cmd/g8s/"

run_test "Disjoint: worker + orchestrator + cmd/g8s" \
    "internal/worker/runner.go
internal/orchestrator/service.go
cmd/g8s/main.go" \
    "false" \
    "disjoint layer ownership violation"

echo ""
echo "[ALL LAYER CHECK TESTS PASSED]"
exit 0
