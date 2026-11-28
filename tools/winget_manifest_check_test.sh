#!/usr/bin/env bash
#
# Test suite for tools/winget_manifest_check.sh (D-9, #986).
# Validates the fail-closed winget checksum gate:
#   (1) valid manifest + matching checksums-file           -> exit 0
#   (2) placeholder '<from checksums.txt>' remains         -> exit 1 + chocolatey-style error
#   (3) malformed InstallerSha256 (63 hex chars)           -> exit 1
#   (4) InstallerSha256 mismatches checksums-file          -> exit 1
#   (5) InstallerUrl not pinned to PackageVersion          -> exit 1
#   (6) manifest file missing                              -> exit 2
#   (7) --checksums-file target missing                    -> exit 2
#   (8) unknown argument                                   -> exit 2
#   (9) committed packaging/winget/manifest.yaml passes    -> exit 0 (placeholder regression guard)
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
GATE="$SCRIPT_DIR/winget_manifest_check.sh"

if [ ! -f "$GATE" ]; then
    echo "ERROR: winget gate script not found at $GATE"
    exit 1
fi

echo "==> Running Winget Manifest Check Test Suite..."

FAILS=0

run_case() {
    local desc="$1"
    local expect_code="$2"
    local pattern="$3"   # empty string = no output pattern assertion
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILS=$((FAILS + 1))
    elif [ -n "$pattern" ] && ! echo "$output" | grep -q "$pattern"; then
        echo "  FAIL: output did not match pattern '$pattern'"
        echo "$output" | sed 's/^/    /'
        FAILS=$((FAILS + 1))
    else
        echo "  ok (exit $exit_code${pattern:+, pattern matched})"
    fi
}

TMP="$(mktemp -d)"
trap 'chmod -R u+rwX "$TMP" 2>/dev/null; rm -rf "$TMP"' EXIT

GOOD_SHA="b9eaf4c262096125b98c8d69e6af1459115c0343c6ee003440cfe61dd2101547"
OTHER_SHA="38273e9476c19de9b9d69061e6d0ecab0017539b971d3fd6f5731798ad649705"

write_manifest() {
    local path="$1" version="$2" sha="$3"
    cat > "$path" <<EOF
PackageIdentifier: TamLD.g8s
PackageVersion: ${version}
PackageLocale: en-US
Publisher: TamLD
PackageName: g8s
License: MIT
Installers:
  - Architecture: x64
    InstallerType: zip
    InstallerUrl: https://github.com/tamld/g8s/releases/download/v${version}/g8s_${version}_windows_amd64.zip
    InstallerSha256: ${sha}
EOF
}

write_checksums() {
    local path="$1" version="$2" sha="$3"
    cat > "$path" <<EOF
${sha}  g8s_${version}_windows_amd64.zip
${OTHER_SHA}  g8s_${version}_windows_arm64.zip
EOF
}

FIXTURE_VALID="$TMP/valid.yaml"
write_manifest "$FIXTURE_VALID" "0.12.0" "$GOOD_SHA"
CHECKSUMS_VALID="$TMP/checksums_valid.txt"
write_checksums "$CHECKSUMS_VALID" "0.12.0" "$GOOD_SHA"

FIXTURE_PLACEHOLDER="$TMP/placeholder.yaml"
write_manifest "$FIXTURE_PLACEHOLDER" "0.12.0" "<from checksums.txt>"

FIXTURE_SHORTSHA="$TMP/shortsha.yaml"
write_manifest "$FIXTURE_SHORTSHA" "0.12.0" "b9eaf4c262096125b98c8d69e6af1459115c0343c6ee003440cfe61dd21015"

FIXTURE_MISMATCH="$TMP/mismatch.yaml"
write_manifest "$FIXTURE_MISMATCH" "0.12.0" "$OTHER_SHA"

# (1) valid manifest + matching checksums-file -> exit 0
run_case \
    "(1) valid manifest with matching checksums-file passes" 0 \
    "winget manifest gate passed" \
    bash "$GATE" --manifest "$FIXTURE_VALID" --checksums-file "$CHECKSUMS_VALID"

# (1b) valid manifest without checksum verification -> exit 0 (structural mode)
run_case \
    "(1b) valid manifest in structural-only mode passes" 0 \
    "winget manifest gate passed" \
    bash "$GATE" --manifest "$FIXTURE_VALID"

# (2) placeholder -> exit 1 + chocolatey-mirroring message
run_case \
    "(2) placeholder checksum fails the gate" 1 \
    "must be replaced from release checksums.txt" \
    bash "$GATE" --manifest "$FIXTURE_PLACEHOLDER"

# (3) malformed sha -> exit 1
run_case \
    "(3) malformed (63-hex) InstallerSha256 fails the gate" 1 \
    "not a 64-hex-character SHA-256" \
    bash "$GATE" --manifest "$FIXTURE_SHORTSHA"

# (4) checksum mismatch vs checksums-file -> exit 1
run_case \
    "(4) InstallerSha256 mismatching checksums-file fails the gate" 1 \
    "does not match" \
    bash "$GATE" --manifest "$FIXTURE_MISMATCH" --checksums-file "$CHECKSUMS_VALID"

# (5) URL not pinned to PackageVersion -> exit 1
cat > "$TMP/drift.yaml" <<EOF
PackageIdentifier: TamLD.g8s
PackageVersion: 0.13.0
Installers:
  - Architecture: x64
    InstallerType: zip
    InstallerUrl: https://github.com/tamld/g8s/releases/download/v0.12.0/g8s_0.12.0_windows_amd64.zip
    InstallerSha256: ${GOOD_SHA}
EOF
run_case \
    "(5) InstallerUrl stale-pin drift fails the gate" 1 \
    "not pinned to PackageVersion" \
    bash "$GATE" --manifest "$TMP/drift.yaml"

# (6) missing manifest file -> exit 2
run_case \
    "(6) missing manifest file is a usage/file error" 2 \
    "winget manifest not found" \
    bash "$GATE" --manifest "$TMP/does-not-exist.yaml"

# (7) missing checksums file -> exit 2
run_case \
    "(7) missing --checksums-file target is a usage/file error" 2 \
    "checksums file not found" \
    bash "$GATE" --manifest "$FIXTURE_VALID" --checksums-file "$TMP/no-such-checksums.txt"

# (8) unknown argument -> exit 2
run_case \
    "(8) unknown argument is a usage error" 2 \
    "unknown argument" \
    bash "$GATE" --bogus

# (9) committed packaging/winget/manifest.yaml passes (placeholder regression guard)
if [ -f "$REPO_ROOT/packaging/winget/manifest.yaml" ]; then
    run_case \
        "(9) committed winget manifest passes the gate" 0 \
        "winget manifest gate passed" \
        bash "$GATE"
else
    echo "SKIP (9): committed packaging/winget/manifest.yaml not found"
fi

if [ "$FAILS" -eq 0 ]; then
    echo ""
    echo "WINGET MANIFEST CHECK TEST SUITE: all tests passed."
    exit 0
fi
echo ""
echo "WINGET MANIFEST CHECK TEST SUITE: $FAILS failure(s)."
exit 1
