#!/usr/bin/env bash
#
# tools/merger_test.sh — Test suite for tools/merger.sh (issue #516, SCORECARD S-8)
#
# Covers:
#   1. Refuses at autonomy_level=0 (exit 3, before any other gates)
#   2. Refuses when lane=build (exit 1)
#   3. Refuses when verifier says unregistered or outcome=fail (exit 1)
#   4. Refuses when checks pending (exit 1)
#   5. Dry-run never merges (all gates pass, prints planned action, exit 0)
#   6. All-green prints all four GATE lines then the MERGED line (exit 0)
#   7. Advisory verdict includes human decision rationale verbatim
#   8. Hard verifier verdict passes
#   9. Argument validation (exit 2)
#

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REAL_REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
MERGER_SRC="${REAL_REPO_ROOT}/tools/merger.sh"
DETECT_SRC="${REAL_REPO_ROOT}/tools/ci_lane_detect.sh"

if [ ! -f "$MERGER_SRC" ]; then
    echo "ERROR: merger.sh not found at $MERGER_SRC"
    exit 1
fi

TMP_TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/merger_test_XXXXXX")
cleanup() {
    rm -rf "$TMP_TEST_DIR"
}
trap cleanup EXIT

FAILURES=0

echo "==> Running tools/merger.sh test suite..."

# Setup mock sandbox
MOCK_DIR="${TMP_TEST_DIR}/mock"
mkdir -p "$MOCK_DIR"

MOCK_BIN="${TMP_TEST_DIR}/bin"
mkdir -p "$MOCK_BIN"

# Mock g8s script
cat > "${MOCK_BIN}/g8s" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

MOCK_DIR="${MOCK_DIR:-/tmp}"

cmd="${1:-}"
shift || true

case "$cmd" in
    config)
        sub="${1:-}"
        shift || true
        key="${1:-}"
        if [ "$sub" = "get" ] && [ "$key" = "autonomy_level" ]; then
            if [ -f "${MOCK_DIR}/autonomy_level" ]; then
                cat "${MOCK_DIR}/autonomy_level"
            else
                echo "0"
            fi
            exit 0
        fi
        echo "unknown config command" >&2
        exit 1
        ;;
    verify)
        task_id=""
        while [ $# -gt 0 ]; do
            case "$1" in
                --task)
                    task_id="$2"
                    shift 2
                    ;;
                *)
                    shift
                    ;;
            esac
        done
        
        if [ -f "${MOCK_DIR}/verifier_envelope.json" ]; then
            cat "${MOCK_DIR}/verifier_envelope.json"
            exit 0
        fi

        v_reg="${MOCK_VERIFIER_REGISTERED:-true}"
        v_class="${MOCK_VERIFIER_CLASS:-docs}"
        v_outcome="${MOCK_VERIFIER_OUTCOME:-pass}"
        v_status="${MOCK_VERIFIER_STATUS:-advisory}"

        cat <<JSON
{
  "v": 1,
  "kind": "verdict",
  "cmd": "verify",
  "sub": "${task_id}",
  "data": {
    "class": "${v_class}",
    "registered": ${v_reg},
    "status": "${v_status}",
    "outcome": "${v_outcome}",
    "checks": [],
    "scope": true,
    "recorded_at": "2026-10-03T12:00:00Z"
  },
  "at": "2026-10-03T12:00:00Z"
}
JSON
        if [ "$v_status" = "hard" ] && [ "$v_outcome" = "fail" ]; then
            exit 1
        fi
        exit 0
        ;;
    *)
        echo "mock g8s: unknown command $cmd" >&2
        exit 1
        ;;
esac
EOF
chmod +x "${MOCK_BIN}/g8s"

# Mock gh script
cat > "${MOCK_BIN}/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

MOCK_DIR="${MOCK_DIR:-/tmp}"

cmd="${1:-}"
shift || true

case "$cmd" in
    pr)
        sub="${1:-}"
        shift || true
        case "$sub" in
            checks)
                pr_num="${1:-}"
                if [ -f "${MOCK_DIR}/gh_checks" ]; then
                    cat "${MOCK_DIR}/gh_checks"
                else
                    echo "docs-battery pass 10s https://github.com/tamld/g8s/actions/runs/1"
                fi
                exit 0
                ;;
            merge)
                pr_num="${1:-}"
                echo "$@" >> "${MOCK_DIR}/merge_calls.log"
                echo "Squashed and merged PR #${pr_num}"
                exit 0
                ;;
            view)
                pr_num="${1:-}"
                if [ -f "${MOCK_DIR}/pr_commit" ]; then
                    commit=$(cat "${MOCK_DIR}/pr_commit")
                    echo "{\"headRefOid\":\"${commit}\"}"
                else
                    echo "{}"
                fi
                exit 0
                ;;
            *)
                echo "mock gh pr: unknown subcommand $sub" >&2
                exit 1
                ;;
        esac
        ;;
    *)
        echo "mock gh: unknown command $cmd" >&2
        exit 1
        ;;
