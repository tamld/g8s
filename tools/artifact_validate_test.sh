#!/usr/bin/env bash
#
# artifact_validate_test.sh — Test suite for tools/artifact_validate.sh
# (#1022 / audit M-02 post-GoReleaser artifact validation gate)
#
# Strategy (mirrors tools/release_matrix_check_test.sh / tools/ci_fetch_test.sh):
# builds a tiny Go fixture program for every platform-matrix target, packages
# fixture dist/ trees (tar.gz + zip + checksums.txt, exactly like GoReleaser
# leaves them), then asserts:
#   (a) a valid dist passes the gate,
#   (b) every tampered/missing/corrupted variant fails CLOSED with the
#       expected typed [E_*] code,
#   (c) the gate derives the expected archive set from the real repository
#       .goreleaser.yaml (darwin_all universal + linux + windows zip matrix).
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
GATE="${SCRIPT_DIR}/artifact_validate.sh"

if [ ! -f "$GATE" ]; then
    echo "ERROR: gate script not found at $GATE" >&2
    exit 1
fi
if ! command -v go >/dev/null 2>&1; then
    echo "ERROR: go toolchain required to build fixture binaries" >&2
    exit 1
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/artifact_validate_test_XXXXXX")"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

echo "==> Running artifact validation gate test suite..."
echo "    fixtures: ${TMP_DIR}"

FAILURES=0

run_test() {
    local desc="$1"
    local expect="$2"   # pass | fail
    local expected_pattern="${3:-}"
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?

    if [ "$expect" = "pass" ] && [ "$exit_code" -ne 0 ]; then
        echo "  [FAIL] Expected exit 0, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
        return
    elif [ "$expect" = "fail" ] && [ "$exit_code" -eq 0 ]; then
        echo "  [FAIL] Expected non-zero (fail-closed), got 0"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
        return
    fi

    if [ -n "$expected_pattern" ]; then
        if ! echo "$output" | grep -q "$expected_pattern"; then
            echo "  [FAIL] Output missing expected pattern: $expected_pattern"
            echo "$output" | sed 's/^/    /'
            FAILURES=$((FAILURES + 1))
            return
        fi
    fi

    echo "  [PASS] ok"
}

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

# ---------------------------------------------------------------------------
# Fixture project: tiny Go program embedding Version via ldflags, printing
# "fixture version <v>" so the gate's native execution binding can check it.
# ---------------------------------------------------------------------------
V="0.27.1"
TAG="v${V}"
SRC="${TMP_DIR}/src"
BINS="${TMP_DIR}/bins"
mkdir -p "$SRC" "$BINS"

cat > "$SRC/go.mod" <<EOF
module fixture
go 1.22
EOF

cat > "$SRC/main.go" <<'EOF'
package main

import "fmt"

var Version = "dev"

func main() {
	fmt.Printf("fixture version %s\n", Version)
}
EOF

echo "==> Building fixture binaries (${V})..."
build_fixture() {
    local os="$1" arch="$2" out="$3" ver="$4"
    (cd "$SRC" && GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build \
        -ldflags "-s -w -X main.Version=${ver}" -o "$BINS/$out" .)
}
build_fixture darwin amd64 fixture_darwin_amd64 "$V"
build_fixture darwin arm64 fixture_darwin_arm64 "$V"
build_fixture linux amd64 fixture_linux_amd64 "$V"
build_fixture linux arm64 fixture_linux_arm64 "$V"
build_fixture windows amd64 fixture_windows_amd64.exe "$V"
build_fixture windows arm64 fixture_windows_arm64.exe "$V"
# Wrong-version binary for the E_VERSION_MISMATCH fixture.
build_fixture linux amd64 fixture_linux_amd64_bad "9.9.9"

# Universal darwin binary via lipo when available (macOS); on other hosts a
# plain darwin/amd64 binary stands in (the gate only reads its build info).
if command -v lipo >/dev/null 2>&1; then
    lipo -create "$BINS/fixture_darwin_amd64" "$BINS/fixture_darwin_arm64" \
        -output "$BINS/fixture_universal"
else
    cp "$BINS/fixture_darwin_amd64" "$BINS/fixture_universal"
fi

