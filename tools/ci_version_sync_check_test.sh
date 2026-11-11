#!/usr/bin/env bash
#
# Test suite for tools/ci_version_sync_check.sh (#476)
# Validates version sync check tooling polish:
#   (a) no DEBUG output in execution
#   (b) 0.13.0-vs-0.13.0 match passes cleanly
#   (c) prerelease ordering: rc.10 > rc.9 does not report drift (fixture)
#   (d) prerelease ordering: rc.9 < rc.10 reports behind failure (fixture)
#   (e) unit compare: rc.10 vs rc.9 returns 1
#   (f) unit compare: rc.9 vs rc.10 returns -1
#   (g) unit compare: 0.13.0 vs 0.13.0 returns 0
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CHECK_SCRIPT="${SCRIPT_DIR}/ci_version_sync_check.sh"

if [ ! -f "$CHECK_SCRIPT" ]; then
    echo "ERROR: version sync check script not found at $CHECK_SCRIPT"
    exit 1
fi

echo "==> Running CI Version Sync Check Test Suite..."

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

run_test_without_output() {
    local desc="$1"
    local expect_code="$2"
    local forbidden_pattern="$3"
    shift 3
    echo "Testing: $desc"
    local output=""
    local exit_code=0
    output=$("$@" 2>&1) || exit_code=$?
    if [ "$exit_code" -ne "$expect_code" ]; then
        echo "  FAIL: expected exit code $expect_code, got $exit_code"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    elif echo "$output" | grep -q "$forbidden_pattern"; then
        echo "  FAIL: output contained forbidden pattern '$forbidden_pattern'"
        echo "$output" | sed 's/^/    /'
        FAILURES=$((FAILURES + 1))
    else
        echo "  ok (exit $exit_code, forbidden pattern absent: '$forbidden_pattern')"
    fi
}

# --- fixtures -------------------------------------------------------------

FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT

setup_fixture_repo() {
    local dir="$1"
    local ver_go="$2"
    local tag="$3"
    mkdir -p "$dir/cmd/g8s"
    (
        cd "$dir"
        git init -q -b main
        git config user.name "Test Runner"
        git config user.email "test@example.com"

        cat > "cmd/g8s/version.go" <<EOF
package main

var (
	Version   = "$ver_go"
	Commit    = "testcommit"
	BuildTime = "testtime"
)
EOF

        cat > "CHANGELOG.md" <<EOF
# Changelog

## [$ver_go] - 2026-10-01

### Added
- Test entry
EOF

        git add cmd/g8s/version.go CHANGELOG.md
        git commit -q -m "tag target commit"
        git tag "v$tag"

        local tag_commit tag_date
        tag_commit="$(git rev-list -n1 "v$tag" | cut -c1-7)"
        tag_date="$(git show -s --format=%cs "v$tag^{commit}")"

        cat > "manifest.json" <<EOF
{
  "version": "$ver_go",
  "latest_release": {
    "tag": "v$tag",
    "commit": "$tag_commit",
    "date": "$tag_date"
  }
}
EOF

        git add manifest.json
        git commit -q -m "sync manifest latest_release metadata"
    )
}

# Fixture 1: rc.10 version with rc.9 git tag (ahead, valid prerelease)
FIXTURE_RC_AHEAD="$FIXTURE_ROOT/rc_ahead"
setup_fixture_repo "$FIXTURE_RC_AHEAD" "0.13.0-rc.10" "0.13.0-rc.9"

# Fixture 2: rc.9 version with rc.10 git tag (behind, invalid)
FIXTURE_RC_BEHIND="$FIXTURE_ROOT/rc_behind"
setup_fixture_repo "$FIXTURE_RC_BEHIND" "0.13.0-rc.9" "0.13.0-rc.10"

# --- test cases -----------------------------------------------------------

# (a) no DEBUG output in live execution
run_test_without_output \
    "(a) live script run does not emit DEBUG lines" 0 \
    "DEBUG:" \
    bash "$CHECK_SCRIPT"