esac
EOF
chmod +x "${MOCK_BIN}/gh"

export PATH="${MOCK_BIN}:${PATH}"
export MOCK_DIR

# Setup fixture git repository
FIXTURE_REPO="${TMP_TEST_DIR}/repo"
mkdir -p "${FIXTURE_REPO}"
(
    cd "$FIXTURE_REPO"
    git init -q
    git checkout -q -b main 2>/dev/null || git branch -m main
    git config user.email "ci-test@example.com"
    git config user.name "CI Tester"
    echo "# g8s test fixture" > README.md
    git add README.md
    git commit -q -m "initial commit"

    # Setup tools directory in fixture repo
    mkdir -p tools bin
    cp "$DETECT_SRC" tools/ci_lane_detect.sh
    chmod +x tools/ci_lane_detect.sh
    cp "$MERGER_SRC" tools/merger.sh
    chmod +x tools/merger.sh
    cp "${MOCK_BIN}/g8s" bin/g8s
    chmod +x bin/g8s

    git add tools/ README.md
    git commit -q -m "add tools"

    # Ensure origin/main ref exists
    git update-ref refs/remotes/origin/main HEAD

    # Create PR 1: Docs only
    git checkout -q -b branch-docs main
    echo "Documentation addition" >> README.md
    git commit -q -am "docs update"
    git update-ref refs/remotes/origin/pr/1 HEAD

    # Create PR 2: Code change (build lane)
    git checkout -q -b branch-build main
    echo "package main" > main.go
    git add main.go
    git commit -q -m "code change"
    git update-ref refs/remotes/origin/pr/2 HEAD

    # Return to main
    git checkout -q main
)

MERGER_BIN="${FIXTURE_REPO}/tools/merger.sh"

run_merger() {
    (
        cd "$FIXTURE_REPO"
        bash "$MERGER_BIN" "$@"
    )
}

# -----------------------------------------------------------------------------
# Test 1: Refuses at autonomy_level=0 (exit 3, before any other gates)
# -----------------------------------------------------------------------------
echo "Testing: [1/9] Refuses at level 0 (exit 3, before other gates)..."
echo "0" > "${MOCK_DIR}/autonomy_level"
rm -f "${MOCK_DIR}/merge_calls.log"

out=""
code=0
out=$(run_merger --pr 1 --task "task-001" 2>&1) || code=$?

if [ "$code" -ne 3 ]; then
    echo "  FAIL: expected exit code 3, got $code"
    echo "  Output: $out"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE autonomy: fail — autonomy_level=0: the merger refuses to act (operator flip required, ADR-0029/0024 posture)"; then
    echo "  FAIL: expected autonomy refusal message, got: $out"
    FAILURES=$((FAILURES + 1))
elif echo "$out" | grep -q "GATE lane:"; then
    echo "  FAIL: executed lane gate despite autonomy refusal!"
    FAILURES=$((FAILURES + 1))
elif [ -f "${MOCK_DIR}/merge_calls.log" ]; then
    echo "  FAIL: executed merge despite refusal!"
    FAILURES=$((FAILURES + 1))
else
    echo "  ok (exit 3, stopped before other gates)"
fi

# -----------------------------------------------------------------------------
# Test 2: Refuses when lane=build (exit 1)
# -----------------------------------------------------------------------------
echo "Testing: [2/9] Refuses when lane=build (exit 1)..."
echo "1" > "${MOCK_DIR}/autonomy_level"
rm -f "${MOCK_DIR}/merge_calls.log"

out=""
code=0
out=$(run_merger --pr 2 --task "task-002" 2>&1) || code=$?

if [ "$code" -ne 1 ]; then
    echo "  FAIL: expected exit code 1, got $code"
    echo "  Output: $out"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE autonomy: pass"; then
    echo "  FAIL: autonomy gate should have passed"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE lane: fail — lane=build"; then
    echo "  FAIL: lane gate should have failed with lane=build, got: $out"
    FAILURES=$((FAILURES + 1))
elif echo "$out" | grep -q "GATE verifier:"; then
    echo "  FAIL: executed verifier gate despite lane failure!"
    FAILURES=$((FAILURES + 1))
elif [ -f "${MOCK_DIR}/merge_calls.log" ]; then
    echo "  FAIL: executed merge despite refusal!"
    FAILURES=$((FAILURES + 1))
else
    # Verify the failing gate line is the last line of output
    last_line=$(echo "$out" | tail -n 1)
    if ! echo "$last_line" | grep -q "GATE lane: fail"; then
        echo "  FAIL: failing gate line must be the last output line, got: $last_line"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (refused lane=build, exit 1)"
    fi
