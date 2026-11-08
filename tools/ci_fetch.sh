#!/usr/bin/env bash
#
# tools/ci_fetch.sh — Retry wrapper for CI tool downloads (Issue #841)
# Usage: ci_fetch <url> <dest-file>
#   or:  source tools/ci_fetch.sh && ci_fetch <url> <dest-file>
#

set -euo pipefail

ci_fetch() {
    local url="${1:?ci_fetch: url required}"
    local dest="${2:?ci_fetch: dest-file required}"
    local max_attempts=3
    local attempt=1
    local curl_status=1

    while [ "$attempt" -le "$max_attempts" ]; do
        echo "attempt ${attempt}/${max_attempts} ..."
        if curl -fsSL --max-time 300 -o "$dest" "$url"; then
            return 0
        else
            curl_status=$?
        fi
        if [ "$attempt" -lt "$max_attempts" ]; then
            sleep 10
        fi
        attempt=$((attempt + 1))
    done

    echo "ERROR: failed to download after ${max_attempts} attempts: ${url}" >&2
    return "$curl_status"
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    if [ "$#" -ne 2 ]; then
        echo "usage: $(basename "$0") <url> <dest-file>" >&2
        exit 1
    fi
    ci_fetch "$@"
fi
