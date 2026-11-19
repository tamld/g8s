#!/usr/bin/env bash
# tools/assurance_benchmark.sh — Offline Third-Party Assurance Benchmark Suite (#922)
# Runs deterministically across macOS, Linux, and Windows (Git Bash) without any
# external LLM API keys or network calls. Emits machine-readable JSON or Markdown.
set -euo pipefail

MODE="markdown"
BIN_PATH=""
REQUIRE_KERNEL="false"
VERIFY_BINARY_COMMIT=""
REQUIRE_BINARY_MATCH="false"
REQUIRE_CLEAN_VCS="false"

usage() {
    cat <<'EOF'
Usage: bash tools/assurance_benchmark.sh [--json | --markdown] [--bin <path>] [--require-kernel-enforced] [--verify-binary-commit <sha>] [--require-binary-match] [--require-clean-vcs] [-h | --help]

Runs the deterministic offline g8s assurance benchmark suite (Category: assurance)
across 5 runtime probes without external LLM credentials:
  1. as-001 escape-containment (5 runtime vectors + active OS sandbox telemetry)
  2. as-002 seeded-mutant-anti-false-pass (verifier canary + reviewer false-PASS gates)
  3. as-003 crash-lease-recovery-rto (stale SQLite WAL lease reclaim RTO)
  4. as-004 control-plane-cas-overhead (submit/claim CAS + Ed25519 sign/verify latency)
  5. as-005 audit-trail-cryptographic-completeness (Ed25519 tamper + replay + 0600/0700)

Flags:
  --require-kernel-enforced     Fail non-zero unless as-001 executes under mode=kernel_enforced
  --verify-binary-commit <sha>  Fail non-zero unless binary_commit (and source_commit when in git) match <sha>
  --require-binary-match        Fail non-zero unless trust_assurance_status.binary_matches_source == true
  --require-clean-vcs           Fail non-zero unless trust_assurance_status.vcs_modified == false
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
        --verify-binary-commit)
            if [ $# -lt 2 ]; then
                echo "ERROR: --verify-binary-commit requires a commit SHA" >&2
                exit 2
            fi
            VERIFY_BINARY_COMMIT="$2"
            shift 2
            ;;
        --require-binary-match)
            REQUIRE_BINARY_MATCH="true"
            shift
            ;;
        --require-clean-vcs)
            REQUIRE_CLEAN_VCS="true"
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

