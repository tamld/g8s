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
#
# Output:
#   stdout: exactly "lane=docs" or "lane=build"
#   exit:   0 always (detection never breaks a run)
#

set -uo pipefail

detect_lane() {
    local base="${1:-origin/main}"
    local files=""

    # 1. Try git diff against base ref (merge-base ... HEAD)
    if git rev-parse --verify "$base" >/dev/null 2>&1; then
        files=$(git diff --name-only "$base"...HEAD 2>/dev/null || git diff --name-only "$base" HEAD 2>/dev/null || true)
    fi

    # 2. Fallback when the branch has no upstream diff: staged + unstaged files
    if [ -z "$files" ]; then
        local staged unstaged
        staged=$(git diff --name-only --cached 2>/dev/null || true)
        unstaged=$(git diff --name-only 2>/dev/null || true)
        files=$(printf "%s\n%s\n" "$staged" "$unstaged" | sed '/^[[:space:]]*$/d' | sort -u)
    fi

    local has_files=0
    local is_docs=1

    while IFS= read -r file; do
        file=$(echo "$file" | tr -d '\r')
        [ -z "$file" ] && continue
        file="${file#./}"
        has_files=1

        # Docs-only set: *.md, docs/*, docs/**, plans/**, skills/**, assets/**, offer/**
        if [[ "$file" == *.md ]] || \
           [[ "$file" == docs/* ]] || [[ "$file" == docs ]] || \
           [[ "$file" == plans/* ]] || [[ "$file" == plans ]] || \
           [[ "$file" == skills/* ]] || [[ "$file" == skills ]] || \
           [[ "$file" == assets/* ]] || [[ "$file" == assets ]] || \
           [[ "$file" == offer/* ]] || [[ "$file" == offer ]]; then
            : # matched docs set
        else
            is_docs=0
            break
        fi
    done <<< "$files"

    # Docs-only iff EVERY changed path matches docs set. Empty diff => build (deny-by-default).
    if [ "$has_files" -eq 1 ] && [ "$is_docs" -eq 1 ]; then
        echo "lane=docs"
    else
        echo "lane=build"
    fi
}

result=$(detect_lane "${1:-origin/main}" 2>/dev/null || echo "lane=build")
if [ "$result" = "lane=docs" ]; then
    echo "lane=docs"
else
    echo "lane=build"
fi
exit 0
