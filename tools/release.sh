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
Usage: $0 <patch|minor|major|VERSION> [--dry-run]

Bumps version, updates CHANGELOG, creates tag, and pushes to origin.

Arguments:
  patch     Increment patch version (0.10.0 -> 0.10.1)
  minor     Increment minor version (0.10.0 -> 0.11.0)
  major     Increment major version (0.10.0 -> 1.0.0)
  VERSION   Explicit version (e.g., 0.11.0, 1.0.0-rc.1)

Options:
  --dry-run    Show what would be done without making changes
  --help       Show this help

Examples:
  $0 patch
  $0 minor
  $0 0.11.0
  $0 1.0.0-rc.1 --dry-run
EOF
}

DRY_RUN=0
if [[ "${1:-}" == "--help" ]] || [[ "${1:-}" == "-h" ]]; then
    usage
    exit 0
fi

if [[ "${1:-}" == "--dry-run" ]]; then
    DRY_RUN=1
    shift
fi

if [[ $# -lt 1 ]]; then
    usage
    exit 1
fi

VERSION_ARG="$1"

# Get current version from cmd/g8s/version.go
CURRENT_VERSION=$(grep 'Version' cmd/g8s/version.go | head -1 | sed -E 's/.*=[[:space:]]*"([^"]+)".*/\1/')
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

# Check for uncommitted changes (excluding version.go and CHANGELOG.md which we'll modify)
UNCOMMITTED=$(git status --porcelain | grep -v -E '^(M|M | M) (cmd/g8s/version\.go|CHANGELOG\.md)$' || true)
if [ -n "$UNCOMMITTED" ] && [ "$DRY_RUN" -eq 0 ]; then
    echo -e "${RED}Error: Uncommitted changes detected. Commit or stash first.${NC}" >&2
    git status --short
    exit 1
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

if [ "$DRY_RUN" -eq 1 ]; then
    echo -e "${YELLOW}Would run: git add cmd/g8s/version.go CHANGELOG.md${NC}"
    echo -e "${YELLOW}Would run: git commit -m \"chore: release ${NEW_VERSION}\"${NC}"
    echo -e "${YELLOW}Would run: git tag -a v${NEW_VERSION} -m \"Release ${NEW_VERSION}\"${NC}"
    echo -e "${YELLOW}Would run: git push origin main --tags${NC}"
    exit 0
fi

# Commit changes
git add cmd/g8s/version.go CHANGELOG.md
git commit -m "chore: release ${NEW_VERSION}"

# Create tag
git tag -a "v${NEW_VERSION}" -m "Release ${NEW_VERSION}"

# Push
echo -e "${BLUE}Pushing to origin...${NC}"
git push origin main
git push origin "v${NEW_VERSION}"

echo -e "${GREEN}✓ Release ${NEW_VERSION} created and pushed!${NC}"