# Fixture GoReleaser config: mirrors the repository config shape (block
# goos/goarch lists, env/ldflags keys between lists, universal darwin_all
# replace, tar.gz archives with a windows zip format_override) so the gate's
# parser is exercised against the exact constructs it must handle.
FIXTURE_CONFIG="${TMP_DIR}/fixture_goreleaser.yaml"
cat > "$FIXTURE_CONFIG" <<EOF
version: 2
project_name: fixture

builds:
  - id: fixture
    main: .
    binary: fixture
    env:
      - CGO_ENABLED=0
    goos:
      - darwin
      - linux
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X main.Version=${V}
  - id: fixture-windows
    main: .
    binary: fixture
    goos:
      - windows
    goarch:
      - amd64
      - arm64

universal_binaries:
  - id: darwin_all
    ids:
      - fixture
    replace: true

archives:
  - id: default
    formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files:
      - README.md
    format_overrides:
      - goos: windows
        formats: [zip]

checksum:
  name_template: "checksums.txt"
EOF

EXPECTED_ARCHIVES="fixture_${V}_darwin_all.tar.gz fixture_${V}_linux_amd64.tar.gz fixture_${V}_linux_arm64.tar.gz fixture_${V}_windows_amd64.zip fixture_${V}_windows_arm64.zip"

# ---------------------------------------------------------------------------
# dist/ fixture builder — assembles a GoReleaser-shaped dist tree
#   make_dist <dir> [variant]
# variant swaps in corrupted content for the negative cases.
# ---------------------------------------------------------------------------
STAGES="${TMP_DIR}/stages"
mkdir -p "$STAGES"

stage_archive() {
    local stage="$1" contents="$2" name="$3"
    rm -rf "$stage"; mkdir -p "$stage"
    if [ -n "$contents" ]; then
        # Archives carry the binary at their root named exactly like the
        # GoReleaser `binary:` field ("fixture").
        cp "$contents" "$stage/fixture"
    fi
    printf 'fixture readme\n' > "$stage/README.md"
    # Write the archive OUTSIDE the stage dir so tar never archives itself.
    (cd "$stage" && tar -czf "../${name}" .)
}

stage_zip() {
    local stage="$1" contents="$2" name="$3"
    rm -rf "$stage"; mkdir -p "$stage"
    if [ -n "$contents" ]; then
        # Windows archives carry the binary as "fixture.exe" at their root.
        cp "$contents" "$stage/fixture.exe"
    fi
    (cd "$stage" && zip -q "../${name}" "fixture.exe")
}

