#!/usr/bin/env bash
#
# tools/ci_fetch.sh — Retry wrapper for CI tool downloads (Issue #841)
# Usage: ci_fetch <url> <dest-file> [expected-sha256]
#   or:  source tools/ci_fetch.sh && ci_fetch <url> <dest-file> [expected-sha256]
#
# Supply-chain hardening (#983): when an expected SHA-256 digest is supplied,
# every successful download is verified before ci_fetch reports success.
# A mismatch is treated like a failed download (retried), and after the final
# attempt ci_fetch fails closed: the artifact is removed and a non-zero
# status is returned — an unverified artifact is never left at <dest-file>.
#

set -euo pipefail

# Seconds between download attempts (overridable for tests).
: "${CI_FETCH_RETRY_DELAY:=10}"

ci_fetch_sha256() {
    # Print the SHA-256 hex digest of a file using the first available tool.
    local file="${1:?ci_fetch_sha256: file required}"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    else
        echo "ERROR: no SHA-256 tool available (need sha256sum or shasum)" >&2
        return 1
    fi
}

ci_fetch() {
    local url="${1:?ci_fetch: url required}"
    local dest="${2:?ci_fetch: dest-file required}"
    local expected_sha256="${3:-}"
    local max_attempts=3
    local attempt=1
    local fetch_status=1

    # Fail closed on malformed expectations before touching the network.
    if [ -n "$expected_sha256" ] && ! printf '%s' "$expected_sha256" | grep -Eq '^[a-fA-F0-9]{64}$'; then
        echo "ERROR: expected-sha256 must be a 64-character hex digest (got '${expected_sha256}')" >&2
        return 2
    fi

    while [ "$attempt" -le "$max_attempts" ]; do
        echo "attempt ${attempt}/${max_attempts} ..."
        if curl -fsSL --max-time 300 -o "$dest" "$url"; then
            if [ -z "$expected_sha256" ]; then
                return 0
            fi
            local actual=""
            if ! actual="$(ci_fetch_sha256 "$dest")"; then
                actual=""
            fi
            if [ "${actual}" = "$(printf '%s' "$expected_sha256" | tr 'A-F' 'a-f')" ]; then
                echo "SHA-256 verified: ${actual}"
                return 0
            fi
            fetch_status=1
            echo "WARN: SHA-256 mismatch for ${url} — re-fetching" >&2
            echo "  expected: ${expected_sha256}" >&2
            echo "  actual:   ${actual:-<verification failed>}" >&2
            rm -f "$dest"
        else
            fetch_status=$?
        fi
        if [ "$attempt" -lt "$max_attempts" ]; then
            sleep "$CI_FETCH_RETRY_DELAY"
        fi
        attempt=$((attempt + 1))
    done

    echo "ERROR: failed to download${expected_sha256:+ and verify} after ${max_attempts} attempts: ${url}" >&2
    rm -f "$dest"  # fail-closed: never leave an unverified artifact at dest
    return "$fetch_status"
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
        echo "usage: $(basename "$0") <url> <dest-file> [expected-sha256]" >&2
        exit 1
    fi
    ci_fetch "$@"
fi
