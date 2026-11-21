#!/usr/bin/env bash
# gofumpt_check.sh — fail-closed gofumpt gate (issue #1021, audit M-03).
# Tool absence or execution failure FAILS the check; only genuine
# unformatted-file findings (minus the reference/ exemption) fail it
# with the file list. Exits:
#   0 — tree gofumpt-clean
#   1 — tool missing, tool failed, or unformatted files found
set -uo pipefail

TARGET="${1:-.}"

if ! command -v gofumpt >/dev/null 2>&1; then
    echo "::error::gofumpt is not installed — install a pinned version (mvdan.cc/gofumpt) before running this gate" >&2
    exit 1
fi

raw="$(gofumpt -l "$TARGET")"
if [ $? -ne 0 ]; then
    echo "::error::gofumpt execution failed" >&2
    exit 1
fi

unformatted="$(printf '%s\n' "$raw" | grep -v '^reference/' || true)"
if [ -n "$unformatted" ]; then
    echo "::error::Files not gofumpt-clean:"
    printf '%s\n' "$unformatted"
    exit 1
fi
exit 0
