#!/usr/bin/env bash
#
# tools/ci_lane_detect.sh — Two-lane CI/CD lane detector (ADR-0031)
#
# Classifies a git diff into either:
#   lane=docs  (prose & asset changes only)
#   lane=build (any code/build/infrastructure changes, or empty diff)
#
# Input:
#   $1 - base ref (default: origin/main)
#   $2 - optional base SHA (e.g. github.event.before on push events)
#
# Output:
#   stdout: exactly "lane=docs" or "lane=build"
#   exit:   0 always (detection never breaks a run)
#

set -uo pipefail

# Checks if a filename has an allowed prose extension: .md, .markdown, .txt, .adoc
is_prose_extension() {
    local f="$1"
    case "$f" in
        *.md|*.markdown|*.txt|*.adoc|*.MD|*.MARKDOWN|*.TXT|*.ADOC)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

# Classifies a single repository-relative path.
# Returns 0 if prose/asset, 1 if build/code.
is_prose_path() {
    local p="$1"

    # F3: anything under .github/** => build lane regardless of extension (workflow-adjacent)
    case "$p" in
        .github|.github/*|.GITHUB|.GITHUB/*)
            return 1
            ;;
    esac

    # Any .go file anywhere => build
    case "$p" in
        *.go|*.GO)
            return 1
            ;;
    esac

    # F2: a path under docs/ is prose ONLY if its extension is prose (.md, .markdown, .txt, .adoc)
    case "$p" in
        docs/*|DOCS/*|Docs/*)
            if is_prose_extension "$p"; then
                return 0
            else
                return 1
            fi
            ;;
        docs|DOCS|Docs)
            return 1
            ;;
    esac

    # Allowed prose/asset locations per ADR-0031
    case "$p" in
        *.md|*.markdown|*.MD|*.MARKDOWN)
            return 0
            ;;
        plans/*|plans|PLANS/*|PLANS)
            return 0
            ;;
        skills/*|skills|SKILLS/*|SKILLS)
            return 0
            ;;
        assets/*|assets|ASSETS/*|ASSETS)
            return 0
            ;;
        offer/*|offer|OFFER/*|OFFER)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

detect_lane() {
    local base="${1:-origin/main}"
    local base_sha="${2:-}"
    local files=""
    local used_base_sha=0

    # 1. Main-push detection: if base_sha is given, not all-zeros, and is a valid commit
    if [ -n "$base_sha" ] && ! [[ "$base_sha" =~ ^0+$ ]] && git rev-parse --verify "$base_sha^{commit}" >/dev/null 2>&1; then
        used_base_sha=1
        files=$(git diff --name-only "$base_sha"...HEAD 2>/dev/null || git diff --name-only "$base_sha" HEAD 2>/dev/null || true)
    elif git rev-parse --verify "$base" >/dev/null 2>&1; then
        files=$(git diff --name-only "$base"...HEAD 2>/dev/null || git diff --name-only "$base" HEAD 2>/dev/null || true)
    fi

    # 2. Fallback when the branch has no upstream diff: staged + unstaged files.
    # Note: if a valid base_sha was specified and produced an empty diff, preserve deny-by-default (do not fall back).
    if [ -z "$files" ] && [ "$used_base_sha" -eq 0 ]; then
        local staged unstaged
        staged=$(git diff --name-only --cached 2>/dev/null || true)
        unstaged=$(git diff --name-only 2>/dev/null || true)
        files=$(printf "%s\n%s\n" "$staged" "$unstaged" | sed '/^[[:space:]]*$/d' | sort -u)
    fi

    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
    repo_root=$(readlink -f "$repo_root" 2>/dev/null || echo "$repo_root")

    local readlink_available=0
    if command -v readlink >/dev/null 2>&1 && readlink -f / >/dev/null 2>&1; then
        readlink_available=1
    fi

    local has_files=0
    local is_docs=1

    while IFS= read -r file; do
        file=$(echo "$file" | tr -d '\r')
        [ -z "$file" ] && continue
        file="${file#./}"
        has_files=1

        # F1: Symlink resolution
        if [ -L "$file" ] || [ -h "$file" ]; then
            if [ "$readlink_available" -eq 0 ]; then
                # readlink -f not available => classify build (deny-by-default)
                is_docs=0
                break
            fi

            local target
            target=$(readlink -f "$file" 2>/dev/null || true)
            if [ -z "$target" ] || [ ! -e "$target" ]; then
                # Broken or unresolvable symlink => build (deny-by-default)
                is_docs=0
                break
            fi

            # Guard against escaping the repo boundary
            if [[ "$target" != "$repo_root"/* ]] && [ "$target" != "$repo_root" ]; then
                is_docs=0
                break
            fi

            if [ -d "$target" ]; then
                local dir_files
                dir_files=$(find "$target" \! -type d 2>/dev/null || true)
                if [ -z "$dir_files" ]; then
                    # Empty target directory => build (deny-by-default)
                    is_docs=0
                    break
                fi
                while IFS= read -r subfile; do
                    [ -z "$subfile" ] && continue
                    local rel_sub="${subfile#$repo_root/}"
                    if ! is_prose_path "$rel_sub"; then
                        is_docs=0
                        break 2
                    fi
                done <<< "$dir_files"
            else
                local rel_target="${target#$repo_root/}"
                if ! is_prose_path "$rel_target"; then
                    is_docs=0
                    break
                fi
            fi
        else
            if ! is_prose_path "$file"; then
                is_docs=0
                break
            fi
        fi
    done <<< "$files"

    # Docs-only iff EVERY changed path matches docs set. Empty diff => build (deny-by-default).
    if [ "$has_files" -eq 1 ] && [ "$is_docs" -eq 1 ]; then
        echo "lane=docs"
    else
        echo "lane=build"
    fi
}

result=$(detect_lane "${1:-origin/main}" "${2:-}" 2>/dev/null || echo "lane=build")
if [ "$result" = "lane=docs" ]; then
    echo "lane=docs"
else
    echo "lane=build"
fi
exit 0
