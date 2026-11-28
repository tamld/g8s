#!/usr/bin/env bash
# Pre-tag validation gate — run before pushing any release tag
# Exit 0 = ready to tag, Exit 1 = blocked with reasons
#
# Fail-closed semantics (D-11 #986):
#   - gh CLI absence is a HARD failure: the CI-status check is a release gate
#     and "cannot verify" must block the tag, not skip it.
#   - Re-pushing an existing remote tag requires BOTH --force AND the explicit
#     acknowledgment env var G8S_RELEASE_FORCE_TAG=1. Bare --force fails closed.
#
# Test seam: tools/pre_tag_test.sh sources this file with
# G8S_PRE_TAG_SOURCE_ONLY=1 to exercise individual check functions without
# running the full gate.

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

FAILED=0
FORCE_TAG=false

# Parse arguments
for arg in "$@"; do
    case $arg in
        --force|-f) FORCE_TAG=true ;;
        *) ;;
    esac
done

extract_version_code() {
    # Extract version from cmd/g8s/version.go - handles spaces/tabs
    grep '^[[:space:]]*Version[[:space:]]*=' cmd/g8s/version.go | head -1 | sed 's/.*= *"\([^"]*\)".*/\1/'
}

extract_version_changelog_latest() {
    # Extract latest released version from CHANGELOG (first ## [x.y.z] after [Unreleased])
    awk '/^## \[Unreleased\]/ {found=1; next} found && /^## \[/ {print $2; exit}' CHANGELOG.md | tr -d '[]'
}

check_version_sync() {
    log_info "Checking version sync..."
    local version_code
    version_code=$(extract_version_code)
    local version_changelog
    version_changelog=$(extract_version_changelog_latest)
    
    if [ -z "$version_code" ]; then
        log_error "Cannot extract version from cmd/g8s/version.go"
        return 1
    fi
    if [ -z "$version_changelog" ]; then
        log_error "Cannot extract latest version from CHANGELOG.md"
        return 1
    fi
    
    if [ "$version_code" != "$version_changelog" ] && [ "${G8S_TARGET_VERSION:-}" != "$version_changelog" ]; then
        log_error "Version mismatch: code=$version_code, changelog=$version_changelog"
        return 1
    fi
    log_info "Version sync OK: $version_changelog"
    return 0
}

check_changelog_has_content() {
    log_info "Checking CHANGELOG has content for latest version..."
    local version
    version=$(extract_version_changelog_latest)
    if [ -z "$version" ]; then
        log_error "Cannot determine latest version from CHANGELOG"
        return 1
    fi
    # Count bullet points in the latest version section
    local content_lines
    content_lines=$(awk -v ver="$version" '$0 ~ "## \\[" ver "\\]" {found=1; next} found && /^## \[/ {exit} found {print}' CHANGELOG.md | grep -c '^[-*]' || true)
    if [ "$content_lines" -eq 0 ]; then
        log_warn "CHANGELOG latest version ($version) has no bullet points"
        # Warning only, not blocking
    else
        log_info "CHANGELOG latest version ($version) has $content_lines changes documented"
    fi
    return 0
}

check_git_clean() {
    log_info "Checking git working tree clean..."
    if ! git diff --quiet || ! git diff --cached --quiet; then
        log_error "Working tree has uncommitted changes"
        git status --short
        return 1
    fi
    log_info "Git working tree clean"
    return 0
}

check_on_main() {
    log_info "Checking current branch is main..."
    local branch
    branch=$(git rev-parse --abbrev-ref HEAD)
    if [ "$branch" != "main" ]; then
        log_error "Not on main branch (current: $branch)"
        return 1
    fi
    log_info "On main branch"
    return 0
}

check_tag_not_exists() {
    log_info "Checking tag doesn't exist remotely..."
    local version
    version="${G8S_TARGET_VERSION:-$(extract_version_code)}"
    local tag="v$version"
    
    if [ -z "$version" ]; then
        log_error "Cannot determine version for tag check"
        return 1
    fi
    
    if git rev-parse "refs/tags/$tag" >/dev/null 2>&1; then
        if [ "$FORCE_TAG" = true ]; then
            log_warn "Tag $tag exists locally (force mode: allowing re-push)"
        else
            log_warn "Tag $tag exists locally"
        fi
    fi
    if git ls-remote --tags origin "refs/tags/$tag" 2>/dev/null | grep -q "refs/tags/$tag$"; then
        if [ "$FORCE_TAG" = true ] && [ "${G8S_RELEASE_FORCE_TAG:-0}" = "1" ]; then
            log_warn "FORCED TAG RE-PUSH ACKNOWLEDGED (--force + G8S_RELEASE_FORCE_TAG=1): tag $tag already exists on remote"
            log_warn "Deleting or moving a published release tag is destructive; the push will fail server-side unless the upstream tag is removed first"
        elif [ "$FORCE_TAG" = true ]; then
            log_error "Tag $tag already exists on remote: bare --force is not enough — set G8S_RELEASE_FORCE_TAG=1 to acknowledge the destructive re-push"
            return 1
        else
            log_error "Tag $tag already exists on remote (use --force with G8S_RELEASE_FORCE_TAG=1 to re-push)"
            return 1
        fi
    fi
    log_info "Tag $tag check passed"
    return 0
}

