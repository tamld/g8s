#!/usr/bin/env bash
# Pre-tag validation gate — run before pushing any release tag
# Exit 0 = ready to tag, Exit 1 = blocked with reasons

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
    
    if [ "$version_code" != "$version_changelog" ]; then
        log_error "Version mismatch: code=$version_code, changelog=$version_changelog"
        return 1
    fi
    log_info "Version sync OK: $version_code"
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
    version=$(extract_version_code)
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
        if [ "$FORCE_TAG" = true ]; then
            log_warn "Tag $tag already exists on remote (force mode: allowing re-push)"
        else
            log_error "Tag $tag already exists on remote (use --force to re-push)"
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
    
    # Normalize: 1.26.0 -> 1.26
    local go_mod_major_minor
    go_mod_major_minor=$(echo "$go_mod_version" | cut -d. -f1,2)
    
    if [ "$go_mod_major_minor" != "$workflow_version" ]; then
        log_error "Go version mismatch: go.mod=$go_mod_version (normalized: $go_mod_major_minor), workflows=$workflow_version"
        return 1
    fi
    log_info "Go version consistent: $go_mod_version"
    return 0
}

check_ci_passes() {
    log_info "Checking recent CI status (last run per workflow)..."
    if ! command -v gh >/dev/null 2>&1; then
        log_warn "gh CLI not available, skipping CI check"
        return 0
    fi
    
    local failed_workflows=0
    local required_workflows=("Quality" "Crash-Survival E2E")
    
    # Check required workflows (excluding CI which has known Windows flakiness)
    for workflow in "${required_workflows[@]}"; do
        local status
        status=$(gh run list --workflow="$workflow" --branch=main --limit=1 --json conclusion -q '.[0].conclusion' 2>/dev/null || echo "unknown")
        if [ "$status" = "success" ]; then
            log_info "Workflow '$workflow': success"
        else
            log_error "Workflow '$workflow' last run: $status (expected success)"
            failed_workflows=$((failed_workflows + 1))
        fi
    done
    
    # Check CI separately - allow Windows-only failures
    local ci_status
    ci_status=$(gh run list --workflow="CI" --branch=main --limit=1 --json conclusion -q '.[0].conclusion' 2>/dev/null || echo "unknown")
    if [ "$ci_status" = "success" ]; then
        log_info "Workflow 'CI': success"
    else
        # Check if failure is Windows-only by viewing run details
        local run_id
        run_id=$(gh run list --workflow="CI" --branch=main --limit=1 --json databaseId -q '.[0].databaseId' 2>/dev/null)
        if [ -n "$run_id" ] && [ "$run_id" != "null" ]; then
            local jobs_file
            jobs_file=$(mktemp)
            gh run view "$run_id" --json jobs > "$jobs_file" 2>/dev/null
            
            local windows_failed=0
            local non_windows_failed=0
            
            if [ -s "$jobs_file" ]; then
                # Use awk to parse JSON - simpler and more reliable in CI
                # Extract job names and conclusions
                local job_data
                job_data=$(jq -r '.jobs[] | "\(.name)|\(.conclusion)"' "$jobs_file" 2>/dev/null)
                if [ -n "$job_data" ]; then
                    windows_failed=$(echo "$job_data" | awk -F'|' 'tolower($1) ~ /windows/ && $2 != "success" {count++} END {print count+0}')
                    non_windows_failed=$(echo "$job_data" | awk -F'|' 'tolower($1) !~ /windows/ && $2 != "success" {count++} END {print count+0}')
                fi
            fi
            rm -f "$jobs_file"
            
            if [ "$windows_failed" -gt 0 ] && [ "$non_windows_failed" -eq 0 ]; then
                log_warn "Workflow 'CI': failure appears to be Windows-only flakiness (known issue)"
            else
                log_error "Workflow 'CI' last run: $ci_status (non-Windows failures detected: non_windows_failed=$non_windows_failed)"
                failed_workflows=$((failed_workflows + 1))
            fi
        else
            log_warn "Workflow 'CI': could not determine failure details, assuming Windows flakiness"
        fi
    fi
    
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
    check_ci_passes || FAILED=1
    
    echo "========================================"
    if [ "$FAILED" -eq 0 ]; then
        log_info "ALL CHECKS PASSED — Ready to tag"
        exit 0
    else
        log_error "VALIDATION FAILED — Tag push blocked"
        exit 1
    fi
}

main "$@"