make_dist() {
    local dist="$1"
    local variant="${2:-valid}"
    rm -rf "$dist"; mkdir -p "$dist"

    case "$variant" in
        wrong_version)
            stage_archive "$STAGES/s1" "$BINS/fixture_linux_amd64_bad" "fixture_${V}_linux_amd64.tar.gz"
            ;;
        no_build_info)
            # A text file masquerading as the binary inside the archive.
            stage_archive "$STAGES/s1" "" "fixture_${V}_linux_amd64.tar.gz"
            printf 'this is not a go binary\n' > "$STAGES/s1/fixture"
            (cd "$STAGES/s1" && tar -czf "../fixture_${V}_linux_amd64.tar.gz" fixture)
            ;;
        no_binary)
            stage_archive "$STAGES/s1" "" "fixture_${V}_linux_amd64.tar.gz"
            ;;
        *)
            stage_archive "$STAGES/s1" "$BINS/fixture_linux_amd64" "fixture_${V}_linux_amd64.tar.gz"
            ;;
    esac
    stage_archive "$STAGES/s2" "$BINS/fixture_universal" "fixture_${V}_darwin_all.tar.gz"
    stage_archive "$STAGES/s3" "$BINS/fixture_linux_arm64" "fixture_${V}_linux_arm64.tar.gz"
    stage_zip "$STAGES/z1" "$BINS/fixture_windows_amd64.exe" "fixture_${V}_windows_amd64.zip"
    stage_zip "$STAGES/z2" "$BINS/fixture_windows_arm64.exe" "fixture_${V}_windows_arm64.zip"
    mv "$STAGES/fixture_${V}_linux_amd64.tar.gz" "$dist/"
    mv "$STAGES/fixture_${V}_darwin_all.tar.gz" "$dist/"
    mv "$STAGES/fixture_${V}_linux_arm64.tar.gz" "$dist/"
    mv "$STAGES/fixture_${V}_windows_amd64.zip" "$dist/"
    mv "$STAGES/fixture_${V}_windows_arm64.zip" "$dist/"

    # Non-archive extras GoReleaser leaves behind (must be allowed).
    printf 'fake-linux-package\n' > "$dist/fixture_${V}_amd64.deb"
    printf '{"project_name":"fixture"}\n' > "$dist/metadata.json"

    case "$variant" in
        no_checksums)
            return
            ;;
        archive_not_listed)
            # checksums.txt missing the linux_arm64 entry
            : > "$dist/checksums.txt"
            for f in "$dist"/*; do
                b="$(basename "$f")"
                [ "$b" = "checksums.txt" ] && continue
                case "$b" in
                    "fixture_${V}_linux_arm64.tar.gz") continue ;;
                esac
                printf '%s  %s\n' "$(sha256_file "$f")" "$b" >> "$dist/checksums.txt"
            done
            return
            ;;
        lists_missing_file)
            # checksums.txt references a file that does not exist
            : > "$dist/checksums.txt"
            for f in "$dist"/*; do
                b="$(basename "$f")"
                [ "$b" = "checksums.txt" ] && continue
                printf '%s  %s\n' "$(sha256_file "$f")" "$b" >> "$dist/checksums.txt"
            done
            printf '%s  ghost_gone.tar.gz\n' "$(printf '%064d' 0 | tr '0' 'a')" >> "$dist/checksums.txt"
            return
            ;;
    esac

    # Default: checksums over every dist file (archives + extras).
    # GoReleaser never lists checksums.txt itself — mirror that.
    : > "$dist/checksums.txt"
    for f in "$dist"/*; do
        b="$(basename "$f")"
        [ "$b" = "checksums.txt" ] && continue
        printf '%s  %s\n' "$(sha256_file "$f")" "$b" >> "$dist/checksums.txt"
    done

    case "$variant" in
        tampered)
            # Corrupt AFTER checksums.txt was written (release-tamper model).
            printf 'TAMPERED' >> "$dist/fixture_${V}_linux_arm64.tar.gz"
            ;;
        extra_archive)
            printf 'junk\n' > "$dist/rogue_extra.tar.gz"
            ;;
    esac
}

VALID_DIST="${TMP_DIR}/dist_valid"

# ---------------------------------------------------------------------------
# (a) Positive controls
# ---------------------------------------------------------------------------
make_dist "$VALID_DIST" valid

run_test "Fixture (a): valid dist passes the gate (config-derived expectation)" \
    "pass" "GATE PASS" \
    bash "$GATE" --dist "$VALID_DIST" --tag "$TAG" --config "$FIXTURE_CONFIG"

run_test "Fixture (b): valid dist passes with explicit --archives list" \
    "pass" "GATE PASS" \
    bash "$GATE" --dist "$VALID_DIST" --tag "$TAG" --archives "$EXPECTED_ARCHIVES" --binary fixture

# ---------------------------------------------------------------------------
# (b) Negative acceptance — tampered / missing / corrupted dist variants
# ---------------------------------------------------------------------------
NEG="${TMP_DIR}/dist_neg"

make_dist "$NEG" missing; rm "$NEG/fixture_${V}_linux_arm64.tar.gz"
run_test "Fixture (c): missing dist archive refuses (fail-closed)" \
    "fail" "E_MISSING_ARCHIVE" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" tampered
run_test "Fixture (d): tampered archive fails sha256 recomputation" \
    "fail" "E_CHECKSUM_MISMATCH" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" no_checksums
run_test "Fixture (e): missing checksums.txt refuses" \
    "fail" "E_MISSING_CHECKSUMS" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" archive_not_listed
run_test "Fixture (f): archive missing from checksums.txt refuses" \
    "fail" "E_ARCHIVE_NOT_IN_CHECKSUMS" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" lists_missing_file
run_test "Fixture (g): checksums.txt referencing absent file refuses" \
    "fail" "E_CHECKSUM_FILE_MISSING" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" wrong_version
run_test "Fixture (h): wrong embedded version (9.9.9 != ${V}) refuses" \
    "fail" "E_VERSION_MISMATCH" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" no_build_info
run_test "Fixture (i): non-Go binary in archive refuses" \
    "fail" "E_NO_BUILD_INFO" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" no_binary
run_test "Fixture (j): archive without binary refuses" \
    "fail" "E_BINARY_MISSING" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" extra_archive
run_test "Fixture (k): unexpected rogue archive in dist refuses" \
    "fail" "E_UNEXPECTED_ARCHIVE" \
    bash "$GATE" --dist "$NEG" --tag "$TAG" --config "$FIXTURE_CONFIG"

make_dist "$NEG" valid
run_test "Fixture (l): wrong tag vs dist contents (version-in-name binding)" \
    "fail" "E_MISSING_ARCHIVE" \
    bash "$GATE" --dist "$NEG" --tag v9.9.9 --config "$FIXTURE_CONFIG"

# ---------------------------------------------------------------------------
# (c) Usage / environment errors
# ---------------------------------------------------------------------------
run_test "Usage: missing --tag fails with E_USAGE" \
    "fail" "E_USAGE" \
    bash "$GATE" --dist "$VALID_DIST"

run_test "Usage: missing dist directory fails with E_DIST_NOT_FOUND" \
    "fail" "E_DIST_NOT_FOUND" \
    bash "$GATE" --dist "${TMP_DIR}/does_not_exist" --tag "$TAG" --config "$FIXTURE_CONFIG"

# ---------------------------------------------------------------------------
# (d) Real repository config derivation — the gate must derive exactly the
#     platform matrix advertised by .goreleaser.yaml (darwin_all universal,
#     linux amd64/arm64, windows amd64/arm64 zips).
# ---------------------------------------------------------------------------
REAL_DIST="${TMP_DIR}/dist_real"
mkdir -p "$REAL_DIST"
: > "$REAL_DIST/g8s_${V}_darwin_all.tar.gz"
: > "$REAL_DIST/g8s_${V}_linux_amd64.tar.gz"
: > "$REAL_DIST/g8s_${V}_linux_arm64.tar.gz"
: > "$REAL_DIST/g8s_${V}_windows_amd64.zip"
: > "$REAL_DIST/g8s_${V}_windows_arm64.zip"
: > "$REAL_DIST/checksums.txt"
for f in "$REAL_DIST"/*; do
    b="$(basename "$f")"
    [ "$b" = "checksums.txt" ] && continue
    printf '%s  %s\n' "$(sha256_file "$f")" "$b" >> "$REAL_DIST/checksums.txt"
done

run_test "Repository .goreleaser.yaml derives the exact 5-archive platform matrix" \
    "fail" "expected archives (5)" \
    bash "$GATE" --dist "$REAL_DIST" --tag "$TAG" --config "$REPO_ROOT/.goreleaser.yaml"

GATE_OUTPUT="$(bash "$GATE" --dist "$REAL_DIST" --tag "$TAG" --config "$REPO_ROOT/.goreleaser.yaml" 2>&1 || true)"
for expected in \
    "g8s_${V}_darwin_all.tar.gz" \
    "g8s_${V}_linux_amd64.tar.gz" \
    "g8s_${V}_linux_arm64.tar.gz" \
    "g8s_${V}_windows_amd64.zip" \
    "g8s_${V}_windows_arm64.zip"; do
    if ! printf '%s' "$GATE_OUTPUT" | grep -qF "$expected"; then
        echo "  [FAIL] repository config derivation missing expected archive: $expected"
        FAILURES=$((FAILURES + 1))
    fi
done
# The empty fixture archives fail during per-archive binding — the exact
# typed code is platform-dependent (GNU tar refuses to extract zero-byte
# archives, bsdtar extracts them and the binary check then fires), so accept
# any of the binding-stage codes.
if printf '%s' "$GATE_OUTPUT" | grep -Eq "E_(BINARY_MISSING|EXTRACT_FAILED|NO_BUILD_INFO)"; then
    echo "  [PASS] repository config derivation lists full matrix, then fail-closes on empty fixtures"
else
    echo "  [FAIL] repository config derivation did not reach the binary check"
    echo "$GATE_OUTPUT" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
if [ "$FAILURES" -gt 0 ]; then
    echo "ARTIFACT VALIDATE TEST SUITE: $FAILURES failure(s)."
    exit 1
fi

echo "ARTIFACT VALIDATE TEST SUITE: all tests passed."
exit 0
