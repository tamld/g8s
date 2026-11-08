#!/usr/bin/env bash
#
# Test suite for tools/ci_fetch.sh (Issue #841)
# Uses PATH-shimmed curl to simulate download failures and successes.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FETCH_SCRIPT="${SCRIPT_DIR}/ci_fetch.sh"

if [ ! -f "$FETCH_SCRIPT" ]; then
    echo "ERROR: ci_fetch script not found at $FETCH_SCRIPT" >&2
    exit 1
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ci_fetch_test_XXXXXX")"
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

MOCK_BIN="${TMP_DIR}/bin"
mkdir -p "$MOCK_BIN"
CALLS_FILE="${TMP_DIR}/curl_calls"
PAYLOAD_FILE="${TMP_DIR}/payload.bin"
printf 'fixture-payload-bytes' >"$PAYLOAD_FILE"

cat >"$MOCK_BIN/curl" <<'EOF'
#!/usr/bin/env bash
set -uo pipefail
calls_file="${CURL_SHIM_CALLS_FILE:?}"
n=0
if [ -f "$calls_file" ]; then
    n=$(cat "$calls_file")
fi
n=$((n + 1))
echo "$n" >"$calls_file"

dest=""
prev=""
for arg in "$@"; do
    if [ "$prev" = "-o" ]; then
        dest="$arg"
    fi
    prev="$arg"
done
url="${!#}"

mode="${CURL_SHIM_MODE:-ok}"
case "$mode" in
    fail_twice)
        if [ "$n" -lt 3 ]; then
            exit 22
        fi
        ;;
    always_fail)
        exit 22
        ;;
esac

if [ -z "$dest" ]; then
    echo "shim curl: missing -o dest" >&2
    exit 2
fi
cp "${CURL_SHIM_PAYLOAD_FILE:?}" "$dest"
exit 0
EOF
chmod +x "$MOCK_BIN/curl"

export PATH="${MOCK_BIN}:${PATH}"
export CURL_SHIM_CALLS_FILE="$CALLS_FILE"
export CURL_SHIM_PAYLOAD_FILE="$PAYLOAD_FILE"

TEST_URL="https://example.test/ci-fetch/fixture.tar.gz"

echo "==> Running ci_fetch Test Suite..."

# shellcheck disable=SC1090
source "$FETCH_SCRIPT"

echo "Testing: attempt 1 succeeds (single curl call)"
: >"$CALLS_FILE"
export CURL_SHIM_MODE=ok
dest="${TMP_DIR}/t1.tar"
ci_fetch "$TEST_URL" "$dest"
calls=$(cat "$CALLS_FILE")
if [ "$calls" -ne 1 ]; then
    echo "  [FAIL] expected 1 curl call, got $calls"
    exit 1
fi
if ! cmp -s "$dest" "$PAYLOAD_FILE"; then
    echo "  [FAIL] dest content mismatch"
    exit 1
fi
echo "  [PASS]"

echo "Testing: fails twice then succeeds (3 curl calls, content intact)"
: >"$CALLS_FILE"
export CURL_SHIM_MODE=fail_twice
dest="${TMP_DIR}/t2.tar"
ci_fetch "$TEST_URL" "$dest"
calls=$(cat "$CALLS_FILE")
if [ "$calls" -ne 3 ]; then
    echo "  [FAIL] expected 3 curl calls, got $calls"
    exit 1
fi
if ! cmp -s "$dest" "$PAYLOAD_FILE"; then
    echo "  [FAIL] dest content mismatch after retries"
    exit 1
fi
echo "  [PASS]"

echo "Testing: always fails (non-zero exit, URL on stderr)"
: >"$CALLS_FILE"
export CURL_SHIM_MODE=always_fail
dest="${TMP_DIR}/t3.tar"
output=""
exit_code=0
set +e
output=$(ci_fetch "$TEST_URL" "$dest" 2>&1)
exit_code=$?
set -e
if [ "$exit_code" -eq 0 ]; then
    echo "  [FAIL] expected non-zero exit, got 0"
    exit 1
fi
if ! echo "$output" | grep -qF "$TEST_URL"; then
    echo "  [FAIL] expected URL in stderr"
    echo "$output"
    exit 1
fi
calls=$(cat "$CALLS_FILE")
if [ "$calls" -ne 3 ]; then
    echo "  [FAIL] expected 3 curl calls, got $calls"
    exit 1
fi
echo "  [PASS]"

echo ""
echo "[ALL CI_FETCH TESTS PASSED]"
exit 0
