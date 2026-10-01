#!/usr/bin/env bash
#
# Test suite for tools/ci_version_sync_check.sh (#476)
# Validates version sync check tooling polish:
#   (a) no DEBUG output in execution
#   (b) 0.13.0-vs-0.13.0 match passes cleanly
#   (c) prerelease ordering: rc.10 > rc.9 does not report drift (fixture)
#   (d) prerelease ordering: rc.9 < rc.10 reports behind failure (fixture)
#   (e) unit compare: rc.10 vs rc.9 returns 1
#   (f) unit compare: rc.9 vs rc.10 returns -1
#   (g) unit compare: 0.13.0 vs 0.13.0 returns 0
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CHECK_SCRIPT="${SCRIPT_DIR}/ci_version_sync_check.sh"

if [ ! -f "$CHECK_SCRIPT" ]; then
    echo "ERROR: version sync check script not found at $CHECK_SCRIPT"
    exit 1
fi

echo "==> Running CI Version Sync Check Test Suite..."

FAILURES=0

run_test() {
    local desc="$1"
    local expect_code="$2"
    shift 2
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit $exit_code)"
    fi
}

run_test_with_output() {
    local desc="$1"
    local expect_code="$2"
    local pattern="$3"
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    elif ! echo "$output" | grep -q "$pattern"; then
        echo "  FAIL: output did not match pattern '$pattern'"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit $exit_code, pattern matched: '$pattern')"
    fi
}

run_test_without_output() {
    local desc="$1"
    local expect_code="$2"
    local forbidden_pattern="$3"
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    elif echo "$output" | grep -q "$forbidden_pattern"; then
        echo "  FAIL: output contained forbidden pattern '$forbidden_pattern'"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit $exit_code, forbidden pattern absent: '$forbidden_pattern')"
    fi
}

# --- fixtures -------------------------------------------------------------

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

setup_fixture_repo() {
    local dir="$1"
    local ver_go="$2"
    local tag="$3"
    mkdir -p "$dir/cmd/g8s"
    (
        cd "$dir"
        git init -q -b main
        git config user.name "Test Runner"
        git config user.email "test@example.com"

        cat > "cmd/g8s/version.go" <<EOF
package main

var (
	Version   = "$ver_go"
	Commit    = "testcommit"
	BuildTime = "testtime"
)
EOF

        cat > "CHANGELOG.md" <<EOF
# Changelog

## [$ver_go] - 2026-10-01

### Added
- Test entry
EOF

        cat > "manifest.json" <<EOF
{
  "version": "$ver_go",
  "latest_release": {
    "tag": "v$tag"
  }
}
EOF

        git add .
        git commit -q -m "initial fixture commit"
        git tag "v$tag"
    )
}

# Fixture 1: rc.10 version with rc.9 git tag (ahead, valid prerelease)
FIXTURE_RC_AHEAD="$FIXTURE_ROOT/rc_ahead"
setup_fixture_repo "$FIXTURE_RC_AHEAD" "0.13.0-rc.10" "0.13.0-rc.9"

# Fixture 2: rc.9 version with rc.10 git tag (behind, invalid)
FIXTURE_RC_BEHIND="$FIXTURE_ROOT/rc_behind"
setup_fixture_repo "$FIXTURE_RC_BEHIND" "0.13.0-rc.9" "0.13.0-rc.10"

# --- test cases -----------------------------------------------------------

# (a) no DEBUG output in live execution
run_test_without_output \
    "(a) live script run does not emit DEBUG lines" 0 \
    "DEBUG:" \
    bash "$CHECK_SCRIPT"

# (b) 0.13.0-vs-0.13.0 match passes cleanly
run_test_with_output \
    "(b) live 0.13.0 matches git tag and passes cleanly" 0 \
    "VERSION (0.13.0) matches latest git tag" \
    bash "$CHECK_SCRIPT"

# (c) fixture: rc.10 > rc.9 must not report drift (exit 0, warn ahead)
run_test_with_output \
    "(c) fixture rc.10 > rc.9 does not report drift and exits 0" 0 \
    "VERSION (0.13.0-rc.10) is ahead of latest git tag (v0.13.0-rc.9)" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_RC_AHEAD"

# (d) fixture: rc.9 < rc.10 reports behind and fails (exit 1)
run_test_with_output \
    "(d) fixture rc.9 < rc.10 reports behind and fails" 1 \
    "VERSION (0.13.0-rc.9) is behind latest git tag (v0.13.0-rc.10)" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_RC_BEHIND"

# (e) unit compare: rc.10 vs rc.9 returns 1
run_test_with_output \
    "(e) unit compare 0.13.0-rc.10 > 0.13.0-rc.9 returns 1" 0 \
    "^1$" \
    bash "$CHECK_SCRIPT" --compare "0.13.0-rc.10" "0.13.0-rc.9"

# (f) unit compare: rc.9 vs rc.10 returns -1
run_test_with_output \
    "(f) unit compare 0.13.0-rc.9 < 0.13.0-rc.10 returns -1" 0 \
    "^-1$" \
    bash "$CHECK_SCRIPT" --compare "0.13.0-rc.9" "0.13.0-rc.10"

# (g) unit compare: 0.13.0 vs 0.13.0 returns 0
run_test_with_output \
    "(g) unit compare 0.13.0 == 0.13.0 returns 0" 0 \
    "^0$" \
    bash "$CHECK_SCRIPT" --compare "0.13.0" "0.13.0"

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "CI VERSION SYNC CHECK TEST SUITE: $FAILURES failure(s)."
    exit 1
fi

echo ""
echo "CI VERSION SYNC CHECK TEST SUITE: all tests passed."
exit 0
