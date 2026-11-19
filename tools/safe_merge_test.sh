#!/usr/bin/env bash
#
# tools/safe_merge_test.sh — Offline test suite for tools/safe_merge.sh
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SAFE_MERGE="${SCRIPT_DIR}/safe_merge.sh"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/safe_merge_test_XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

PROV_BIN="${TMP_DIR}/provenance_check"
CGO_ENABLED=0 go build -o "$PROV_BIN" "${REPO_ROOT}/tools/provenance_check"
export G8S_PROVENANCE_CHECK_BIN="$PROV_BIN"

MOCK_GH="${TMP_DIR}/mock_gh"
cat > "$MOCK_GH" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
cmd="${1:-}"
sub="${2:-}"

if [ "$cmd" = "pr" ] && [ "$sub" = "view" ]; then
    if [ -n "${MOCK_GH_VIEW_LOG:-}" ]; then
        printf '%s\n' "$*" >> "$MOCK_GH_VIEW_LOG"
    fi
    if [ -n "${MOCK_GH_STDERR_HINT:-}" ]; then
        printf '%s\n' "$MOCK_GH_STDERR_HINT" >&2
    fi
    cat "$MOCK_GH_PR_JSON_FILE"
    exit 0
fi

if [ "$cmd" = "pr" ] && [ "$sub" = "merge" ]; then
    if [ -n "${MOCK_GH_MERGE_LOG:-}" ]; then
        printf '%s\n' "$*" >> "$MOCK_GH_MERGE_LOG"
    fi
    shift 2
    while [ $# -gt 0 ]; do
        case "$1" in
            --subject)
                if [ -n "${MOCK_GH_CAPTURE_SUBJECT:-}" ]; then
                    printf '%s' "$2" > "$MOCK_GH_CAPTURE_SUBJECT"
                fi
                shift 2
                ;;
            --body-file)
                if [ -n "${MOCK_GH_CAPTURE_BODY:-}" ]; then
                    cp "$2" "$MOCK_GH_CAPTURE_BODY"
                fi
                shift 2
                ;;
            *)
                shift
                ;;
        esac
    done
    exit 0
fi

echo "unexpected mock gh invocation: $*" >&2
exit 1
EOF
chmod +x "$MOCK_GH"
export GH_BIN="$MOCK_GH"

expect_rc() {
    local expected_rc="$1"
    local label="$2"
    shift 2
    local rc=0
    local out=""
    set +e
    out="$("$@" 2>&1)"
    rc=$?
    set -e
    if [ "$rc" -ne "$expected_rc" ]; then
        echo "FAIL [${label}]: expected rc=${expected_rc}, got rc=${rc}" >&2
        echo "--- output ---" >&2
        echo "$out" >&2
        exit 1
    fi
    printf '  [PASS] %s (rc=%d)\n' "$label" "$rc"
}

write_pr_json() {
    local path="$1"
    cat > "$path"
}

GREEN_PR="${TMP_DIR}/pr_green.json"
write_pr_json "$GREEN_PR" <<'EOF'
{
  "title": "fix(harness): tighten holdout boundary",
  "body": "Summary of changes.\n\nCloses #943\nFixes #944\n",
  "state": "OPEN",
  "statusCheckRollup": [
    {"__typename": "CheckRun", "name": "Quality Gate", "status": "COMPLETED", "conclusion": "SUCCESS"},
    {"__typename": "CheckRun", "name": "Docs lane scope", "status": "COMPLETED", "conclusion": "SKIPPED"},
    {"__typename": "StatusContext", "context": "ci/external", "state": "SUCCESS"}
  ]
}
EOF

echo "==> Running tools/safe_merge.sh offline test suite..."

# 1. Argument validation failures (exit 2)
expect_rc 2 "rejects missing --pr" "$SAFE_MERGE" --receipt task-20261119-001
expect_rc 2 "rejects non-numeric --pr" "$SAFE_MERGE" --pr abc --receipt task-20261119-001
expect_rc 2 "rejects missing provenance flag" "$SAFE_MERGE" --pr 953
expect_rc 2 "rejects both --receipt and --f0-direct" "$SAFE_MERGE" --pr 953 --receipt task-20261119-001 --f0-direct "why"

