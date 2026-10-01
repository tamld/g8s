#!/usr/bin/env bash
#
# Test suite for tools/release.sh (#461 follow-up)
# Validates release script version synchronization:
#   (1) Dry-run on files pinned at 0.12.0 -> prints intended changes, files byte-identical afterwards
#   (2) Real run bumping 0.12.0 -> 0.13.0 -> version.go, manifest.json (version + latest_release.tag),
#       nsi, wxs, chocolatey URL all read 0.13.0/v0.13.0; latest_release.commit UNCHANGED;
#       manifest guard & packaging drift checks pass cleanly
#   (3) Idempotency: running again on already-0.13.0 files changes nothing and still exits 0
#   (4) Missing packaging files (fixture omits one) -> script warns but does not abort, exit 0
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RELEASE_SCRIPT="${REPO_ROOT}/tools/release.sh"
VERSION_SYNC_SCRIPT="${REPO_ROOT}/tools/ci_version_sync_check.sh"

if [ ! -f "$RELEASE_SCRIPT" ]; then
    echo "ERROR: release script not found at $RELEASE_SCRIPT"
    exit 1
fi

echo "==> Running Release Script Test Suite..."

FAILURES=0

run_test() {
    local desc="$1"
    local expect_code="$2"
    shift 2
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit $exit_code)"
    fi
}

run_test_with_output() {
    local desc="$1"
    local expect_code="$2"
    local pattern="$3"
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    elif ! echo "$output" | grep -q "$pattern"; then
        echo "  FAIL: output did not match pattern '$pattern'"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit $exit_code, pattern matched: '$pattern')"
    fi
}

# --- fixtures -------------------------------------------------------------

TEST_TMP="$(mktemp -d)"
trap 'rm -rf "$TEST_TMP"' EXIT

setup_fixture() {
    local target_dir="$1"
    local version="${2:-0.12.0}"
    local omit_file="${3:-}"

    mkdir -p "$target_dir/cmd/g8s" \
             "$target_dir/tools" \
             "$target_dir/packaging/windows" \
             "$target_dir/packaging/chocolatey/tools"

    # 1. version.go
    cat > "$target_dir/cmd/g8s/version.go" <<EOF
package main

var (
	Version   = "${version}"
	Commit    = "unknown"
	BuildTime = "unknown"
)
EOF

    # 2. CHANGELOG.md
    cat > "$target_dir/CHANGELOG.md" <<EOF
# Changelog

All notable changes to this project will be documented in this file.

## [${version}] - 2026-09-27

### Added
- Initial release notes for ${version}
EOF

    # 3. manifest.json
    cat > "$target_dir/manifest.json" <<EOF
{
  "\$schema": "https://json-schema.org/draft/2020-12/schema",
  "name": "g8s",
  "display_name": "g8s (The Gatekeepers)",
  "tagline": "A Lightweight, Zero-Trust Process Execution & Capability Harness for AI Agent CLI Workers",
  "version": "${version}",
  "go_version": "1.26.0",
  "latest_release": {
    "tag": "v${version}",
    "commit": "a255bd5",
    "date": "2026-09-27"
  },
  "license": "MIT OR Apache-2.0"
}
EOF

    # 4. packaging/windows/g8s.nsi
    if [ "$omit_file" != "packaging/windows/g8s.nsi" ]; then
        cat > "$target_dir/packaging/windows/g8s.nsi" <<EOF
; g8s (The Gatekeepers) - Windows installer
!include "LogicLib.nsh"

!ifndef VERSION
  !define VERSION "${version}"
!endif

Name "g8s (The Gatekeepers)"
EOF
    fi

    # 5. packaging/windows/g8s.wxs
    if [ "$omit_file" != "packaging/windows/g8s.wxs" ]; then
        cat > "$target_dir/packaging/windows/g8s.wxs" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <?ifndef Version?>
    <?define Version = "${version}" ?>
  <?endif?>
</Wix>
EOF
    fi

    # 6. packaging/chocolatey/tools/chocolateyinstall.ps1
    if [ "$omit_file" != "packaging/chocolatey/tools/chocolateyinstall.ps1" ]; then
        cat > "$target_dir/packaging/chocolatey/tools/chocolateyinstall.ps1" <<EOF
\$url = 'https://github.com/tamld/g8s/releases/download/v${version}/g8s_${version}_windows_amd64.msi'
\$checksum = '<from checksums.txt>'
\$packageArgs = @{
  packageName    = 'g8s'
  fileType       = 'MSI'
  url            = \$url
}
Install-ChocolateyPackage @packageArgs
EOF
    fi

    # Copy current release.sh and ci_version_sync_check.sh
    cp "$RELEASE_SCRIPT" "$target_dir/tools/release.sh"
    chmod +x "$target_dir/tools/release.sh"
    if [ -f "$VERSION_SYNC_SCRIPT" ]; then
        cp "$VERSION_SYNC_SCRIPT" "$target_dir/tools/ci_version_sync_check.sh"
        chmod +x "$target_dir/tools/ci_version_sync_check.sh"
    fi

    # Initialize hermetic git repo with bare remote for origin
    (
        cd "$target_dir"
        git init -q
        git config user.email "test@example.com"
        git config user.name "Test User"
        git checkout -q -b main 2>/dev/null || git checkout -q -B main
        git add .
        git commit -q -m "chore: initial fixture pinned at ${version}"
        git tag -a "v${version}" -m "Release ${version}"

        mkdir -p "$TEST_TMP/remotes"
        local bare_remote="$TEST_TMP/remotes/$(basename "$target_dir").git"
        rm -rf "$bare_remote"
        git clone --bare -q . "$bare_remote"
        git remote add origin "$bare_remote"
        git branch -q --set-upstream-to=origin/main main 2>/dev/null || true
    )
}

