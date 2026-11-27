#!/usr/bin/env bash
#
# tools/artifact_validate.sh — Post-GoReleaser artifact validation gate (#1022 / audit M-02)
#
# The release workflow's verify/build jobs test binaries that are NOT the
# GoReleaser-published ones. This gate binds the published artifacts to the
# verified build by validating dist/ BEFORE the release becomes public:
#
#   1. Assert dist/ contains the expected archive set derived from the
#      platform matrix in .goreleaser.yaml (naming pattern, universal
#      binaries, format overrides) — and nothing unexpected.
#   2. Re-compute SHA-256 for every entry in checksums.txt and match it.
#   3. Assert the embedded version of every archived binary (ldflags
#      -X main.Version, read via `go version -m`) equals the release tag;
#      for native targets additionally execute `<binary> version`.
#   4. Fail CLOSED with a typed [E_*] message on ANY mismatch/absence.
#
# Usage:
#   bash tools/artifact_validate.sh --dist dist --tag v0.27.1 [--config .goreleaser.yaml]
#   bash tools/artifact_validate.sh --dist dist --tag v0.27.1 \
#        --archives "g8s_0.27.1_linux_amd64.tar.gz g8s_0.27.1_windows_amd64.zip ..."
#
# Expected archives are derived from --config (preferred; mirrors the
# GoReleaser archive pipeline: builds × goarch, universal_binaries replace,
# name_template + formats + format_overrides — the same parsing approach as
# tools/release_matrix_check.sh) or supplied explicitly via --archives.
# Non-archive extras (deb/rpm/apk, SBOMs, sigstore bundles, metadata.json)
# are allowed but, when listed in checksums.txt, checksum verified.
#
# Exit codes: 0 = validated; 1 = typed validation failure (fail-closed).

set -euo pipefail

# ---------------------------------------------------------------------------
# Typed failure reporting (fail-closed)
# ---------------------------------------------------------------------------
fail() {
    local code="$1"
    local msg="$2"
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
        echo "::error::[${code}] ${msg}" >&2
    else
        echo "[${code}] ${msg}" >&2
    fi
    exit 1
}

usage() {
    cat >&2 <<'EOF'
usage: artifact_validate.sh --dist <dir> --tag <vX.Y.Z> [--config <goreleaser.yaml>]
                            [--archives "n1 n2 ..."] [--binary <name>]
EOF
}

# ---------------------------------------------------------------------------
# Arguments (fail closed on malformed invocations)
# ---------------------------------------------------------------------------
DIST=""
TAG=""
CONFIG=""
ARCHIVES_EXPLICIT=""
BINARY_OVERRIDE=""

while [ $# -gt 0 ]; do
    case "$1" in
        --dist)      [ $# -ge 2 ] || fail E_USAGE "--dist requires a value"; DIST="$2"; shift 2 ;;
        --tag)       [ $# -ge 2 ] || fail E_USAGE "--tag requires a value"; TAG="$2"; shift 2 ;;
        --config|-c) [ $# -ge 2 ] || fail E_USAGE "--config requires a value"; CONFIG="$2"; shift 2 ;;
        --archives)  [ $# -ge 2 ] || fail E_USAGE "--archives requires a value"; ARCHIVES_EXPLICIT="$2"; shift 2 ;;
        --binary)    [ $# -ge 2 ] || fail E_USAGE "--binary requires a value"; BINARY_OVERRIDE="$2"; shift 2 ;;
        -h|--help)   usage; exit 0 ;;
        *)           usage; fail E_USAGE "unknown argument: $1" ;;
    esac
done

[ -n "$DIST" ] || { usage; fail E_USAGE "missing --dist"; }
[ -n "$TAG" ]  || { usage; fail E_USAGE "missing --tag"; }
[ -d "$DIST" ] || fail E_DIST_NOT_FOUND "dist directory not found: ${DIST}"

# GoReleaser renders {{ .Version }} as the tag without the leading 'v'.
VERSION="${TAG#v}"
[ -n "$VERSION" ] || fail E_BAD_TAG "empty version derived from tag '${TAG}'"

# Tools required by the gate (present on GitHub runners and Go checkouts).
command -v go    >/dev/null 2>&1 || fail E_TOOL_MISSING "go toolchain required for version binding (go version -m)"
command -v tar   >/dev/null 2>&1 || fail E_TOOL_MISSING "tar required for archive extraction"
command -v unzip >/dev/null 2>&1 || fail E_TOOL_MISSING "unzip required for archive extraction"

