#!/usr/bin/env bash
#
# install_ps1_test.sh — Static assertion suite for scripts/install.ps1
# (Issue #986 D-8)
#
# CI has no Windows PowerShell runner wired for this gate, so instead of
# executing the installer this suite parses the .ps1 source with grep and
# asserts the fail-closed contract: fail-fast error preference, checksum
# verification, GitHub release URLs, env overrides, and no leftover
# placeholders. Mirrors the grep-assertion style of tools/ci_fetch_test.sh.
#
# If a PowerShell runner lands in CI later, replace this with a real
# Pester/pwsh invocation against scripts/install.ps1.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PS1_SCRIPT="${REPO_ROOT}/scripts/install.ps1"

if [ ! -f "$PS1_SCRIPT" ]; then
    echo "ERROR: install.ps1 not found at $PS1_SCRIPT" >&2
    exit 1
fi

echo "==> Running install.ps1 static assertion suite..."

assert_contains() {
    local desc="$1"
    local pattern="$2"
    if grep -qF -- "$pattern" "$PS1_SCRIPT"; then
        echo "  [PASS] $desc"
    else
        echo "  [FAIL] $desc — pattern not found: $pattern" >&2
        exit 1
    fi
}

assert_not_contains() {
    local desc="$1"
    local pattern="$2"
    if grep -qE -- "$pattern" "$PS1_SCRIPT"; then
        echo "  [FAIL] $desc — forbidden pattern found: $pattern" >&2
        exit 1
    else
        echo "  [PASS] $desc"
    fi
}

echo "Testing: fail-fast error preference (ErrorActionPreference = Stop)"
assert_contains "ErrorActionPreference set to Stop" '$ErrorActionPreference = "Stop"'

echo "Testing: env overrides honored (G8S_REPO / G8S_VERSION / G8S_INSTALL_DIR)"
assert_contains "G8S_REPO override" '$env:G8S_REPO'
assert_contains "G8S_VERSION override" '$env:G8S_VERSION'
assert_contains "G8S_INSTALL_DIR override" '$env:G8S_INSTALL_DIR'

echo "Testing: GitHub release URLs (latest-tag resolve + download base + checksums)"
assert_contains "GitHub releases API for latest tag" 'https://api.github.com/repos/$Repo/releases/latest'
assert_contains "GitHub release download base URL" 'https://github.com/$Repo/releases/download/$Version'
assert_contains "checksums.txt URL derived from base" '"$BaseUrl/checksums.txt"'
assert_contains "checksums.txt actually downloaded" 'Invoke-WebRequest -Uri $ChecksumsUrl -OutFile $ChecksumsPath'

echo "Testing: archive naming matches GoReleaser windows matrix (g8s_<ver>_windows_<arch>.zip)"
assert_contains "windows archive name pattern" 'g8s_${VersionNum}_windows_${Arch}.zip'

echo "Testing: SHA-256 verification is fail-closed"
assert_contains "Get-FileHash with SHA256 algorithm" '-Algorithm SHA256'
assert_contains "checksum mismatch throws" 'SHA-256 mismatch'
assert_contains "archive absent from checksums.txt throws" 'not found in checksums.txt'
assert_contains "extracted binary presence gate (g8s.exe)" 'g8s.exe not found'

echo "Testing: temp dir cleanup runs in finally block"
assert_contains "Remove-Item temp cleanup" 'Remove-Item -Path $TmpDir'

echo "Testing: no leftover placeholders"
assert_not_contains "no TODO/FIXME/PLACEHOLDER/CHANGEME markers" 'TODO|FIXME|PLACEHOLDER|CHANGEME'

echo ""
echo "[ALL INSTALL_PS1 STATIC ASSERTIONS PASSED]"
exit 0
