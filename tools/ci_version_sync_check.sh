#!/usr/bin/env bash
#
# ci_version_sync_check.sh — Verify VERSION constant, CHANGELOG, and git tag are in sync
#
# This script validates:
# 1. VERSION in cmd/g8s/version.go matches latest git tag (or is a valid prerelease)
# 2. CHANGELOG.md has an entry for the current VERSION
# 3. manifest.json version and latest_release.tag match version.go and latest git tag
# 4. No uncommitted changes to version-related files without version bump

set -euo pipefail

ROOT_DIR=""
COMPARE_ONLY=false
V1=""
V2=""

while [ $# -gt 0 ]; do
    case "$1" in
        --root)
            if [ $# -lt 2 ]; then
                echo "ERROR: --root requires a directory path" >&2
                exit 2
            fi
            ROOT_DIR="$2"
            shift 2
            ;;
        --compare)
            if [ $# -lt 3 ]; then
                echo "ERROR: --compare requires two version strings" >&2
                exit 2
            fi
            COMPARE_ONLY=true
            V1="$2"
            V2="$3"
            shift 3
            ;;
        -h|--help)
            echo "Usage: $0 [--root <dir>] [--compare <v1> <v2>]"
            exit 0
            ;;
        -*)
            echo "ERROR: unknown argument: $1" >&2
            exit 2
            ;;
        *)
            break
            ;;
    esac
done