# 2. Invalid receipt ID rejected via real provenance_check (exit 1)
export MOCK_GH_PR_JSON_FILE="$GREEN_PR"
expect_rc 1 "rejects invalid --receipt short via provenance_check" "$SAFE_MERGE" --pr 953 --receipt short

# 3. Whitespace-only --f0-direct rejected
set +e
"$SAFE_MERGE" --pr 953 --f0-direct "   " >/dev/null 2>&1
rc_ws=$?
set -e
if [ "$rc_ws" -ne 1 ] && [ "$rc_ws" -ne 2 ]; then
    echo "FAIL [whitespace-only --f0-direct]: expected rc 1 or 2, got $rc_ws" >&2
    exit 1
fi
printf '  [PASS] rejects whitespace-only --f0-direct (rc=%d)\n' "$rc_ws"

# 4. Rejects PR with state MERGED
MERGED_PR="${TMP_DIR}/pr_merged.json"
write_pr_json "$MERGED_PR" <<'EOF'
{
  "title": "chore: done",
  "body": "",
  "state": "MERGED",
  "statusCheckRollup": [
    {"__typename": "CheckRun", "name": "Quality Gate", "status": "COMPLETED", "conclusion": "SUCCESS"}
  ]
}
EOF
export MOCK_GH_PR_JSON_FILE="$MERGED_PR"
expect_rc 1 "rejects non-OPEN PR (MERGED)" "$SAFE_MERGE" --pr 953 --f0-direct "already merged"

# 5. Rejects failing CheckRun
FAIL_CR_PR="${TMP_DIR}/pr_fail_cr.json"
write_pr_json "$FAIL_CR_PR" <<'EOF'
{
  "title": "fix: test",
  "body": "",
  "state": "OPEN",
  "statusCheckRollup": [
    {"__typename": "CheckRun", "name": "Quality Gate", "status": "COMPLETED", "conclusion": "SUCCESS"},
    {"__typename": "CheckRun", "name": "Test windows-latest", "status": "COMPLETED", "conclusion": "FAILURE"}
  ]
}
EOF
export MOCK_GH_PR_JSON_FILE="$FAIL_CR_PR"
expect_rc 1 "rejects failing CheckRun" "$SAFE_MERGE" --pr 953 --f0-direct "should fail on FAILURE check"

# 6. Rejects pending CheckRun
PENDING_CR_PR="${TMP_DIR}/pr_pending_cr.json"
write_pr_json "$PENDING_CR_PR" <<'EOF'
{
  "title": "fix: test",
  "body": "",
  "state": "OPEN",
  "statusCheckRollup": [
    {"__typename": "CheckRun", "name": "Test macos-latest", "status": "IN_PROGRESS", "conclusion": null}
  ]
}
EOF
export MOCK_GH_PR_JSON_FILE="$PENDING_CR_PR"
expect_rc 1 "rejects pending CheckRun" "$SAFE_MERGE" --pr 953 --f0-direct "should fail on IN_PROGRESS check"

# 7. Rejects failing StatusContext
FAIL_SC_PR="${TMP_DIR}/pr_fail_sc.json"
write_pr_json "$FAIL_SC_PR" <<'EOF'
{
  "title": "fix: test",
  "body": "",
  "state": "OPEN",
  "statusCheckRollup": [
    {"__typename": "StatusContext", "context": "ci/ext", "state": "ERROR"}
  ]
}
EOF
export MOCK_GH_PR_JSON_FILE="$FAIL_SC_PR"
expect_rc 1 "rejects failing StatusContext" "$SAFE_MERGE" --pr 953 --f0-direct "should fail on ERROR status"

# 8. Rejects zero checks
ZERO_PR="${TMP_DIR}/pr_zero.json"
write_pr_json "$ZERO_PR" <<'EOF'
{
  "title": "fix: empty rollup",
  "body": "",
  "state": "OPEN",
  "statusCheckRollup": []
}
EOF
export MOCK_GH_PR_JSON_FILE="$ZERO_PR"
expect_rc 1 "rejects zero checks (fail-closed)" "$SAFE_MERGE" --pr 953 --f0-direct "should fail on empty rollup"

