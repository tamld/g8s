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

# Case 8: F1 symlink docs -> dir with .go files -> build
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-symlink-code main
    mkdir -p codelib
    echo "package codelib" > codelib/lib.go
    git add codelib
    git commit -q -m "add codelib"
    ln -s codelib docs
    git add docs
    git commit -q -m "symlink docs to codelib"
)
assert_lane "case 8: F1 symlink docs -> dir with .go files -> build" "lane=build" "$FIXTURE_REPO" "main"

# Case 9: F1 symlink with readlink -f unavailable -> build (deny-by-default)
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-symlink-unavailable main
    mkdir -p docdir
    echo "# Doc" > docdir/doc.md
    git add docdir
    git commit -q -m "add docdir"
    ln -s docdir docs
    git add docs
    git commit -q -m "symlink docs to docdir"
)
MOCK_BIN="$TMP_DIR/mock_readlink_bin"
mkdir -p "$MOCK_BIN"
cat > "$MOCK_BIN/readlink" << 'EOF'
#!/bin/sh
echo "readlink: illegal option -- f" >&2
exit 1
EOF
chmod +x "$MOCK_BIN/readlink"
echo "Testing: case 9: F1 symlink with readlink -f unavailable -> build"
output_c9=$(cd "$FIXTURE_REPO" && PATH="$MOCK_BIN:$PATH" bash "$DETECT_SCRIPT" "main" 2>&1) || true
if [ "$output_c9" != "lane=build" ]; then
    echo "  FAIL: expected stdout 'lane=build', got '$output_c9'"
    FAILURES=$((FAILURES + 1))
else
    echo "  ok (stdout: $output_c9)"
fi

# Case 10: F2 code file inside docs/ -> build
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-docs-evil-go main
    mkdir -p docs
    echo "package evil" > docs/evil.go
    git add docs/evil.go
    git commit -q -m "add docs/evil.go"
)
assert_lane "case 10: F2 code file inside docs/ -> build" "lane=build" "$FIXTURE_REPO" "main"

# Case 11: F2 non-prose extension inside docs/ -> build
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-docs-script-sh main
    mkdir -p docs
    echo "#!/bin/sh" > docs/script.sh
    git add docs/script.sh
    git commit -q -m "add docs/script.sh"
)
assert_lane "case 11: F2 non-prose extension inside docs/ -> build" "lane=build" "$FIXTURE_REPO" "main"

# Case 12: F2 prose extensions inside docs/ (.md, .markdown, .txt, .adoc) -> docs
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-docs-prose main
    mkdir -p docs
    echo "# Guide" > docs/guide.markdown
    echo "plain notes" > docs/notes.txt
    echo "= AsciiDoc Manual" > docs/manual.adoc
    git add docs/guide.markdown docs/notes.txt docs/manual.adoc
    git commit -q -m "add prose extension files in docs"
)
assert_lane "case 12: F2 prose extensions inside docs/ -> docs" "lane=docs" "$FIXTURE_REPO" "main"

# Case 13: F3 markdown inside .github/workflows/ -> build
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-github-md main
    mkdir -p .github/workflows
    echo "# Workflow README" > .github/workflows/README.md
    git add .github/workflows/README.md
    git commit -q -m "add markdown inside .github/workflows"
)
assert_lane "case 13: F3 markdown inside .github/workflows/ -> build" "lane=build" "$FIXTURE_REPO" "main"

# Case 14: Main push with base SHA: docs commit between before-SHA and HEAD -> docs
MAIN_PUSH_REPO="$TMP_DIR/main_push_repo"
mkdir -p "$MAIN_PUSH_REPO"
(
    cd "$MAIN_PUSH_REPO"
    git init -q
    git checkout -q -b main 2>/dev/null || git branch -m main
    git config user.email "ci-test@example.com"
    git config user.name "CI Tester"
    echo "init" > README.md
    git add README.md
    git commit -q -m "initial commit"
)
BEFORE_SHA_DOCS=$(cd "$MAIN_PUSH_REPO" && git rev-parse HEAD)
(
    cd "$MAIN_PUSH_REPO"
    mkdir -p docs
    echo "# New Guide" > docs/new_guide.md
    git add docs/new_guide.md
    git commit -q -m "docs push to main"
)
assert_lane "case 14: main push with base SHA (docs commit) -> docs" "lane=docs" "$MAIN_PUSH_REPO" "origin/main" "$BEFORE_SHA_DOCS"

# Case 15: Main push with base SHA: code commit between before-SHA and HEAD -> build
BEFORE_SHA_CODE=$(cd "$MAIN_PUSH_REPO" && git rev-parse HEAD)
(
    cd "$MAIN_PUSH_REPO"
    echo "package app" > app.go
    git add app.go
    git commit -q -m "code push to main"
)
assert_lane "case 15: main push with base SHA (code commit) -> build" "lane=build" "$MAIN_PUSH_REPO" "origin/main" "$BEFORE_SHA_CODE"

# Case 16: Main push with base SHA: empty before-range (before-SHA == HEAD) -> build (deny-by-default preserved)
HEAD_SHA=$(cd "$MAIN_PUSH_REPO" && git rev-parse HEAD)
assert_lane "case 16: main push with base SHA (empty before-range) -> build" "lane=build" "$MAIN_PUSH_REPO" "origin/main" "$HEAD_SHA"

# Case 17: Main push with all-zeros before-SHA: falls back to base ref
(
    cd "$FIXTURE_REPO"
    git checkout -q -b branch-allzeros main
    mkdir -p docs
    echo "# All Zeros Test" > docs/allzeros.md
    git add docs/allzeros.md
    git commit -q -m "add allzeros doc"
)
assert_lane "case 17: main push with all-zeros before-SHA falls back -> docs" "lane=docs" "$FIXTURE_REPO" "main" "0000000000000000000000000000000000000000"

echo ""
if [ "$FAILURES" -gt 0 ]; then
    echo "ci_lane_detect_test: $FAILURES test(s) failed."
    exit 1
fi

echo "ci_lane_detect_test: ALL tests passed."
exit 0
