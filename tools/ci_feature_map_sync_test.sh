#!/usr/bin/env bash
#
# Self-test for tools/ci_feature_map_sync.sh (#828)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYNC_SCRIPT="${SCRIPT_DIR}/ci_feature_map_sync.sh"

if [ ! -f "$SYNC_SCRIPT" ]; then
    echo "ERROR: sync script not found at $SYNC_SCRIPT"
    exit 1
fi

TMP_DIR="$(mktemp -d -t feature_map_sync_test_XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

MAP_FILE="${TMP_DIR}/feature-map.yml"
cat <<'EOF' > "$MAP_FILE"
schema_version: "1.0"
features:
  - id: "FEAT-TEST-01"
    title: "Test Feature"
    persona_story: "Story"
    srs_reqs: ["FR-1"]
    code_paths: ["internal/testpkg/**"]
    invariants: ["Inv"]
    visible_tests: ["echo ok"]
    verifier_class: "test"
    blast_radius:
      depends_on: []
      direct_consumers: []
  - id: "FEAT-TEST-02"
    title: "Second Feature"
    persona_story: "Story"
    srs_reqs: ["FR-2"]
    code_paths: ["internal/other/**"]
    collision_domain: "shared-domain"
    invariants: ["Inv"]
    visible_tests: ["echo ok"]
    verifier_class: "test"
    blast_radius:
      depends_on: []
      direct_consumers: []
EOF

mkdir -p "${TMP_DIR}/internal/testpkg" "${TMP_DIR}/internal/other"
echo 'package testpkg' > "${TMP_DIR}/internal/testpkg/x.go"
echo 'package other' > "${TMP_DIR}/internal/other/y.go"

output=""
exit_code=0
output=$(bash "$SYNC_SCRIPT" --file "$MAP_FILE" --repo "$TMP_DIR" 2>&1) || exit_code=$?

if [ "$exit_code" -ne 0 ]; then
    echo "FAIL: expected fixture sync to pass, got exit $exit_code"
    echo "$output"
    exit 1
fi

for line in \
    "feature FEAT-TEST-01: FEAT-TEST-01 — packages: 1, orphans: 0" \
    "feature FEAT-TEST-02: shared-domain — packages: 1, orphans: 0"
do
    if ! echo "$output" | grep -Fq "$line"; then
        echo "FAIL: missing summary line: $line"
        echo "$output"
        exit 1
    fi
done

echo "[ALL FEATURE-MAP SYNC SELF-TESTS PASSED]"
exit 0