# 9. Passes --dry-run on green PR with --receipt (even with gh stderr hints) and does NOT call gh pr merge
export MOCK_GH_PR_JSON_FILE="$GREEN_PR"
export MOCK_GH_MERGE_LOG="${TMP_DIR}/merge_calls_dry.log"
export MOCK_GH_STDERR_HINT="hint: if you need to wait for this pull request to be reviewed, use gh event listen"
rm -f "$MOCK_GH_MERGE_LOG"
DRY_OUT="$("$SAFE_MERGE" --pr 953 --receipt task-20261119-001 --dry-run)"
unset MOCK_GH_STDERR_HINT
if [ -e "$MOCK_GH_MERGE_LOG" ]; then
    echo "FAIL [dry-run]: gh pr merge was unexpectedly invoked during --dry-run" >&2
    exit 1
fi
if ! printf '%s\n' "$DRY_OUT" | grep -q "^Closes #943$" || ! printf '%s\n' "$DRY_OUT" | grep -q "^Receipt: task-20261119-001$"; then
    echo "FAIL [dry-run]: expected Closes #943 and Receipt: task-20261119-001 in output:" >&2
    echo "$DRY_OUT" >&2
    exit 1
fi
# Ensure blank line precedes Receipt:
if ! printf '%s\n' "$DRY_OUT" | awk '/^Fixes #944$/{getline; if ($0 != "") exit 1; getline; if ($0 != "Receipt: task-20261119-001") exit 1; found=1} END{if (!found) exit 1}'; then
    echo "FAIL [dry-run]: Closes/Fixes lines not separated from Receipt trailer by a blank line:" >&2
    echo "$DRY_OUT" >&2
    exit 1
fi
echo "  [PASS] --dry-run validates green checks, preserves blank-line trailer layout, skips merge"

# 10. Real merge path captures exact --subject and --body-file and passes provenance_check
export MOCK_GH_MERGE_LOG="${TMP_DIR}/merge_calls_real.log"
export MOCK_GH_CAPTURE_SUBJECT="${TMP_DIR}/captured_subject.txt"
export MOCK_GH_CAPTURE_BODY="${TMP_DIR}/captured_body.txt"
rm -f "$MOCK_GH_MERGE_LOG" "$MOCK_GH_CAPTURE_SUBJECT" "$MOCK_GH_CAPTURE_BODY"

MERGE_OUT="$("$SAFE_MERGE" --pr 953 --f0-direct "bootstrap safe merge helper")"
if [ ! -s "$MOCK_GH_MERGE_LOG" ]; then
    echo "FAIL [real-merge]: gh pr merge was not invoked" >&2
    exit 1
fi
if ! grep -q "pr merge 953 --squash --delete-branch" "$MOCK_GH_MERGE_LOG"; then
    echo "FAIL [real-merge]: expected --squash --delete-branch in gh invocation: $(cat "$MOCK_GH_MERGE_LOG")" >&2
    exit 1
fi
CAPTURED_SUBJ="$(cat "$MOCK_GH_CAPTURE_SUBJECT")"
CAPTURED_BODY="$(cat "$MOCK_GH_CAPTURE_BODY")"
PROV_VERIFY="$(printf '%s\n\n%s\n' "$CAPTURED_SUBJ" "$CAPTURED_BODY" | "$PROV_BIN")"
if ! printf '%s\n' "$PROV_VERIFY" | grep -q "OK: kind=f0_direct ref=bootstrap safe merge helper"; then
    echo "FAIL [real-merge]: captured squash commit failed provenance_check: $PROV_VERIFY" >&2
    exit 1
fi
if ! printf '%s\n' "$MERGE_OUT" | grep -q "MERGED #953 at "; then
    echo "FAIL [real-merge]: expected MERGED summary line, got: $MERGE_OUT" >&2
    exit 1
fi
echo "  [PASS] real merge invokes gh pr merge --squash --delete-branch with valid ADR-0039 body"

echo "[SAFE-MERGE TESTS PASSED: 10/10]"