# ---------------------------------------------------------------------------
# SHA-256 helper (same portability pattern as tools/ci_fetch.sh)
# ---------------------------------------------------------------------------
sha256_of() {
    local file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    else
        fail E_TOOL_MISSING "no SHA-256 tool available (need sha256sum or shasum)"
    fi
}

# ---------------------------------------------------------------------------
# Expected archive derivation from .goreleaser.yaml (awk; mirrors the
# GoReleaser archive pipeline subset used by this repository — the same
# parsing approach as tools/release_matrix_check.sh).
#
# Output lines (TAB-separated):
#   A<TAB><archive-filename><TAB><goos><TAB><goarch|all>
#   M<TAB><project_name><TAB><binary_name>
# ---------------------------------------------------------------------------
derive_expected_archives() {
    local config="$1"
    [ -f "$config" ] || fail E_CONFIG_NOT_FOUND "goreleaser config not found: ${config}"
    # The awk program is fed through a quoted heredoc so no shell quoting
    # interpretation applies to the program text itself.
    local prog derived
    prog="$(mktemp "${TMPDIR:-/tmp}/artifact_validate_awk_XXXXXX")"
    cat > "$prog" <<'AWK_PROG'
        function norm_tmpl(s) {
            gsub(/\{\{[[:space:]]*/, "{{", s)
            gsub(/[[:space:]]*\}\}/, "}}", s)
            return s
        }
        function render(t, os, arch,   r) {
            r = norm_tmpl(t)
            gsub(/\{\{\.ProjectName\}\}/, proj, r)
            gsub(/\{\{\.Version\}\}/, version, r)
            gsub(/\{\{\.Tag\}\}/, tag, r)
            gsub(/\{\{\.Os\}\}/, os, r)
            gsub(/\{\{\.Arch\}\}/, arch, r)
            if (r ~ /\{\{/) return ""   # unresolvable template variable
            return r
        }
        BEGIN {
            in_section = ""
            build_count = 0; in_build_goos = 0; in_build_goarch = 0
            ub_count = 0
            archive_count = 0; in_archive_formats = 0; in_archive_builds = 0
            in_format_override = 0
            proj = ""
        }
        # Top-level YAML sections
        /^[a-zA-Z0-9_]+:/ {
            split($0, parts, ":")
            in_section = parts[1]
            in_build_goos = 0; in_build_goarch = 0
            in_archive_formats = 0; in_archive_builds = 0; in_format_override = 0
            if (in_section == "project_name") {
                sub(/^project_name:[[:space:]]*/, "", $0)
                gsub(/["']/, "", $0)
                proj = $0
            }
            next
        }
        # builds section
        in_section == "builds" {
            if (/^[[:space:]]{2}-[[:space:]]/) {
                build_count++
                in_build_goos = 0; in_build_goarch = 0
                b_goos_cnt[build_count] = 0; b_goarch_cnt[build_count] = 0
                b_id[build_count] = ""; b_bin[build_count] = ""
                next
            }
            if (/^[[:space:]]+id:[[:space:]]*/) {
                line = $0; sub(/.*id:[[:space:]]*/, "", line); gsub(/["']/, "", line)
                b_id[build_count] = line; next
            }
            if (/^[[:space:]]+binary:[[:space:]]*/) {
                line = $0; sub(/.*binary:[[:space:]]*/, "", line); gsub(/["']/, "", line)
                b_bin[build_count] = line; next
            }
            if (/^[[:space:]]+goos:/) {
                sub(/^[[:space:]]+goos:[[:space:]]*/, "", $0)
                if ($0 ~ /^\[.*\]$/) {
                    gsub(/[\[\]"']/, "", $0)
                    n = split($0, arr, ",")
                    for (i = 1; i <= n; i++) {
                        if (arr[i] == "") continue
                        b_goos_cnt[build_count]++
                        b_goos[build_count, b_goos_cnt[build_count]] = arr[i]
                    }
                    in_build_goos = 0
                } else if ($0 != "") {
                    gsub(/["']/, "", $0)
                    b_goos_cnt[build_count]++; b_goos[build_count, b_goos_cnt[build_count]] = $0
                    in_build_goos = 0
                } else { in_build_goos = 1; in_build_goarch = 0 }
                next
            }
            if (in_build_goos && /^[[:space:]]+-[[:space:]]/) {
                sub(/^[[:space:]]+-[[:space:]]*/, "", $0); gsub(/["']/, "", $0)
                if ($0 != "") { b_goos_cnt[build_count]++; b_goos[build_count, b_goos_cnt[build_count]] = $0 }
                next
            } else if (in_build_goos && /^[[:space:]]+[a-zA-Z0-9_]+:/) {
                in_build_goos = 0
            }
            if (/^[[:space:]]+goarch:/) {
                sub(/^[[:space:]]+goarch:[[:space:]]*/, "", $0)
                if ($0 ~ /^\[.*\]$/) {
                    gsub(/[\[\]"']/, "", $0)
                    n = split($0, arr, ",")
                    for (i = 1; i <= n; i++) {
                        if (arr[i] == "") continue
                        b_goarch_cnt[build_count]++
                        b_goarch[build_count, b_goarch_cnt[build_count]] = arr[i]
                    }
                    in_build_goarch = 0
                } else if ($0 != "") {
                    gsub(/["']/, "", $0)
                    b_goarch_cnt[build_count]++; b_goarch[build_count, b_goarch_cnt[build_count]] = $0
                    in_build_goarch = 0
                } else { in_build_goarch = 1; in_build_goos = 0 }
                next
            }
            if (in_build_goarch && /^[[:space:]]+-[[:space:]]/) {
                sub(/^[[:space:]]+-[[:space:]]*/, "", $0); gsub(/["']/, "", $0)
                if ($0 != "") { b_goarch_cnt[build_count]++; b_goarch[build_count, b_goarch_cnt[build_count]] = $0 }
                next
            } else if (in_build_goarch && /^[[:space:]]+[a-zA-Z0-9_]+:/) {
                in_build_goarch = 0
            }
        }
        # universal_binaries section
        in_section == "universal_binaries" {
            if (/^[[:space:]]{2}-[[:space:]]/) {
                ub_count++; ub_id[ub_count] = ""; ub_replace[ub_count] = 0
                next
            }
            if (/^[[:space:]]+id:[[:space:]]*/) {
                line = $0; sub(/.*id:[[:space:]]*/, "", line); gsub(/["']/, "", line)
                ub_id[ub_count] = line; next
            }
            if (/^[[:space:]]+replace:[[:space:]]*true/) { ub_replace[ub_count] = 1; next }
        }
        # archives section
        in_section == "archives" {
            if (/^[[:space:]]{2}-[[:space:]]/) {
                archive_count++
                arch_id[archive_count] = ""; arch_tmpl[archive_count] = ""
                arch_fmt_cnt[archive_count] = 0; arch_fo_cnt[archive_count] = 0
                arch_bld_cnt[archive_count] = 0
                in_format_override = 0; in_archive_formats = 0; in_archive_builds = 0
                next
            }
            if (/^[[:space:]]+id:[[:space:]]*/) {
                line = $0; sub(/.*id:[[:space:]]*/, "", line); gsub(/["']/, "", line)
                arch_id[archive_count] = line; next
            }
            if (/^[[:space:]]+name_template:/) {
                sub(/^[[:space:]]+name_template:[[:space:]]*/, "", $0)
                gsub(/^["']+|["']+$/, "", $0)
                arch_tmpl[archive_count] = $0; next
            }
            if (/^[[:space:]]+(builds|ids):/) {
                sub(/^[[:space:]]+(builds|ids):[[:space:]]*/, "", $0)
                if ($0 ~ /^\[.*\]$/) {
                    gsub(/[\[\]"']/, "", $0)
                    n = split($0, arr, ",")
                    for (i = 1; i <= n; i++) {
                        if (arr[i] == "") continue
                        arch_bld_cnt[archive_count]++
                        arch_bld[archive_count, arch_bld_cnt[archive_count]] = arr[i]
                    }
                    in_archive_builds = 0
                } else if ($0 != "") {
                    gsub(/["']/, "", $0)
                    arch_bld_cnt[archive_count]++; arch_bld[archive_count, arch_bld_cnt[archive_count]] = $0
                    in_archive_builds = 0
                } else { in_archive_builds = 1 }
                next
            }
            if (in_archive_builds && /^[[:space:]]+-[[:space:]]/) {
                sub(/^[[:space:]]+-[[:space:]]*/, "", $0); gsub(/["']/, "", $0)
                if ($0 != "") { arch_bld_cnt[archive_count]++; arch_bld[archive_count, arch_bld_cnt[archive_count]] = $0 }
                next
            } else if (in_archive_builds && /^[[:space:]]+[a-zA-Z0-9_]+:/) {
                in_archive_builds = 0
            }
            if (/^[[:space:]]+formats:/) {
                sub(/^[[:space:]]+formats:[[:space:]]*/, "", $0)
                if ($0 ~ /^\[.*\]$/) {
                    gsub(/[\[\]"']/, "", $0)
                    n = split($0, arr, ",")
                    for (i = 1; i <= n; i++) {
                        if (arr[i] == "") continue
                        if (in_format_override) {
                            fo_idx = arch_fo_cnt[archive_count]
                            arch_fo_fmt_cnt[archive_count, fo_idx]++
                            arch_fo_fmt[archive_count, fo_idx, arch_fo_fmt_cnt[archive_count, fo_idx]] = arr[i]
                        } else {
                            arch_fmt_cnt[archive_count]++
                            arch_fmt[archive_count, arch_fmt_cnt[archive_count]] = arr[i]
                        }
                    }
                    in_archive_formats = 0
                } else if ($0 != "") {
                    gsub(/["']/, "", $0)
                    if (in_format_override) {
                        fo_idx = arch_fo_cnt[archive_count]
                        arch_fo_fmt_cnt[archive_count, fo_idx]++
                        arch_fo_fmt[archive_count, fo_idx, arch_fo_fmt_cnt[archive_count, fo_idx]] = $0
                    } else {
                        arch_fmt_cnt[archive_count]++
                        arch_fmt[archive_count, arch_fmt_cnt[archive_count]] = $0
                    }
                    in_archive_formats = 0
                } else { in_archive_formats = 1 }
                next
            }
            if (in_archive_formats && /^[[:space:]]+-[[:space:]]/) {
                sub(/^[[:space:]]+-[[:space:]]*/, "", $0); gsub(/["']/, "", $0)
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
            if (/^[[:space:]]+format_overrides:/) { in_format_override = 1; in_archive_formats = 0; next }
            # Unanchored like tools/release_matrix_check.sh: override entries
            # are written as `- goos: windows` inside a block list.
            if (in_format_override && /goos:/) {
                sub(/.*goos:[[:space:]]*/, "", $0); gsub(/["']/, "", $0)
                arch_fo_cnt[archive_count]++
                fo_idx = arch_fo_cnt[archive_count]
                arch_fo_goos[archive_count, fo_idx] = $0
                arch_fo_fmt_cnt[archive_count, fo_idx] = 0
                next
            } else if (in_format_override && /^[[:space:]]+[a-zA-Z0-9_]+:/) {
                in_format_override = 0
            }
        }
        END {
            if (build_count == 0) { print "E_NO_BUILDS: no builds[] parsed from config" > "/dev/stderr"; exit 3 }
            if (proj == "") proj = "g8s"
            # Binary name: first build's explicit `binary:` or the project name.
            bin_name = proj
            for (b = 1; b <= build_count; b++) if (b_bin[b] != "") { bin_name = b_bin[b]; break }

            # Targets from builds
            target_count = 0
            for (b = 1; b <= build_count; b++) {
                for (o = 1; o <= b_goos_cnt[b]; o++) {
                    for (a = 1; a <= b_goarch_cnt[b]; a++) {
                        target_count++
                        tgt_os[target_count] = b_goos[b, o]
                        tgt_arch[target_count] = b_goarch[b, a]
                        tgt_build[target_count] = b_id[b]
                    }
                }
            }
            # universal_binaries with replace: true — collapse the first OS
            # (darwin in this repo, matching tools/release_matrix_check.sh)
            # into <os>/all, keeping all other targets untouched.
            for (u = 1; u <= ub_count; u++) {
                if (!ub_replace[u]) continue
                new_tc = 0; collapsed_os = ""
                for (t = 1; t <= target_count; t++) {
                    if (tgt_os[t] == collapsed_os) continue
                    if (tgt_os[t] == "darwin" && collapsed_os == "") {
                        collapsed_os = tgt_os[t]
                        new_tc++
                        new_os[new_tc] = tgt_os[t]; new_arch[new_tc] = "all"; new_bld[new_tc] = tgt_build[t]
                    } else {
                        new_tc++
                        new_os[new_tc] = tgt_os[t]; new_arch[new_tc] = tgt_arch[t]; new_bld[new_tc] = tgt_build[t]
                    }
                }
                target_count = new_tc
                for (t = 1; t <= target_count; t++) {
                    tgt_os[t] = new_os[t]; tgt_arch[t] = new_arch[t]; tgt_build[t] = new_bld[t]
                }
            }
            if (archive_count == 0) {
                archive_count = 1
                arch_tmpl[1] = "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
                arch_fmt_cnt[1] = 1; arch_fmt[1, 1] = "tar.gz"
            }
            # Expected archive filenames
            exp_count = 0
            for (ar = 1; ar <= archive_count; ar++) {
                tmpl = arch_tmpl[ar]
                if (tmpl == "") tmpl = "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
                if (arch_fmt_cnt[ar] == 0) { arch_fmt_cnt[ar] = 1; arch_fmt[ar, 1] = "tar.gz" }
                for (t = 1; t <= target_count; t++) {
                    os_val = tgt_os[t]; arch_val = tgt_arch[t]; bld_val = tgt_build[t]
                    if (arch_bld_cnt[ar] > 0) {
                        bld_matched = 0
                        for (ab = 1; ab <= arch_bld_cnt[ar]; ab++)
                            if (arch_bld[ar, ab] == bld_val) { bld_matched = 1; break }
                        if (!bld_matched) continue
                    }
                    exts_count = 0
                    for (fo = 1; fo <= arch_fo_cnt[ar]; fo++) {
                        if (arch_fo_goos[ar, fo] == os_val) {
                            for (f = 1; f <= arch_fo_fmt_cnt[ar, fo]; f++) {
                                exts_count++
                                ext[exts_count] = arch_fo_fmt[ar, fo, f]
                            }
                        }
                    }
                    if (exts_count == 0) {
                        for (f = 1; f <= arch_fmt_cnt[ar]; f++) {
                            exts_count++
                            ext[exts_count] = arch_fmt[ar, f]
                        }
                    }
                    for (e = 1; e <= exts_count; e++) {
                        base = render(tmpl, os_val, arch_val)
                        if (base == "") {
                            print "E_UNRESOLVED_TEMPLATE: cannot render archive name_template: " tmpl > "/dev/stderr"
                            exit 3
                        }
                        fname = base "." ext[e]
                        if (!seen_exp[fname]) {
                            seen_exp[fname] = 1
                            exp_count++
                            printf "A\t%s\t%s\t%s\n", fname, os_val, arch_val
                        }
                    }
                }
            }
            if (exp_count == 0) { print "E_NO_EXPECTED_ARCHIVES: derivation produced zero archives" > "/dev/stderr"; exit 3 }
            printf "M\t%s\t%s\n", proj, bin_name
        }
AWK_PROG
    if ! derived="$(awk -v version="$VERSION" -v tag="$TAG" -f "$prog" "$config" 2>&1)"; then
        rm -f "$prog"
        printf '%s\n' "$derived" >&2
        exit 1
    fi
    rm -f "$prog"
    printf '%s\n' "$derived"
}
# ---------------------------------------------------------------------------
# Load expected archive set (config-derived or explicit)
# ---------------------------------------------------------------------------
BIN_META=""
if [ -n "$ARCHIVES_EXPLICIT" ]; then
    EXPECTED_LIST=""
    for a in ${ARCHIVES_EXPLICIT}; do
        EXPECTED_LIST="${EXPECTED_LIST}${a}"$'\n'
    done
    PROJECT_NAME="g8s"
else
    if [ -z "$CONFIG" ]; then
        REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
        CONFIG="$REPO_ROOT/.goreleaser.yaml"
    fi
    DERIVED="$(derive_expected_archives "$CONFIG")"
    EXPECTED_LIST=""
    PROJECT_NAME=""
    while IFS=$'\t' read -r kind name os; do
        if [ "$kind" = "A" ]; then
            EXPECTED_LIST="${EXPECTED_LIST}${name}"$'\n'
        elif [ "$kind" = "M" ]; then
            PROJECT_NAME="$name"
            BIN_META="$os"
        fi
    done <<EOF2
${DERIVED}
EOF2
    [ -n "$PROJECT_NAME" ] || PROJECT_NAME="g8s"
fi
BINARY_NAME="${BINARY_OVERRIDE:-${BIN_META:-$PROJECT_NAME}}"

EXPECTED_NAMES="$(printf '%s' "$EXPECTED_LIST" | grep -v '^$' || true)"
[ -n "$EXPECTED_NAMES" ] || fail E_NO_EXPECTED_ARCHIVES "expected archive set is empty"
EXPECTED_COUNT="$(printf '%s' "$EXPECTED_NAMES" | grep -c '^' || true)"

echo "==> Artifact validation gate (#1022 / audit M-02)"
echo "    dist    : ${DIST}"
echo "    tag     : ${TAG}"
echo "    version : ${VERSION}"
echo "    binary  : ${BINARY_NAME}[.exe]"
echo "    expected archives (${EXPECTED_COUNT}):"
printf '%s\n' "$EXPECTED_NAMES" | sed 's/^/      - /'

# ---------------------------------------------------------------------------
# 1. Every expected archive must be present in dist/
# ---------------------------------------------------------------------------
MISSING=0
while IFS= read -r name; do
    [ -n "$name" ] || continue
    if [ ! -f "${DIST}/${name}" ]; then
        echo "[E_MISSING_ARCHIVE] expected artifact missing from ${DIST}: ${name}" >&2
        MISSING=$((MISSING + 1))
    fi
done <<EOF3
${EXPECTED_NAMES}
EOF3
[ "$MISSING" -eq 0 ] || fail E_MISSING_ARCHIVE "${MISSING} expected archive(s) missing from dist (see above)"

# ---------------------------------------------------------------------------
# 2. No unexpected tar.gz/tgz/zip archives in dist/ (set equality on the
#    platform-matrix archive types; deb/rpm/apk/SBOM/bundle extras allowed)
# ---------------------------------------------------------------------------
while IFS= read -r f; do
    [ -n "$f" ] || continue
    base="$(basename "$f")"
    if ! printf '%s\n' "$EXPECTED_NAMES" | grep -Fxq "$base"; then
        fail E_UNEXPECTED_ARCHIVE "unexpected archive in ${DIST} (not in the expected platform matrix set): ${base}"
    fi
done < <(find "$DIST" -maxdepth 1 -type f \( -name '*.tar.gz' -o -name '*.tgz' -o -name '*.zip' \) | sort)

# ---------------------------------------------------------------------------
# 3. checksums.txt: re-compute SHA-256 of every listed artifact and match
# ---------------------------------------------------------------------------
CHECKSUMS_FILE="${DIST}/checksums.txt"
[ -f "$CHECKSUMS_FILE" ] || fail E_MISSING_CHECKSUMS "dist/checksums.txt not found — GoReleaser checksum pipeline did not run"

CHECK_NAMES=""
CHECK_FAILS=0
while IFS= read -r line; do
    [ -n "$line" ] || continue
    sha="${line%%  *}"
    name="${line#*  }"
    name="${name## }"
    if ! printf '%s' "$sha" | grep -Eq '^[a-fA-F0-9]{64}$'; then
        fail E_CHECKSUM_MALFORMED "checksums.txt entry does not start with a 64-hex SHA-256: '${line}'"
    fi
    if [ ! -f "${DIST}/${name}" ]; then
        echo "[E_CHECKSUM_FILE_MISSING] checksums.txt lists a file absent from dist: ${name}" >&2
        CHECK_FAILS=$((CHECK_FAILS + 1))
        continue
    fi
    actual="$(sha256_of "${DIST}/${name}")"
    if [ "${actual}" != "$(printf '%s' "$sha" | tr 'A-F' 'a-f')" ]; then
        echo "[E_CHECKSUM_MISMATCH] ${name}" >&2
        echo "    expected: ${sha}" >&2
        echo "    actual  : ${actual}" >&2
        CHECK_FAILS=$((CHECK_FAILS + 1))
        continue
    fi
    echo "    sha256 ok: ${name}"
    CHECK_NAMES="${CHECK_NAMES}${name}"$'\n'
done < "$CHECKSUMS_FILE"
[ "$CHECK_FAILS" -eq 0 ] || fail E_CHECKSUM_MISMATCH "${CHECK_FAILS} checksum mismatch(es)/missing file(s) in checksums.txt (see above)"

# Every expected archive must be checksum-covered (binds the validated set
# to the cosign-signed checksums.txt subject).
while IFS= read -r name; do
    [ -n "$name" ] || continue
    if ! printf '%s' "$CHECK_NAMES" | grep -Fxq "$name"; then
        fail E_ARCHIVE_NOT_IN_CHECKSUMS "expected archive has no checksums.txt entry: ${name}"
    fi
done <<EOF4
${EXPECTED_NAMES}
EOF4

# ---------------------------------------------------------------------------
# 4. Embedded version binding: extract every archive, read the binary's
#    ldflags -X main.Version via `go version -m`; execute <binary> version
#    when the target matches the host platform.
# ---------------------------------------------------------------------------
HOST_OS="$(go env GOOS)"
HOST_ARCH="$(go env GOARCH)"

extract_archive() {
    local archive="$1"
    local dest="$2"
    case "$archive" in
        *.tar.gz|*.tgz) tar -xzf "$archive" -C "$dest" ;;
        *.zip)          unzip -q "$archive" -d "$dest" ;;
        *) fail E_EXTRACT_FAILED "unsupported archive format for version binding: ${archive}" ;;
    esac
}

VER_RE="$(printf '%s' "$VERSION" | sed 's/[.]/[.]/g')"

while IFS= read -r name; do
    [ -n "$name" ] || continue

    # Resolve the target os/arch of this archive (absent in --archives mode).
    arch_os=""; arch_arch=""
    if [ -n "${DERIVED:-}" ]; then
        while IFS=$'\t' read -r kind aname aos aarch; do
            if [ "$kind" = "A" ] && [ "$aname" = "$name" ]; then
                arch_os="$aos"; arch_arch="$aarch"
                break
            fi
        done <<EOF5
${DERIVED}
EOF5
    fi

    T="$(mktemp -d "${TMPDIR:-/tmp}/artifact_validate_XXXXXX")"
    if ! extract_archive "${DIST}/${name}" "$T"; then
        rm -rf "$T"
        fail E_EXTRACT_FAILED "cannot extract archive (corrupt or truncated): ${name}"
    fi

    BIN_PATH="$(find "$T" -type f \( -name "$BINARY_NAME" -o -name "${BINARY_NAME}.exe" \) -print -quit)"
    [ -n "$BIN_PATH" ] || { rm -rf "$T"; fail E_BINARY_MISSING "archive contains no ${BINARY_NAME}[.exe] binary: ${name}"; }

    if ! BUILD_INFO="$(go version -m "$BIN_PATH" 2>/dev/null)"; then
        rm -rf "$T"
        fail E_NO_BUILD_INFO "binary in ${name} has no Go build info (not a Go binary or corrupted)"
    fi
    EMBEDDED="$(printf '%s\n' "$BUILD_INFO" | grep -oE '\-X main\.Version=[^" ]+' | head -n1 | cut -d= -f2- || true)"
    if [ -z "$EMBEDDED" ]; then
        rm -rf "$T"
        fail E_VERSION_MISSING "binary in ${name} does not embed -X main.Version via ldflags"
    fi
    if [ "$EMBEDDED" != "$VERSION" ]; then
        rm -rf "$T"
        fail E_VERSION_MISMATCH "embedded version in ${name} is '${EMBEDDED}', expected '${VERSION}' (tag ${TAG})"
    fi

    # Strongest binding for native targets: execute the archived binary.
    if [ -n "$arch_os" ] && [ "$arch_os" = "$HOST_OS" ] && { [ "$arch_arch" = "$HOST_ARCH" ] || [ "$arch_arch" = "all" ]; }; then
        chmod +x "$BIN_PATH" 2>/dev/null || true
        if ! RUN_OUT="$("$BIN_PATH" version 2>&1)"; then
            rm -rf "$T"
            fail E_NATIVE_RUN_FAILED "archived binary refused to run: ${name}"
        fi
        if ! printf '%s\n' "$RUN_OUT" | grep -Eq "version ${VER_RE}([[:space:]]|$)"; then
            rm -rf "$T"
            fail E_NATIVE_VERSION_MISMATCH "archived binary '${name}' printed unexpected version output: $(printf '%s' "$RUN_OUT" | head -n1)"
        fi
        echo "    version ok (embedded + executed): ${name} -> ${EMBEDDED}"
    else
        echo "    version ok (embedded): ${name} -> ${EMBEDDED}"
    fi
    rm -rf "$T"
done <<EOF6
${EXPECTED_NAMES}
EOF6

echo "==> [GATE PASS] dist/ validated: ${EXPECTED_COUNT} archives present, checksums verified, embedded version == ${VERSION}"
