#!/usr/bin/env bash
# tools/assurance_benchmark.sh — Offline Third-Party Assurance Benchmark Suite (#922)
# Runs deterministically across macOS, Linux, and Windows (Git Bash) without any
# external LLM API keys or network calls. Emits machine-readable JSON or Markdown.
set -euo pipefail

MODE="markdown"
BIN_PATH=""
REQUIRE_KERNEL="false"

usage() {
    cat <<'EOF'
Usage: bash tools/assurance_benchmark.sh [--json | --markdown] [--bin <path>] [--require-kernel-enforced] [-h | --help]

Runs the deterministic offline g8s assurance benchmark suite (Category: assurance)
across 5 runtime probes without external LLM credentials:
  1. as-001 escape-containment (5 runtime vectors + active OS sandbox telemetry)
  2. as-002 seeded-mutant-anti-false-pass (verifier canary + reviewer false-PASS gates)
  3. as-003 crash-lease-recovery-rto (stale SQLite WAL lease reclaim RTO)
  4. as-004 control-plane-cas-overhead (submit/claim CAS + Ed25519 sign/verify latency)
  5. as-005 audit-trail-cryptographic-completeness (Ed25519 tamper + replay + 0600/0700)

Flags:
  --require-kernel-enforced  Fail non-zero unless as-001 executes under mode=kernel_enforced
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --json)
            MODE="json"
            shift
            ;;
        --markdown)
            MODE="markdown"
            shift
            ;;
        --require-kernel-enforced)
            REQUIRE_KERNEL="true"
            shift
            ;;
        --bin)
            if [ $# -lt 2 ]; then
                echo "ERROR: --bin requires a binary path" >&2
                exit 2
            fi
            BIN_PATH="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "ERROR: unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if ! command -v jq >/dev/null 2>&1; then
    echo "ERROR: jq is required for tools/assurance_benchmark.sh" >&2
    exit 2
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

EXT=""
case "$(uname -s 2>/dev/null || echo unknown)" in
    MINGW*|MSYS*|CYGWIN*) EXT=".exe" ;;
esac

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

if [ -z "$BIN_PATH" ]; then
    BIN_PATH="${TMP_DIR}/g8s${EXT}"
    CGO_ENABLED=0 go build -o "$BIN_PATH" ./cmd/g8s
fi

STATE_DIR="${TMP_DIR}/state"
mkdir -p "$STATE_DIR"
chmod 700 "$STATE_DIR" 2>/dev/null || true

# Isolate completely from operator providers or external API keys.
export G8S_STATE_DIR="$STATE_DIR"
export G8S_DB="${STATE_DIR}/controlplane.db"
export G8S_PROVIDERS="${TMP_DIR}/absent-providers.json"
export OPENAI_API_KEY=""
export ANTHROPIC_API_KEY=""
export GEMINI_API_KEY=""
export CURSOR_API_KEY=""
export TYPESAFE_API_KEYS=""

VER_JSON="$("$BIN_PATH" version --json | tr -d '\r')"
DOC_JSON="$("$BIN_PATH" doctor --json | tr -d '\r')"
EVAL_JSON="$("$BIN_PATH" eval run --category assurance --json | tr -d '\r')"

if ! printf '%s\n' "$EVAL_JSON" | jq -e '.data.score == 1 and .data.failed_probes == 0 and .data.passed_probes == 5' >/dev/null; then
    echo "ERROR: offline assurance benchmark failed one or more probes:" >&2
    printf '%s\n' "$EVAL_JSON" >&2
    exit 1
fi

