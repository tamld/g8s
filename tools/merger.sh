#!/usr/bin/env bash
#
# tools/merger.sh — Gated auto-merge executor for docs-lane PRs (issue #516, SCORECARD S-8)
#
# D3 boundary (hard): the merger never mints receipts, never submits/resubmits
# tasks, never touches the controlplane state — it reads config and verdicts,
# runs read-only checks, and merges a PR whose checks are already green.
#
# Usage:
#   tools/merger.sh --pr <num> --task <task-id> [--dry-run]
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Ensure we run from repository root
cd "$REPO_ROOT"

PR_NUM=""
TASK_ID=""
DRY_RUN=false

usage() {
    echo "Usage: $0 --pr <num> --task <task-id> [--dry-run]" >&2
    echo "  --pr <num>         GitHub pull request number (integer)" >&2
    echo "  --task <task-id>   Task ID for verifier verdict check" >&2
    echo "  --dry-run          Run all gates and report planned action without merging" >&2
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
        --task)
            if [ $# -lt 2 ]; then
                echo "error: --task requires a task ID" >&2
                usage
                exit 2
            fi
            TASK_ID="$2"
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

if [ -z "$PR_NUM" ]; then
    echo "error: missing required argument --pr" >&2
    usage
    exit 2
fi

if ! [[ "$PR_NUM" =~ ^[0-9]+$ ]]; then
    echo "error: --pr must be a positive integer, got '$PR_NUM'" >&2
    usage
    exit 2
fi

if [ -z "$TASK_ID" ]; then
    echo "error: missing required argument --task" >&2
    usage
    exit 2
fi

# Resolve g8s binary
G8S_CMD="${G8S_BIN:-}"
if [ -z "$G8S_CMD" ]; then
    if [ -x "bin/g8s" ]; then
        G8S_CMD="bin/g8s"
    elif [ -x "${REPO_ROOT}/bin/g8s" ]; then
        G8S_CMD="${REPO_ROOT}/bin/g8s"
    elif command -v g8s >/dev/null 2>&1; then
        G8S_CMD="g8s"
    else
        G8S_CMD="bin/g8s"
    fi
fi

# Resolve gh binary
GH_CMD="${GH_BIN:-}"
if [ -z "$GH_CMD" ]; then
    if command -v gh >/dev/null 2>&1; then
        GH_CMD="gh"
    else
        GH_CMD="gh"
    fi
fi

# -----------------------------------------------------------------------------
# Gate a: Autonomy gate
# bin/g8s config get autonomy_level must be exactly 1.
# At 0 the tool refuses with exit 3 before ANY other work.
# -----------------------------------------------------------------------------
autonomy_val=""
autonomy_err=0
autonomy_val=$("$G8S_CMD" config get autonomy_level 2>/dev/null) || autonomy_err=$?

autonomy_val=$(echo "$autonomy_val" | tr -d '[:space:]')

if [ "$autonomy_err" -ne 0 ] || [ "$autonomy_val" != "1" ]; then
    if [ "$autonomy_val" = "0" ] || [ -z "$autonomy_val" ]; then
        echo "GATE autonomy: fail — autonomy_level=0: the merger refuses to act (operator flip required, ADR-0029/0024 posture)"
        exit 3
    else
        echo "GATE autonomy: fail — autonomy_level=${autonomy_val}: the merger refuses to act (operator flip required, ADR-0029/0024 posture)"
        exit 3
    fi
fi
echo "GATE autonomy: pass — autonomy_level=1 (operator flip recorded)"

# -----------------------------------------------------------------------------
# Gate b: Lane gate
# Fetch the PR ref, bash tools/ci_lane_detect.sh origin/main against the PR diff
# Must be lane=docs; single code file => refuse.
# -----------------------------------------------------------------------------
# Attempt to fetch PR ref
git fetch origin "pull/${PR_NUM}/head:refs/remotes/origin/pr/${PR_NUM}" 2>/dev/null || \
git fetch origin "refs/pull/${PR_NUM}/head:refs/remotes/origin/pr/${PR_NUM}" 2>/dev/null || \
git fetch origin "pull/${PR_NUM}/head" 2>/dev/null || true

PR_COMMIT=""
for ref in "refs/remotes/origin/pr/${PR_NUM}" "FETCH_HEAD" "refs/pull/${PR_NUM}/head" "pull/${PR_NUM}/head" "pr-${PR_NUM}" "origin/pr-${PR_NUM}"; do
    if git rev-parse --verify "${ref}^{commit}" >/dev/null 2>&1; then
        PR_COMMIT="$(git rev-parse --verify "${ref}^{commit}")"
        break
    fi
done

if [ -z "$PR_COMMIT" ] && command -v "$GH_CMD" >/dev/null 2>&1; then
    PR_COMMIT="$("$GH_CMD" pr view "$PR_NUM" --json headRefOid --jq .headRefOid 2>/dev/null || true)"
fi

if [ -z "$PR_COMMIT" ]; then
    echo "GATE lane: fail — could not resolve commit for PR #${PR_NUM}"
    exit 1
fi

WORKTREE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/merger_wt_XXXXXX")"
cleanup_worktree() {
    if [ -d "$WORKTREE_DIR" ]; then
        git worktree remove --force "$WORKTREE_DIR" >/dev/null 2>&1 || true
        rm -rf "$WORKTREE_DIR" 2>/dev/null || true
    fi
}
trap cleanup_worktree EXIT

if ! git worktree add --detach "$WORKTREE_DIR" "$PR_COMMIT" >/dev/null 2>&1; then
    echo "GATE lane: fail — could not create detached worktree for commit ${PR_COMMIT}"
    exit 1
fi

lane_detected=$(
    cd "$WORKTREE_DIR" && \
    bash "${REPO_ROOT}/tools/ci_lane_detect.sh" origin/main 2>/dev/null || echo "lane=build"
)
cleanup_worktree
trap - EXIT

if [ "$lane_detected" != "lane=docs" ]; then
    echo "GATE lane: fail — ${lane_detected} (non-docs files detected or empty diff)"
    exit 1
fi
echo "GATE lane: pass — lane=docs (docs-only PR)"

# -----------------------------------------------------------------------------
# Gate c: Verifier gate
# bin/g8s verify --task <task-id>
# Require registered=true, class=docs, outcome=pass, status=advisory|hard
# Advisory verdicts are ACCEPTED with verbatim human decision justification.
# -----------------------------------------------------------------------------
verify_raw=""
verify_code=0
verify_raw=$("$G8S_CMD" verify --task "$TASK_ID" 2>&1) || verify_code=$?

# Extract fields from verdict JSON envelope
v_reg=""
v_class=""
v_outcome=""
v_status=""

if command -v jq >/dev/null 2>&1 && echo "$verify_raw" | jq -e . >/dev/null 2>&1; then
    v_reg=$(echo "$verify_raw" | jq -r 'if .data.registered != null then .data.registered else (.registered // empty) end')
    v_class=$(echo "$verify_raw" | jq -r 'if .data.class != null then .data.class else (.class // empty) end')
    v_outcome=$(echo "$verify_raw" | jq -r 'if .data.outcome != null then .data.outcome else (.outcome // empty) end')
    v_status=$(echo "$verify_raw" | jq -r 'if .data.status != null then .data.status else (.status // empty) end')
elif command -v python3 >/dev/null 2>&1; then
    eval "$(python3 -c '
import json, sys
try:
    raw = sys.stdin.read()
    d = json.loads(raw)
    data = d.get("data", d)
    print(f"v_reg=\"{str(data.get(\"registered\", \"\")).lower()}\"")
    print(f"v_class=\"{data.get(\"class\", \"\")}\"")
    print(f"v_outcome=\"{data.get(\"outcome\", \"\")}\"")
    print(f"v_status=\"{data.get(\"status\", \"\")}\"")
except Exception:
    pass
' <<< "$verify_raw")"
fi

if [ -z "$v_class" ] && [ -z "$v_outcome" ]; then
    echo "GATE verifier: fail — failed to parse verifier output or verify command failed (exit code ${verify_code}): ${verify_raw}"
    exit 1
fi

if [ "$v_reg" != "true" ]; then
    echo "GATE verifier: fail — unregistered task class (registered=${v_reg:-false}, class=${v_class})"
    exit 1
fi

if [ "$v_class" != "docs" ]; then
    echo "GATE verifier: fail — non-docs class (class=${v_class}, expected docs)"
    exit 1
fi

if [ "$v_outcome" != "pass" ]; then
    echo "GATE verifier: fail — verification outcome was ${v_outcome} (expected pass)"
    exit 1
fi

if [ "$v_status" != "advisory" ] && [ "$v_status" != "hard" ]; then
    echo "GATE verifier: fail — unexpected status: ${v_status} (expected advisory|hard)"
    exit 1
fi

if [ "$v_status" = "advisory" ]; then
    echo "GATE verifier: pass — class=docs registered=true outcome=pass status=advisory (advisory verdicts are ACCEPTED here because the operator flip to level 1 IS the recorded human decision for this round)"
else
    echo "GATE verifier: pass — class=docs registered=true outcome=pass status=hard"
fi

# -----------------------------------------------------------------------------
# Gate d: CI gate
# gh pr checks <num> -> pending=0 AND failure=0
# -----------------------------------------------------------------------------
checks_raw=""
checks_err=0
checks_raw=$("$GH_CMD" pr checks "$PR_NUM" 2>&1) || checks_err=$?

pending_count=0
failure_count=0
pass_count=0

if command -v jq >/dev/null 2>&1 && echo "$checks_raw" | jq -e . >/dev/null 2>&1; then
    pending_count=$(echo "$checks_raw" | jq -r '[.[]? | select((.state // .status // "") | ascii_downcase | test("pending|in_progress|queued|waiting"))] | length')
    failure_count=$(echo "$checks_raw" | jq -r '[.[]? | select((.state // .status // .conclusion // "") | ascii_downcase | test("fail|timed_out|cancelled|action_required|startup_failure"))] | length')
    pass_count=$(echo "$checks_raw" | jq -r '[.[]? | select((.state // .status // .conclusion // "") | ascii_downcase | test("pass|success|skipped|neutral"))] | length')
else
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        lower_line=$(echo "$line" | tr '[:upper:]' '[:lower:]')
        if echo "$lower_line" | grep -qE "pending|in_progress|queued|waiting"; then
            pending_count=$((pending_count + 1))
        elif echo "$lower_line" | grep -qE "fail|timed_out|cancelled|action_required|startup_failure"; then
            failure_count=$((failure_count + 1))
        elif echo "$lower_line" | grep -qE "pass|success|skipping|neutral"; then
            pass_count=$((pass_count + 1))
        fi
    done <<< "$checks_raw"
fi

total_checks=$((pending_count + failure_count + pass_count))
if [ "$total_checks" -eq 0 ]; then
    echo "GATE ci: fail — no checks reported or gh pr checks failed: ${checks_raw}"
    exit 1
fi

if [ "$pending_count" -gt 0 ]; then
    echo "GATE ci: fail — checks pending (pending=${pending_count}, failure=${failure_count})"
    exit 1
fi

if [ "$failure_count" -gt 0 ]; then
    echo "GATE ci: fail — checks failed (pending=${pending_count}, failure=${failure_count})"
    exit 1
fi

echo "GATE ci: pass — checks green (pass=${pass_count}, pending=0, failure=0)"

# -----------------------------------------------------------------------------
# Merge action
# -----------------------------------------------------------------------------
if [ "$DRY_RUN" = true ]; then
    echo "DRY-RUN: would execute: ${GH_CMD} pr merge ${PR_NUM} --squash --delete-branch"
    exit 0
fi

if ! "$GH_CMD" pr merge "$PR_NUM" --squash --delete-branch; then
    echo "ERROR: gh pr merge failed for PR #${PR_NUM}" >&2
    exit 1
fi

iso_ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "MERGED ${PR_NUM} at ${iso_ts}"
exit 0
