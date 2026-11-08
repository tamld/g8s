#!/usr/bin/env bash
#
# Test suite for tools/ci_platform_matrix_check.sh (ADR-0040 / #832)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK_SCRIPT="${SCRIPT_DIR}/ci_platform_matrix_check.sh"

if [ ! -f "$CHECK_SCRIPT" ]; then
    echo "ERROR: check script not found at $CHECK_SCRIPT" >&2
    exit 1
fi

echo "==> Running Platform Matrix Check Test Suite..."

run_test() {
    local desc="$1"
    local expect_pass="$2"
    local expected_substr="$3"
    local goreleaser="$4"
    local readme="$5"

    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$(bash "$CHECK_SCRIPT" --goreleaser "$goreleaser" --readme "$readme" 2>&1) || exit_code=$?

    if [ "$expect_pass" = "true" ]; then
        if [ "$exit_code" -ne 0 ]; then
            echo "  [FAIL] Expected pass, got exit $exit_code"
            echo "$output" | sed 's/^/    /'
            exit 1
        fi
    else
        if [ "$exit_code" -eq 0 ]; then
            echo "  [FAIL] Expected fail, got exit 0"
            echo "$output" | sed 's/^/    /'
            exit 1
        fi
    fi

    if [ -n "$expected_substr" ] && ! echo "$output" | grep -qF "$expected_substr"; then
        echo "  [FAIL] Expected output to contain: $expected_substr"
        echo "$output" | sed 's/^/    /'
        exit 1
    fi

    echo "  [PASS]"
}

FIXTURE_DIR="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_DIR"' EXIT

cat >"$FIXTURE_DIR/goreleaser_three.yaml" <<'EOF'
version: 2
project_name: demo
builds:
  - id: demo
    goos:
      - linux
    goarch:
      - amd64
      - arm64
EOF

cat >"$FIXTURE_DIR/readme_three_ok.md" <<'EOF'
## 4. Installation

| Platform | Architecture | Artifact |
|:---|:---|:---|
| **Linux** | `linux/amd64` | demo_linux_amd64.tar.gz |
| **Linux** | `linux/arm64` | demo_linux_arm64.tar.gz |
EOF

cat >"$FIXTURE_DIR/readme_missing_arm64.md" <<'EOF'
## 4. Installation

| Platform | Architecture | Artifact |
|:---|:---|:---|
| **Linux** | `linux/amd64` | demo_linux_amd64.tar.gz |
EOF

cat >"$FIXTURE_DIR/readme_phantom_freebsd.md" <<'EOF'
## 4. Installation

| Platform | Architecture | Artifact |
|:---|:---|:---|
| **Linux** | `linux/amd64` | demo_linux_amd64.tar.gz |
| **Linux** | `linux/arm64` | demo_linux_arm64.tar.gz |
| **FreeBSD** | `freebsd/amd64` | demo_freebsd_amd64.tar.gz |
EOF

run_test \
    "Fixture: 3 goreleaser combos, README covers all" \
    "true" \
    "matrix linux/arm64: README ok" \
    "$FIXTURE_DIR/goreleaser_three.yaml" \
    "$FIXTURE_DIR/readme_three_ok.md"

run_test \
    "Fixture: missing arm64 row in README" \
    "false" \
    "matrix linux/arm64: MISSING from README" \
    "$FIXTURE_DIR/goreleaser_three.yaml" \
    "$FIXTURE_DIR/readme_missing_arm64.md"

run_test \
    "Fixture: phantom freebsd claim in README" \
    "false" \
    "matrix freebsd/amd64: PHANTOM in README" \
    "$FIXTURE_DIR/goreleaser_three.yaml" \
    "$FIXTURE_DIR/readme_phantom_freebsd.md"

echo ""
cat >"$FIXTURE_DIR/goreleaser_multi.yaml" <<'EOF'
version: 2
project_name: demo
builds:
  - id: demo
    goos:
      - darwin
      - linux
    goarch:
      - amd64
      - arm64
  - id: demo-win
    goos:
      - windows
    goarch:
      - amd64
      - arm64
EOF

# The #831 README layout: packages table (slash tokens) + archives table
# (universal darwin_all + brace-expansion rows) — no bold markers.
cat >"$FIXTURE_DIR/readme_new_layout_ok.md" <<'EOF'
## 4. Installation

### Package managers / packages (Linux, from the release page)

| Architecture | `.deb` | `.rpm` |
| :--- | :--- | :--- |
| `linux/amd64` | `demo_linux_amd64.deb` | `demo_linux_amd64.rpm` |
| `linux/arm64` | `demo_linux_arm64.deb` | `demo_linux_arm64.rpm` |

### Archives (all platforms)

| Platform | Release asset pattern |
| :--- | :--- |
| macOS (universal) | `demo_darwin_all.tar.gz` |
| Linux | `demo_linux_{amd64,arm64}.tar.gz` (plus packages above) |
| Windows | `demo_windows_{amd64,arm64}.zip` |
EOF

cat >"$FIXTURE_DIR/readme_new_layout_missing_win.md" <<'EOF'
## 4. Installation

### Archives (all platforms)

| Platform | Release asset pattern |
| :--- | :--- |
| macOS (universal) | `demo_darwin_all.tar.gz` |
| Linux | `demo_linux_{amd64,arm64}.tar.gz` |
EOF

run_test \
    "Fixture: #831 layout — universal + braces + package rows cover all 6 combos" \
    "true" \
    "matrix windows/arm64: README ok" \
    "$FIXTURE_DIR/goreleaser_multi.yaml" \
    "$FIXTURE_DIR/readme_new_layout_ok.md"

run_test \
    "Fixture: #831 layout missing the Windows row" \
    "false" \
    "matrix windows/arm64: MISSING from README" \
    "$FIXTURE_DIR/goreleaser_multi.yaml" \
    "$FIXTURE_DIR/readme_new_layout_missing_win.md"

echo ""
echo "[ALL PLATFORM MATRIX CHECK TESTS PASSED]"
exit 0
