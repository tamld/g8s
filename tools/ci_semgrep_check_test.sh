#!/usr/bin/env bash
#
# tools/ci_semgrep_check_test.sh — Self-test suite for tools/ci_semgrep_check.sh
#
# Verifies that the Semgrep SAST gate:
#   1. Rejects production Go code writing files with 0o644 (g8s-writefile-perm-0600)
#   2. Rejects CGO / mattn sqlite3 imports (g8s-zero-cgo-import)
#   3. Passes on compliant 0o600 pure-Go code

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE_SCRIPT="${SCRIPT_DIR}/ci_semgrep_check.sh"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "==> Running Semgrep SAST Gate Self-Tests..."

# Case 1: Violation of g8s-writefile-perm-0600 in non-test file must FAIL
mkdir -p "$TMP_DIR/bad_perm"
cat << 'EOF' > "$TMP_DIR/bad_perm/bad.go"
package badperm

import "os"

func writeBad() error {
	return os.WriteFile("/tmp/x", []byte("x"), 0o644)
}
EOF

if bash "$GATE_SCRIPT" "$TMP_DIR/bad_perm" >/dev/null 2>&1; then
    echo "FAIL: ci_semgrep_check.sh passed on 0o644 WriteFile violation" >&2
    exit 1
fi
echo "  [PASS] Rejects 0o644 os.WriteFile in production Go code"

# Case 2: Violation of g8s-zero-cgo-import must FAIL
mkdir -p "$TMP_DIR/bad_cgo"
cat << 'EOF' > "$TMP_DIR/bad_cgo/cgo.go"
package badcgo

import "github.com/mattn/go-sqlite3"

var _ = sqlite3.Version
EOF

if bash "$GATE_SCRIPT" "$TMP_DIR/bad_cgo" >/dev/null 2>&1; then
    echo "FAIL: ci_semgrep_check.sh passed on prohibited mattn/go-sqlite3 import" >&2
    exit 1
fi
echo "  [PASS] Rejects prohibited CGO / mattn sqlite3 import"

# Case 3: Compliant 0o600 code and 0o644 in _test.go must PASS
mkdir -p "$TMP_DIR/good"
cat << 'EOF' > "$TMP_DIR/good/good.go"
package good

import "os"

func writeGood() error {
	return os.WriteFile("/tmp/x", []byte("x"), 0o600)
}
EOF

cat << 'EOF' > "$TMP_DIR/good/good_test.go"
package good

import "os"

func writeFixture() error {
	return os.WriteFile("/tmp/test", []byte("test"), 0o644)
}
EOF

if ! bash "$GATE_SCRIPT" "$TMP_DIR/good" >/dev/null 2>&1; then
    echo "FAIL: ci_semgrep_check.sh rejected compliant 0o600 production code + _test.go fixture" >&2
    exit 1
fi
echo "  [PASS] Accepts compliant 0o600 production code and _test.go fixtures"

# Case 4: Violation of g8s-no-silent-fs-error-swallow in internal/ must FAIL
mkdir -p "$TMP_DIR/swallow/internal/conv"
cat << 'EOF' > "$TMP_DIR/swallow/internal/conv/bad.go"
package conv

import "os"

func swallowWrite() {
	_ = os.WriteFile("/tmp/x", []byte("x"), 0o600)
}
EOF

if bash "$GATE_SCRIPT" "$TMP_DIR/swallow" >/dev/null 2>&1; then
    echo "FAIL: ci_semgrep_check.sh passed on silent _ = os.WriteFile error swallow" >&2
    exit 1
fi
echo "  [PASS] Rejects silent _ = os.WriteFile error swallowing in internal/"

echo "[SEMGREP-TEST] ALL SELF-TESTS PASSED"
