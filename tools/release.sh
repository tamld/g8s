#!/usr/bin/env bash
#
# release.sh — Automated release script for g8s
#
# Usage:
#   ./tools/release.sh patch|minor|major [--dry-run]
#   ./tools/release.sh 0.11.0 [--dry-run]
#
# This script:
# 1. Bumps version in cmd/g8s/version.go
# 2. Updates CHANGELOG.md with new version header (if not present)
# 3. Creates git commit
# 4. Creates git tag
# 5. Pushes to origin (unless --dry-run)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m'

usage() {
    cat <<EOF
Usage: $0 <patch|minor|major|VERSION> [--dry-run] [--no-push]

Bumps version, updates CHANGELOG, creates tag, and pushes to origin.

Arguments:
  patch     Increment patch version (0.10.0 -> 0.10.1)
  minor     Increment minor version (0.10.0 -> 0.11.0)
  major     Increment major version (0.10.0 -> 1.0.0)
  VERSION   Explicit version (e.g., 0.11.0, 1.0.0-rc.1)

Options:
  --dry-run    Show what would be done without making changes
  --no-push    Commit and tag locally but do not push to origin
  --help       Show this help

Examples:
  $0 patch
  $0 minor
  $0 0.11.0
  $0 1.0.0-rc.1 --dry-run
EOF
}

DRY_RUN=0
NO_PUSH="${NO_PUSH:-${G8S_RELEASE_NO_PUSH:-0}}"
VERSION_ARG=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            usage
            exit 0
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        --no-push|--skip-push)
            NO_PUSH=1
            shift
            ;;
        *)
            if [ -z "$VERSION_ARG" ]; then
                VERSION_ARG="$1"
            else
                echo -e "${RED}Error: Unexpected argument: $1${NC}" >&2
                usage
                exit 1
            fi
            shift
            ;;
    esac
done

if [ -z "$VERSION_ARG" ]; then
    usage
    exit 1
fi

# Get current version from cmd/g8s/version.go
CURRENT_VERSION=$(grep -E '^[[:space:]]*Version[[:space:]]*=' cmd/g8s/version.go | head -1 | sed -E 's/.*=[[:space:]]*"([^"]+)".*/\1/')
if [ -z "$CURRENT_VERSION" ]; then
    CURRENT_VERSION=$(grep 'Version' cmd/g8s/version.go | head -1 | sed -E 's/.*=[[:space:]]*"([^"]+)".*/\1/')
fi
if [ -z "$CURRENT_VERSION" ]; then
    echo -e "${RED}Error: Could not extract current version${NC}" >&2
    exit 1
fi

# Parse current version
IFS='.' read -r CUR_MAJOR CUR_MINOR CUR_PATCH <<< "${CURRENT_VERSION%%-*}"
CUR_PATCH="${CUR_PATCH%%-*}"

# Calculate new version
if [[ "$VERSION_ARG" =~ ^(patch|minor|major)$ ]]; then
    case "$VERSION_ARG" in
        patch)
            NEW_VERSION="${CUR_MAJOR}.${CUR_MINOR}.$((CUR_PATCH + 1))"
            ;;
        minor)
            NEW_VERSION="${CUR_MAJOR}.$((CUR_MINOR + 1)).0"
            ;;
        major)
            NEW_VERSION="$((CUR_MAJOR + 1)).0.0"
            ;;
    esac
else
    # Explicit version
    if [[ ! "$VERSION_ARG" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]]; then
        echo -e "${RED}Error: Invalid version format: $VERSION_ARG${NC}" >&2
        echo "Expected: MAJOR.MINOR.PATCH[-PRERELEASE]" >&2
        exit 1
    fi
    NEW_VERSION="$VERSION_ARG"
fi

echo -e "${BLUE}Current version: ${CURRENT_VERSION}${NC}"
echo -e "${BLUE}New version:     ${NEW_VERSION}${NC}"

if [ "$DRY_RUN" -eq 1 ]; then
    echo -e "${YELLOW}DRY RUN - no changes will be made${NC}"
fi

# Branch guard: refuse unless on main and up to date with origin
CURRENT_BRANCH=$(git branch --show-current 2>/dev/null || true)
if [ "$CURRENT_BRANCH" != "main" ]; then
    echo -e "${RED}Error: Branch guard: release must be cut from 'main' (currently on '${CURRENT_BRANCH:-detached HEAD}'). Please run from an up-to-date main.${NC}" >&2
    exit 1
fi

if ! git fetch -q origin 2>/dev/null && ! git fetch -q origin main 2>/dev/null; then
    echo -e "${YELLOW}Warning: git fetch failed (offline?). Continuing with local check...${NC}" >&2
