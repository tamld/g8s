#!/usr/bin/env bash
# Draft cleanup automation - remove draft releases, keep only published ones
# Run via cron or manual

set -euo pipefail

REPO="tamld/g8s"

log_info() { echo -e "\033[0;32m[INFO]\033[0m $*"; }
log_warn() { echo -e "\033[1;33m[WARN]\033[0m $*"; }
log_error() { echo -e "\033[0;31m[ERROR]\033[0m $*"; }

# Check gh CLI
if ! command -v gh >/dev/null 2>&1; then
    log_error "gh CLI not available"
    exit 1
fi

log_info "Fetching releases for $REPO..."
releases=$(gh release list --repo "$REPO" --json name,isDraft,publishedAt,tagName -q '.[]' 2>/dev/null)

if [ -z "$releases" ]; then
    log_warn "No releases found"
    exit 0
fi

# Count drafts
draft_count=$(echo "$releases" | jq -s 'map(select(.isDraft)) | length')
published_count=$(echo "$releases" | jq -s 'map(select(.isDraft | not)) | length')

log_info "Found $published_count published releases, $draft_count draft releases"

if [ "$draft_count" -eq 0 ]; then
    log_info "No draft releases to clean up"
    exit 0
fi

# List drafts to be deleted
echo "$releases" | jq -r 'select(.isDraft) | "\(.tagName) (\(.name))"' | while read -r line; do
    log_warn "Draft to delete: $line"
done

# Delete drafts
echo "$releases" | jq -r 'select(.isDraft) | .tagName' | while read -r tag; do
    log_info "Deleting draft release for tag: $tag"
    gh release delete "$tag" --repo "$REPO" --yes 2>/dev/null && log_info "Deleted draft: $tag" || log_error "Failed to delete: $tag"
done

log_info "Draft cleanup complete"