# --- test cases -----------------------------------------------------------

# Case 1: Dry-run on files pinned at 0.12.0
echo "Testing: (1) Dry-run on files pinned at 0.12.0 prints intended changes, files byte-identical"
FIXTURE_1="$TEST_TMP/fixture_1"
setup_fixture "$FIXTURE_1" "0.12.0"

SNAPSHOT_1="$TEST_TMP/snapshot_1"
mkdir -p "$SNAPSHOT_1"
(
    cd "$FIXTURE_1"
    git status --porcelain > "$SNAPSHOT_1/status.before"
    git rev-parse HEAD > "$SNAPSHOT_1/head.before"
    shasum cmd/g8s/version.go CHANGELOG.md manifest.json packaging/windows/g8s.nsi packaging/windows/g8s.wxs packaging/chocolatey/tools/chocolateyinstall.ps1 > "$SNAPSHOT_1/checksums.before"
)

output_1=""
exit_code_1=0
output_1=$( (cd "$FIXTURE_1" && bash tools/release.sh 0.13.0 --dry-run) 2>&1 ) || exit_code_1=$?

if [ "$exit_code_1" -ne 0 ]; then
    echo "  FAIL: dry-run returned exit code $exit_code_1"
    echo "$output_1" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
elif ! echo "$output_1" | grep -q "DRY RUN"; then
    echo "  FAIL: dry-run output missing 'DRY RUN'"
    FAILURES=$((FAILURES + 1))
elif ! echo "$output_1" | grep -q "manifest.json"; then
    echo "  FAIL: dry-run output did not mention manifest.json"
    FAILURES=$((FAILURES + 1))
else
    # Verify files are byte-identical
    (
        cd "$FIXTURE_1"
        shasum cmd/g8s/version.go CHANGELOG.md manifest.json packaging/windows/g8s.nsi packaging/windows/g8s.wxs packaging/chocolatey/tools/chocolateyinstall.ps1 > "$SNAPSHOT_1/checksums.after"
    )
    if ! diff -u "$SNAPSHOT_1/checksums.before" "$SNAPSHOT_1/checksums.after" >/dev/null; then
        echo "  FAIL: dry-run modified repository files!"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit 0, dry-run clean, files byte-identical)"
    fi
fi

# Case 2: Real run bumping 0.12.0 -> 0.13.0
echo "Testing: (2) Real run bumping 0.12.0 -> 0.13.0 updates all files, preserves commit sha, passes sync checks"
FIXTURE_2="$TEST_TMP/fixture_2"
setup_fixture "$FIXTURE_2" "0.12.0"

output_2=""
exit_code_2=0
output_2=$( (cd "$FIXTURE_2" && bash tools/release.sh 0.13.0) 2>&1 ) || exit_code_2=$?

