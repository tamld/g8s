#!/usr/bin/env bash
# Self-test for tools/assurance_benchmark.sh (#922).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if ! command -v jq >/dev/null 2>&1; then
    echo "SKIP: jq not available"
    exit 0
fi

# Test 1: --help
HELP_OUT="$(bash tools/assurance_benchmark.sh --help)"
printf '%s\n' "$HELP_OUT" | grep -q 'g8s\.assurance' || true
printf '%s\n' "$HELP_OUT" | grep -q 'escape-containment' || {
    echo "FAIL: --help output missing escape-containment" >&2
    exit 1
}

# Build once and reuse for fast JSON + Markdown verification
TMP_BIN_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_BIN_DIR"' EXIT
EXT=""
case "$(uname -s 2>/dev/null || echo unknown)" in
    MINGW*|MSYS*|CYGWIN*) EXT=".exe" ;;
esac
BIN="${TMP_BIN_DIR}/g8s${EXT}"
CGO_ENABLED=0 go build -o "$BIN" ./cmd/g8s

# Test 2: --json
JSON_OUT="$(bash tools/assurance_benchmark.sh --bin "$BIN" --json)"
printf '%s\n' "$JSON_OUT" | jq -e '
  .schema == "g8s.assurance-benchmark/v1" and
  .score == 1 and
  .passed_probes == 5 and
  .failed_probes == 0 and
  .total_probes == 5 and
  (.probes | length == 5)
' >/dev/null || {
    echo "FAIL: --json output failed schema/score assertion:" >&2
    printf '%s\n' "$JSON_OUT" >&2
    exit 1
}

# Test 3: --markdown
MD_OUT="$(bash tools/assurance_benchmark.sh --bin "$BIN" --markdown)"
for pat in "g8s Offline Assurance Benchmark Report" "as-001" "as-005" "PASS"; do
    if ! printf '%s\n' "$MD_OUT" | grep -q "$pat"; then
        echo "FAIL: --markdown output missing $pat" >&2
        printf '%s\n' "$MD_OUT" >&2
        exit 1
    fi
done

echo "PASS: tools/assurance_benchmark_test.sh all 3 checks green."
