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
for req_flag in "--require-kernel-enforced" "--verify-binary-commit" "--require-binary-match" "--require-clean-vcs"; do
    printf '%s\n' "$HELP_OUT" | grep -q -- "$req_flag" || {
        echo "FAIL: --help output missing $req_flag" >&2
        exit 1
    }
done
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
  .epistemic_class == "measured_metric" and
  (.kernel_enforced | type == "boolean") and
  (.sandbox_mode | length > 0) and
  .score == 1 and
  .passed_probes == 5 and
  .failed_probes == 0 and
  .total_probes == 5 and
  (.probes | length == 5) and
  .trust_assurance_status.schema == "g8s.trust-assurance/v1" and
  .trust_assurance_status.epistemic_class == "measured_metric" and
  (.trust_assurance_status.binary_sha256 | length == 64) and
  .trust_assurance_status.binary_matches_source == true and
  .trust_assurance_status.non_compensatory_veto == false and
  ((.trust_assurance_status.overall_verdict == "ENFORCED") or (.trust_assurance_status.overall_verdict == "MEDIATED_ONLY")) and
  (.trust_assurance_status.gates | length == 6) and
  ([.trust_assurance_status.gates[].gate] == ["TRUST-G1","TRUST-G2","TRUST-G3","TRUST-G4","TRUST-G5","TRUST-G6"]) and
  ([.trust_assurance_status.gates[].status | IN("ENFORCED","MEDIATED_ONLY","NOT_ENFORCED","UNVERIFIED","CAPABILITY_DETECTED")] | all)
' >/dev/null || {
    echo "FAIL: --json output failed schema/score assertion:" >&2
    printf '%s\n' "$JSON_OUT" >&2
    exit 1
}

# Test 2b: --require-kernel-enforced matches host kernel_enforced state
KERNEL_ENFORCED="$(printf '%s\n' "$JSON_OUT" | jq -r '.kernel_enforced')"
if [ "$KERNEL_ENFORCED" = "true" ]; then
    bash tools/assurance_benchmark.sh --bin "$BIN" --require-kernel-enforced --json >/dev/null || {
        echo "FAIL: --require-kernel-enforced failed despite kernel_enforced=true" >&2
        exit 1
    }
else
    if bash tools/assurance_benchmark.sh --bin "$BIN" --require-kernel-enforced --json >/dev/null 2>&1; then
        echo "FAIL: --require-kernel-enforced succeeded despite kernel_enforced=false" >&2
        exit 1
    fi
fi

# Test 2c: --verify-binary-commit and --require-binary-match succeed on matching HEAD commit,
# and release-stamped build passes --require-clean-vcs deterministically even if checkout has untracked files.
HEAD_SHA="$(git rev-parse HEAD | tr -d '\r')"
bash tools/assurance_benchmark.sh --bin "$BIN" --verify-binary-commit "$HEAD_SHA" --require-binary-match --json >/dev/null || {
    echo "FAIL: --verify-binary-commit + --require-binary-match failed on HEAD ${HEAD_SHA}" >&2
    exit 1
}

STAMPED_BIN="${TMP_BIN_DIR}/g8s-stamped${EXT}"
CGO_ENABLED=0 go build -ldflags="-X main.Version=0.25.0-test -X main.Commit=${HEAD_SHA} -X main.BuildTime=2026-11-20T00:00:00Z -X main.BuildSource=release" -o "$STAMPED_BIN" ./cmd/g8s
bash tools/assurance_benchmark.sh --bin "$STAMPED_BIN" --verify-binary-commit "$HEAD_SHA" --require-binary-match --require-clean-vcs --json >/dev/null || {
    echo "FAIL: stamped binary failed --require-clean-vcs + --verify-binary-commit verification" >&2
    exit 1
}

# Test 2d: --verify-binary-commit fails closed on mismatched commit SHA
if bash tools/assurance_benchmark.sh --bin "$BIN" --verify-binary-commit "0000000000000000000000000000000000000000" --json >/dev/null 2>&1; then
    echo "FAIL: --verify-binary-commit succeeded despite commit mismatch" >&2
    exit 1
fi

# Test 3: --markdown
MD_OUT="$(bash tools/assurance_benchmark.sh --bin "$BIN" --markdown)"
for pat in "g8s Offline Assurance Benchmark Report" "as-001" "as-005" "PASS" "TRUST Assurance Status" "TRUST-G1" "TRUST-G6"; do
    if ! printf '%s\n' "$MD_OUT" | grep -q "$pat"; then
        echo "FAIL: --markdown output missing $pat" >&2
        printf '%s\n' "$MD_OUT" >&2
        exit 1
    fi
done

echo "PASS: tools/assurance_benchmark_test.sh all checks green."
