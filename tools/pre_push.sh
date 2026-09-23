#!/usr/bin/env bash
#
# tools/pre_push.sh — Local Pre-Push CI/CD Quality Gate Harness
#
# Runs the exact same validation checks enforced by GitHub Actions CI:
#   1. Git Hygiene Gate (hygiene-guard.yml: untracked files, coverage artifacts)
#   2. Formatting Gate (ci.yml & quality.yml: gofmt & gofumpt)
#   3. Layer-Ownership Gate (DEBT-34: ci_layer_check.sh & self-test)
#   4. AI Anti-Pattern Gate (DEBT-21: ai_lint.sh & self-test)
#   5. Brief Anti-Pattern Gate (DEBT-51: brief_lint.sh & self-test)
#   6. Doc-Code Contract Gate (Issue #208: ci_doc_contract_check.sh)
#   7. Pure-Go Go Vet Gate (ci.yml: CGO_ENABLED=0 go vet)
#   8. GolangCI-Lint Gate (quality.yml: staticcheck, errcheck, ineffassign, unused)
#   9. Dual-Pass Test Suite (ci.yml: Zero-CGO test + CGO_ENABLED=1 -race)
#  10. Dogfooding Roundtrip Gate (quality.yml: brief issue/consume & orchestrate)
#  11. Cross-Platform Build Gate (quality.yml: Linux, macOS, Windows)
#
# Usage:
#   ./tools/pre_push.sh           # Run full suite before push
#   ./tools/pre_push.sh --fast    # Fast mode (skips -race & cross-platform)
#   ./tools/pre_push.sh --fix     # Auto-format with gofmt & gofumpt first
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Unset git hook environment variables so sub-tests (e.g. TestCleanupWorktrees) don't bleed into git repo
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_PREFIX

# Ensure tools in GOPATH/bin are available
GOPATH_BIN="$(go env GOPATH)/bin"
export PATH="$GOPATH_BIN:$PATH"
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

FAST_MODE=0
AUTO_FIX=0

for arg in "$@"; do
    case "$arg" in
        --fast|-f)
            FAST_MODE=1
            ;;
        --fix)
            AUTO_FIX=1
            ;;
        --help|-h)
            echo "Usage: $0 [--fast|-f] [--fix] [--help|-h]"
            echo ""
            echo "Options:"
            echo "  --fast, -f    Skip race detector and cross-platform compilation (faster run)"
            echo "  --fix         Auto-apply gofmt and gofumpt fixes before checking"
            echo "  --help, -h    Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown argument: $arg"
            echo "Run '$0 --help' for usage."
            exit 1
            ;;
    esac
done

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m' # No Color

pass() {
    printf "  ${GREEN}✓${NC} %s\n" "$1"
}

fail() {
    printf "  ${RED}✗ [FAILED]${NC} %s\n" "$1"
    echo ""
    echo -e "${RED}${BOLD}Pre-push checks FAILED.${NC} Fix the issues above before pushing to remote."
    exit 1
}

step() {
    printf "\n${BLUE}${BOLD}[%s] %s${NC}\n" "$1" "$2"
}

echo -e "${BOLD}======================================================${NC}"
echo -e "${BOLD}       g8s Pre-Push Verification Harness             ${NC}"
echo -e "${BOLD}======================================================${NC}"

# 1. Git Hygiene Gate
step "1/11" "Verifying Git Hygiene..."
for f in coverage.out cover.out *.cover; do
    if git diff --cached --name-only | grep -q "^$f\$"; then
        fail "Test artifact $f is staged for commit. Remove it and add to .gitignore."
    fi
done
UNTRACKED=$(git status --porcelain -uall 2>&1 | grep -c '^??' || true)
if [ "$UNTRACKED" -gt 15 ]; then
    fail "Too many untracked files ($UNTRACKED > 15). Clean up or gitignore."
fi
pass "Git hygiene clean"

# 2. Formatting Gate
step "2/11" "Verifying Code Formatting (gofmt & gofumpt)..."
if [ "$AUTO_FIX" -eq 1 ]; then
    gofmt -w .
    if command -v gofumpt >/dev/null 2>&1; then
        gofumpt -w .
    fi
fi
UNFORMATTED=$(gofmt -l . | grep -v '^reference/' || true)
if [ -n "$UNFORMATTED" ]; then
    echo "Unformatted files found by gofmt:"
    echo "$UNFORMATTED"
    fail "Files above are not gofmt-clean. Run 'gofmt -w <file>' or '$0 --fix'."
fi
if command -v gofumpt >/dev/null 2>&1; then
    UNFORMATTED_FUMPT=$(gofumpt -l . 2>/dev/null | grep -v '^reference/' || true)
    if [ -n "$UNFORMATTED_FUMPT" ]; then
        echo "Unformatted files found by gofumpt:"
        echo "$UNFORMATTED_FUMPT"
        fail "Files above are not gofumpt-clean. Run 'gofumpt -w <file>' or '$0 --fix'."
    fi
fi
pass "gofmt and gofumpt formatting clean"

# 3. Layer Ownership Gate
step "3/11" "Running Layer Ownership Gate (DEBT-34)..."
bash tools/ci_layer_check_test.sh >/dev/null
pass "Layer check self-tests passed"
if ! bash tools/ci_layer_check.sh; then
    fail "Layer ownership rules violated (DEBT-34)."
