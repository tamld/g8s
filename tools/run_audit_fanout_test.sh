#!/usr/bin/env bash
#
# tools/run_audit_fanout_test.sh — Test suite for deep-audit fan-out runner (S6-5, #420)
#
# Validates the audit fan-out runner:
#   (a) --dry-run prints 6 DRY lines (one per dimension)
#   (b) --dimensions docs --dry-run prints exactly 1 DRY line
#   (c) unknown dimension -> exit 2
#   (d) each DRY line contains 'role verifier'
#   (e) individual dimensions have exact prompts
#   (f) multi-dimension subsets
#   (g) argument validation & error handling
#   (h) non-dry-run submit execution & task ID extraction
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNNER="${SCRIPT_DIR}/run_audit_fanout.sh"

echo "==> Running Deep-Audit Fan-Out Runner Test Suite..."

FAILURES=0

assert_eq() {
    local label="$1"
    local expected="$2"
    local actual="$3"
    if [ "$expected" != "$actual" ]; then
        echo "  FAIL: [$label] expected '$expected', got '$actual'" >&2
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok: [$label]"
    fi
}

assert_exit_code() {
    local label="$1"
    local expected_code="$2"
    shift 2
    local exit_code=0
    local output=""
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expected_code" ]; then
        echo "  FAIL: [$label] expected exit code $expected_code, got $exit_code" >&2
        echo "  Output: $output" >&2
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok: [$label] (exit $expected_code)"
    fi
}

# --- Check runner script exists ---
if [ ! -f "$RUNNER" ]; then
    echo "ERROR: Runner script not found at $RUNNER"
    exit 1
fi

# --- 1. Fixture (a): --dry-run prints 6 DRY lines (one per dimension) ---
echo "Testing: Fixture (a) --dry-run prints 6 DRY lines..."
dry_out=$("$RUNNER" --dry-run)
dry_count=$(printf '%s\n' "$dry_out" | grep -c '^DRY:' || true)
total_lines=$(printf '%s\n' "$dry_out" | sed '/^$/d' | wc -l | tr -d ' ')
assert_eq "fixture (a) DRY line count is 6" "6" "$dry_count"
assert_eq "fixture (a) total non-empty line count is 6" "6" "$total_lines"

# --- 2. Fixture (b): --dimensions docs --dry-run prints exactly 1 DRY line ---
echo "Testing: Fixture (b) --dimensions docs --dry-run prints exactly 1 DRY line..."
docs_out=$("$RUNNER" --dimensions docs --dry-run)
docs_dry_count=$(printf '%s\n' "$docs_out" | grep -c '^DRY:' || true)
docs_total_lines=$(printf '%s\n' "$docs_out" | sed '/^$/d' | wc -l | tr -d ' ')
assert_eq "fixture (b) docs DRY line count is 1" "1" "$docs_dry_count"
assert_eq "fixture (b) docs total non-empty line count is 1" "1" "$docs_total_lines"

# --- 3. Fixture (c): unknown dimension -> exit 2 ---
echo "Testing: Fixture (c) unknown dimension -> exit 2..."
assert_exit_code "fixture (c) single unknown dimension" 2 "$RUNNER" --dimensions unknown --dry-run
assert_exit_code "fixture (c) mixed valid and invalid dimension" 2 "$RUNNER" --dimensions "docs,invalid_dim" --dry-run
assert_exit_code "fixture (c) empty dimension argument" 2 "$RUNNER" --dimensions "" --dry-run

# --- 4. Fixture (d): each DRY line contains 'role verifier' ---
echo "Testing: Fixture (d) each DRY line contains 'role verifier'..."
while IFS= read -r line; do
    [ -z "$line" ] && continue
    if ! echo "$line" | grep -q "role verifier"; then
        echo "  FAIL: DRY line does not contain 'role verifier': $line" >&2
        FAILURES=$((FAILURES + 1))
    fi
done <<< "$dry_out"
echo "  ok: all 6 DRY lines contain 'role verifier'"

# --- 5. Individual dimension prompts verification ---
echo "Testing: exact prompts for each of the 6 dimensions..."

# Dimension: docs
docs_line=$("$RUNNER" --dimensions docs --dry-run)
if ! echo "$docs_line" | grep -F -q "Verify README.md Project Structure matches the filesystem (run tools/ci_structure_sync.sh) and all relative links in README.md + docs/ resolve. Report file:line for every drift."; then
    echo "  FAIL: docs prompt mismatch: $docs_line" >&2
    FAILURES=$((FAILURES + 1))
else
    echo "  ok: docs prompt verified"
fi

# Dimension: spec
spec_line=$("$RUNNER" --dimensions spec --dry-run)
if ! echo "$spec_line" | grep -F -q "Verify every #### Scenario in spec/openspec/*.md with Status APPLIED/ACCEPTED has a <!-- tests: ... --> pin mapping to an existing Go test (run tools/ci_spec_code_sync.sh). Report unpinned/ghost scenarios."; then
    echo "  FAIL: spec prompt mismatch: $spec_line" >&2
    FAILURES=$((FAILURES + 1))
else
    echo "  ok: spec prompt verified"
fi

