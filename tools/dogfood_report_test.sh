#!/usr/bin/env bash
#
# tools/dogfood_report_test.sh — Test suite for tools/dogfood_report.sh
# Validates report generation, JSON schema, skip logic, and CLI flags.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPORT_SCRIPT="${SCRIPT_DIR}/dogfood_report.sh"

if [ ! -f "$REPORT_SCRIPT" ]; then
    echo "ERROR: Report script not found at $REPORT_SCRIPT"
    exit 1
fi

TMP_DIR="$(mktemp -d -t dogfood_test_XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

echo "==> Test 1: Run dogfood_report.sh in standard mode..."
output=$(bash "$REPORT_SCRIPT" 2>&1)
echo "$output"

if ! echo "$output" | grep -q "g8s SELF-DOGFOODING AUDIT REPORT"; then
    echo "FAIL: Expected standard report header"
    exit 1
fi
if ! echo "$output" | grep -q "Brief Roundtrip"; then
    echo "FAIL: Expected Brief Roundtrip check in output"
    exit 1
fi
echo "[PASS] Test 1: Standard output valid"

echo "==> Test 2: Run dogfood_report.sh with --json flag..."
json_out=$(bash "$REPORT_SCRIPT" --json)

# Check JSON contains required keys
for key in '"version"' '"timestamp"' '"overall_status"' '"total_checks"' '"passed_checks"' '"checks"'; do
    if ! echo "$json_out" | grep -q "$key"; then
        echo "FAIL: JSON output missing key $key"
        echo "$json_out"
        exit 1
    fi
done
echo "[PASS] Test 2: JSON output valid"

echo "==> Test 3: Run dogfood_report.sh with --output flag..."
json_file="$TMP_DIR/report.json"
bash "$REPORT_SCRIPT" --output "$json_file" >/dev/null

if [ ! -f "$json_file" ]; then
    echo "FAIL: Expected report file at $json_file"
    exit 1
fi
if ! grep -q '"overall_status": "PASSED"' "$json_file"; then
    echo "FAIL: Saved JSON report did not contain PASSED status"
    exit 1
fi
echo "[PASS] Test 3: Output file written successfully"

echo "==> Test 4: Run dogfood_report.sh with --skip flag..."
skip_json=$(bash "$REPORT_SCRIPT" --skip make_dogfood --json)

if ! echo "$skip_json" | grep -q '"skipped_checks": 1'; then
    echo "FAIL: Expected 1 skipped check"
    echo "$skip_json"
    exit 1
fi
if ! echo "$skip_json" | grep -q '"status": "SKIP"'; then
    echo "FAIL: Expected check with status SKIP"
    exit 1
fi
echo "[PASS] Test 4: Skip logic verified"

echo ""
echo "[ALL DOGFOOD REPORT TESTS PASSED]"
exit 0