SOURCE_COMMIT="$(git rev-parse HEAD 2>/dev/null | tr -d '\r' || true)"

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
    --arg source_commit "$SOURCE_COMMIT" \
    --arg verify_commit "$VERIFY_BINARY_COMMIT" \
    '
    def norm_rev: ascii_downcase | sub("^[[:space:]]+"; "") | sub("[[:space:]]+$"; "") | sub("-dirty$"; "");
    def is_hex_rev: norm_rev | test("^[0-9a-f]{7,40}$");
    def rev_match($a; $b):
      ($a | norm_rev) as $na |
      ($b | norm_rev) as $nb |
      if (($na | test("^[0-9a-f]{7,40}$")) and ($nb | test("^[0-9a-f]{7,40}$"))) then
        ((if ($na | length) < ($nb | length) then ($na | length) else ($nb | length) end) as $n | $na[0:$n] == $nb[0:$n])
      else false end;
    def gate_rank:
      if . == "ENFORCED" then 3
      elif . == "MEDIATED_ONLY" then 2
      elif . == "UNVERIFIED" then 1
      else 0 end;

    (([$ev.data.details[] | select(.probe_id == "as-001") | (.actual_outcome | fromjson)][0]) // {}) as $as1 |
    ($as1.sandbox_mode // "unknown") as $sbx_mode |
    ($as1.sandbox_backend // "unknown") as $sbx_backend |
    ($doc.data.trust_assurance_status // {}) as $doc_ta |
    ($doc_ta.gates // []) as $doc_gates |
    def find_gate($id):
      ([ $doc_gates[] | select(.gate == $id) ][0]) // {
        gate: $id,
        name: $id,
        status: "UNVERIFIED",
        epistemic_class: "measured_metric",
        evidence_source: "doctor_absent",
        detail: "gate assessment missing from doctor output"
      };

    ([ $ev.data.details[] | select(.probe_id == "as-001") ][0] // {passed: false}) as $p1 |
    ([ $ev.data.details[] | select(.probe_id == "as-002") ][0] // {passed: false}) as $p2 |
    ([ $ev.data.details[] | select(.probe_id == "as-003") ][0] // {passed: false}) as $p3 |
    ([ $ev.data.details[] | select(.probe_id == "as-004") ][0] // {passed: false}) as $p4 |
    ([ $ev.data.details[] | select(.probe_id == "as-005") ][0] // {passed: false}) as $p5 |
    ($p1.actual_outcome | fromjson? // {}) as $m1 |
    ($p2.actual_outcome | fromjson? // {}) as $m2 |
    ($p3.actual_outcome | fromjson? // {}) as $m3 |
    ($p4.actual_outcome | fromjson? // {}) as $m4 |
    ($p5.actual_outcome | fromjson? // {}) as $m5 |

    ($doc_ta.binary_commit // $ver.data.commit // "") as $raw_bin_commit |
    (if ($raw_bin_commit | is_hex_rev) then ($raw_bin_commit | norm_rev) else "" end) as $bin_commit |
    ($source_commit | if length > 0 then . else ($doc_ta.source_commit // "") end) as $raw_src_commit |
    (if ($raw_src_commit | is_hex_rev) then ($raw_src_commit | norm_rev) else "" end) as $src_commit |
    (if ($verify_commit | length > 0) then (if ($verify_commit | is_hex_rev) then ($verify_commit | norm_rev) else "INVALID" end) else "" end) as $expected_commit |
    (if $src_commit != "" then ($bin_commit != "" and rev_match($bin_commit; $src_commit)) else ($doc_ta.binary_matches_source // null) end) as $bin_matches_src |
    ($doc_ta.binary_sha256 // "") as $bin_sha |
    ($ver.data.vcs_modified // $doc_ta.vcs_modified // false) as $vcs_mod |

    (find_gate("TRUST-G1") |
      if ($p1.passed | not) then
        .status = "NOT_ENFORCED" |
        .evidence_source = "os_sandbox_probe+assurance_probe:as-001" |
        .detail = "as-001 escape containment probe failed"
      else
        .evidence_source = "os_sandbox_probe+assurance_probe:as-001" |
        .detail = (.detail + "; as-001 blocked=" + (($m1.vectors_blocked // 0) | tostring) + "/" + (($m1.vectors_tested // 0) | tostring))
      end
    ) as $g1 |

    (find_gate("TRUST-G2") |
      if (($p1.passed | not) or ($p5.passed | not)) then
        .status = "NOT_ENFORCED" |
        .evidence_source = "trust_root_self_check+assurance_probe:as-001+as-005" |
        .detail = "as-001 escape containment or as-005 cryptographic integrity probe failed"
      else . end
    ) as $g2 |

    (if ($p2.passed and (($m2.false_passes // 0) == 0) and (($m2.mutants_caught // 0) == ($m2.mutants_seeded // 0))) then
      {
        gate: "TRUST-G3",
        name: "Independent Evaluation & Mutant Canaries",
        status: "ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-002",
        detail: ("as-002 caught=" + (($m2.mutants_caught // 0) | tostring) + "/" + (($m2.mutants_seeded // 0) | tostring) + ", false_passes=0")
      }
     else
      {
        gate: "TRUST-G3",
        name: "Independent Evaluation & Mutant Canaries",
        status: "NOT_ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-002",
        detail: "as-002 seeded mutant canary or anti-false-PASS check failed"
      }
     end) as $g3 |

    (if ($p3.passed and $p4.passed) then
      {
        gate: "TRUST-G4",
        name: "Recoverability & WAL CAS Fencing",
        status: "ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-003+as-004",
        detail: ("as-003 reclaimed attempt=" + (($m3.attempt // 0) | tostring) + ", rto_ms=" + (((($m3.recovery_rto_ms // 0) * 100) | round) / 100 | tostring) + "; as-004 iterations=" + (($m4.iterations // 0) | tostring))
      }
     else
      {
        gate: "TRUST-G4",
        name: "Recoverability & WAL CAS Fencing",
        status: "NOT_ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-003+as-004",
        detail: "as-003 crash lease recovery or as-004 WAL CAS benchmark failed"
      }
     end) as $g4 |

    (if ($verify_commit != "" and $expected_commit == "INVALID") then
      {
        gate: "TRUST-G5",
        name: "Evidence Integrity & Binary Provenance",
        status: "NOT_ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-005+binary_provenance",
        detail: ("invalid --verify-binary-commit SHA: " + $verify_commit)
      }
     elif ($verify_commit != "" and ($bin_commit == "" or (rev_match($bin_commit; $expected_commit) | not))) then
      {
        gate: "TRUST-G5",
        name: "Evidence Integrity & Binary Provenance",
        status: "NOT_ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-005+binary_provenance",
        detail: ("binary commit " + (if $bin_commit == "" then "<missing>" else $bin_commit end) + " does not match expected commit " + $expected_commit)
      }
     elif ($verify_commit != "" and $src_commit != "" and (rev_match($src_commit; $expected_commit) | not)) then
      {
        gate: "TRUST-G5",
        name: "Evidence Integrity & Binary Provenance",
        status: "NOT_ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-005+binary_provenance",
        detail: ("source commit " + $src_commit + " does not match expected commit " + $expected_commit)
      }
     elif ($src_commit != "" and $bin_commit != "" and (rev_match($bin_commit; $src_commit) | not)) then
      {
        gate: "TRUST-G5",
        name: "Evidence Integrity & Binary Provenance",
        status: "NOT_ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-005+binary_provenance",
        detail: ("binary commit " + $bin_commit + " does not match source commit " + $src_commit)
      }
     elif ($bin_commit == "") then
      {
        gate: "TRUST-G5",
        name: "Evidence Integrity & Binary Provenance",
        status: "UNVERIFIED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-005+binary_provenance",
        detail: "running binary lacks VCS commit SHA"
      }
     elif ($bin_sha == "") then
      {
        gate: "TRUST-G5",
        name: "Evidence Integrity & Binary Provenance",
        status: "UNVERIFIED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-005+binary_provenance",
        detail: "binary SHA-256 digest unavailable"
      }
     elif $p5.passed then
      {
        gate: "TRUST-G5",
        name: "Evidence Integrity & Binary Provenance",
        status: "ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-005+binary_provenance",
        detail: ("binary commit=" + $bin_commit + " sha256=" + $bin_sha[0:12] + "...; as-005 checks=" + (($m5.checks_passed // 0) | tostring) + "/" + (($m5.checks_total // 0) | tostring))
      }
     else
      {
        gate: "TRUST-G5",
        name: "Evidence Integrity & Binary Provenance",
        status: "NOT_ENFORCED",
        epistemic_class: "measured_metric",
        evidence_source: "assurance_probe:as-005+binary_provenance",
        detail: "as-005 cryptographic audit trail probe failed"
      }
     end) as $g5 |

    (find_gate("TRUST-G6")) as $g6 |

    ([$g1, $g2, $g3, $g4, $g5, $g6]) as $gates |
    ([ $gates[] | select(.status == "NOT_ENFORCED" or .status == "UNVERIFIED") | (.gate + ": " + .detail) ]) as $veto_reasons |
    ([ $gates[] | select(.status == "MEDIATED_ONLY") | .gate ]) as $mediated_gates |
    ([ $gates[] | .status | gate_rank ] | min) as $min_rank |
    (if $min_rank >= 3 then "ENFORCED"
     elif $min_rank == 2 then "MEDIATED_ONLY"
     elif $min_rank == 1 then "UNVERIFIED"
     else "NOT_ENFORCED" end) as $overall_verdict |
    (($veto_reasons | length) > 0) as $non_comp_veto |

    {
      schema: "g8s.assurance-benchmark/v1",
      epistemic_class: "measured_metric",
      platform: (($ver.data.os // "unknown") + "/" + ($ver.data.arch // "unknown")),
      version: ($ver.data.version // "dev"),
      sandbox_mode: $sbx_mode,
      sandbox_backend: $sbx_backend,
      kernel_enforced: ($sbx_mode == "kernel_enforced"),
      os_sandbox: (($doc.data.capabilities // []) | map(select(.name == "os_sandbox")) | .[0] // {available: false, verification_state: "unprobed", detail: "unknown"}),
      trust_assurance_status: ({
        schema: "g8s.trust-assurance/v1",
        epistemic_class: "measured_metric",
        os: ($doc_ta.os // $ver.data.os // "unknown"),
        arch: ($doc_ta.arch // $ver.data.arch // "unknown"),
        platform: (($ver.data.os // "unknown") + "/" + ($ver.data.arch // "unknown")),
        binary_version: ($doc_ta.binary_version // $ver.data.version // "dev"),
        binary_commit: $bin_commit,
        binary_build_source: ($doc_ta.binary_build_source // $ver.data.build_source // ""),
        vcs_modified: $vcs_mod,
        binary_path: ($doc_ta.binary_path // $ver.data.binary_path // ""),
        binary_sha256: $bin_sha,
        source_commit: $src_commit,
        sandbox_mode: ($doc_ta.sandbox_mode // $sbx_mode),
        sandbox_backend: ($doc_ta.sandbox_backend // $sbx_backend),
        kernel_enforced: ($sbx_mode == "kernel_enforced"),
        network_enforcement: ($doc_ta.network_enforcement // "unrestricted"),
        s0_key_provisioned: ($doc_ta.s0_key_provisioned // false),
        overall_verdict: $overall_verdict,
        non_compensatory_veto: $non_comp_veto,
        gates: $gates
      }
      + (if $bin_matches_src != null then {binary_matches_source: $bin_matches_src} else {} end)
      + (if ($veto_reasons | length) > 0 then {veto_reasons: $veto_reasons} else {} end)
      + (if ($mediated_gates | length) > 0 then {mediated_gates: $mediated_gates} else {} end)),
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

if [ -n "$VERIFY_BINARY_COMMIT" ]; then
    G5_STATUS="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.trust_assurance_status.gates[] | select(.gate == "TRUST-G5") | .status')"
    G5_DETAIL="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.trust_assurance_status.gates[] | select(.gate == "TRUST-G5") | .detail')"
    if [ "$G5_STATUS" != "ENFORCED" ]; then
        echo "ERROR: --verify-binary-commit failed (${G5_STATUS}): ${G5_DETAIL}" >&2
        exit 1
    fi
fi

if [ "$REQUIRE_BINARY_MATCH" = "true" ]; then
    MATCH_OK="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.trust_assurance_status.binary_matches_source')"
    if [ "$MATCH_OK" != "true" ]; then
        BIN_C="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.trust_assurance_status.binary_commit')"
        SRC_C="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.trust_assurance_status.source_commit')"
        echo "ERROR: --require-binary-match failed: binary_commit=${BIN_C} source_commit=${SRC_C} binary_matches_source=${MATCH_OK}" >&2
        exit 1
    fi
fi

if [ "$REQUIRE_CLEAN_VCS" = "true" ]; then
    VCS_MOD="$(printf '%s\n' "$SUMMARY_JSON" | jq -r '.trust_assurance_status.vcs_modified')"
    if [ "$VCS_MOD" != "false" ]; then
        echo "ERROR: --require-clean-vcs failed: running binary built from modified/dirty VCS state (vcs_modified=${VCS_MOD})" >&2
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

printf '\n#### TRUST Assurance Status (Non-Compensatory — ADR-0042)\n\n'
printf '%s\n' "$SUMMARY_JSON" | jq -r '
  .trust_assurance_status |
  "- **Verdict**: `" + .overall_verdict + "` (`non_compensatory_veto=" + (.non_compensatory_veto | tostring) + "`)\n" +
  "- **Binary Provenance**: `commit=" + (if .binary_commit != "" then .binary_commit else "unknown" end) + "` | `source=" + (if .source_commit != "" then .source_commit else "none" end) + "` | `sha256=" + (if .binary_sha256 != "" then .binary_sha256[0:12] + "..." else "none" end) + "`\n" +
  "- **Isolation**: `mode=" + .sandbox_mode + "` | `backend=" + .sandbox_backend + "` | `network_enforcement=" + .network_enforcement + "`\n"
'
printf '| Gate | Status | Evidence | Detail |\n'
printf '| :--- | :---: | :--- | :--- |\n'
printf '%s\n' "$SUMMARY_JSON" | jq -r '
  .trust_assurance_status.gates[] |
  "| `" + .gate + "` | `" + .status + "` | `" + .evidence_source + "` | " + (.detail | gsub("\\|"; "/")) + " |"
'