fi

if git rev-parse --verify -q origin/main >/dev/null 2>&1; then
    LOCAL_MAIN=$(git rev-parse main 2>/dev/null || true)
    REMOTE_MAIN=$(git rev-parse origin/main 2>/dev/null || true)
    if [ "$LOCAL_MAIN" != "$REMOTE_MAIN" ]; then
        echo -e "${RED}Error: Local main is not up-to-date with origin/main. Please run from an up-to-date main.${NC}" >&2
        exit 1
    fi
fi

# Check for uncommitted changes (excluding release-managed files)
RELEASE_FILE_PATTERNS="cmd/g8s/version\.go|CHANGELOG\.md|manifest\.json|packaging/windows/g8s\.nsi|packaging/windows/g8s\.wxs|packaging/chocolatey/tools/chocolateyinstall\.ps1"
UNCOMMITTED=$(git status --porcelain | grep -v -E "^(M|M | M) (${RELEASE_FILE_PATTERNS})$" || true)
if [ -n "$UNCOMMITTED" ] && [ "$DRY_RUN" -eq 0 ]; then
    echo -e "${RED}Error: Uncommitted changes detected. Commit or stash first.${NC}" >&2
    git status --short
    exit 1
fi

# Pre-tag validation gates
if [ -f "tools/pre_tag.sh" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
        echo -e "${YELLOW}Would run pre-tag gates: bash tools/pre_tag.sh${NC}"
    else
        echo -e "${BLUE}Running pre-tag gates (tools/pre_tag.sh)...${NC}"
        if ! bash tools/pre_tag.sh; then
            echo -e "${RED}Error: Pre-tag gates failed. Aborting release.${NC}" >&2
            exit 1
        fi
        echo -e "${GREEN}✓ Pre-tag gates passed${NC}"
    fi
else
    echo -e "${YELLOW}Warning: tools/pre_tag.sh not found, skipping pre-tag gates${NC}" >&2
fi

# Update version.go
if [ "$DRY_RUN" -eq 1 ]; then
    echo -e "${YELLOW}Would update cmd/g8s/version.go: Version = \"${NEW_VERSION}\"${NC}"
else
    sed -i.bak -E "s/^([[:space:]]*Version[[:space:]]*=[[:space:]]*\")[^\"]+(\".*)$/\1${NEW_VERSION}\2/" cmd/g8s/version.go
    rm -f cmd/g8s/version.go.bak
    echo -e "${GREEN}✓ Updated cmd/g8s/version.go${NC}"
fi

# Update CHANGELOG.md - add new version header if not present
DATE=$(date +%Y-%m-%d)
CHANGELOG_HEADER="## [${NEW_VERSION}] - ${DATE}"
if grep -q "^## \[${NEW_VERSION}\]" CHANGELOG.md; then
    echo -e "${YELLOW}CHANGELOG.md already has entry for ${NEW_VERSION}${NC}"
else
    if [ "$DRY_RUN" -eq 1 ]; then
        echo -e "${YELLOW}Would add to CHANGELOG.md:${NC}"
        echo -e "  ${CHANGELOG_HEADER}"
        echo -e "  "
        echo -e "  ### Added"
        echo -e "  - "
    else
        # Insert after the first line (title)
        sed -i.bak "3a\\
\\
${CHANGELOG_HEADER}\\
\\
### Added\\
- " CHANGELOG.md
        rm -f CHANGELOG.md.bak
        echo -e "${GREEN}✓ Added ${NEW_VERSION} entry to CHANGELOG.md${NC}"
    fi
fi

# Update manifest.json (version and latest_release.tag)
# Note: latest_release.commit is intentionally NOT synced by this script
# due to self-reference circularity: the release commit sha is only final
# after the commit exists; the guard does not check it. commit and date
# are left alone.
if [ -f "manifest.json" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
        echo -e "${YELLOW}Would update manifest.json: version = \"${NEW_VERSION}\", latest_release.tag = \"v${NEW_VERSION}\"${NC}"
    else
        sed -i.bak -E "s/^([[:space:]]*\"version\":[[:space:]]*\")[^\"]+(\".*)$/\1${NEW_VERSION}\2/" manifest.json
        sed -i.bak -E "s/^([[:space:]]*\"tag\":[[:space:]]*\")v?[^\"]+(\".*)$/\1v${NEW_VERSION}\2/" manifest.json
        rm -f manifest.json.bak
        echo -e "${GREEN}✓ Updated manifest.json${NC}"
    fi
else
    echo -e "${YELLOW}Warning: manifest.json not found, skipping manifest update${NC}" >&2
fi

# Update packaging templates (best-effort: warn loudly if missing, do not abort)
if [ -f "packaging/windows/g8s.nsi" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
        echo -e "${YELLOW}Would update packaging/windows/g8s.nsi: VERSION = \"${NEW_VERSION}\"${NC}"
    else
        sed -i.bak -E "s/^([[:space:]]*!define[[:space:]]+VERSION[[:space:]]+\")[^\"]+(\".*)$/\1${NEW_VERSION}\2/" packaging/windows/g8s.nsi
        rm -f packaging/windows/g8s.nsi.bak
        echo -e "${GREEN}✓ Updated packaging/windows/g8s.nsi${NC}"
    fi
else
    echo -e "${YELLOW}Warning: packaging/windows/g8s.nsi not found, skipping packaging template update${NC}" >&2
fi

if [ -f "packaging/windows/g8s.wxs" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
        echo -e "${YELLOW}Would update packaging/windows/g8s.wxs: Version = \"${NEW_VERSION}\"${NC}"
    else
        sed -i.bak -E "s/^([[:space:]]*<\?define[[:space:]]+Version[[:space:]]*=[[:space:]]*\")[^\"]+(\".*)$/\1${NEW_VERSION}\2/" packaging/windows/g8s.wxs
        rm -f packaging/windows/g8s.wxs.bak
        echo -e "${GREEN}✓ Updated packaging/windows/g8s.wxs${NC}"
    fi
else
    echo -e "${YELLOW}Warning: packaging/windows/g8s.wxs not found, skipping packaging template update${NC}" >&2
fi

if [ -f "packaging/chocolatey/tools/chocolateyinstall.ps1" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
        echo -e "${YELLOW}Would update packaging/chocolatey/tools/chocolateyinstall.ps1: download URL -> v${NEW_VERSION}${NC}"
    else
        sed -i.bak -E \
            -e "s|(/download/)v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?/|\1v${NEW_VERSION}/|g" \
            -e "s|(/g8s_)v?[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?_|\1${NEW_VERSION}_|g" \
            packaging/chocolatey/tools/chocolateyinstall.ps1
        rm -f packaging/chocolatey/tools/chocolateyinstall.ps1.bak
        echo -e "${GREEN}✓ Updated packaging/chocolatey/tools/chocolateyinstall.ps1${NC}"
    fi
else
    echo -e "${YELLOW}Warning: packaging/chocolatey/tools/chocolateyinstall.ps1 not found, skipping chocolatey script update${NC}" >&2
fi

# Build list of release files to stage
STAGE_FILES=("cmd/g8s/version.go")
if [ -f "CHANGELOG.md" ]; then
    STAGE_FILES+=("CHANGELOG.md")
fi
if [ -f "manifest.json" ]; then
    STAGE_FILES+=("manifest.json")
fi
for pkg_file in "packaging/windows/g8s.nsi" "packaging/windows/g8s.wxs" "packaging/chocolatey/tools/chocolateyinstall.ps1"; do
    if [ -f "$pkg_file" ]; then
        STAGE_FILES+=("$pkg_file")
    fi
done

if [ "$DRY_RUN" -eq 1 ]; then
    echo -e "${YELLOW}Would run: git add ${STAGE_FILES[*]}${NC}"
    echo -e "${YELLOW}Would run: git commit -m \"chore: release ${NEW_VERSION}\"${NC}"
    echo -e "${YELLOW}Would run: git tag -a v${NEW_VERSION} -m \"Release ${NEW_VERSION}\"${NC}"
    echo -e "${YELLOW}Would run: git push origin main --tags${NC}"
    exit 0
fi

# Commit changes
git add "${STAGE_FILES[@]}"

if git diff --cached --quiet; then
    echo -e "${YELLOW}No changes to commit (already at version ${NEW_VERSION})${NC}"
else
    git commit -m "chore: release ${NEW_VERSION}"
fi

# Create tag
if git rev-parse "refs/tags/v${NEW_VERSION}" >/dev/null 2>&1; then
    echo -e "${YELLOW}Tag v${NEW_VERSION} already exists${NC}"
else
    git tag -a "v${NEW_VERSION}" -m "Release ${NEW_VERSION}"
fi

# Push
if [ "$NO_PUSH" -eq 1 ]; then
    echo -e "${YELLOW}Skipping push (--no-push)${NC}"
else
    echo -e "${BLUE}Pushing to origin...${NC}"
    git push origin main
    git push origin "v${NEW_VERSION}"
fi

echo -e "${GREEN}✓ Release ${NEW_VERSION} created and pushed!${NC}"
