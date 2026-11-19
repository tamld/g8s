#!/usr/bin/env bash
#
# tools/safe_merge.sh — Provenance-enforced & CI-gated squash-merge helper (ADR-0039 / #926)
#
# Enforces before calling `gh pr merge --squash --delete-branch`:
#   1. PR state is OPEN and all statusCheckRollup items passed (0 pending, 0 failed, total > 0).
#   2. Squash commit body places summary and Closes/Fixes/Part of lines ABOVE a blank line,
#      followed by an isolated ADR-0039 trailer paragraph (`Receipt: <task-id>` or
#      `Provenance: F0-direct — <reason>`).
#   3. Real Go validator (`tools/provenance_check` / `internal/dogfood.ClassifyCommit`) accepts
#      the exact combined `<subject>\n\n<body>` squash message before GitHub is touched.
#
# Usage:
#   tools/safe_merge.sh --pr <num> (--receipt <task-id> | --f0-direct "<reason>") \
#     [--subject "<subject>"] [--summary "<summary>"] [--closes "<#123,#456>"] [--dry-run]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "$REPO_ROOT"

PR_NUM=""
RECEIPT_ID=""
HAS_RECEIPT=0
F0_DIRECT_REASON=""
HAS_F0_DIRECT=0
SUBJECT_ARG=""
HAS_SUBJECT=0
SUMMARY_ARG=""
HAS_SUMMARY=0
CLOSES_ARG=""
HAS_CLOSES=0
DRY_RUN=false

usage() {
    cat >&2 <<'EOF'
Usage: tools/safe_merge.sh --pr <num> (--receipt <task-id> | --f0-direct "<reason>") [options]

Required:
  --pr <num>              GitHub pull request number (positive integer)
  --receipt <task-id>     Receipt task ID for worker-backed PRs
  --f0-direct "<reason>"  Non-empty reason for direct F0 maintenance PRs

Options:
  --subject "<subject>"   Custom squash commit subject (default: "<PR title> (#<num>)")
  --summary "<summary>"   Custom squash body summary (default: PR title)
  --closes "<#123,#456>"  Comma-separated issue references (default: extracted from PR body)
  --dry-run               Validate all gates and print planned merge message without merging
  -h, --help              Show this help text
EOF
}

trim_ws() {
    printf '%s' "$1" | tr '\r\n\t' '   ' | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//'
}

collapse_ws() {
    printf '%s' "$1" | tr '\r\n\t' '   ' | sed -E 's/[[:space:]]+/ /g; s/^ //; s/ $//'
}