SUMMARY_JSON="$(jq -n \
    --argjson ver "$VER_JSON" \
    --argjson doc "$DOC_JSON" \
    --argjson ev "$EVAL_JSON" \
    '
    (([$ev.data.details[] | select(.probe_id == "as-001") | (.actual_outcome | fromjson)][0]) // {}) as $as1 |
    ($as1.sandbox_mode // "unknown") as $sbx_mode |
    ($as1.sandbox_backend // "unknown") as $sbx_backend |
    {
      schema: "g8s.assurance-benchmark/v1",
      epistemic_class: "measured_metric",
      platform: (($ver.data.os // "unknown") + "/" + ($ver.data.arch // "unknown")),
      version: ($ver.data.version // "dev"),
      sandbox_mode: $sbx_mode,
      sandbox_backend: $sbx_backend,
      kernel_enforced: ($sbx_mode == "kernel_enforced"),
      os_sandbox: (($doc.data.capabilities // []) | map(select(.name == "os_sandbox")) | .[0] // {available: false, verification_state: "unprobed", detail: "unknown"}),
      score: $ev.data.score,
      passed_probes: $ev.data.passed_probes,
      failed_probes: $ev.data.failed_probes,
      total_probes: $ev.data.total_probes,
      total_duration_ms: (([ $ev.data.details[].duration ] | add // 0) / 1000000),
      probes: [
        $ev.data.details[] | {
          probe_id: .probe_id,
          name: .name,
          passed: .passed,
          duration_ms: (.duration / 1000000),
          metrics: (.actual_outcome | fromjson)
        }
      ]
    }')"

if [ "$REQUIRE_KERNEL" = "true" ]; then
    KERNEL_OK="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.kernel_enforced')"
    if [ "$KERNEL_OK" != "true" ]; then
        SBX_MODE="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.sandbox_mode')"
        SBX_BACKEND="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.sandbox_backend')"
        echo "ERROR: --require-kernel-enforced failed: host sandbox_mode=${SBX_MODE} (backend=${SBX_BACKEND}) is not kernel_enforced" >&2
        exit 1
    fi
fi

if [ "$MODE" = "json" ]; then
    printf '%s\n' "$SUMMARY_JSON"
    exit 0
fi

printf '### g8s Offline Assurance Benchmark Report\n\n'
printf '%s\n' "$SUMMARY_JSON" | jq -r '
  "- **Platform**: `" + .platform + "` | **Version**: `" + .version + "`\n" +
  "- **OS Sandbox**: `" + (.os_sandbox.detail // "unknown") + "` (`kernel_enforced=" + (.kernel_enforced | tostring) + "`)\n" +
  (if .kernel_enforced then "" else "- **Containment Warning**: Degraded mode (`sandbox_mode=" + .sandbox_mode + " != kernel_enforced`) — Vector 2 evaluated via post-run diff firewall fallback, not kernel filesystem isolation.\n" end) +
  "- **Overall Score**: `" + ((.score * 100) | tostring) + "%` (" + (.passed_probes | tostring) + "/" + (.total_probes | tostring) + " probes passed in " + (((.total_duration_ms * 10) | round) / 10 | tostring) + " ms)\n"
'
printf '\n| Probe | Dimension | Result | Measured Metrics | Duration (ms) |\n'
printf '| :--- | :--- | :---: | :--- | ---: |\n'
printf '%s\n' "$SUMMARY_JSON" | jq -r '
  .probes[] |
  (if .probe_id == "as-001" then "blocked=" + (.metrics.vectors_blocked | tostring) + "/" + (.metrics.vectors_tested | tostring) + " (" + .metrics.sandbox_mode + "/" + .metrics.sandbox_backend + ")"
   elif .probe_id == "as-002" then "caught=" + (.metrics.mutants_caught | tostring) + "/" + (.metrics.mutants_seeded | tostring) + ", false_passes=" + (.metrics.false_passes | tostring)
   elif .probe_id == "as-003" then "reclaimed=" + (.metrics.reclaimed | tostring) + ", attempt=" + (.metrics.attempt | tostring) + ", rto_ms=" + (((.metrics.recovery_rto_ms * 100) | round) / 100 | tostring)
   elif .probe_id == "as-004" then "cas_ms=" + (((.metrics.avg_submit_claim_ms * 100) | round) / 100 | tostring) + ", sign_verify_ms=" + (((.metrics.avg_receipt_sign_verify_ms * 100) | round) / 100 | tostring)
   elif .probe_id == "as-005" then "checks=" + (.metrics.checks_passed | tostring) + "/" + (.metrics.checks_total | tostring) + ", ratio=" + (.metrics.completeness_ratio | tostring)
   else (.metrics | tostring) end) as $m |
  "| `" + .probe_id + "` | `" + .name + "` | " + (if .passed then "PASS" else "FAIL" end) + " | " + $m + " | " + ((((.duration_ms * 100) | round) / 100) | tostring) + " |"
'
