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

echo "Testing: F08 get_latest_version fails closed on API error (no v0.1.0 downgrade)"
setup_shim "fail_all"
ver_rc=0
ver_out="$(
    PATH="${SHIM_DIR}:${PATH}" INSTALL_SH="${INSTALL_SH}" bash -c '
        source "${INSTALL_SH}"
        get_latest_version
    ' 2>&1
)" || ver_rc=$?
if [ "$ver_rc" -eq 0 ] || echo "$ver_out" | grep -qF "v0.1.0"; then
    echo "  [FAIL] get_latest_version must fail closed instead of falling back (rc=$ver_rc out=$ver_out)" >&2
    exit 1
fi
echo "  [PASS] get_latest_version failed closed"

echo "Testing: B01 resolve_asset_name maps darwin arm64/amd64 to darwin_all and keeps linux arch"
asset_arm="$(INSTALL_SH="${INSTALL_SH}" bash -c 'source "${INSTALL_SH}"; resolve_asset_name darwin arm64 0.25.0')" || {
    echo "  [FAIL] resolve_asset_name failed for darwin/arm64" >&2
    exit 1
}
asset_amd="$(INSTALL_SH="${INSTALL_SH}" bash -c 'source "${INSTALL_SH}"; resolve_asset_name darwin amd64 0.25.0')" || {
    echo "  [FAIL] resolve_asset_name failed for darwin/amd64" >&2
    exit 1
}
asset_lin="$(INSTALL_SH="${INSTALL_SH}" bash -c 'source "${INSTALL_SH}"; resolve_asset_name linux amd64 0.25.0')" || {
    echo "  [FAIL] resolve_asset_name failed for linux/amd64" >&2
    exit 1
}
if [ "$asset_arm" != "g8s_0.25.0_darwin_all.tar.gz" ] || [ "$asset_amd" != "g8s_0.25.0_darwin_all.tar.gz" ] || [ "$asset_lin" != "g8s_0.25.0_linux_amd64.tar.gz" ]; then
    echo "  [FAIL] unexpected asset mapping: darwin_arm64=$asset_arm darwin_amd64=$asset_amd linux_amd64=$asset_lin" >&2
    exit 1
fi
echo "  [PASS] resolve_asset_name matched darwin_all & linux_amd64"

echo "Testing: F09 G8S_VERIFY_SIGNATURE=1 fails closed without verifier or bad sig and passes with valid cosign shim"
setup_shim "web_ok"
printf 'deadbeef  g8s_0.25.0_linux_amd64.tar.gz\n' > "${TEST_ROOT}/checksums.txt"
printf 'tarball' > "${TEST_ROOT}/g8s_0.25.0_linux_amd64.tar.gz"
cat > "${SHIM_DIR}/cosign" <<'COSIGN_OK'
#!/usr/bin/env bash
exit 0
COSIGN_OK
chmod +x "${SHIM_DIR}/cosign"
PATH="${SHIM_DIR}:${PATH}" G8S_VERIFY_SIGNATURE=1 INSTALL_SH="${INSTALL_SH}" TEST_ROOT="${TEST_ROOT}" bash -c '
    source "${INSTALL_SH}"
    verify_release_signature "v0.25.0" "${TEST_ROOT}" "g8s_0.25.0_linux_amd64.tar.gz"
'
cat > "${SHIM_DIR}/cosign" <<'COSIGN_BAD'
#!/usr/bin/env bash
echo "invalid signature" >&2
exit 1
COSIGN_BAD
chmod +x "${SHIM_DIR}/cosign"
sig_bad_rc=0
PATH="${SHIM_DIR}:${PATH}" G8S_VERIFY_SIGNATURE=1 INSTALL_SH="${INSTALL_SH}" TEST_ROOT="${TEST_ROOT}" bash -c '
    source "${INSTALL_SH}"
    verify_release_signature "v0.25.0" "${TEST_ROOT}" "g8s_0.25.0_linux_amd64.tar.gz"
' >/dev/null 2>&1 || sig_bad_rc=$?
if [ "$sig_bad_rc" -eq 0 ]; then
    echo "  [FAIL] verify_release_signature must fail closed when cosign fails" >&2
    exit 1
fi
rm -f "${SHIM_DIR}/cosign"
sig_miss_rc=0
PATH="${SHIM_DIR}:/usr/bin:/bin" G8S_VERIFY_SIGNATURE=1 INSTALL_SH="${INSTALL_SH}" TEST_ROOT="${TEST_ROOT}" bash -c '
    command() {
        if [ "${1:-}" = "-v" ] && { [ "${2:-}" = "cosign" ] || [ "${2:-}" = "gh" ]; }; then
            return 1
        fi
        builtin command "$@"
    }
    source "${INSTALL_SH}"
    verify_release_signature "v0.25.0" "${TEST_ROOT}" "g8s_0.25.0_linux_amd64.tar.gz"
' >/dev/null 2>&1 || sig_miss_rc=$?
if [ "$sig_miss_rc" -eq 0 ]; then
    echo "  [FAIL] verify_release_signature must fail closed when cosign and gh are absent" >&2
    exit 1
fi
echo "  [PASS] verify_release_signature checks passed"

echo "==> All install.sh tests passed."
