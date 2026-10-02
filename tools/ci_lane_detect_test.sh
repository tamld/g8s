#!/usr/bin/env bash
#
# tools/ci_lane_detect_test.sh — Test suite for tools/ci_lane_detect.sh (ADR-0031)
#
# Tests fixture git repositories in a temp directory:
#   1. docs-only diff -> docs
#   2. one .go file -> build
#   3. empty tree / diff -> build (deny-by-default)
#   4. nested docs path -> docs
#   5. mixed md+go -> build
#   6. local fallback (staged docs-only) -> docs
#   7. local fallback (staged mixed/go) -> build
#

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DETECT_SCRIPT="${SCRIPT_DIR}/ci_lane_detect.sh"

if [ ! -f "$DETECT_SCRIPT" ]; then
    echo "ERROR: detect script not found at $DETECT_SCRIPT"
    exit 1
fi

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/ci_lane_test_XXXXXX")
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

FAILURES=0

assert_lane() {
    local desc="$1"
    local expected="$2"
    local dir="$3"
    shift 3

    echo "Testing: $desc"
    local output exit_code=0
    output=$(cd "$dir" && bash "$DETECT_SCRIPT" "$@" 2>&1) || exit_code=$?

    if [ "$exit_code" -ne 0 ]; then
        echo "  FAIL: expected exit code 0, got $exit_code"
        echo "  output: $output"
        FAILURES=$((FAILURES + 1))
        return
    fi

    if [ "$output" != "$expected" ]; then
        echo "  FAIL: expected stdout '$expected', got '$output'"
        FAILURES=$((FAILURES + 1))
        return
    fi

    echo "  ok (stdout: $output)"
}

# Setup fixture git repository
FIXTURE_REPO="$TMP_DIR/repo"
mkdir -p "$FIXTURE_REPO"
(
    cd "$FIXTURE_REPO"
    git init -q
    git checkout -q -b main 2>/dev/null || git branch -m main
    git config user.email "ci-test@example.com"
    git config user.name "CI Tester"
    echo "init" > README.md
    git add README.md
    git commit -q -m "initial commit"
)

# Case 3: empty tree / empty diff -> build (deny-by-default)
assert_lane "case 3: empty tree / diff -> build" "lane=build" "$FIXTURE_REPO" "HEAD"

# Case 1: docs-only diff -> docs
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-docs
    mkdir -p docs
    echo "# Guide" > docs/guide.md
    git add docs/guide.md
    git commit -q -m "add docs guide"
)
assert_lane "case 1: docs-only diff -> docs" "lane=docs" "$FIXTURE_REPO" "main"

# Case 2: one .go file -> build
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-go main
    echo "package main" > main.go
    git add main.go
    git commit -q -m "add main.go"
)
assert_lane "case 2: one .go file -> build" "lane=build" "$FIXTURE_REPO" "main"

# Case 4: nested docs path -> docs
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-nested main
    mkdir -p plans/261002-factory
    echo "plan" > plans/261002-factory/plan.txt
    mkdir -p skills/supervisor/references
    echo "ref" > skills/supervisor/references/manual.txt
    mkdir -p assets/diagrams
    echo "svg" > assets/diagrams/arch.svg
    mkdir -p offer/pricing
    echo "pdf" > offer/pricing/tier.txt
    git add plans skills assets offer
    git commit -q -m "add nested docs/asset paths"
)
assert_lane "case 4: nested docs path -> docs" "lane=docs" "$FIXTURE_REPO" "main"

# Case 5: mixed md+go -> build
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-mixed main
    echo "# Note" > note.md
    echo "package foo" > foo.go
    git add note.md foo.go
    git commit -q -m "add mixed md and go"
)
assert_lane "case 5: mixed md+go -> build" "lane=build" "$FIXTURE_REPO" "main"

# Case 6: local fallback staged docs-only -> docs
LOCAL_REPO="$TMP_DIR/local_repo"
mkdir -p "$LOCAL_REPO"
(
    cd "$LOCAL_REPO"
    git init -q
    git config user.email "ci-test@example.com"
    git config user.name "CI Tester"
    echo "init" > README.md
    git add README.md
    git commit -q -m "initial commit"
    echo "# Local doc" > docs_local.md
    git add docs_local.md
)
assert_lane "case 6: local fallback staged docs-only -> docs" "lane=docs" "$LOCAL_REPO" "nonexistent_ref"

# Case 7: local fallback staged mixed -> build
(
    cd "$LOCAL_REPO"
    echo "package bar" > bar.go
    git add bar.go
)
assert_lane "case 7: local fallback staged mixed -> build" "lane=build" "$LOCAL_REPO" "nonexistent_ref"

echo ""
if [ "$FAILURES" -gt 0 ]; then
    echo "ci_lane_detect_test: $FAILURES test(s) failed."
    exit 1
fi

echo "ci_lane_detect_test: ALL tests passed."
exit 0