# Compare versions using bash (no python3 dependency)
# Returns: 0 if equal, 1 if v1 > v2, -1 if v1 < v2
compare_versions() {
    local v1="$1" v2="$2"
    
    # Strip 'v' prefix
    v1="${v1#v}"
    v2="${v2#v}"
    
    local v1_core="$v1"
    local v1_pre=""
    if [[ "$v1" == *-* ]]; then
        v1_core="${v1%%-*}"
        v1_pre="${v1#*-}"
    fi

    local v2_core="$v2"
    local v2_pre=""
    if [[ "$v2" == *-* ]]; then
        v2_core="${v2%%-*}"
        v2_pre="${v2#*-}"
    fi

    # Split core version by .
    IFS='.' read -ra v1_parts <<< "$v1_core"
    IFS='.' read -ra v2_parts <<< "$v2_core"
    
    local v1_major="${v1_parts[0]:-0}"
    local v1_minor="${v1_parts[1]:-0}"
    local v1_patch="${v1_parts[2]:-0}"
    
    local v2_major="${v2_parts[0]:-0}"
    local v2_minor="${v2_parts[1]:-0}"
    local v2_patch="${v2_parts[2]:-0}"
    
    # Compare core version
    if [ "$v1_major" -gt "$v2_major" ]; then echo 1; return; fi
    if [ "$v1_major" -lt "$v2_major" ]; then echo -1; return; fi
    if [ "$v1_minor" -gt "$v2_minor" ]; then echo 1; return; fi
    if [ "$v1_minor" -lt "$v2_minor" ]; then echo -1; return; fi
    if [ "$v1_patch" -gt "$v2_patch" ]; then echo 1; return; fi
    if [ "$v1_patch" -lt "$v2_patch" ]; then echo -1; return; fi
    
    # Same core version - release is newer than prerelease
    if [ -z "$v1_pre" ] && [ -n "$v2_pre" ]; then echo 1; return; fi
    if [ -n "$v1_pre" ] && [ -z "$v2_pre" ]; then echo -1; return; fi
    if [ -z "$v1_pre" ] && [ -z "$v2_pre" ]; then echo 0; return; fi

    # Both have prerelease - compare segments numerically when digits
    IFS='.' read -ra v1_pre_parts <<< "$v1_pre"
    IFS='.' read -ra v2_pre_parts <<< "$v2_pre"

    local len1=${#v1_pre_parts[@]}
    local len2=${#v2_pre_parts[@]}
    local max_len=$len1
    if [ "$len2" -gt "$max_len" ]; then max_len=$len2; fi

    for ((i=0; i<max_len; i++)); do
        local id1="${v1_pre_parts[i]:-}"
        local id2="${v2_pre_parts[i]:-}"

        if [ -z "$id1" ] && [ -n "$id2" ]; then echo -1; return; fi
        if [ -n "$id1" ] && [ -z "$id2" ]; then echo 1; return; fi

        if [ "$id1" = "$id2" ]; then
            continue
        fi

        # Numeric segment comparison (e.g. 10 > 9)
        if [[ "$id1" =~ ^[0-9]+$ ]] && [[ "$id2" =~ ^[0-9]+$ ]]; then
            if [ "$id1" -gt "$id2" ]; then echo 1; return; fi
            if [ "$id1" -lt "$id2" ]; then echo -1; return; fi
        elif [[ "$id1" =~ ^[0-9]+$ ]]; then
            echo -1; return
        elif [[ "$id2" =~ ^[0-9]+$ ]]; then
            echo 1; return
        else
            # Check for identical prefix followed by digits (e.g. rc10 vs rc9)
            local pfx1="${id1%%[0-9]*}"
            local pfx2="${id2%%[0-9]*}"
            local sfx1="${id1#$pfx1}"
            local sfx2="${id2#$pfx2}"
            if [ "$pfx1" = "$pfx2" ] && [[ "$sfx1" =~ ^[0-9]+$ ]] && [[ "$sfx2" =~ ^[0-9]+$ ]]; then
                if [ "$sfx1" -gt "$sfx2" ]; then echo 1; return; fi
                if [ "$sfx1" -lt "$sfx2" ]; then echo -1; return; fi
            else
                if [ "$id1" \> "$id2" ]; then echo 1; return; fi
                if [ "$id1" \< "$id2" ]; then echo -1; return; fi
            fi
        fi
    done

    echo 0
}

if [ "$COMPARE_ONLY" = true ]; then
    compare_versions "$V1" "$V2"
    exit 0
fi

if [ -n "$ROOT_DIR" ]; then
    REPO_ROOT="$ROOT_DIR"
else
    REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fi
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

# Get latest git tag (excluding test-tag)
LATEST_TAG=$(git tag -l 'v*' | grep -v 'test-tag' | sort -V | tail -1 | sed 's/^v//')
if [ -z "$LATEST_TAG" ]; then
    LATEST_TAG="0.0.0"
fi

CMP=$(compare_versions "$VERSION_GO" "$LATEST_TAG")

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

# Check manifest.json version and latest_release.tag
if [ ! -f "manifest.json" ]; then
    fail "manifest.json not found"
fi

if command -v jq >/dev/null 2>&1; then
    MANIFEST_VERSION=$(jq -r '.version // empty' manifest.json)
    MANIFEST_TAG=$(jq -r '.latest_release.tag // empty' manifest.json)
else
    MANIFEST_VERSION=$(grep -E '^[[:space:]]*"version":' manifest.json | head -1 | sed -E 's/.*"version":[[:space:]]*"([^"]+)".*/\1/')
    MANIFEST_TAG=$(sed -n '/"latest_release"[[:space:]]*:[[:space:]]*{/,/}/p' manifest.json | grep -E '"tag":' | head -1 | sed -E 's/.*"tag":[[:space:]]*"([^"]+)".*/\1/')
fi

if [ -z "$MANIFEST_VERSION" ]; then
    fail "Could not extract version from manifest.json"
fi

if [ -z "$MANIFEST_TAG" ]; then
    fail "Could not extract latest_release.tag from manifest.json"
fi

if [ "$MANIFEST_VERSION" != "$VERSION_GO" ]; then
    fail "manifest.json version mismatch: field 'version' ($MANIFEST_VERSION) does not match cmd/g8s/version.go ($VERSION_GO)"
fi

if [ "$MANIFEST_TAG" != "v$LATEST_TAG" ]; then
    fail "manifest.json latest_release.tag mismatch: field 'latest_release.tag' ($MANIFEST_TAG) does not match latest git tag (v$LATEST_TAG)"
fi

pass "manifest.json version ($MANIFEST_VERSION) and latest_release.tag ($MANIFEST_TAG) match codebase and git tag"

# Check for uncommitted changes to version-related files
VERSION_FILES=("cmd/g8s/version.go" "CHANGELOG.md" "manifest.json")
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