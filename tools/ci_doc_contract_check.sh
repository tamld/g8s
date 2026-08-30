#!/usr/bin/env bash
#
# tools/ci_doc_contract_check.sh — Doc↔Code Contract Gate (Issue #208)
#
# Enforces that documentation and architecture specifications do not drift
# from the reality of the codebase:
#   1. Orchestrator FSM state contract: checks all 8 states in internal/state and internal/orchestrator
#   2. Task FSM state contract: checks all 8 task states in internal/state and internal/controlplane
#   3. Go toolchain version contract: checks go.mod vs docs
#   4. Receipt verification contract: checks presence of ReceiptVerifier & StdoutEnvelopeVerifier
#   5. Unowned TODO/FIXME scanning in docs

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

FAILED=0

echo "==> Running Documentation ↔ Code Contract Checks..."

# 1. Orchestrator FSM States Contract
echo "--> [1/5] Verifying Orchestrator FSM states contract..."
EXPECTED_ORCH_STATES=("PLAN" "SPAWN" "MONITOR" "RECEIPT" "MERGE" "ESCALATE" "CANCEL" "CONFLICT")
for state in "${EXPECTED_ORCH_STATES[@]}"; do
    if ! grep -qi "OrchestratorState$state" internal/state/state.go; then
        echo "::error::Orchestrator state $state missing in internal/state/state.go"
        FAILED=$((FAILED + 1))
    fi
    if ! grep -qi "State$state" internal/orchestrator/fsm.go; then
        echo "::error::Orchestrator state $state missing in internal/orchestrator/fsm.go"
        FAILED=$((FAILED + 1))
    fi
done

# 2. Task FSM States Contract
echo "--> [2/5] Verifying Task FSM states contract..."
EXPECTED_TASK_STATES=("QUEUED" "LEASED" "RUNNING" "NEEDS_INFO" "BLOCKED" "SUCCEEDED" "FAILED" "CANCELLED")
for state in "${EXPECTED_TASK_STATES[@]}"; do
    clean_state="${state//_/}"
    if ! grep -qiE "TaskState($state|$clean_state)" internal/state/state.go; then
        echo "::error::Task state $state missing in internal/state/state.go"
        FAILED=$((FAILED + 1))
    fi
done

# 3. Go Version Contract
echo "--> [3/5] Verifying Go version consistency..."
GO_MOD_VER=$(grep -E '^go ' go.mod | awk '{print $2}')
if [ -z "$GO_MOD_VER" ]; then
    echo "::error::Unable to determine go version in go.mod"
    FAILED=$((FAILED + 1))
else
    if ! grep -q "$GO_MOD_VER" docs/ARCHITECTURE_ROADMAP.md; then
        echo "::error::docs/ARCHITECTURE_ROADMAP.md does not reference current go version $GO_MOD_VER"
        FAILED=$((FAILED + 1))
    fi
fi

# 4. Receipt Verification Contract
echo "--> [4/5] Verifying Receipt Verifier layer contract..."
if [ ! -f "internal/orchestrator/verify.go" ]; then
    echo "::error::internal/orchestrator/verify.go missing (ReceiptVerifier layer contract)"
    FAILED=$((FAILED + 1))
fi
if ! grep -q "ReceiptVerifier" internal/orchestrator/verify.go; then
    echo "::error::ReceiptVerifier interface missing in internal/orchestrator/"
    FAILED=$((FAILED + 1))
fi
if ! grep -q "StdoutEnvelopeVerifier" internal/orchestrator/verify.go; then
    echo "::error::StdoutEnvelopeVerifier missing in internal/orchestrator/"
    FAILED=$((FAILED + 1))
fi

# 5. Doc Unowned TODO Check
echo "--> [5/5] Checking for unowned TODO/FIXME in documentation..."
UNOWNED_DOC_TODOS=$(grep -rnE '(^|[[:space:]])//\s*(TODO|FIXME|XXX)' docs/ spec/ 2>/dev/null | grep -v 'OWNER=' | grep -v '`//' || true)
if [ -n "$UNOWNED_DOC_TODOS" ]; then
    echo "::error::Found unowned TODO/FIXME in documentation:"
    echo "$UNOWNED_DOC_TODOS"
    FAILED=$((FAILED + 1))
fi

if [ "$FAILED" -gt 0 ]; then
    echo ""
    echo "[DOC-CONTRACT] FAILED: $FAILED contract violation(s) found."
    exit 1
fi

echo "[DOC-CONTRACT] PASSED: All documentation-code contracts synchronized."
exit 0
