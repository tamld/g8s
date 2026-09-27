#!/usr/bin/env bash
#
# ci_pr_contract.sh — G5+G6 PR-contract gates (S6-3a, #420, ADR-0024)
#
# Rules:
#   - kind=docs|chore: exempt from both G5 and G6 (exit 0).
#   - kind=feat|fix:
#       * G5: body must link its tracking issue ('Closes #', 'Fixes #', or 'Part of #').
#       * G6: if changed-lines > 200, body must reference a plan ledger (grep 'plans/').
#             if changed-lines <= 200, G6 is exempt.
#
# Usage:
#   ci_pr_contract.sh --kind <feat|fix|docs|chore> --changed-lines <n> --body-file <file>
#
# Exit codes:
#   0: PR contract satisfied (or exempt)
#   1: PR contract violation (G5 / G6)
#   2: invalid CLI usage / missing arguments
#

set -euo pipefail

KIND=""
CHANGED_LINES=""
BODY_FILE=""

while [ $# -gt 0 ]; do
    case "$1" in
        --kind)
            if [ $# -lt 2 ]; then
                echo "ERROR: --kind requires an argument" >&2
                exit 2
            fi
            KIND="$2"
            shift 2
            ;;
        --changed-lines)
            if [ $# -lt 2 ]; then
                echo "ERROR: --changed-lines requires an argument" >&2
                exit 2
            fi
            CHANGED_LINES="$2"
            shift 2
            ;;
        --body-file)
            if [ $# -lt 2 ]; then
                echo "ERROR: --body-file requires an argument" >&2
                exit 2
            fi
            BODY_FILE="$2"
            shift 2
            ;;
        *)
            echo "ERROR: unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [ -z "$KIND" ]; then
    echo "ERROR: --kind is required" >&2
    exit 2
fi

# Exempt kinds exit 0 immediately
case "$KIND" in
    docs|chore)
        echo "PR CONTRACT GATE: OK (kind '$KIND' is exempt from G5/G6 PR contract checks)."
        exit 0
        ;;
    feat|fix)
        ;;
    *)
        echo "PR CONTRACT GATE: OK (kind '$KIND' is exempt from G5/G6 PR contract checks)."
        exit 0
        ;;
esac

CHANGED_LINES="${CHANGED_LINES:-0}"
case "$CHANGED_LINES" in
    ''|*[!0-9]*)
        echo "ERROR: --changed-lines must be a non-negative integer: $CHANGED_LINES" >&2
        exit 2
        ;;
esac

FAIL=0

# G5: PR must link its tracking issue (Closes #, Fixes #, Part of #)
has_issue_link=0
if [ -n "$BODY_FILE" ] && [ -f "$BODY_FILE" ]; then
    if grep -q -e 'Closes #' -e 'Fixes #' -e 'Part of #' "$BODY_FILE" 2>/dev/null; then
        has_issue_link=1
    fi
fi

if [ "$has_issue_link" -eq 0 ]; then
    echo "::error::G5: PR must link its tracking issue" >&2
    FAIL=1
fi

# G6: large feat/fix PR (> 200 lines) must reference a plan ledger
if [ "$CHANGED_LINES" -gt 200 ]; then
    has_ledger_ref=0
    if [ -n "$BODY_FILE" ] && [ -f "$BODY_FILE" ]; then
        if grep -q 'plans/' "$BODY_FILE" 2>/dev/null; then
            has_ledger_ref=1
        fi
    fi

    if [ "$has_ledger_ref" -eq 0 ]; then
        echo "::error::G6: large feat/fix PR must reference a plan ledger" >&2
        FAIL=1
    fi
fi

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi

echo "PR CONTRACT GATE: OK (kind '$KIND', changed lines $CHANGED_LINES)."
exit 0
