#!/usr/bin/env bash
#
# ci_version_sync_check.sh — Verify VERSION constant, CHANGELOG, and git tag are in sync
#
# This script validates:
# 1. VERSION in cmd/g8s/version.go matches latest git tag (or is a valid prerelease)
# 2. CHANGELOG.md has an entry for the current VERSION
# 3. No uncommitted changes to version-related files without version bump

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m'

pass() { echo "  [PASS] $1"; }
warn() { echo "  [WARN] $1"; }
fail() {
    echo "  [FAIL] $1"
    exit 1
}

# Extract VERSION from cmd/g8s/version.go
VERSION_GO=$(grep 'Version' cmd/g8s/version.go | head -1 | sed -E 's/.*=[[:space:]]*"([^"]+)".*/\1/')
if [ -z "$VERSION_GO" ]; then
    fail "Could not extract VERSION from cmd/g8s/version.go"
fi
echo "DEBUG: VERSION_GO=$VERSION_GO"

# Get latest git tag (excluding test-tag)
LATEST_TAG=$(git tag -l 'v*' | grep -v 'test-tag' | sort -V | tail -1 | sed 's/^v//')
if [ -z "$LATEST_TAG" ]; then
    LATEST_TAG="0.0.0"
fi
echo "DEBUG: LATEST_TAG=$LATEST_TAG"

# Compare versions using bash (no python3 dependency)
# Returns: 0 if equal, 1 if v1 > v2, -1 if v1 < v2
compare_versions() {
    local v1="$1" v2="$2"
    
    # Strip 'v' prefix
    v1="${v1#v}"
    v2="${v2#v}"
    
    # Split by . and -
    IFS='.-' read -ra v1_parts <<< "$v1"
    IFS='.-' read -ra v2_parts <<< "$v2"
    
    local v1_major="${v1_parts[0]:-0}"
    local v1_minor="${v1_parts[1]:-0}"
    local v1_patch="${v1_parts[2]:-0}"
    local v1_pre="${v1_parts[3]:-}"
    
    local v2_major="${v2_parts[0]:-0}"
    local v2_minor="${v2_parts[1]:-0}"
    local v2_patch="${v2_parts[2]:-0}"
    local v2_pre="${v2_parts[3]:-}"
    
    # Compare core version
    if [ "$v1_major" -gt "$v2_major" ]; then echo 1; return; fi
    if [ "$v1_major" -lt "$v2_major" ]; then echo -1; return; fi
    if [ "$v1_minor" -gt "$v2_minor" ]; then echo 1; return; fi
    if [ "$v1_minor" -lt "$v2_minor" ]; then echo -1; return; fi
    if [ "$v1_patch" -gt "$v2_patch" ]; then echo 1; return; fi
    if [ "$v1_patch" -lt "$v2_patch" ]; then echo -1; return; fi
    
    # Same core version - prerelease is older
    if [ -z "$v1_pre" ] && [ -n "$v2_pre" ]; then echo 1; return; fi
    if [ -n "$v1_pre" ] && [ -z "$v2_pre" ]; then echo -1; return; fi
    
    # Both have prerelease or both don't - compare alphabetically
    if [ "$v1_pre" \> "$v2_pre" ]; then echo 1; return; fi
    if [ "$v1_pre" \< "$v2_pre" ]; then echo -1; return; fi
    
    echo 0
}

CMP=$(compare_versions "$VERSION_GO" "$LATEST_TAG")
echo "DEBUG: CMP=$CMP"

if [ "$CMP" -eq 0 ]; then
    pass "VERSION ($VERSION_GO) matches latest git tag (v$LATEST_TAG)"
elif [ "$CMP" -gt 0 ]; then
    warn "VERSION ($VERSION_GO) is ahead of latest git tag (v$LATEST_TAG) - prerelease or unreleased"
else
    fail "VERSION ($VERSION_GO) is behind latest git tag (v$LATEST_TAG). Bump version before pushing."
fi

# Check CHANGELOG has entry for VERSION_GO
if grep -q "## \[$VERSION_GO\]" CHANGELOG.md; then
    pass "CHANGELOG.md has entry for version $VERSION_GO"
else
    fail "CHANGELOG.md missing entry for version $VERSION_GO. Add release notes before pushing."
fi

# Check for uncommitted changes to version-related files
VERSION_FILES=("cmd/g8s/version.go" "CHANGELOG.md")
for f in "${VERSION_FILES[@]}"; do
    if git diff --name-only | grep -q "^$f$"; then
        warn "Uncommitted changes to $f - ensure version bump is intentional"
    fi
done

# Check if VERSION_GO follows semver
if [[ ! "$VERSION_GO" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]]; then
    fail "VERSION ($VERSION_GO) does not follow semantic versioning (MAJOR.MINOR.PATCH[-PRERELEASE])"
fi

pass "Version sync check passed"
exit 0