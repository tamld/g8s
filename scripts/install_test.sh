#!/usr/bin/env bash
#
# Test suite for scripts/install.sh fetch_asset API fallback (Issue #838)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_SH="${SCRIPT_DIR}/install.sh"

if [ ! -f "$INSTALL_SH" ]; then
    echo "ERROR: install script not found at $INSTALL_SH"
    exit 1
fi

echo "==> Running install.sh fetch_asset Test Suite..."

TEST_ROOT=""
SHIM_DIR=""
CURL_LOG_FILE=""

cleanup() {
    if [ -n "$TEST_ROOT" ] && [ -d "$TEST_ROOT" ]; then
        rm -rf "$TEST_ROOT"
    fi
}
trap cleanup EXIT

setup_shim() {
    local mode="$1"
    TEST_ROOT="$(mktemp -d -t install_test_XXXXXX)"
    SHIM_DIR="${TEST_ROOT}/shim"
    CURL_LOG_FILE="${TEST_ROOT}/curl.log"
    mkdir -p "$SHIM_DIR"
    : >"$CURL_LOG_FILE"

    cat >"${SHIM_DIR}/curl" <<'SHIM'
#!/usr/bin/env bash
set -u
echo "$*" >> "${CURL_LOG_FILE:?}"

url=""
dest=""
prev=""
for arg in "$@"; do
    case "$arg" in
        -o)
            prev="o"
            ;;
        -f|-s|-S|-L|-fsSL)
            ;;
        http*)
            url="$arg"
            ;;
        *)
            if [ "${prev:-}" = "o" ]; then
                dest="$arg"
                prev=""
            fi
            ;;
    esac
done

case "$url" in
    *://github.com/*/releases/download/*)
        if [ "${FAKE_CURL_MODE:-fail_web}" = "web_ok" ]; then
            printf 'web-payload' >"${dest:?}"
            exit 0
        fi
        exit 22
        ;;
    *://api.github.com/repos/*/releases/tags/*)
        if [ "${FAKE_CURL_MODE:-fail_web}" = "fail_all" ]; then
            exit 22
        fi
        printf '%s\n' '{"assets":[{"name":"checksums.txt","id":99901},{"name":"g8s_1.0.0_linux_amd64.tar.gz","id":99902}]}'
        exit 0
        ;;
    *://api.github.com/repos/*/releases/assets/*)
        if [ "${FAKE_CURL_MODE:-fail_web}" = "fail_all" ]; then
            exit 22
        fi
        printf 'api-payload' >"${dest:?}"
        exit 0
        ;;
    *)
        exit 22
        ;;
esac
SHIM
    chmod +x "${SHIM_DIR}/curl"
    export FAKE_CURL_MODE="$mode"
    export CURL_LOG_FILE
}

count_api_invocations() {
    grep -c 'api\.github\.com' "$CURL_LOG_FILE" 2>/dev/null || true
}

run_test() {
    local desc="$1"
    local mode="$2"
    local expect_pass="$3"
    local expected_substr="$4"
    local check_no_api="${5:-false}"

    echo "Testing: $desc"
    setup_shim "$mode"

    local dest="${TEST_ROOT}/out.bin"
    local output=""
    local exit_code=0
    local expected_payload="api-payload"
    if [ "$mode" = "web_ok" ]; then
        expected_payload="web-payload"
    fi

    local fail_msg="Failed to download https://github.com/tamld/g8s/releases/download/v1.0.0/checksums.txt; refusing unverified binary installation"
    output="$(
        PATH="${SHIM_DIR}:${PATH}" \
            INSTALL_SH="${INSTALL_SH}" \
            DEST="${dest}" \
            FAIL_MSG="${fail_msg}" \
            bash -c '
            # shellcheck disable=SC1091
            source "${INSTALL_SH}"
            fetch_asset "v1.0.0" "checksums.txt" "${DEST}" "${FAIL_MSG}"
        ' 2>&1
    )" || exit_code=$?

    if [ "$expect_pass" = "true" ]; then
        if [ "$exit_code" -ne 0 ]; then
            echo "  [FAIL] Expected success, got exit code $exit_code"
            echo "  Output: $output"
            exit 1
        fi
        if [ ! -f "$dest" ] || [ "$(cat "$dest")" != "$expected_payload" ]; then
            echo "  [FAIL] Expected payload ${expected_payload} in dest"
            exit 1
        fi
        if [ "$check_no_api" = "true" ]; then
            local api_count
            api_count="$(count_api_invocations)"
            if [ "$api_count" -ne 0 ]; then
                echo "  [FAIL] Expected no api.github.com calls, got $api_count"
                echo "  Log: $(cat "$CURL_LOG_FILE")"
                exit 1
            fi
        fi
        echo "  [PASS] Passed as expected"
    else
        if [ "$exit_code" -eq 0 ]; then
            echo "  [FAIL] Expected failure, got exit code 0"
            echo "  Output: $output"
            exit 1
        fi
        if [ -n "$expected_substr" ] && ! echo "$output" | grep -qF "$expected_substr"; then
            echo "  [FAIL] Expected output to contain: $expected_substr"
            echo "  Output: $output"
            exit 1
        fi
        echo "  [PASS] Failed as expected"
    fi
}

run_test "Web 504 → API fallback serves asset" "fail_web" "true" "" "false"
run_test "Web and API both fail → fail-closed loud error" "fail_all" "false" \
    "refusing unverified binary installation" "false"
run_test "Web URL OK → no API requests" "web_ok" "true" "" "true"

echo "==> All install.sh fetch_asset tests passed."
