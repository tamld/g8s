#!/usr/bin/env bash
#
# release_matrix_check_test.sh — Test suite for release_matrix_check.sh recurrence guard (#433)
#
# Validates:
# (a) A fixture config whose table matches its archives -> check exits 0
# (b) A fixture config whose table names a missing asset -> exits 1 and prints the offending row
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CHECK_SCRIPT="${SCRIPT_DIR}/release_matrix_check.sh"

if [ ! -f "$CHECK_SCRIPT" ]; then
    echo "ERROR: Guard script not found at $CHECK_SCRIPT" >&2
    exit 1
fi

echo "==> Running Release Matrix Recurrence Guard Test Suite..."

FAILURES=0

run_test() {
    local desc="$1"
    local expect="$2" # pass | fail
    local expected_pattern="${3:-}"
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?

    if [ "$expect" = "pass" ] && [ "$exit_code" -ne 0 ]; then
        echo "  [FAIL] Expected exit 0, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
        return
    elif [ "$expect" = "fail" ] && [ "$exit_code" -eq 0 ]; then
        echo "  [FAIL] Expected non-zero exit, got 0"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
        return
    fi

    if [ -n "$expected_pattern" ]; then
        if ! echo "$output" | grep -q "$expected_pattern"; then
            echo "  [FAIL] Output missing expected pattern: $expected_pattern"
            echo "$output" | sed 's/^/    /'
            FAILURES=$((FAILURES + 1))
            return
        fi
    fi

    echo "  [PASS] ok"
}

FIXTURE_DIR="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_DIR"' EXIT

# --- Fixture 1: (a) Config whose table matches its archives (multi-platform with universal macOS) ---
cat > "$FIXTURE_DIR/valid_matrix.yaml" <<'EOF'
version: 2
project_name: myapp

builds:
  - id: myapp
    goos:
      - darwin
      - linux
    goarch:
      - amd64
      - arm64
  - id: myapp-windows
    goos:
      - windows
    goarch:
      - amd64

universal_binaries:
  - id: darwin_all
    ids:
      - myapp
    replace: true

archives:
  - id: default
    formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format_overrides:
      - goos: windows
        formats: [zip]

release:
  header: |
    ### 📦 Downloads & Platform Matrix

    | Platform | Architecture | Binary / Archive |
    |:---|:---|:---|
    | **macOS (Apple Silicon)** | `darwin/arm64` (M1/M2/M3/M4) | `myapp_{{ .Version }}_darwin_all.tar.gz` |
    | **macOS (Intel)** | `darwin/amd64` (x86_64) | `myapp_{{ .Version }}_darwin_all.tar.gz` |
    | **Linux** | `linux/amd64` | `myapp_{{ .Version }}_linux_amd64.tar.gz` (also `.deb`) |
    | **Linux** | `linux/arm64` | `myapp_{{ .Version }}_linux_arm64.tar.gz` (also `.deb`) |
    | **Windows** | `windows/amd64` | `myapp_{{ .Version }}_windows_amd64.zip` |
EOF

run_test "Fixture (a): table matches actual archives (exits 0)" \
    "pass" \
    "All 5 release matrix assets match" \
    bash "$CHECK_SCRIPT" "$FIXTURE_DIR/valid_matrix.yaml"

# --- Fixture 2: (b) Config whose table names missing asset (exits 1 and prints offending row) ---
cat > "$FIXTURE_DIR/missing_asset.yaml" <<'EOF'
version: 2
project_name: myapp

builds:
  - id: myapp
    goos:
      - darwin
      - linux
    goarch:
      - amd64
      - arm64

universal_binaries:
  - id: darwin_all
    ids:
      - myapp
    replace: true

archives:
  - id: default
    formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"

release:
  header: |
    ### 📦 Downloads & Platform Matrix

    | Platform | Architecture | Binary / Archive |
    |:---|:---|:---|
    | **macOS (Apple Silicon)** | `darwin/arm64` | `myapp_{{ .Version }}_darwin_arm64.tar.gz` |
    | **Linux** | `linux/amd64` | `myapp_{{ .Version }}_linux_amd64.tar.gz` |
EOF

run_test "Fixture (b): table names missing asset (exits 1 and prints offending row)" \
    "fail" \
    "Offending row: | \*\*macOS (Apple Silicon)\*\* | \`darwin/arm64\` | \`myapp_{{ .Version }}_darwin_arm64.tar.gz\` |" \
    bash "$CHECK_SCRIPT" "$FIXTURE_DIR/missing_asset.yaml"

# --- Fixture 3: Missing Windows archive format override (expected zip but table advertises tar.gz) ---
cat > "$FIXTURE_DIR/wrong_format.yaml" <<'EOF'
version: 2
project_name: sample

builds:
  - id: sample-win
    goos:
      - windows
    goarch:
      - amd64

archives:
  - id: default
    formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format_overrides:
      - goos: windows
        formats: [zip]

release:
  header: |
    | Platform | Architecture | Binary / Archive |
    |:---|:---|:---|
    | **Windows** | `windows/amd64` | `sample_{{ .Version }}_windows_amd64.tar.gz` |
EOF

run_test "Fixture (c): wrong archive format in table (exits 1 and prints offending row)" \
    "fail" \
    "sample_{{ .Version }}_windows_amd64.tar.gz" \
    bash "$CHECK_SCRIPT" "$FIXTURE_DIR/wrong_format.yaml"

# --- Fixture 4: Missing table in release header ---
cat > "$FIXTURE_DIR/no_table.yaml" <<'EOF'
version: 2
project_name: sample
builds:
  - id: sample
    goos: [linux]
    goarch: [amd64]
release:
  header: |
    ## Notes
    No matrix here.
EOF

run_test "Fixture (d): release header without platform matrix table (exits 1)" \
    "fail" \
    "No release header matrix table found" \
    bash "$CHECK_SCRIPT" "$FIXTURE_DIR/no_table.yaml"

# --- Fixture 5: Non-existent config path ---
run_test "Non-existent config path fails with actionable error" \
    "fail" \
    "Config file not found" \
    bash "$CHECK_SCRIPT" "$FIXTURE_DIR/does_not_exist.yaml"

# --- Test Live Repository Config ---
run_test "Repository .goreleaser.yaml passes matrix check" \
    "pass" \
    "All 5 release matrix assets match" \
    bash "$CHECK_SCRIPT" "$REPO_ROOT/.goreleaser.yaml"

# --- Summary ---
echo ""
if [ "$FAILURES" -gt 0 ]; then
    echo "RELEASE MATRIX CHECK TEST SUITE: $FAILURES failure(s)."
    exit 1
fi

echo "RELEASE MATRIX CHECK TEST SUITE: all tests passed."
exit 0