while [ $# -gt 0 ]; do
    case "$1" in
        --pr)
            if [ $# -lt 2 ]; then
                echo "error: --pr requires a PR number" >&2
                usage
                exit 2
            fi
            PR_NUM="$2"
            shift 2
            ;;
        --receipt)
            if [ $# -lt 2 ]; then
                echo "error: --receipt requires a task ID" >&2
                usage
                exit 2
            fi
            RECEIPT_ID="$2"
            HAS_RECEIPT=1
            shift 2
            ;;
        --f0-direct)
            if [ $# -lt 2 ]; then
                echo "error: --f0-direct requires a reason" >&2
                usage
                exit 2
            fi
            F0_DIRECT_REASON="$2"
            HAS_F0_DIRECT=1
            shift 2
            ;;
        --subject)
            if [ $# -lt 2 ]; then
                echo "error: --subject requires text" >&2
                usage
                exit 2
            fi
            SUBJECT_ARG="$2"
            HAS_SUBJECT=1
            shift 2
            ;;
        --summary)
            if [ $# -lt 2 ]; then
                echo "error: --summary requires text" >&2
                usage
                exit 2
            fi
            SUMMARY_ARG="$2"
            HAS_SUMMARY=1
            shift 2
            ;;
        --closes)
            if [ $# -lt 2 ]; then
                echo "error: --closes requires a comma-separated issue list" >&2
                usage
                exit 2
            fi
            CLOSES_ARG="$2"
            HAS_CLOSES=1
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "error: unknown argument: $1" >&2
            usage
            exit 2
            ;;
    esac
done

if [ -z "$PR_NUM" ] || ! [[ "$PR_NUM" =~ ^[0-9]+$ ]] || [ "$PR_NUM" -le 0 ]; then
    echo "error: --pr must be a positive integer, got '${PR_NUM}'" >&2
    usage
    exit 2
fi

if [ "$HAS_RECEIPT" -eq 1 ] && [ "$HAS_F0_DIRECT" -eq 1 ]; then
    echo "error: specify exactly one of --receipt or --f0-direct, not both" >&2
    usage
    exit 2
fi

if [ "$HAS_RECEIPT" -eq 0 ] && [ "$HAS_F0_DIRECT" -eq 0 ]; then
    echo "error: must specify either --receipt <task-id> or --f0-direct \"<reason>\"" >&2
    usage
    exit 2
fi

if [ "$HAS_RECEIPT" -eq 1 ]; then
    RECEIPT_ID="$(trim_ws "$RECEIPT_ID")"
    if [ -z "$RECEIPT_ID" ]; then
        echo "error: --receipt requires a non-empty task ID" >&2
        exit 2
    fi
fi

if [ "$HAS_F0_DIRECT" -eq 1 ]; then
    F0_DIRECT_REASON="$(collapse_ws "$F0_DIRECT_REASON")"
    if [ -z "$F0_DIRECT_REASON" ]; then
        echo "error: --f0-direct requires a non-empty reason" >&2
        exit 2
    fi
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "error: jq is required by tools/safe_merge.sh" >&2
    exit 2
fi

GH_CMD="${GH_BIN:-gh}"
if [[ "$GH_CMD" == */* ]]; then
    if [ ! -x "$GH_CMD" ]; then
        echo "error: GH_BIN is not executable: $GH_CMD" >&2
        exit 2
    fi
elif ! command -v "$GH_CMD" >/dev/null 2>&1; then
    echo "error: gh CLI not found: $GH_CMD" >&2
    exit 2
fi

run_provenance_check() {
    local msg_file="$1"
    if [ -n "${G8S_PROVENANCE_CHECK_BIN:-}" ]; then
        if [ ! -x "$G8S_PROVENANCE_CHECK_BIN" ]; then
            echo "error: G8S_PROVENANCE_CHECK_BIN is not executable: $G8S_PROVENANCE_CHECK_BIN" >&2
            return 2
        fi
        "$G8S_PROVENANCE_CHECK_BIN" -file "$msg_file"
    else
        go run ./tools/provenance_check -file "$msg_file"
    fi
}

PR_ERR_FILE="$(mktemp "${TMPDIR:-/tmp}/safe_merge_err_XXXXXX")"
BODY_FILE=""
FULL_MSG_FILE=""
cleanup() {
    rm -f "${PR_ERR_FILE:-}" "${BODY_FILE:-}" "${FULL_MSG_FILE:-}"
}
trap cleanup EXIT

# -----------------------------------------------------------------------------
# Gate 1: Fetch PR metadata & enforce OPEN state + 100% green statusCheckRollup
# -----------------------------------------------------------------------------
PR_JSON=""
if ! PR_JSON="$("$GH_CMD" pr view "$PR_NUM" --json title,body,state,statusCheckRollup 2>"$PR_ERR_FILE")"; then
    PR_ERR="$(cat "$PR_ERR_FILE" 2>/dev/null || true)"
    echo "GATE pr: fail — failed to query PR #${PR_NUM}: ${PR_ERR:-$PR_JSON}" >&2
    exit 1
fi

if ! printf '%s\n' "$PR_JSON" | jq -e . >/dev/null 2>&1; then
    echo "GATE pr: fail — invalid JSON returned from gh pr view for PR #${PR_NUM}" >&2
    exit 1
fi

PR_STATE="$(printf '%s\n' "$PR_JSON" | jq -r '.state // empty')"
if [ "$PR_STATE" != "OPEN" ]; then
    echo "GATE pr: fail — PR #${PR_NUM} state is '${PR_STATE:-unknown}' (expected OPEN)" >&2
    exit 1
fi
echo "GATE pr: pass — PR #${PR_NUM} is OPEN"

ROLLUP_SUMMARY="$(printf '%s\n' "$PR_JSON" | jq -r '
  def item_name: (.name // .context // "(unnamed check)");
  def item_kind:
    if .status != null then
      if (.status | tostring | ascii_upcase) != "COMPLETED" then "pending"
      elif ((.conclusion // "") | tostring | ascii_upcase) == "SUCCESS"
        or ((.conclusion // "") | tostring | ascii_upcase) == "SKIPPED"
        or ((.conclusion // "") | tostring | ascii_upcase) == "NEUTRAL" then "pass"
      else "fail"
      end
    elif .state != null then
      if ((.state | tostring | ascii_upcase) == "PENDING")
        or ((.state | tostring | ascii_upcase) == "EXPECTED") then "pending"
      elif ((.state | tostring | ascii_upcase) == "SUCCESS") then "pass"
      else "fail"
      end
    else "fail"
    end;
  (.statusCheckRollup // []) as $items
  | ($items | map({name: item_name, kind: item_kind})) as $classified
  | {
      total: ($classified | length),
      pass: ($classified | map(select(.kind == "pass")) | length),
      pending: ($classified | map(select(.kind == "pending")) | length),
      fail: ($classified | map(select(.kind == "fail")) | length),
      pending_names: ($classified | map(select(.kind == "pending") | .name) | join(", ")),
      fail_names: ($classified | map(select(.kind == "fail") | .name) | join(", "))
    }
  | "\(.total)|\(.pass)|\(.pending)|\(.fail)|\(.pending_names)|\(.fail_names)"
')"

IFS='|' read -r TOTAL_CHECKS PASS_CHECKS PENDING_CHECKS FAIL_CHECKS PENDING_NAMES FAIL_NAMES <<< "$ROLLUP_SUMMARY"

if [ "$TOTAL_CHECKS" -eq 0 ]; then
    echo "GATE ci: fail — zero checks found on PR #${PR_NUM} (fail-closed)" >&2
    exit 1
fi

if [ "$PENDING_CHECKS" -gt 0 ] || [ "$FAIL_CHECKS" -gt 0 ]; then
    echo "GATE ci: fail — pass=${PASS_CHECKS}, pending=${PENDING_CHECKS}, failure=${FAIL_CHECKS}" >&2
    if [ -n "$PENDING_NAMES" ]; then
        echo "  pending checks: ${PENDING_NAMES}" >&2
    fi
    if [ -n "$FAIL_NAMES" ]; then
        echo "  failing checks: ${FAIL_NAMES}" >&2
    fi
    exit 1
fi

echo "GATE ci: pass — checks green (pass=${PASS_CHECKS}, pending=0, failure=0)"

# -----------------------------------------------------------------------------
# Gate 2: Assemble ADR-0039 Squash Commit Subject & Body
# -----------------------------------------------------------------------------
PR_TITLE="$(printf '%s\n' "$PR_JSON" | jq -r '.title // empty')"
PR_BODY="$(printf '%s\n' "$PR_JSON" | jq -r '.body // ""')"

if [ -z "$PR_TITLE" ] && [ "$HAS_SUBJECT" -eq 0 ]; then
    echo "error: PR #${PR_NUM} title is empty and --subject was not provided" >&2
    exit 1
fi

if [ "$HAS_SUBJECT" -eq 1 ]; then
    SUBJECT="$(trim_ws "$SUBJECT_ARG")"
else
    case "$PR_TITLE" in
        *"(#${PR_NUM})") SUBJECT="$PR_TITLE" ;;
        *) SUBJECT="${PR_TITLE} (#${PR_NUM})" ;;
    esac
fi

if [ -z "$SUBJECT" ]; then
    echo "error: resolved squash subject is empty" >&2
    exit 1
fi

if [ "$HAS_SUMMARY" -eq 1 ]; then
    SUMMARY="$(printf '%s\n' "$SUMMARY_ARG" | tr -d '\r' | sed -E 's/[[:space:]]+$//')"
else
    SUMMARY="$PR_TITLE"
fi

if [ -z "$(trim_ws "$SUMMARY")" ]; then
    SUMMARY="$SUBJECT"
fi

CLOSES_LINES=""
if [ "$HAS_CLOSES" -eq 1 ]; then
    IFS=',' read -r -a RAW_CLOSES <<< "$CLOSES_ARG"
    PARSED_CLOSES=""
    for tok in "${RAW_CLOSES[@]}"; do
        tok="$(trim_ws "$tok")"
        [ -z "$tok" ] && continue
        tok="${tok#\#}"
        if ! [[ "$tok" =~ ^[0-9]+$ ]] || [ "$tok" -le 0 ]; then
            echo "error: invalid issue number in --closes: '$tok'" >&2
            exit 2
        fi
        if [ -z "$PARSED_CLOSES" ]; then
            PARSED_CLOSES="Closes #${tok}"
        else
            PARSED_CLOSES="${PARSED_CLOSES}"$'\n'"Closes #${tok}"
        fi
    done
    if [ -n "$PARSED_CLOSES" ]; then
        CLOSES_LINES="$(printf '%s\n' "$PARSED_CLOSES" | awk '!seen[$0]++')"
    fi
else
    CLOSES_LINES="$(printf '%s\n' "$PR_BODY" \
        | tr -d '\r' \
        | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//' \
        | grep -E '^(Closes|Fixes|Part of)[[:space:]]+#[0-9]+' \
        | sed -E 's/^(Closes|Fixes|Part of)[[:space:]]+#([0-9]+).*/\1 #\2/' \
        | awk '!seen[$0]++' || true)"
fi

if [ "$HAS_RECEIPT" -eq 1 ]; then
    TRAILER_LINE="Receipt: ${RECEIPT_ID}"
else
    TRAILER_LINE="Provenance: F0-direct — ${F0_DIRECT_REASON}"
fi

BODY_FILE="$(mktemp "${TMPDIR:-/tmp}/safe_merge_body_XXXXXX")"
FULL_MSG_FILE="$(mktemp "${TMPDIR:-/tmp}/safe_merge_full_XXXXXX")"

{
    printf '%s\n' "$SUMMARY"
    if [ -n "$CLOSES_LINES" ]; then
        printf '\n%s\n' "$CLOSES_LINES"
    fi
    printf '\n%s\n' "$TRAILER_LINE"
} > "$BODY_FILE"

{
    printf '%s\n\n' "$SUBJECT"
    cat "$BODY_FILE"
} > "$FULL_MSG_FILE"

# -----------------------------------------------------------------------------
# Gate 3: Mechanical Provenance Validation via internal/dogfood.ClassifyCommit
# -----------------------------------------------------------------------------
PROV_OUT=""
if ! PROV_OUT="$(run_provenance_check "$FULL_MSG_FILE" 2>&1)"; then
    echo "GATE provenance: fail — ADR-0039 trailer validation rejected squash message:" >&2
    echo "  ${PROV_OUT}" >&2
    exit 1
fi
echo "GATE provenance: pass — ${PROV_OUT}"

# -----------------------------------------------------------------------------
# Gate 4: Dry-Run or Execute Squash Merge
# -----------------------------------------------------------------------------
if [ "$DRY_RUN" = true ]; then
    echo "DRY-RUN: would execute: ${GH_CMD} pr merge ${PR_NUM} --squash --delete-branch --subject \"${SUBJECT}\" --body-file <tmp>"
    echo "--- Verified Subject ---"
    printf '%s\n' "$SUBJECT"
    echo "--- Verified Body ---"
    cat "$BODY_FILE"
    exit 0
fi

if ! "$GH_CMD" pr merge "$PR_NUM" --squash --delete-branch --subject "$SUBJECT" --body-file "$BODY_FILE"; then
    echo "ERROR: gh pr merge failed for PR #${PR_NUM}" >&2
    exit 1
fi

ISO_TS="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
echo "MERGED #${PR_NUM} at ${ISO_TS}"
exit 0
