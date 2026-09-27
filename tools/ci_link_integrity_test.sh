#!/usr/bin/env bash
#
# Test suite for tools/ci_link_integrity.sh (S6-2 / #420 G3)
# Validates the markdown link-integrity gate:
#   (a) md file with a dead relative link -> gate exit 1 listing file:line
#   (b) valid relative links -> exit 0
#   (c) http(s) external links ignored
#   (d) #anchor-only links ignored
#   (e) nested docs/ subdirectory links resolved relative to the md file's own directory
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE_SCRIPT="${SCRIPT_DIR}/ci_link_integrity.sh"

if [ ! -f "$GATE_SCRIPT" ]; then
    echo "ERROR: link-integrity gate script not found at $GATE_SCRIPT"
    exit 1
fi

echo "==> Running Link Integrity Gate Test Suite..."

FAILURES=0

run_test() {
    local desc="$1"
    local expect="$2" # pass | fail
    shift 2
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$expect" = "pass" ] && [ "$exit_code" -ne 0 ]; then
        echo "  FAIL: expected pass, got exit $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    elif [ "$expect" = "fail" ] && [ "$exit_code" -eq 0 ]; then
        echo "  FAIL: expected fail, got success"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok"
    fi
}

# --- fixture: fake doc tree ------------------------------------------------

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