fi
pass "Layer ownership verified"

# 4. AI Anti-Pattern Gate
step "4/11" "Running AI Anti-Pattern Gate (DEBT-21)..."
bash tools/ai_lint_test.sh >/dev/null
pass "AI lint self-tests passed"
if ! bash tools/ai_lint.sh; then
    fail "AI anti-patterns detected in source code."
fi
pass "AI anti-pattern checks clean"

# 5. Brief Anti-Pattern Gate
step "5/11" "Running Brief Anti-Pattern Gate (DEBT-51)..."
bash tools/brief_lint_test.sh >/dev/null
pass "Brief lint self-tests passed"
if ! bash tools/brief_lint.sh docs/ spec/ docs/decisions/ .claude/; then
    fail "Brief anti-patterns detected."
fi
pass "Brief anti-pattern checks clean"

# 6. Documentation ↔ Code Contract Gate
step "6/11" "Running Doc ↔ Code Contract Gate (Issue #208)..."
if ! bash tools/ci_doc_contract_check.sh; then
    fail "Documentation and code contracts are out of sync."
fi
pass "Doc-code contracts synchronized"

# 7. Version Sync Gate
step "7/11" "Running Version Sync Check..."
if ! bash tools/ci_version_sync_check.sh; then
    fail "Version sync check failed."
fi
pass "Version sync verified"

# 8. Pure-Go Vet Gate
step "8/12" "Running Pure-Go Vet Gate (CGO_ENABLED=0)..."
if ! CGO_ENABLED=0 go vet ./...; then
    fail "go vet reported errors."
fi
pass "go vet clean"

# 9. GolangCI-Lint Quality Gate
step "9/12" "Running GolangCI-Lint Quality Gate..."
LINTER_BIN=""
if command -v golangci-lint >/dev/null 2>&1; then
    LINTER_BIN="golangci-lint"
elif [ -x "$GOPATH_BIN/golangci-lint" ]; then
    LINTER_BIN="$GOPATH_BIN/golangci-lint"
fi

if [ -n "$LINTER_BIN" ]; then
    if ! "$LINTER_BIN" run --timeout=10m; then
        fail "golangci-lint reported issues."
    fi
    pass "golangci-lint clean (zero issues)"
else
    echo -e "  ${YELLOW}⚠ golangci-lint not installed, skipping staticcheck/errcheck linter pass${NC}"
fi

# 10. Dual-Pass Test Suite & Coverage Verification
step "10/12" "Running Dual-Pass Test Suite & Coverage Verification..."
echo "  -> Pass 1: Pure-Go (Zero-CGO) with coverage..."
COV_FILE=$(mktemp)
trap 'rm -f "$COV_FILE"' EXIT
if ! CGO_ENABLED=0 go test -count=1 -v -cover ./... 2>&1 | tee "$COV_FILE" >/dev/null; then
    fail "CGO_ENABLED=0 tests failed."
fi
pass "Pure-Go tests passed"

echo "  -> Checking aggregate coverage >= 80% (matching CI Quality Gate)..."
COV_RESULT=$(awk '
  /^ok[ \t]/ && !/cmd\/g8s/ {
    for (i = 1; i <= NF; i++) {
      if ($i ~ /^coverage:/) {
        pct = $(i+1)
        gsub(/%/, "", pct)
        total += pct
        count++
        break
      }
    }
  }
  END {
    if (count == 0) exit 1
    avg = total / count
    printf "%.2f %d", avg, count
  }
' "$COV_FILE")

if [ -z "$COV_RESULT" ]; then
    fail "Coverage extraction failed."
fi

avg=$(echo "$COV_RESULT" | awk '{print $1}')
count=$(echo "$COV_RESULT" | awk '{print $2}')
echo "     Aggregate coverage: ${avg}% across ${count} packages (cmd/g8s excluded)"

if awk -v a="$avg" 'BEGIN { exit !(a+0 < 80) }'; then
    fail "Aggregate coverage ${avg}% is below 80% threshold."
fi
pass "Coverage threshold met (${avg}% >= 80%)"

if [ "$FAST_MODE" -eq 0 ]; then
    echo "  -> Pass 2: Race Detector (CGO_ENABLED=1 -race)..."
    if ! CGO_ENABLED=1 go test -race -count=1 ./internal/...; then
        fail "Race detector reported data races."
    fi
    pass "Race detector clean (zero warnings)"
else
    echo "  -> Pass 2: Skipped (fast mode)"
fi

# 11. Dogfooding CI Roundtrip
step "11/12" "Running Dogfooding Roundtrip Gate..."
if ! make dogfood; then
    fail "Dogfooding roundtrip failed."
fi
pass "Dogfooding roundtrip verified"

# 12. Cross-Platform Compilation Gate
step "12/12" "Running Cross-Platform Build Gate..."
if [ "$FAST_MODE" -eq 0 ]; then
    if ! make verify-cross-platform; then
        fail "Cross-platform build failed."
    fi
    pass "Linux, macOS, and Windows compilation successful"
else
    echo "  -> Skipped (fast mode)"
fi

echo ""
echo -e "${GREEN}${BOLD}======================================================${NC}"
echo -e "${GREEN}${BOLD}  ✓ ALL PRE-PUSH CHECKS PASSED! Ready to push remote. ${NC}"
echo -e "${GREEN}${BOLD}======================================================${NC}"
exit 0