if [ "$exit_code_2" -ne 0 ]; then
    echo "  FAIL: real run returned exit code $exit_code_2"
    echo "$output_2" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    # 2a. version.go
    actual_ver_go=$(grep 'Version' "$FIXTURE_2/cmd/g8s/version.go" | sed -E 's/.*=[[:space:]]*"([^"]+)".*/\1/')
    if [ "$actual_ver_go" != "0.13.0" ]; then
        echo "  FAIL: version.go has '$actual_ver_go', expected '0.13.0'"
        FAILURES=$((FAILURES + 1))
    fi

    # 2b. manifest.json version and latest_release.tag
    manifest_ver=$(grep -E '^[[:space:]]*"version":' "$FIXTURE_2/manifest.json" | head -1 | sed -E 's/.*"version":[[:space:]]*"([^"]+)".*/\1/')
    manifest_tag=$(sed -n '/"latest_release"[[:space:]]*:[[:space:]]*{/,/}/p' "$FIXTURE_2/manifest.json" | grep -E '"tag":' | head -1 | sed -E 's/.*"tag":[[:space:]]*"([^"]+)".*/\1/')
    manifest_commit=$(sed -n '/"latest_release"[[:space:]]*:[[:space:]]*{/,/}/p' "$FIXTURE_2/manifest.json" | grep -E '"commit":' | head -1 | sed -E 's/.*"commit":[[:space:]]*"([^"]+)".*/\1/')

    if [ "$manifest_ver" != "0.13.0" ]; then
        echo "  FAIL: manifest.json version is '$manifest_ver', expected '0.13.0'"
        FAILURES=$((FAILURES + 1))
    fi
    if [ "$manifest_tag" != "v0.13.0" ]; then
        echo "  FAIL: manifest.json latest_release.tag is '$manifest_tag', expected 'v0.13.0'"
        FAILURES=$((FAILURES + 1))
    fi
    if [ "$manifest_commit" != "a255bd5" ]; then
        echo "  FAIL: manifest.json latest_release.commit changed to '$manifest_commit', expected unchanged 'a255bd5'"
        FAILURES=$((FAILURES + 1))
    fi

    # 2c. packaging/windows/g8s.nsi
    nsi_ver=$(grep -oE '"[0-9]+\.[0-9]+\.[0-9]+"' "$FIXTURE_2/packaging/windows/g8s.nsi" | head -1 | tr -d '"')
    if [ "$nsi_ver" != "0.13.0" ]; then
        echo "  FAIL: g8s.nsi literal version is '$nsi_ver', expected '0.13.0'"
        FAILURES=$((FAILURES + 1))
    fi

    # 2d. packaging/windows/g8s.wxs
    wxs_ver=$(grep -oE '"[0-9]+\.[0-9]+\.[0-9]+"' "$FIXTURE_2/packaging/windows/g8s.wxs" | head -1 | tr -d '"')
    if [ "$wxs_ver" != "0.13.0" ]; then
        echo "  FAIL: g8s.wxs literal version is '$wxs_ver', expected '0.13.0'"
        FAILURES=$((FAILURES + 1))
    fi

    # 2e. packaging/chocolatey/tools/chocolateyinstall.ps1
    choco_url=$(grep '$url' "$FIXTURE_2/packaging/chocolatey/tools/chocolateyinstall.ps1" | head -1)
    if ! echo "$choco_url" | grep -q 'download/v0.13.0/'; then
        echo "  FAIL: chocolatey URL missing 'download/v0.13.0/': $choco_url"
        FAILURES=$((FAILURES + 1))
    fi
    if ! echo "$choco_url" | grep -q 'g8s_0.13.0_windows_amd64.msi'; then
        echo "  FAIL: chocolatey URL missing 'g8s_0.13.0_windows_amd64.msi': $choco_url"
        FAILURES=$((FAILURES + 1))
    fi

    # 2f. git tag and commit
    tag_exists=$(cd "$FIXTURE_2" && git rev-parse "refs/tags/v0.13.0" >/dev/null 2>&1 && echo "yes" || echo "no")
    if [ "$tag_exists" != "yes" ]; then
        echo "  FAIL: tag v0.13.0 was not created in git"
        FAILURES=$((FAILURES + 1))
    fi

    commit_files=$(cd "$FIXTURE_2" && git show --name-only --oneline HEAD)
    for expected_file in "cmd/g8s/version.go" "CHANGELOG.md" "manifest.json" "packaging/windows/g8s.nsi" "packaging/windows/g8s.wxs" "packaging/chocolatey/tools/chocolateyinstall.ps1"; do
        if ! echo "$commit_files" | grep -q "$expected_file"; then
            echo "  FAIL: release commit does not stage $expected_file"
            FAILURES=$((FAILURES + 1))
        fi
    done

    # 2g. Run acceptance oracle: tools/ci_version_sync_check.sh
    if [ -f "$FIXTURE_2/tools/ci_version_sync_check.sh" ]; then
        oracle_out=""
        oracle_code=0
        oracle_out=$( (cd "$FIXTURE_2" && bash tools/ci_version_sync_check.sh) 2>&1 ) || oracle_code=$?
        if [ "$oracle_code" -ne 0 ]; then
            echo "  FAIL: ci_version_sync_check.sh failed on tree produced by release.sh"
            echo "$oracle_out" | sed 's/^/    /'
            FAILURES=$((FAILURES + 1))
        fi
    fi

    # 2h. Run packaging-drift check from version-sync.yml
    drift_fail=0
    for f in "$FIXTURE_2/packaging/windows/g8s.nsi" "$FIXTURE_2/packaging/windows/g8s.wxs"; do
        lit=$(grep -oE '"[0-9]+\.[0-9]+\.[0-9]+"' "$f" | head -1 | tr -d '"')
        if [ "$lit" != "0.13.0" ]; then
            drift_fail=1
        fi
    done
    c_ver=$(grep -oE 'download/v[0-9]+\.[0-9]+\.[0-9]+/' "$FIXTURE_2/packaging/chocolatey/tools/chocolateyinstall.ps1" | head -1 | sed 's|download/v||; s|/$||')
    if [ "$c_ver" != "0.13.0" ]; then
        drift_fail=1
    fi
    if [ "$drift_fail" -ne 0 ]; then
        echo "  FAIL: packaging installer version drift check failed"
        FAILURES=$((FAILURES + 1))
    fi

    if [ "$FAILURES" -eq 0 ]; then
        echo "  ok (all files updated, commit sha unchanged, tags & sync check clean)"
    fi