# Dimension: tests
tests_line=$("$RUNNER" --dimensions tests --dry-run)
if ! echo "$tests_line" | grep -F -q "Run CGO_ENABLED=0 go test -count=1 ./... and report any failure with file:line."; then
    echo "  FAIL: tests prompt mismatch: $tests_line" >&2
    FAILURES=$((FAILURES + 1))
else
    echo "  ok: tests prompt verified"
fi

# Dimension: security
sec_line=$("$RUNNER" --dimensions security --dry-run)
if ! echo "$sec_line" | grep -F -q 'Scan tracked files for leaked absolute home paths (/Users/<name>, C:\Users), api-key shapes (sk-...), and raw .env content. Report file:line only — never paste the secret value.'; then
    echo "  FAIL: security prompt mismatch: $sec_line" >&2
    FAILURES=$((FAILURES + 1))
else
    echo "  ok: security prompt verified"
fi

# Dimension: lifecycle
life_line=$("$RUNNER" --dimensions lifecycle --dry-run)
if ! echo "$life_line" | grep -F -q "Check .g8s/escalations.jsonl for patterns with >= 2 escalations of the same directory shape. Report promote candidates."; then
    echo "  FAIL: lifecycle prompt mismatch: $life_line" >&2
    FAILURES=$((FAILURES + 1))
else
    echo "  ok: lifecycle prompt verified"
fi

# Dimension: evidence
evid_line=$("$RUNNER" --dimensions evidence --dry-run)
if ! echo "$evid_line" | grep -F -q "Verify every plans/*/plan.md has a **Session type** marker and every recent squash merge on main references its PR number. Report gaps."; then
    echo "  FAIL: evidence prompt mismatch: $evid_line" >&2
    FAILURES=$((FAILURES + 1))
else
    echo "  ok: evidence prompt verified"
fi

# --- 6. Subset dimensions test ---
echo "Testing: Multi-dimension subsets..."
subset_out=$("$RUNNER" --dimensions "docs,tests,evidence" --dry-run)
subset_count=$(printf '%s\n' "$subset_out" | grep -c '^DRY:' || true)
assert_eq "subset line count is 3" "3" "$subset_count"

# --- 7. Flag validation and error handling ---
echo "Testing: CLI argument error handling..."
assert_exit_code "unknown flag exits 2" 2 "$RUNNER" --unknown-flag
assert_exit_code "--repo missing argument exits 2" 2 "$RUNNER" --repo
assert_exit_code "--dimensions missing argument exits 2" 2 "$RUNNER" --dimensions
assert_exit_code "non-existent repo directory exits 2" 2 "$RUNNER" --repo /nonexistent/path/to/repo --dry-run
assert_exit_code "--help exits 0" 0 "$RUNNER" --help
assert_exit_code "-h exits 0" 0 "$RUNNER" -h

# --- 8. Non-dry-run submit execution with mock g8s ---
echo "Testing: Non-dry-run submission execution (mock)..."
TMP_TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_TEST_DIR"' EXIT

# Create a mock g8s script
MOCK_G8S="${TMP_TEST_DIR}/mock_g8s.sh"
cat > "$MOCK_G8S" <<'EOF'
#!/usr/bin/env bash
if [ "$1" = "submit" ]; then
    mock_id="mock-task-$(date +%s)-$RANDOM"
    cat <<JSON
{
  "schema_uri": "g8s://envelope/task/v1",
  "actor": "operator",
  "status": "ok",
  "data": {
    "task_id": "$mock_id",
    "state": "QUEUED"
  }
}
JSON
    exit 0
fi
echo "unknown mock command" >&2
exit 1
EOF
chmod +x "$MOCK_G8S"

# Run non-dry-run with G8S_BIN pointing to mock_g8s
exec_out=$(G8S_BIN="$MOCK_G8S" "$RUNNER" --dimensions docs,spec)
task_id_count=$(printf '%s\n' "$exec_out" | grep -c 'task_id:' || true)
assert_eq "mock submit output contains 2 task_id entries" "2" "$task_id_count"

# --- 9. Non-dry-run live submission with real g8s CLI & isolated state dir ---
echo "Testing: Non-dry-run live submission with real g8s CLI..."
REAL_STATE_DIR="${TMP_TEST_DIR}/state"
mkdir -p "$REAL_STATE_DIR"
live_out=$(G8S_STATE_DIR="$REAL_STATE_DIR" "$RUNNER" --dimensions "docs")
live_task_id_count=$(printf '%s\n' "$live_out" | grep -c 'task_id:' || true)
assert_eq "live submit output contains 1 task_id entry" "1" "$live_task_id_count"

# Verify task_id format (UUID format from real controlplane)
live_task_id=$(printf '%s\n' "$live_out" | sed -n 's/.*task_id:[[:space:]]*\([a-zA-Z0-9-]*\).*/\1/p' | head -n 1)
if [ -z "$live_task_id" ] || [ "$live_task_id" = "unknown" ]; then
    echo "  FAIL: live submit did not return a valid task ID: $live_out" >&2
    FAILURES=$((FAILURES + 1))
else
    echo "  ok: live task_id extracted: $live_task_id"
fi

# --- Final report ---
if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "DEEP-AUDIT FAN-OUT TEST SUITE: $FAILURES failure(s)."
    exit 1
fi

echo ""
echo "DEEP-AUDIT FAN-OUT TEST SUITE: all tests passed."
exit 0