fi

# -----------------------------------------------------------------------------
# Test 3a: Refuses when verifier says unregistered (exit 1)
# -----------------------------------------------------------------------------
echo "Testing: [3/9] Refuses when verifier says unregistered..."
echo "1" > "${MOCK_DIR}/autonomy_level"
export MOCK_VERIFIER_REGISTERED="false"
export MOCK_VERIFIER_CLASS="docs"
export MOCK_VERIFIER_OUTCOME="pass"
export MOCK_VERIFIER_STATUS="advisory"
rm -f "${MOCK_DIR}/merge_calls.log"

out=""
code=0
out=$(run_merger --pr 1 --task "task-003a" 2>&1) || code=$?

if [ "$code" -ne 1 ]; then
    echo "  FAIL: expected exit code 1, got $code"
    echo "  Output: $out"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE verifier: fail — unregistered task class"; then
    echo "  FAIL: expected verifier unregistered failure, got: $out"
    FAILURES=$((FAILURES + 1))
elif echo "$out" | grep -q "GATE ci:"; then
    echo "  FAIL: executed CI gate despite verifier failure!"
    FAILURES=$((FAILURES + 1))
else
    last_line=$(echo "$out" | tail -n 1)
    if ! echo "$last_line" | grep -q "GATE verifier: fail"; then
        echo "  FAIL: failing gate line must be the last output line, got: $last_line"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (refused unregistered class)"
    fi
fi

# -----------------------------------------------------------------------------
# Test 3b: Refuses when verifier says outcome=fail (exit 1)
# -----------------------------------------------------------------------------
echo "Testing: [4/9] Refuses when verifier says outcome=fail..."
echo "1" > "${MOCK_DIR}/autonomy_level"
export MOCK_VERIFIER_REGISTERED="true"
export MOCK_VERIFIER_CLASS="docs"
export MOCK_VERIFIER_OUTCOME="fail"
export MOCK_VERIFIER_STATUS="advisory"
rm -f "${MOCK_DIR}/merge_calls.log"

out=""
code=0
out=$(run_merger --pr 1 --task "task-003b" 2>&1) || code=$?

if [ "$code" -ne 1 ]; then
    echo "  FAIL: expected exit code 1, got $code"
    echo "  Output: $out"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE verifier: fail — verification outcome was fail"; then
    echo "  FAIL: expected verifier outcome=fail message, got: $out"
    FAILURES=$((FAILURES + 1))
else
    echo "  ok (refused outcome=fail)"
fi

# -----------------------------------------------------------------------------
# Test 4: Refuses when checks pending (exit 1)
# -----------------------------------------------------------------------------
echo "Testing: [5/9] Refuses when checks pending (exit 1)..."
echo "1" > "${MOCK_DIR}/autonomy_level"
export MOCK_VERIFIER_REGISTERED="true"
export MOCK_VERIFIER_CLASS="docs"
export MOCK_VERIFIER_OUTCOME="pass"
export MOCK_VERIFIER_STATUS="advisory"
echo "docs-battery pending - https://github.com/tamld/g8s/actions/runs/1" > "${MOCK_DIR}/gh_checks"
rm -f "${MOCK_DIR}/merge_calls.log"

out=""
code=0
out=$(run_merger --pr 1 --task "task-004" 2>&1) || code=$?

if [ "$code" -ne 1 ]; then
    echo "  FAIL: expected exit code 1, got $code"
    echo "  Output: $out"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE ci: fail — checks pending"; then
    echo "  FAIL: expected CI pending failure, got: $out"
    FAILURES=$((FAILURES + 1))
elif [ -f "${MOCK_DIR}/merge_calls.log" ]; then
    echo "  FAIL: executed merge despite CI pending!"
    FAILURES=$((FAILURES + 1))
else
    echo "  ok (refused pending checks)"
fi

# -----------------------------------------------------------------------------
# Test 5: Dry-run never merges
# -----------------------------------------------------------------------------
echo "Testing: [6/9] Dry-run never merges (all gates pass, no merge executed)..."
echo "1" > "${MOCK_DIR}/autonomy_level"
export MOCK_VERIFIER_REGISTERED="true"
export MOCK_VERIFIER_CLASS="docs"
export MOCK_VERIFIER_OUTCOME="pass"
export MOCK_VERIFIER_STATUS="advisory"
echo "docs-battery pass 10s https://github.com/tamld/g8s/actions/runs/1" > "${MOCK_DIR}/gh_checks"
rm -f "${MOCK_DIR}/merge_calls.log"

out=""
code=0
out=$(run_merger --pr 1 --task "task-005" --dry-run 2>&1) || code=$?

