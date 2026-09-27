#!/usr/bin/env bash
#
# ci_link_integrity.sh — G3 gate (S6-2, #420, ADR-0024 Layer 1)
#
# Scans README.md + docs/**/*.md (recursively) for markdown links `[text](target)`.
# ONLY relative targets are checked (resolved against the markdown file's own directory).
# External links (http/https), mailto:, and #anchor-only links are skipped.
# Any dead target causes an exit code 1 listing every dead link (file:line).
# Clean runs exit 0.
#
# Usage:
#   ci_link_integrity.sh [--root <dir>]
#

set -euo pipefail

ROOT_DIR="."

while [ $# -gt 0 ]; do
    case "$1" in
        --root)
            if [ $# -lt 2 ]; then
                echo "error: --root requires a directory argument" >&2
                exit 2
            fi
            ROOT_DIR="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [--root <dir>]"
            exit 0
            ;;
        *)
            echo "unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

export LC_ALL=C
ROOT_DIR="${ROOT_DIR%/}"
[ -z "$ROOT_DIR" ] && ROOT_DIR="."

FILES=()
if [ -f "$ROOT_DIR/README.md" ]; then
    FILES+=("$ROOT_DIR/README.md")
fi
if [ -d "$ROOT_DIR/docs" ]; then
    while IFS= read -r f; do
        [ -n "$f" ] && FILES+=("$f")
    done < <(find "$ROOT_DIR/docs" -type f -name "*.md" | sort)
fi

if [ ${#FILES[@]} -eq 0 ]; then
    echo "LINK INTEGRITY GATE: No markdown files found under $ROOT_DIR."
    exit 0
fi

FAIL=0
CHECKED=0

while IFS=$'\t' read -r file line_no raw_target path_part; do
    [ -n "$file" ] || continue
    CHECKED=$((CHECKED + 1))

    # Resolve target path
    case "$path_part" in
        /*)
            resolved="$ROOT_DIR/${path_part#/}"
            ;;
        *)
            file_dir="$(dirname "$file")"
            resolved="$file_dir/$path_part"
            ;;
    esac

    if [ ! -e "$resolved" ]; then
        echo "::error::link integrity: $file:$line_no dead relative link -> '$raw_target' (resolved: $resolved)"
        FAIL=$((FAIL + 1))
    fi
done < <(awk '
FNR == 1 {
    in_code_block = 0
}
{
    line = $0

    # Handle fenced code blocks (``` or ~~~)
    if (line ~ /^[[:space:]]*(```|~~~)/) {
        in_code_block = !in_code_block
        next
    }
    if (in_code_block) {
        next
    }

    # Strip inline code spans to avoid false positives inside backticks
    gsub(/`[^`]+`/, "", line)

    while (match(line, /\]\([^)]+\)/)) {
        m = substr(line, RSTART, RLENGTH)
        line = substr(line, RSTART + RLENGTH)

        # Strip leading ]( and trailing )
        sub(/^\]\(/, "", m)
        sub(/\)$/, "", m)

        # Trim leading and trailing whitespace
        sub(/^[[:space:]]+/, "", m)
        sub(/[[:space:]]+$/, "", m)

        # Strip optional title (e.g. "title" or '\''title'\'')
        sub(/[[:space:]]+["'\''].*$/, "", m)

        # Strip enclosing < >
        if (m ~ /^<.*>$/) {
            sub(/^</, "", m)
            sub(/>$/, "", m)
        }

        # Skip empty, http(s)://, mailto:, #anchor-only, or URI schemes
        if (m == "" || m ~ /^(https?:\/\/|mailto:|#|[a-zA-Z][a-zA-Z0-9+.-]*:\/\/)/) {
            continue
        }

        # Extract file path part (strip fragment and query parameters)
        path_part = m
        sub(/#.*$/, "", path_part)
        sub(/\?.*$/, "", path_part)

        if (path_part == "") {
            continue
        }

        # Decode URL-encoded spaces (%20)
        gsub(/%20/, " ", path_part)

        printf "%s\t%d\t%s\t%s\n", FILENAME, FNR, m, path_part
    }
}
' "${FILES[@]}")

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo "LINK INTEGRITY GATE: $FAIL dead link(s) found across ${#FILES[@]} file(s)."
    exit 1
fi

echo "LINK INTEGRITY GATE: OK ($CHECKED relative link(s) verified across ${#FILES[@]} file(s))."
exit 0