reset_fixture() {
    rm -rf "${FIXTURE_ROOT:?}"/*
    mkdir -p "$FIXTURE_ROOT/docs/sub" "$FIXTURE_ROOT/docs/deep/nested" "$FIXTURE_ROOT/assets"
    touch "$FIXTURE_ROOT/docs/guide.md"
    touch "$FIXTURE_ROOT/docs/sub/topic.md"
    touch "$FIXTURE_ROOT/docs/deep/nested/page.md"
    touch "$FIXTURE_ROOT/assets/logo.png"
}

# --- tests -----------------------------------------------------------------

# 1. Valid relative links pass (exit 0)
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Fixture Project

Check out the [Guide](docs/guide.md) or the [Topic](docs/sub/topic.md).
See image: ![Logo](assets/logo.png)
EOF

cat > "$FIXTURE_ROOT/docs/guide.md" <<'EOF'
# Guide

Go back to [Home](../README.md) or see [Sub Topic](sub/topic.md).
EOF

cat > "$FIXTURE_ROOT/docs/sub/topic.md" <<'EOF'
# Topic

Deep link to [Nested Page](../deep/nested/page.md).
EOF

run_test "valid relative links pass" pass \
    "$GATE_SCRIPT" --root "$FIXTURE_ROOT"

# 2. Dead relative link fails (exit 1) and lists file:line
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Title

Line 3 has a [Broken Link](docs/nonexistent.md).
Line 4 is ok.
EOF

output=""
exit_code=0
output=$("$GATE_SCRIPT" --root "$FIXTURE_ROOT" 2>&1) || exit_code=$?
echo "Testing: dead relative link fails with file:line"
if [ "$exit_code" -ne 1 ]; then
    echo "  FAIL: expected exit 1, got $exit_code"
    FAILURES=$((FAILURES + 1))
elif ! echo "$output" | grep -qE "(README\.md:3|README\.md:[[:space:]]*3)"; then
    echo "  FAIL: expected output to list README.md:3"
    echo "$output" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    echo "  ok"
fi

# 3. HTTP and HTTPS external links are ignored
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# External Links

Check [Google](https://google.com/search?q=test) or [HTTP Site](http://example.com/index.html).
Also [Badge](https://img.shields.io/badge/test-badge.svg).
EOF
run_test "http and https links ignored" pass \
    "$GATE_SCRIPT" --root "$FIXTURE_ROOT"

# 4. #anchor-only links are ignored
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Anchors

Jump to [Overview](#overview) or [FAQ](#faq-section).

## Overview
Content.

## FAQ Section
Content.
EOF
run_test "anchor-only links ignored" pass \
    "$GATE_SCRIPT" --root "$FIXTURE_ROOT"

# 5. Mailto links are ignored
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Contact

Send email to [Maintainers](mailto:team@example.com).
EOF
run_test "mailto links ignored" pass \
    "$GATE_SCRIPT" --root "$FIXTURE_ROOT"

# 6. Nested docs/ subdirectory links resolved relative to md file's directory
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Main
[Topic](docs/sub/topic.md)
EOF

cat > "$FIXTURE_ROOT/docs/sub/topic.md" <<'EOF'
# Topic
[Sibling](../guide.md)
[Deep](../deep/nested/page.md)
[Dead Relative](../deep/missing.md)
EOF

output=""
exit_code=0
output=$("$GATE_SCRIPT" --root "$FIXTURE_ROOT" 2>&1) || exit_code=$?
echo "Testing: nested subdirectory dead link resolved relative to md file and fails with file:line"
if [ "$exit_code" -ne 1 ]; then
    echo "  FAIL: expected exit 1, got $exit_code"
    FAILURES=$((FAILURES + 1))
elif ! echo "$output" | grep -qE "(docs/sub/topic\.md:4|topic\.md:4)"; then
    echo "  FAIL: expected output to list docs/sub/topic.md:4"
    echo "$output" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    echo "  ok"
fi

# 7. Relative links with anchor fragments
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Anchored Targets
Valid link with anchor: [Guide Install](docs/guide.md#installation)
Dead link with anchor: [Missing Install](docs/missing.md#installation)
EOF
cat > "$FIXTURE_ROOT/docs/guide.md" <<'EOF'
# Installation Guide
EOF

output=""
exit_code=0
output=$("$GATE_SCRIPT" --root "$FIXTURE_ROOT" 2>&1) || exit_code=$?
echo "Testing: relative link with anchor target checked properly"
if [ "$exit_code" -ne 1 ]; then
    echo "  FAIL: expected exit 1, got $exit_code"
    FAILURES=$((FAILURES + 1))
elif ! echo "$output" | grep -qE "(README\.md:3|README\.md:[[:space:]]*3)"; then
    echo "  FAIL: expected output to list README.md:3"
    echo "$output" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    echo "  ok"
fi

# 8. Multiple links on the same line
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Multiple links on one line
[Valid](docs/guide.md) and [Dead One](docs/dead1.md) and [Dead Two](docs/dead2.md)
EOF

output=""
exit_code=0
output=$("$GATE_SCRIPT" --root "$FIXTURE_ROOT" 2>&1) || exit_code=$?
echo "Testing: multiple links on single line detected"
if [ "$exit_code" -ne 1 ]; then
    echo "  FAIL: expected exit 1, got $exit_code"
    FAILURES=$((FAILURES + 1))
elif ! echo "$output" | grep -q "dead1.md" || ! echo "$output" | grep -q "dead2.md"; then
    echo "  FAIL: expected output to list both dead links"
    echo "$output" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    echo "  ok"
fi

# 9. Fenced code blocks with link-like text are ignored
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Code Blocks

```bash
# This is a sample command, not a link
arr[0](not_a_real_file.md)
[fake](docs/nonexistent.md)
```

~~~python
# Another code fence
call[0](other_fake.md)
~~~

Valid outside fence: [Guide](docs/guide.md)
EOF

run_test "fenced code blocks ignored" pass \
    "$GATE_SCRIPT" --root "$FIXTURE_ROOT"

# 10. Links with optional titles and angle brackets
reset_fixture
cat > "$FIXTURE_ROOT/README.md" <<'EOF'
# Titles and Brackets

[With Title](docs/guide.md "Awesome Guide")
[With Angle Brackets](<docs/guide.md>)
[Dead With Title](docs/missing.md "Title")
EOF

output=""
exit_code=0
output=$("$GATE_SCRIPT" --root "$FIXTURE_ROOT" 2>&1) || exit_code=$?
echo "Testing: titled link failure reported"
if [ "$exit_code" -ne 1 ]; then
    echo "  FAIL: expected exit 1, got $exit_code"
    FAILURES=$((FAILURES + 1))
elif ! echo "$output" | grep -q "docs/missing.md"; then
    echo "  FAIL: expected output to report docs/missing.md"
    echo "$output" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    echo "  ok"
fi

# 11. Unknown CLI argument fails with code 2
run_test "unknown argument exits with code 2" fail \
    "$GATE_SCRIPT" --unknown-flag

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "LINK INTEGRITY GATE TEST SUITE: $FAILURES failure(s)."
    exit 1
fi
echo ""
echo "LINK INTEGRITY GATE TEST SUITE: all tests passed."