fi

# Case 3: Idempotency: running again on already-0.13.0 files changes nothing and exits 0
echo "Testing: (3) Idempotency: running again on already-0.13.0 files changes nothing and exits 0"
FIXTURE_3="$TEST_TMP/fixture_3"
setup_fixture "$FIXTURE_3" "0.13.0"

output_3=""
exit_code_3=0
output_3=$( (cd "$FIXTURE_3" && bash tools/release.sh 0.13.0) 2>&1 ) || exit_code_3=$?

if [ "$exit_code_3" -ne 0 ]; then
    echo "  FAIL: idempotent run returned exit code $exit_code_3"
    echo "$output_3" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    # Check git status - must be completely clean
    dirty_status=$(cd "$FIXTURE_3" && git status --porcelain)
    if [ -n "$dirty_status" ]; then
        echo "  FAIL: working tree is dirty after idempotent run: $dirty_status"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit 0, working tree clean, no unexpected mutations)"
    fi
fi

# Case 4: Missing packaging files (fixture omits one) -> warns but does not abort, exits 0
echo "Testing: (4) Missing packaging files (omits g8s.nsi) -> script warns, does not abort, exits 0"
FIXTURE_4="$TEST_TMP/fixture_4"
setup_fixture "$FIXTURE_4" "0.12.0" "packaging/windows/g8s.nsi"

output_4=""
exit_code_4=0
output_4=$( (cd "$FIXTURE_4" && bash tools/release.sh 0.13.0) 2>&1 ) || exit_code_4=$?

if [ "$exit_code_4" -ne 0 ]; then
    echo "  FAIL: run with missing packaging file returned exit code $exit_code_4"
    echo "$output_4" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
elif ! echo "$output_4" | grep -iq "warn.*g8s\.nsi"; then
    echo "  FAIL: output did not contain loud warning for missing g8s.nsi"
    echo "$output_4" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
else
    # Verify version.go and manifest.json were still bumped
    v4_ver=$(grep 'Version' "$FIXTURE_4/cmd/g8s/version.go" | sed -E 's/.*=[[:space:]]*"([^"]+)".*/\1/')
    m4_ver=$(grep -E '^[[:space:]]*"version":' "$FIXTURE_4/manifest.json" | head -1 | sed -E 's/.*"version":[[:space:]]*"([^"]+)".*/\1/')
    if [ "$v4_ver" != "0.13.0" ] || [ "$m4_ver" != "0.13.0" ]; then
        echo "  FAIL: version bump failed when packaging file omitted (version.go: $v4_ver, manifest: $m4_ver)"
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit 0, warning emitted, version bump succeeded)"
    fi
fi

# --- summary --------------------------------------------------------------

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "RELEASE TEST SUITE: $FAILURES failure(s)."
    exit 1
fi

echo ""
echo "RELEASE TEST SUITE: all tests passed."
exit 0