if [ "$code" -ne 0 ]; then
    echo "  FAIL: expected exit code 0 on dry run, got $code"
    echo "  Output: $out"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE autonomy: pass"; then
    echo "  FAIL: autonomy gate missing pass"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE lane: pass"; then
    echo "  FAIL: lane gate missing pass"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE verifier: pass"; then
    echo "  FAIL: verifier gate missing pass"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE ci: pass"; then
    echo "  FAIL: CI gate missing pass"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "DRY-RUN: would execute:"; then
    echo "  FAIL: missing DRY-RUN explanation in output, got: $out"
    FAILURES=$((FAILURES + 1))
elif echo "$out" | grep -q "^MERGED "; then
    echo "  FAIL: dry-run should not print MERGED line, got: $out"
    FAILURES=$((FAILURES + 1))
elif [ -f "${MOCK_DIR}/merge_calls.log" ]; then
    echo "  FAIL: dry-run executed gh pr merge!"
    FAILURES=$((FAILURES + 1))
else
    echo "  ok (all gates pass, dry-run verified no merge)"
fi

# -----------------------------------------------------------------------------
# Test 6: All-green prints all four GATE lines then the MERGED line
# -----------------------------------------------------------------------------
echo "Testing: [7/9] All-green prints all four GATE lines then MERGED line..."
echo "1" > "${MOCK_DIR}/autonomy_level"
export MOCK_VERIFIER_REGISTERED="true"
export MOCK_VERIFIER_CLASS="docs"
export MOCK_VERIFIER_OUTCOME="pass"
export MOCK_VERIFIER_STATUS="advisory"
echo "docs-battery pass 10s https://github.com/tamld/g8s/actions/runs/1" > "${MOCK_DIR}/gh_checks"
rm -f "${MOCK_DIR}/merge_calls.log"

out=""
code=0
out=$(run_merger --pr 1 --task "task-006" 2>&1) || code=$?

if [ "$code" -ne 0 ]; then
    echo "  FAIL: expected exit code 0, got $code"
    echo "  Output: $out"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE autonomy: pass — autonomy_level=1"; then
    echo "  FAIL: missing GATE autonomy: pass"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE lane: pass — lane=docs"; then
    echo "  FAIL: missing GATE lane: pass"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE verifier: pass"; then
    echo "  FAIL: missing GATE verifier: pass"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE ci: pass — checks green"; then
    echo "  FAIL: missing GATE ci: pass"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -qE "MERGED 1 at [0-9]{4}-[0-9]{2}-[0-9]{2}T"; then
    echo "  FAIL: missing MERGED line with ISO timestamp, got: $out"
    FAILURES=$((FAILURES + 1))
elif [ ! -f "${MOCK_DIR}/merge_calls.log" ]; then
    echo "  FAIL: gh pr merge was not called!"
    FAILURES=$((FAILURES + 1))
else
    echo "  ok (all 4 gate lines + MERGED line printed)"
fi

# -----------------------------------------------------------------------------
# Test 7: Advisory verdict includes human decision rationale verbatim
# -----------------------------------------------------------------------------
echo "Testing: [8/9] Advisory verdict includes human decision rationale verbatim..."
expected_rationale="advisory verdicts are ACCEPTED here because the operator flip to level 1 IS the recorded human decision for this round"
if ! echo "$out" | grep -F -q "$expected_rationale"; then
    echo "  FAIL: output does not contain exact verbatim rationale: '$expected_rationale'"
    echo "  Output: $out"
    FAILURES=$((FAILURES + 1))
else
    echo "  ok (verbatim rationale present in verifier gate output)"
fi

# -----------------------------------------------------------------------------
# Test 8: Hard verifier verdict passes
# -----------------------------------------------------------------------------
echo "Testing: [9/9] Hard verifier status passes..."
export MOCK_VERIFIER_STATUS="hard"
rm -f "${MOCK_DIR}/merge_calls.log"

out=""
code=0
out=$(run_merger --pr 1 --task "task-008" --dry-run 2>&1) || code=$?

if [ "$code" -ne 0 ]; then
    echo "  FAIL: expected exit code 0 for hard verdict pass, got $code"
    FAILURES=$((FAILURES + 1))
elif ! echo "$out" | grep -q "GATE verifier: pass — class=docs registered=true outcome=pass status=hard"; then
    echo "  FAIL: expected status=hard in verifier gate detail, got: $out"
    FAILURES=$((FAILURES + 1))
else
    echo "  ok (status=hard verdict passes)"
fi

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
echo "--------------------------------------------------------"
if [ "$FAILURES" -eq 0 ]; then
    echo "ALL TESTS PASSED (9/9)"
    exit 0
else
    echo "FAILURES DETECTED: $FAILURES"
    exit 1
fi
