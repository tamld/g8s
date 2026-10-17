#!/usr/bin/env bash
#
# tools/ci_semgrep_check.sh — Offline Semgrep SAST Policy Gate for g8s
#
# Runs deterministic custom AST rules defined in .semgrep/g8s-rules.yml
# with zero cloud telemetry (--metrics=off). When the semgrep binary is
# unavailable on a minimal developer environment, falls back to a deterministic
# structural check of the same invariants so the gate never silently passes
# a violation.
#
# Usage:
#   bash tools/ci_semgrep_check.sh [target_dir]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RULES_FILE="${G8S_SEMGREP_RULES:-${REPO_ROOT}/.semgrep/g8s-rules.yml}"
TARGET_DIR="${1:-${REPO_ROOT}}"

export PATH="$HOME/.local/bin:/opt/homebrew/bin:$PATH"

if [ ! -f "$RULES_FILE" ]; then
    echo "::error::[semgrep-gate] Missing Semgrep ruleset at $RULES_FILE" >&2
    exit 1
fi

run_fallback_checks() {
    local target="$1"
    local failed=0

    # 1. g8s-zero-cgo-import
    local cgo_hits
    cgo_hits=$(find "$target" -name "*.go" ! -path "*/reference/*" ! -path "*/.git/*" -exec grep -HnE '^[[:space:]]*import[[:space:]]+("C"|"github\.com/mattn/go-sqlite3")' {} + 2>/dev/null || true)
    if [ -n "$cgo_hits" ]; then
        echo "::error::[g8s-zero-cgo-import] Prohibited CGO / mattn sqlite3 import detected:" >&2
        echo "$cgo_hits" >&2
        failed=1
    fi

    # 2. g8s-writefile-perm-0600
    local perm_hits
    perm_hits=$(find "$target" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" -exec grep -HnE 'os\.WriteFile\([^,]+,[^,]+,[[:space:]]*(0o?644|0o?666|0o?755)\)' {} + 2>/dev/null || true)
    if [ -n "$perm_hits" ]; then
        echo "::error::[g8s-writefile-perm-0600] Production os.WriteFile must use 0600 (0o600) or stricter:" >&2
        echo "$perm_hits" >&2
        failed=1
    fi

    # 3. g8s-http-listenandserve-no-timeout
    local http_hits
    http_hits=$(find "$target" -name "*.go" ! -name "*_test.go" ! -path "*/reference/*" ! -path "*/.git/*" -exec grep -HnF 'http.ListenAndServe(' {} + 2>/dev/null || true)
    if [ -n "$http_hits" ]; then
        echo "::error::[g8s-http-listenandserve-no-timeout] Direct http.ListenAndServe detected without ReadHeaderTimeout:" >&2
        echo "$http_hits" >&2
        failed=1
    fi

    # 4. g8s-no-silent-fs-error-swallow
    local swallow_hits
    swallow_hits=$(find "$target" \( -path "*/cmd/*" -o -path "*/internal/*" \) -name "*.go" ! -name "*_test.go" -exec grep -HnE '^[[:space:]]*(_|_,[[:space:]]*_)[[:space:]]*=[[:space:]]*os\.(WriteFile|Rename|OpenFile)\(' {} + 2>/dev/null || true)
    if [ -n "$swallow_hits" ]; then
        echo "::error::[g8s-no-silent-fs-error-swallow] Silent error discard on filesystem write/rename/open detected (Hint: return fmt.Errorf(...) or log a structured warning to os.Stderr):" >&2
        echo "$swallow_hits" >&2
        failed=1
    fi

    return "$failed"
}

if command -v semgrep >/dev/null 2>&1; then
    echo "==> Running Semgrep SAST gate (--config .semgrep/g8s-rules.yml --metrics=off)..."
    semgrep scan --config "$RULES_FILE" --error --metrics=off "$TARGET_DIR"
    echo "[SEMGREP-GATE] PASSED: Zero AST policy violations."
else
    echo "==> Running Semgrep fallback structural gate (semgrep binary not in PATH)..."
    if ! run_fallback_checks "$TARGET_DIR"; then
        exit 1
    fi
    echo "[SEMGREP-GATE] PASSED (fallback mode): Zero policy violations."
fi