check_go_version() {
    log_info "Checking Go version consistency..."
    local go_mod_version
    go_mod_version=$(grep '^go ' go.mod | awk '{print $2}')
    local workflow_version
    workflow_version=$(grep -h "go-version:" .github/workflows/*.yml 2>/dev/null | head -1 | sed "s/.*go-version: '\([^']*\)'.*/\1/")
    
    if [ -z "$go_mod_version" ] || [ -z "$workflow_version" ]; then
        log_warn "Could not extract Go versions for comparison"
        return 0
    fi
    
    # Normalize both to major.minor: 1.26.9 -> 1.26
    local go_mod_major_minor
    go_mod_major_minor=$(echo "$go_mod_version" | cut -d. -f1,2)
    local workflow_major_minor
    workflow_major_minor=$(echo "$workflow_version" | cut -d. -f1,2)
    
    if [ "$go_mod_version" != "$workflow_version" ] && [ "$go_mod_major_minor" != "$workflow_major_minor" ]; then
        log_error "Go version mismatch: go.mod=$go_mod_version (normalized: $go_mod_major_minor), workflows=$workflow_version"
        return 1
    fi
    log_info "Go version consistent: $go_mod_version"
    return 0
}

check_winget_manifest() {
    # D-9 (#986): the winget InstallerSha256 placeholder (<from checksums.txt>)
    # must be replaced from the pinned release's checksums.txt before the
    # manifest is release-bound; mirror of the chocolatey install-time throw.
    log_info "Checking winget manifest checksum gate (D-09)..."
    if [ ! -f "tools/winget_manifest_check.sh" ]; then
        log_error "tools/winget_manifest_check.sh missing — cannot attest the winget installer manifest"
        return 1
    fi
    if ! bash tools/winget_manifest_check.sh; then
        log_error "winget manifest gate failed — replace the InstallerSha256 placeholder from the pinned release's checksums.txt before tagging (see tools/winget_manifest_check.sh)"
        return 1
    fi
    log_info "Winget manifest gate passed"
    return 0
}

check_surface_inventory() {
    log_info "Running fresh surface inventory (D-09 CLOCK: no tag cut without a current docs-claims-vs-wiring receipt)..."
    if [ ! -f "tools/ci_surface_inventory.sh" ]; then
        log_error "tools/ci_surface_inventory.sh missing — cannot attest CI surface claims"
        return 1
    fi
    if ! bash tools/ci_surface_inventory.sh; then
        log_error "Surface inventory found STALE-CLAIM or BROKEN-WIRE — reconcile docs before tagging"
        return 1
    fi
    log_info "Surface inventory clean"
    return 0
}

check_ci_passes() {
    log_info "Checking recent CI status (last completed run per workflow)..."
    if ! command -v gh >/dev/null 2>&1; then
        # D-11 (#986): the CI-status check is a release gate. With gh absent we
        # cannot verify that the required workflows passed, so this must fail
        # closed instead of silently skipping the gate.
        log_error "gh CLI not available — CI status cannot be verified, and the release gate fails closed without it"
        log_error "Install the GitHub CLI (https://cli.github.com/) and authenticate (gh auth login) before tagging"
        return 1
    fi
    
    local failed_workflows=0
    local required_workflows=("Crash-Survival E2E" "Windows E2E" "Quality" "CI")
    
    for workflow in "${required_workflows[@]}"; do
        local status
        status=$(gh run list --workflow="$workflow" --branch=main --status=completed --limit=1 --json conclusion -q '.[0].conclusion' 2>/dev/null || true)
        if [ -z "$status" ]; then
            status=$(gh run list --branch=main --status=completed --limit=40 --json workflowName,conclusion -q "[.[] | select(.workflowName == \"$workflow\")][0].conclusion" 2>/dev/null || echo "unknown")
        fi
        if [ "$status" = "success" ]; then
            log_info "Workflow '$workflow': success"
        else
            log_error "Workflow '$workflow' last completed run: $status (expected success)"
            failed_workflows=$((failed_workflows + 1))
        fi
    done
    
    if [ "$failed_workflows" -gt 0 ]; then
        return 1
    fi
    return 0
}

# Main
main() {
    echo "========================================"
    echo "  Pre-Tag Validation Gate"
    echo "========================================"
    
    check_version_sync || FAILED=1
    check_changelog_has_content || FAILED=1
    check_git_clean || FAILED=1
    check_on_main || FAILED=1
    check_tag_not_exists || FAILED=1
    check_go_version || FAILED=1
    check_winget_manifest || FAILED=1
    check_ci_passes || FAILED=1
    check_surface_inventory || FAILED=1
    
    echo "========================================"
    if [ "$FAILED" -eq 0 ]; then
        log_info "ALL CHECKS PASSED — Ready to tag"
        exit 0
    else
        log_error "VALIDATION FAILED — Tag push blocked"
        exit 1
    fi
}

# Test seam (D-11): source-only mode runs no checks, letting tools/pre_tag_test.sh
# exercise individual check functions in isolation.
if [ "${G8S_PRE_TAG_SOURCE_ONLY:-0}" != "1" ]; then
    main "$@"
fi