# (b) live VERSION matches git tag and passes cleanly
LIVE_VER="$(grep -E '^\s*Version\s*=' "$REPO_ROOT/cmd/g8s/version.go" | head -n 1 | sed -E 's/.*"([^"]+)".*/\1/')"
run_test_with_output \
    "(b) live $LIVE_VER matches git tag and passes cleanly" 0 \
    "VERSION ($LIVE_VER) matches latest git tag" \
    bash "$CHECK_SCRIPT"

# (c) fixture: rc.10 > rc.9 must not report drift (exit 0, warn ahead)
run_test_with_output \
    "(c) fixture rc.10 > rc.9 does not report drift and exits 0" 0 \
    "VERSION (0.13.0-rc.10) is ahead of latest git tag (v0.13.0-rc.9)" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_RC_AHEAD"

# (d) fixture: rc.9 < rc.10 reports behind and fails (exit 1)
run_test_with_output \
    "(d) fixture rc.9 < rc.10 reports behind and fails" 1 \
    "VERSION (0.13.0-rc.9) is behind latest git tag (v0.13.0-rc.10)" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_RC_BEHIND"

# (e) unit compare: rc.10 vs rc.9 returns 1
run_test_with_output \
    "(e) unit compare 0.13.0-rc.10 > 0.13.0-rc.9 returns 1" 0 \
    "^1$" \
    bash "$CHECK_SCRIPT" --compare "0.13.0-rc.10" "0.13.0-rc.9"

# (f) unit compare: rc.9 vs rc.10 returns -1
run_test_with_output \
    "(f) unit compare 0.13.0-rc.9 < 0.13.0-rc.10 returns -1" 0 \
    "^-1$" \
    bash "$CHECK_SCRIPT" --compare "0.13.0-rc.9" "0.13.0-rc.10"

# (g) unit compare: 0.13.0 vs 0.13.0 returns 0
run_test_with_output \
    "(g) unit compare 0.13.0 == 0.13.0 returns 0" 0 \
    "^0$" \
    bash "$CHECK_SCRIPT" --compare "0.13.0" "0.13.0"

# (h) fixture: winget PackageVersion drift fails check
FIXTURE_WINGET_DRIFT="$FIXTURE_ROOT/winget_drift"
setup_fixture_repo "$FIXTURE_WINGET_DRIFT" "0.23.0" "0.23.0"
mkdir -p "$FIXTURE_WINGET_DRIFT/packaging/winget"
cat > "$FIXTURE_WINGET_DRIFT/packaging/winget/manifest.yaml" <<'EOF'
PackageIdentifier: TamLD.g8s
PackageVersion: 0.7.0
Installers:
  - InstallerUrl: https://github.com/tamld/g8s/releases/download/v0.7.0/g8s_0.7.0_windows_amd64.zip
EOF
run_test_with_output \
    "(h) fixture winget version drift fails check" 1 \
    "Winget PackageVersion drift" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_WINGET_DRIFT"

# (i) fixture: chocolatey .msi drift fails check
FIXTURE_CHOCO_MSI="$FIXTURE_ROOT/choco_msi"
setup_fixture_repo "$FIXTURE_CHOCO_MSI" "0.23.0" "0.23.0"
mkdir -p "$FIXTURE_CHOCO_MSI/packaging/chocolatey/tools"
cat > "$FIXTURE_CHOCO_MSI/packaging/chocolatey/g8s.nuspec" <<'EOF'
<package><metadata><id>g8s</id><version>0.23.0</version></metadata></package>
EOF
cat > "$FIXTURE_CHOCO_MSI/packaging/chocolatey/tools/chocolateyinstall.ps1" <<'EOF'
$url = 'https://github.com/tamld/g8s/releases/download/v0.23.0/g8s_0.23.0_windows_amd64.msi'
$packageArgs = @{
  url = $url
}
Install-ChocolateyPackage @packageArgs
EOF
run_test_with_output \
    "(i) fixture chocolatey .msi asset fails check" 1 \
    "unsupported .msi asset" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_CHOCO_MSI"

# (j) fixture: winget InstallerUrl dot-wildcard / malformed version drift fails check
FIXTURE_WINGET_URL="$FIXTURE_ROOT/winget_url_drift"
setup_fixture_repo "$FIXTURE_WINGET_URL" "0.23.0" "0.23.0"
mkdir -p "$FIXTURE_WINGET_URL/packaging/winget"
cat > "$FIXTURE_WINGET_URL/packaging/winget/manifest.yaml" <<'EOF'
PackageIdentifier: TamLD.g8s
PackageVersion: 0.23.0
Installers:
  - InstallerUrl: https://github.com/tamld/g8s/releases/download/v0x23y0/g8s_0x23y0_windows_amd64.zip
EOF
run_test_with_output \
    "(j) fixture winget InstallerUrl 0x23y0 drift fails check" 1 \
    "Winget InstallerUrl drift" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_WINGET_URL"

# (k) fixture: chocolatey $url dot-wildcard / malformed version drift fails check
FIXTURE_CHOCO_URL="$FIXTURE_ROOT/choco_url_drift"
setup_fixture_repo "$FIXTURE_CHOCO_URL" "0.23.0" "0.23.0"
mkdir -p "$FIXTURE_CHOCO_URL/packaging/chocolatey/tools"
cat > "$FIXTURE_CHOCO_URL/packaging/chocolatey/g8s.nuspec" <<'EOF'
<package><metadata><id>g8s</id><version>0.23.0</version></metadata></package>
EOF
cat > "$FIXTURE_CHOCO_URL/packaging/chocolatey/tools/chocolateyinstall.ps1" <<'EOF'
$url = 'https://github.com/tamld/g8s/releases/download/v0x23y0/g8s_0x23y0_windows_amd64.zip'
Install-ChocolateyZipPackage @packageArgs
EOF
run_test_with_output \
    "(k) fixture chocolatey URL 0x23y0 drift fails check" 1 \
    "Chocolatey download URL drift" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_CHOCO_URL"

# (l) fixture: second winget InstallerUrl entry with stale 0.19.0 ZIP fails check
FIXTURE_WINGET_MULTI="$FIXTURE_ROOT/winget_multi_url"
setup_fixture_repo "$FIXTURE_WINGET_MULTI" "0.23.0" "0.23.0"
mkdir -p "$FIXTURE_WINGET_MULTI/packaging/winget"
cat > "$FIXTURE_WINGET_MULTI/packaging/winget/manifest.yaml" <<'EOF'
PackageIdentifier: TamLD.g8s
PackageVersion: 0.23.0
Installers:
  - Architecture: x64
    InstallerUrl: https://github.com/tamld/g8s/releases/download/v0.23.0/g8s_0.23.0_windows_amd64.zip
  - Architecture: x64
    InstallerUrl: https://github.com/tamld/g8s/releases/download/v0.19.0/g8s_0.19.0_windows_amd64.zip
EOF
run_test_with_output \
    "(l) fixture second winget InstallerUrl stale 0.19.0 fails check" 1 \
    "Winget InstallerUrl drift" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_WINGET_MULTI"

# (m) fixture: duplicate chocolatey $url assignment overriding 0.23.0 with 0.19.0 fails check
FIXTURE_CHOCO_REASSIGN="$FIXTURE_ROOT/choco_reassign_url"
setup_fixture_repo "$FIXTURE_CHOCO_REASSIGN" "0.23.0" "0.23.0"
mkdir -p "$FIXTURE_CHOCO_REASSIGN/packaging/chocolatey/tools"
cat > "$FIXTURE_CHOCO_REASSIGN/packaging/chocolatey/g8s.nuspec" <<'EOF'
<package><metadata><id>g8s</id><version>0.23.0</version></metadata></package>
EOF
cat > "$FIXTURE_CHOCO_REASSIGN/packaging/chocolatey/tools/chocolateyinstall.ps1" <<'EOF'
$url = 'https://github.com/tamld/g8s/releases/download/v0.23.0/g8s_0.23.0_windows_amd64.zip'
$url = 'https://github.com/tamld/g8s/releases/download/v0.19.0/g8s_0.19.0_windows_amd64.zip'
Install-ChocolateyZipPackage @packageArgs
EOF
run_test_with_output \
    "(m) fixture duplicate chocolatey \$url reassignment to 0.19.0 fails check" 1 \
    "Chocolatey download URL drift" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_CHOCO_REASSIGN"

# (n) fixture: winget flow-style YAML installer entry with stale 0.19.0 ZIP fails check
FIXTURE_WINGET_FLOW="$FIXTURE_ROOT/winget_flow_url"
setup_fixture_repo "$FIXTURE_WINGET_FLOW" "0.23.0" "0.23.0"
mkdir -p "$FIXTURE_WINGET_FLOW/packaging/winget"
cat > "$FIXTURE_WINGET_FLOW/packaging/winget/manifest.yaml" <<'EOF'
PackageIdentifier: TamLD.g8s
PackageVersion: 0.23.0
Installers:
  - Architecture: x64
    InstallerUrl: https://github.com/tamld/g8s/releases/download/v0.23.0/g8s_0.23.0_windows_amd64.zip
  - {Architecture: arm64, InstallerType: zip, InstallerUrl: https://github.com/tamld/g8s/releases/download/v0.19.0/g8s_0.19.0_windows_arm64.zip}
EOF
run_test_with_output \
    "(n) fixture winget flow-style YAML stale 0.19.0 URL fails check" 1 \
    "Winget InstallerUrl drift" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_WINGET_FLOW"

# (o) fixture: case-insensitive PowerShell $URL = $url.Replace(...) dynamic override fails check
FIXTURE_CHOCO_DYNAMIC="$FIXTURE_ROOT/choco_dynamic_url"
setup_fixture_repo "$FIXTURE_CHOCO_DYNAMIC" "0.23.0" "0.23.0"
mkdir -p "$FIXTURE_CHOCO_DYNAMIC/packaging/chocolatey/tools"
cat > "$FIXTURE_CHOCO_DYNAMIC/packaging/chocolatey/g8s.nuspec" <<'EOF'
<package><metadata><id>g8s</id><version>0.23.0</version></metadata></package>
EOF
cat > "$FIXTURE_CHOCO_DYNAMIC/packaging/chocolatey/tools/chocolateyinstall.ps1" <<'EOF'
$toolsDir = "$(Split-Path -Parent $MyInvocation.MyCommand.Definition)"
$url = 'https://github.com/tamld/g8s/releases/download/v0.23.0/g8s_0.23.0_windows_amd64.zip'
$URL = $url.Replace('0.23.0', '0.19.0')
$packageArgs = @{
  packageName = 'g8s'
  unzipLocation = $toolsDir
  url = $url
}
Install-ChocolateyZipPackage @packageArgs
EOF
run_test_with_output \
    "(o) fixture PowerShell \$URL.Replace dynamic override fails check" 1 \
    "Chocolatey download URL drift" \
    bash "$CHECK_SCRIPT" --root "$FIXTURE_CHOCO_DYNAMIC"

if [ "$FAILURES" -gt 0 ]; then
    echo ""
    echo "CI VERSION SYNC CHECK TEST SUITE: $FAILURES failure(s)."
    exit 1
fi

echo ""
echo "CI VERSION SYNC CHECK TEST SUITE: all tests passed."
exit 0
