#!/usr/bin/env bash
#
# sisyphus_dispatch.sh — Canonical Sisyphus brief dispatch loop for g8s.
# Replaces legacy `cat /tmp/agy-*.md | agy -p ...` workflow (DEBT-25).
#
# Usage:
#   tools/sisyphus_dispatch.sh <brief-file.md> [extra-agy-args...]
#
# Environment variables:
#   G8S_BIN   Path to g8s binary (default: searches PATH, then ./g8s)
#   AGY_BIN   Path to agy binary (default: searches PATH)
#   ISSUED_BY Identity to record on brief (default: sisyphus)
#   TTL       Brief time-to-live (default: 2h)
#

set -euo pipefail

if [ $# -lt 1 ]; then
    echo "Usage: $0 <brief-file.md> [extra-agy-args...]" >&2
    exit 1
fi

BRIEF_FILE="$1"
shift

if [ ! -f "$BRIEF_FILE" ]; then
    echo "::error::Brief file not found: $BRIEF_FILE" >&2
    exit 1
fi

# Locate g8s binary
G8S_BIN="${G8S_BIN:-g8s}"
if ! command -v "$G8S_BIN" &>/dev/null; then
    if [ -x "./g8s" ]; then
        G8S_BIN="./g8s"
    elif [ -x "$(dirname "$0")/../g8s" ]; then
        G8S_BIN="$(dirname "$0")/../g8s"
    else
        echo "::error::g8s binary not found. Please build g8s or add to PATH." >&2
        exit 1
    fi
fi

ISSUED_BY="${ISSUED_BY:-sisyphus}"
TTL="${TTL:-2h}"

# 1. Register and issue brief via g8s orchestrator
echo "==> Issuing task brief with g8s orchestrator ($BRIEF_FILE)..." >&2
BRIEF_ID=$("$G8S_BIN" orchestrate --brief-file "$BRIEF_FILE" --issued-by "$ISSUED_BY" --ttl "$TTL")

if [ -z "$BRIEF_ID" ] || [[ "$BRIEF_ID" != brief-* ]]; then
    echo "::error::Failed to obtain valid brief ID from g8s orchestrate: '$BRIEF_ID'" >&2
    exit 1
fi

echo "==> Brief issued successfully: $BRIEF_ID" >&2

# 2. Prepare payload and dispatch to agy worker
BRIEF_CONTENT=$(cat "$BRIEF_FILE")
PROMPT="[BRIEF_ID: $BRIEF_ID]

$BRIEF_CONTENT"

AGY_BIN="${AGY_BIN:-agy}"
if command -v "$AGY_BIN" &>/dev/null; then
    echo "==> Spawning agy worker for brief $BRIEF_ID..." >&2
    exec "$AGY_BIN" -p "$PROMPT" "$@"
else
    echo "==> Brief $BRIEF_ID is stored and active in g8s." >&2
    echo "    (agy binary not found in PATH; prompt prepared for manual invocation)" >&2
    echo "$BRIEF_ID"
fi
