#!/usr/bin/env bash
#
# tools/worker_boundary_test.sh — Unit Tests for AGY Worker Scope Boundary Enforcement
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$REPO_ROOT/tools/worker_boundary.sh"
chmod +x "$SCRIPT"

FAILED=0

assert_forbidden() {
    local cmd="$1"
    local desc="$2"
    if "$SCRIPT" check "$cmd" >/dev/null 2>&1; then
        echo "FAIL: Expected command to be FORBIDDEN, but was permitted: $desc ('$cmd')"
        FAILED=$((FAILED + 1))
    else
        echo "PASS: Correctly blocked: $desc"
    fi
}

assert_permitted() {
    local cmd="$1"
    local desc="$2"
    if ! "$SCRIPT" check "$cmd" >/dev/null 2>&1; then
        echo "FAIL: Expected command to be PERMITTED, but was blocked: $desc ('$cmd')"
        FAILED=$((FAILED + 1))
    else
        echo "PASS: Correctly permitted: $desc"
    fi
}

echo "==> Running worker_boundary.sh unit tests..."

# Forbidden commands
assert_forbidden "git push origin main" "Push to main"
assert_forbidden "git push --force origin main" "Force push to main"
assert_forbidden "git push -f origin main" "Force push (-f) to main"
assert_forbidden "gh pr merge --auto 123" "Auto-merge PR via gh"
assert_forbidden "gh pr merge 123 --auto" "Auto-merge PR via gh (trailing flag)"
assert_forbidden "gh api -X PATCH /repos/owner/repo/branches/main/protection" "Modify branch protection"
assert_forbidden "git branch -D feat/foo" "Force-delete branch with -D"
assert_forbidden "rm -rf /tmp/something" "rm -rf in /tmp"
assert_forbidden "rm -rf /" "rm -rf root"
assert_forbidden "rm -rf ../outside" "rm -rf outside cwd"

# Permitted commands
assert_permitted "git push origin feat/my-branch" "Push to feature branch"
assert_permitted "git push origin fix/bug-123" "Push to fix branch"
assert_permitted "gh pr create --title 'Fix bug' --body 'Details'" "Open PR via gh"
assert_permitted "gh pr view 123" "View PR via gh"
assert_permitted "git branch -d feat/foo" "Safe-delete branch with -d"
assert_permitted "go test ./..." "Run go test"
assert_permitted "rm -rf build/" "rm -rf in local build dir"

# Test install-hooks
TEMP_GIT=$(mktemp -d)
git -C "$TEMP_GIT" init -q
"$SCRIPT" install-hooks "$TEMP_GIT" >/dev/null
if [ -f "$TEMP_GIT/.git/hooks/pre-push" ] && [ -x "$TEMP_GIT/.git/hooks/pre-push" ]; then
    echo "PASS: Installed executable pre-push hook successfully"
else
    echo "FAIL: pre-push hook was not installed or not executable"
    FAILED=$((FAILED + 1))
fi
rm -rf "$TEMP_GIT"

if [ "$FAILED" -gt 0 ]; then
    echo "FAILED: $FAILED test(s) failed."
    exit 1
fi

echo "ALL TESTS PASSED!"
exit 0
