#!/usr/bin/env bash
#
# ci_platform_matrix_check.sh — ADR-0040 / #832
# Machine-check README Installation platform claims vs .goreleaser.yaml build targets.
#
# Usage:
#   bash tools/ci_platform_matrix_check.sh [--goreleaser <path>] [--readme <path>]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GORELEASER="${REPO_ROOT}/.goreleaser.yaml"
README="${REPO_ROOT}/README.md"

while [ $# -gt 0 ]; do
    case "$1" in
        --goreleaser)
            GORELEASER="$2"
            shift 2
            ;;
        --readme)
            README="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [--goreleaser <path>] [--readme <path>]"
            exit 0
            ;;
        *)
            echo "ERROR: unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [ ! -f "$GORELEASER" ]; then
    echo "ERROR: goreleaser config not found: $GORELEASER" >&2
    exit 1
fi
if [ ! -f "$README" ]; then
    echo "ERROR: readme not found: $README" >&2
    exit 1
fi

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

goreleaser_targets="${scratch}/goreleaser_targets.txt"
readme_targets="${scratch}/readme_targets.txt"

awk '
BEGIN {
    in_section = ""
    build_count = 0
    in_build_goos = 0
    in_build_goarch = 0
    ub_darwin_replace = 0
}
/^[a-zA-Z0-9_]+:/ {
    split($0, parts, ":")
    top_key = parts[1]
    in_section = top_key
    in_build_goos = 0
    in_build_goarch = 0
    next
}
in_section == "builds" {
    if (/^[[:space:]]{2}-[[:space:]]/) {
        build_count++
        in_build_goos = 0
        in_build_goarch = 0
        b_goos_cnt[build_count] = 0
        b_goarch_cnt[build_count] = 0
        next
    }
    if (/^[[:space:]]+goos:/) {
        sub(/^[[:space:]]+goos:[[:space:]]*/, "", $0)
        if ($0 ~ /^\[.*\]$/) {
            gsub(/[\[\]"'\'' ]/, "", $0)
            n = split($0, arr, ",")
            for (i = 1; i <= n; i++) {
                if (arr[i] != "") {
                    b_goos_cnt[build_count]++
                    b_goos[build_count, b_goos_cnt[build_count]] = arr[i]
                }
            }
            in_build_goos = 0
        } else if ($0 != "") {
            gsub(/["'\'' ]/, "", $0)
            b_goos_cnt[build_count]++
            b_goos[build_count, b_goos_cnt[build_count]] = $0
            in_build_goos = 0
        } else {
            in_build_goos = 1
            in_build_goarch = 0
        }
        next
    }
    if (in_build_goos && /^[[:space:]]+-[[:space:]]+/) {
        sub(/^[[:space:]]+-[[:space:]]*/, "", $0)
        gsub(/["'\'' ]/, "", $0)
        if ($0 != "") {
            b_goos_cnt[build_count]++
            b_goos[build_count, b_goos_cnt[build_count]] = $0
        }
        next
    } else if (in_build_goos && /^[[:space:]]+[a-zA-Z0-9_]+:/) {
        in_build_goos = 0
    }
    if (/^[[:space:]]+goarch:/) {
        sub(/^[[:space:]]+goarch:[[:space:]]*/, "", $0)
        if ($0 ~ /^\[.*\]$/) {
            gsub(/[\[\]"'\'' ]/, "", $0)
            n = split($0, arr, ",")
            for (i = 1; i <= n; i++) {
                if (arr[i] != "") {
                    b_goarch_cnt[build_count]++
                    b_goarch[build_count, b_goarch_cnt[build_count]] = arr[i]
                }
            }
            in_build_goarch = 0
        } else if ($0 != "") {
            gsub(/["'\'' ]/, "", $0)
            b_goarch_cnt[build_count]++
            b_goarch[build_count, b_goarch_cnt[build_count]] = $0
            in_build_goarch = 0
        } else {
            in_build_goarch = 1
            in_build_goos = 0
        }
        next
    }
    if (in_build_goarch && /^[[:space:]]+-[[:space:]]+/) {
        sub(/^[[:space:]]+-[[:space:]]*/, "", $0)
        gsub(/["'\'' ]/, "", $0)
        if ($0 != "") {
            b_goarch_cnt[build_count]++
            b_goarch[build_count, b_goarch_cnt[build_count]] = $0
        }
        next
    } else if (in_build_goarch && /^[[:space:]]+[a-zA-Z0-9_]+:/) {
        in_build_goarch = 0
    }
}
in_section == "universal_binaries" {
    if (/^[[:space:]]+replace:[[:space:]]*true/) {
        ub_darwin_replace = 1
    }
}
END {
    for (b = 1; b <= build_count; b++) {
        for (o = 1; o <= b_goos_cnt[b]; o++) {
            goos = b_goos[b, o]
            for (a = 1; a <= b_goarch_cnt[b]; a++) {
                goarch = b_goarch[b, a]
                if (goos == "darwin" && goarch == "all") {
                    print "darwin amd64"
                    print "darwin arm64"
                } else {
                    print goos " " goarch
                }
            }
        }
    }
    if (build_count == 0) {
        print "ERROR: no builds: entries found" > "/dev/stderr"
        exit 1
    }
    # universal_binaries darwin_all: README documents per-arch darwin rows, not darwin/all.
    if (ub_darwin_replace) {
        # No-op: build targets already enumerate darwin amd64+arm64.
    }
}
' "$GORELEASER" | sort -u >"$goreleaser_targets"

awk '
function norm_arch(a) {
    if (a == "x86_64") return "amd64"
    if (a == "aarch64") return "arm64"
    return a
}
function add_claim(goos, goarch,    key) {
    goarch = norm_arch(goarch)
    if (goos == "" || goarch == "") return
    if (goarch != "amd64" && goarch != "arm64") return
    key = goos " " goarch
    if (!seen[key]) {
        seen[key] = 1
        print key
    }
}
function parse_os_tokens(line,    n, i, goos) {
    # Universal binaries and brace-expansion claims cover BOTH arches at once
    # (#831 layout: "macOS (universal) ... darwin_all", "linux_{amd64,arm64}").
    if (line ~ /darwin_all|[Uu]niversal|\{amd64,arm64\}/) {
        if (line ~ /[Mm]ac[Oo][Ss]|[Dd]arwin/) { add_claim("darwin", "amd64"); add_claim("darwin", "arm64") }
        if (line ~ /[Ll]inux/) { add_claim("linux", "amd64"); add_claim("linux", "arm64") }
        if (line ~ /[Ww]indows/) { add_claim("windows", "amd64"); add_claim("windows", "arm64") }
    }
    if (line ~ /[Ff]ree[Bb][Ss][Dd]/) add_claim("freebsd", "amd64")
    if (line ~ /[Mm]ac[Oo][Ss]|darwin/) {
        if (line ~ /Apple Silicon|aarch64|`arm64`|\/arm64| arm64/) add_claim("darwin", "arm64")
        if (line ~ /Intel|x86_64|`amd64`|\/amd64| amd64/) add_claim("darwin", "amd64")
        if (line ~ /arm64/) add_claim("darwin", "arm64")
        if (line ~ /amd64/) add_claim("darwin", "amd64")
    }
    if (line ~ /[Ll]inux/) {
        if (line ~ /arm64|aarch64/) add_claim("linux", "arm64")
        if (line ~ /amd64|x86_64/) add_claim("linux", "amd64")
    }
    if (line ~ /[Ww]indows/) {
        if (line ~ /arm64|aarch64/) add_claim("windows", "arm64")
        if (line ~ /amd64|x86_64/) add_claim("windows", "amd64")
    }
    while (match(line, /`?[a-z]+\/[a-z0-9_]+`?/)) {
        tok = substr(line, RSTART, RLENGTH)
        gsub(/`/, "", tok)
        n = split(tok, parts, "/")
        if (n >= 2) add_claim(parts[1], parts[2])
        line = substr(line, RSTART + RLENGTH)
    }
    while (match(line, /`[a-z0-9_]+`/)) {
        tok = substr(line, RSTART, RLENGTH)
        gsub(/`/, "", tok)
        if (tok == "amd64" || tok == "arm64") {
            if (prev_goos != "") add_claim(prev_goos, tok)
        }
        line = substr(line, RSTART + RLENGTH)
    }
}
BEGIN {
    in_install = 0
    prev_goos = ""
}
/^## 4\. Installation/ {
    in_install = 1
    next
}
in_install && /^## [0-9]+\./ {
    in_install = 0
}
in_install {
    if (/^\|/) {
        parse_os_tokens($0)
        if ($0 ~ /[Mm]ac[Oo][Ss]/) prev_goos = "darwin"
        else if ($0 ~ /[Ll]inux/) prev_goos = "linux"
        else if ($0 ~ /[Ww]indows/) prev_goos = "windows"
        next
    }
    if (/^-[[:space:]]/) {
        parse_os_tokens($0)
        if ($0 ~ /[Mm]ac[Oo][Ss]/) prev_goos = "darwin"
        else if ($0 ~ /[Ll]inux/) prev_goos = "linux"
        else if ($0 ~ /[Ww]indows/) prev_goos = "windows"
        next
    }
}
' "$README" | sort -u >"$readme_targets"

errors=0

while read -r goos goarch; do
    [ -z "$goos" ] && continue
    if grep -qxF "${goos} ${goarch}" "$readme_targets"; then
        echo "matrix ${goos}/${goarch}: README ok"
    else
        echo "matrix ${goos}/${goarch}: MISSING from README"
        errors=$((errors + 1))
    fi
done <"$goreleaser_targets"

while read -r goos goarch; do
    [ -z "$goos" ] && continue
    if grep -qxF "${goos} ${goarch}" "$goreleaser_targets"; then
        continue
    fi
    echo "matrix ${goos}/${goarch}: PHANTOM in README (not in goreleaser)"
    errors=$((errors + 1))
done <"$readme_targets"

if [ "$errors" -gt 0 ]; then
    echo ""
    echo "::error::Platform matrix check failed: ${errors} mismatch(es) between README and .goreleaser.yaml"
    exit 1
fi

echo "Platform matrix check: OK (README matches goreleaser build targets)"
exit 0
