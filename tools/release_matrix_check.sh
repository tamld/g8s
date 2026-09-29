#!/usr/bin/env bash
#
# release_matrix_check.sh — Verify release header download matrix matches GoReleaser archives
#
# Recurrence guard for #433 / #338:
# Fails (exit 1) if any filename in the release header table does not match
# the set of archives the goreleaser config actually produces.
#
# Usage:
#   bash tools/release_matrix_check.sh [--config <path>]
#   bash tools/release_matrix_check.sh [<path>]
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CONFIG=""
while [ $# -gt 0 ]; do
    case "$1" in
        --config|-c)
            CONFIG="$2"
            shift 2
            ;;
        *)
            if [ -z "$CONFIG" ]; then
                CONFIG="$1"
                shift
            else
                echo "ERROR: Unknown argument: $1" >&2
                exit 1
            fi
            ;;
    esac
done

if [ -z "$CONFIG" ]; then
    CONFIG="$REPO_ROOT/.goreleaser.yaml"
fi

if [ ! -f "$CONFIG" ]; then
    echo "ERROR: Config file not found: $CONFIG" >&2
    exit 1
fi

awk '
function norm_tmpl(s) {
    gsub(/\{\{[[:space:]]*/, "{{", s)
    gsub(/[[:space:]]*\}\}/, "}}", s)
    gsub(/\{\{\.Tag\}\}/, "{{.Version}}", s)
    return s
}

function match_asset(t, e) {
    if (norm_tmpl(t) == norm_tmpl(e)) return 1
    pattern = norm_tmpl(e)
    sub(/\{\{\.Version\}\}/, "___VERSION_PLACEHOLDER___", pattern)
    gsub(/\./, "\\.", pattern)
    sub(/___VERSION_PLACEHOLDER___/, "(v?[0-9][a-zA-Z0-9_.-]*|\\{\\{\\.Version\\}\\})", pattern)
    pattern = "^" pattern "$"
    if (norm_tmpl(t) ~ pattern) return 1
    return 0
}

BEGIN {
    in_section = ""
    build_count = 0
    in_build_goos = 0
    in_build_goarch = 0
    in_archive_formats = 0
    in_archive_builds = 0
    in_format_override = 0
    archive_count = 0
    ub_count = 0
    in_header = 0
    table_row_count = 0
    proj = ""
}

# Top-level YAML sections
/^[a-zA-Z0-9_]+:/ {
    split($0, parts, ":")
    top_key = parts[1]
    in_section = top_key
    in_build_goos = 0
    in_build_goarch = 0
    in_archive_formats = 0
    in_archive_builds = 0
    in_format_override = 0
    in_header = 0

    if (top_key == "project_name") {
        sub(/^project_name:[[:space:]]*/, "", $0)
        gsub(/["'\'' ]/, "", $0)
        proj = $0
    }
    next
}

# builds section
in_section == "builds" {
    if (/^[[:space:]]{2}-[[:space:]]/) {
        build_count++
        in_build_goos = 0
        in_build_goarch = 0
        b_goos_cnt[build_count] = 0
        b_goarch_cnt[build_count] = 0
        b_id[build_count] = ""
        if ($0 ~ /id:[[:space:]]*/) {
            line = $0
            sub(/.*id:[[:space:]]*/, "", line)
            gsub(/["'\'' ]/, "", line)
            b_id[build_count] = line
        }
        next
    }
    if (/^[[:space:]]+id:[[:space:]]*/) {
        line = $0
        sub(/.*id:[[:space:]]*/, "", line)
        gsub(/["'\'' ]/, "", line)
        b_id[build_count] = line
        next
    }
    if (/^[[:space:]]+goos:/) {
        sub(/^[[:space:]]+goos:[[:space:]]*/, "", $0)
        if ($0 ~ /^\[.*\]$/) {
            gsub(/[\[\]"'\'' ]/, "", $0)
            n = split($0, arr, ",")
            for (i=1; i<=n; i++) {
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
            for (i=1; i<=n; i++) {
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

# universal_binaries section
in_section == "universal_binaries" {
    if (/^[[:space:]]{2}-[[:space:]]/) {
        ub_count++
        ub_id[ub_count] = ""
        ub_replace[ub_count] = 0
        if ($0 ~ /id:[[:space:]]*/) {
            line = $0
            sub(/.*id:[[:space:]]*/, "", line)
            gsub(/["'\'' ]/, "", line)
            ub_id[ub_count] = line
        }
        next
    }
    if (/^[[:space:]]+id:[[:space:]]*/) {
        line = $0
        sub(/.*id:[[:space:]]*/, "", line)
        gsub(/["'\'' ]/, "", line)
        ub_id[ub_count] = line
        next
    }
    if (/^[[:space:]]+replace:[[:space:]]*true/) {
        ub_replace[ub_count] = 1
        next
    }
}

# archives section
in_section == "archives" {
    if (/^[[:space:]]{2}-[[:space:]]/) {
        archive_count++
        arch_id[archive_count] = ""
        arch_tmpl[archive_count] = ""
        arch_fmt_cnt[archive_count] = 0
        arch_fo_cnt[archive_count] = 0
        arch_bld_cnt[archive_count] = 0
        in_format_override = 0
        in_archive_formats = 0
        in_archive_builds = 0
        if ($0 ~ /id:[[:space:]]*/) {
            line = $0
            sub(/.*id:[[:space:]]*/, "", line)
            gsub(/["'\'' ]/, "", line)
            arch_id[archive_count] = line
        }
        next
    }
    if (/^[[:space:]]+id:[[:space:]]*/) {
        line = $0
        sub(/.*id:[[:space:]]*/, "", line)
        gsub(/["'\'' ]/, "", line)
        arch_id[archive_count] = line
        next
    }
    if (/^[[:space:]]+name_template:/) {
        sub(/^[[:space:]]+name_template:[[:space:]]*/, "", $0)
        gsub(/^["'\'' ]+|["'\'' ]+$/, "", $0)
        arch_tmpl[archive_count] = $0
        next
    }
    if (/^[[:space:]]+(builds|ids):/) {
        sub(/^[[:space:]]+(builds|ids):[[:space:]]*/, "", $0)
        if ($0 ~ /^\[.*\]$/) {
            gsub(/[\[\]"'\'' ]/, "", $0)
            n = split($0, arr, ",")
            for (i=1; i<=n; i++) {
                if (arr[i] != "") {
                    arch_bld_cnt[archive_count]++
                    arch_bld[archive_count, arch_bld_cnt[archive_count]] = arr[i]
                }
            }
            in_archive_builds = 0
        } else if ($0 != "") {
            gsub(/["'\'' ]/, "", $0)
            arch_bld_cnt[archive_count]++
            arch_bld[archive_count, 1] = $0
            in_archive_builds = 0
        } else {
            in_archive_builds = 1
        }
        next
    }
    if (in_archive_builds && /^[[:space:]]+-[[:space:]]+/) {
        sub(/^[[:space:]]+-[[:space:]]*/, "", $0)
        gsub(/["'\'' ]/, "", $0)
        if ($0 != "") {
            arch_bld_cnt[archive_count]++
            arch_bld[archive_count, arch_bld_cnt[archive_count]] = $0
        }
        next
    } else if (in_archive_builds && /^[[:space:]]+[a-zA-Z0-9_]+:/) {
        in_archive_builds = 0
    }

    if (/^[[:space:]]+formats:/) {
        sub(/^[[:space:]]+formats:[[:space:]]*/, "", $0)
        if ($0 ~ /^\[.*\]$/) {
            gsub(/[\[\]"'\'' ]/, "", $0)
            n = split($0, arr, ",")
            for (i=1; i<=n; i++) {
                if (arr[i] != "") {
                    if (in_format_override) {
                        fo_idx = arch_fo_cnt[archive_count]
                        arch_fo_fmt_cnt[archive_count, fo_idx]++
                        arch_fo_fmt[archive_count, fo_idx, arch_fo_fmt_cnt[archive_count, fo_idx]] = arr[i]
                    } else {
                        arch_fmt_cnt[archive_count]++
                        arch_fmt[archive_count, arch_fmt_cnt[archive_count]] = arr[i]
                    }
                }
            }
            in_archive_formats = 0
        } else if ($0 != "") {
            gsub(/["'\'' ]/, "", $0)
            if (in_format_override) {
                fo_idx = arch_fo_cnt[archive_count]
                arch_fo_fmt_cnt[archive_count, fo_idx]++
                arch_fo_fmt[archive_count, fo_idx, 1] = $0
            } else {
                arch_fmt_cnt[archive_count]++
                arch_fmt[archive_count, 1] = $0
            }
            in_archive_formats = 0
        } else {
            in_archive_formats = 1
        }
        next
    }
    if (in_archive_formats && /^[[:space:]]+-[[:space:]]+/) {
        sub(/^[[:space:]]+-[[:space:]]*/, "", $0)
        gsub(/["'\'' ]/, "", $0)
        if ($0 != "") {
            if (in_format_override) {
                fo_idx = arch_fo_cnt[archive_count]
                arch_fo_fmt_cnt[archive_count, fo_idx]++
                arch_fo_fmt[archive_count, fo_idx, arch_fo_fmt_cnt[archive_count, fo_idx]] = $0
            } else {
                arch_fmt_cnt[archive_count]++
                arch_fmt[archive_count, arch_fmt_cnt[archive_count]] = $0
            }
        }
        next
    } else if (in_archive_formats && /^[[:space:]]+[a-zA-Z0-9_]+:/) {
        in_archive_formats = 0
    }

    if (/^[[:space:]]+format_overrides:/) {
        in_format_override = 1
        in_archive_formats = 0
        next
    }
    if (in_format_override && /goos:/) {
        sub(/.*goos:[[:space:]]*/, "", $0)
        gsub(/["'\'' ]/, "", $0)
        arch_fo_cnt[archive_count]++
        fo_idx = arch_fo_cnt[archive_count]
        arch_fo_goos[archive_count, fo_idx] = $0
        arch_fo_fmt_cnt[archive_count, fo_idx] = 0
        next
    }
}

# release section
in_section == "release" {
    if (/^[[:space:]]{2}header:[[:space:]]*\|/) {
        in_header = 1
        next
    }
    if (in_header && /^[[:space:]]{2}[a-zA-Z0-9_]+:/) {
        in_header = 0
        next
    }
    if (in_header && /\|/) {
        if ($0 ~ /:---/ || $0 ~ /Platform/ || $0 ~ /Architecture/ || $0 ~ /Binary \/ Archive/) next
        split($0, cols, "|")
        orig_row = $0
        gsub(/^[ \t]+|[ \t]+$/, "", orig_row)
        for (i = 1; i <= length(cols); i++) {
            col_text = cols[i]
            while (match(col_text, /`?[a-zA-Z0-9_{}. -]+\.(tar\.gz|zip|tgz|tar\.xz|tar\.bz2)`?/)) {
                val = substr(col_text, RSTART, RLENGTH)
                col_text = substr(col_text, RSTART + RLENGTH)
                gsub(/`/, "", val)
                gsub(/^[ \t]+|[ \t]+$/, "", val)
                if (val != "") {
                    table_row_count++
                    table_asset[table_row_count] = val
                    table_row[table_row_count] = orig_row
                }
            }
        }
    }
}

END {
    # 1. Collect targets from builds
    target_count = 0
    for (b = 1; b <= build_count; b++) {
        for (o = 1; o <= b_goos_cnt[b]; o++) {
            goos = b_goos[b, o]
            for (a = 1; a <= b_goarch_cnt[b]; a++) {
                goarch = b_goarch[b, a]
                target_count++
                tgt_os[target_count] = goos
                tgt_arch[target_count] = goarch
                tgt_build[target_count] = b_id[b]
            }
        }
    }

    # 2. Universal binaries replacement / addition
    for (u = 1; u <= ub_count; u++) {
        if (ub_replace[u]) {
            new_tc = 0
            has_darwin_all = 0
            for (t = 1; t <= target_count; t++) {
                if (tgt_os[t] == "darwin") {
                    if (!has_darwin_all) {
                        new_tc++
                        new_tgt_os[new_tc] = "darwin"
                        new_tgt_arch[new_tc] = "all"
                        new_tgt_build[new_tc] = ub_id[u]
                        has_darwin_all = 1
                    }
                } else {
                    new_tc++
                    new_tgt_os[new_tc] = tgt_os[t]
                    new_tgt_arch[new_tc] = tgt_arch[t]
                    new_tgt_build[new_tc] = tgt_build[t]
                }
            }
            target_count = new_tc
            for (t = 1; t <= target_count; t++) {
                tgt_os[t] = new_tgt_os[t]
                tgt_arch[t] = new_tgt_arch[t]
                tgt_build[t] = new_tgt_build[t]
            }
        } else {
            target_count++
            tgt_os[target_count] = "darwin"
            tgt_arch[target_count] = "all"
            tgt_build[target_count] = ub_id[u]
        }
    }

    # Default archive if none defined
    if (archive_count == 0) {
        archive_count = 1
        arch_id[1] = "default"
        arch_tmpl[1] = "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
        arch_fmt_cnt[1] = 1
        arch_fmt[1, 1] = "tar.gz"
        arch_fo_cnt[1] = 0
        arch_bld_cnt[1] = 0
    }

    # 3. Generate expected archive filenames
    exp_count = 0
    for (ar = 1; ar <= archive_count; ar++) {
        tmpl = arch_tmpl[ar]
        if (tmpl == "") {
            tmpl = "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
        }
        if (arch_fmt_cnt[ar] == 0) {
            arch_fmt_cnt[ar] = 1
            arch_fmt[ar, 1] = "tar.gz"
        }

        for (t = 1; t <= target_count; t++) {
            os_val = tgt_os[t]
            arch_val = tgt_arch[t]
            bld_val = tgt_build[t]

            if (arch_bld_cnt[ar] > 0) {
                bld_matched = 0
                for (ab = 1; ab <= arch_bld_cnt[ar]; ab++) {
                    if (arch_bld[ar, ab] == bld_val || arch_bld[ar, ab] == arch_id[ar]) {
                        bld_matched = 1
                        break
                    }
                }
                if (!bld_matched) continue
            }

            if (arch_id[ar] ~ /^darwin/ && os_val != "darwin") continue
            if (arch_id[ar] ~ /^linux/ && os_val != "linux") continue
            if (arch_id[ar] ~ /^windows/ && os_val != "windows") continue

            fmt_found = 0
            for (fo = 1; fo <= arch_fo_cnt[ar]; fo++) {
                if (arch_fo_goos[ar, fo] == os_val) {
                    for (f = 1; f <= arch_fo_fmt_cnt[ar, fo]; f++) {
                        cfmt = arch_fo_fmt[ar, fo, f]
                        sub_tmpl = tmpl
                        gsub(/\{\{[[:space:]]*\.ProjectName[[:space:]]*\}\}/, proj, sub_tmpl)
                        gsub(/\{\{[[:space:]]*\.Os[[:space:]]*\}\}/, os_val, sub_tmpl)
                        gsub(/\{\{[[:space:]]*\.Arch[[:space:]]*\}\}/, arch_val, sub_tmpl)
                        asset_name = sub_tmpl "." cfmt
                        if (!seen_exp[asset_name]) {
                            seen_exp[asset_name] = 1
                            exp_count++
                            exp_asset[exp_count] = asset_name
                        }
                        fmt_found = 1
                    }
                }
            }
            if (!fmt_found) {
                for (f = 1; f <= arch_fmt_cnt[ar]; f++) {
                    cfmt = arch_fmt[ar, f]
                    sub_tmpl = tmpl
                    gsub(/\{\{[[:space:]]*\.ProjectName[[:space:]]*\}\}/, proj, sub_tmpl)
                    gsub(/\{\{[[:space:]]*\.Os[[:space:]]*\}\}/, os_val, sub_tmpl)
                    gsub(/\{\{[[:space:]]*\.Arch[[:space:]]*\}\}/, arch_val, sub_tmpl)
                    asset_name = sub_tmpl "." cfmt
                    if (!seen_exp[asset_name]) {
                        seen_exp[asset_name] = 1
                        exp_count++
                        exp_asset[exp_count] = asset_name
                    }
                }
            }
        }
    }

    if (table_row_count == 0) {
        print "  [FAIL] No release header matrix table found in config: " FILENAME
        exit 1
    }

    # 4. Check each advertised table asset against expected archives
    errors = 0
    for (tr = 1; tr <= table_row_count; tr++) {
        t_asset = table_asset[tr]
        matched = 0
        for (e = 1; e <= exp_count; e++) {
            if (match_asset(t_asset, exp_asset[e])) {
                matched = 1
                break
            }
        }
        if (!matched) {
            print "  [FAIL] Release header table advertises asset not produced by archives: " t_asset
            print "         Offending row: " table_row[tr]
            errors++
        }
    }

    if (errors > 0) {
        print ""
        print "Release matrix check failed: " errors " unavailable asset(s) advertised."
        exit 1
    } else {
        print "  [PASS] All " table_row_count " release matrix assets match GoReleaser archive configuration."
        exit 0
    }
}
' "$CONFIG"
