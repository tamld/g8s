#!/usr/bin/env bash
#
# tools/binary_size_check.sh — machine binding for the README "<20 MiB per
# release architecture" binary-size claim (D-6, #986 / docs/claims.yml
# claim-binary-size-per-arch).
#
# Builds (or measures) the stripped release binary exactly like
# .goreleaser.yaml does (CGO_ENABLED=0, ldflags "-s -w") and enforces a STRICT
# per-architecture size bound: the binary must be strictly smaller than the
# bound, matching the "<20 MiB" wording.
#
# Usage:
#   tools/binary_size_check.sh                    # build + check the host platform target
#   tools/binary_size_check.sh --all-targets      # build + check every release target
#   tools/binary_size_check.sh --target linux/arm64
#   tools/binary_size_check.sh --binary <path>    # measure an existing binary (no build)
#
# Options:
#   --binary <path>    measure the given file instead of building (exit 2 if missing)
#   --target <os/arch> check one specific release target (cross-compiled deterministically
#                      via GOOS/GOARCH; CGO_ENABLED=0 pure Go, so any host can check any target)
#   --all-targets      check the full release matrix: darwin/amd64 darwin/arm64
#                      linux/amd64 linux/arm64 windows/amd64 windows/arm64
#   --limit-bytes <N>  override the bound (default: 20971520 = 20 MiB, or
#                      G8S_BINARY_SIZE_LIMIT_BYTES); binary must be STRICTLY smaller
#
# Exit codes:
#   0 = all measured binaries are within the bound
#   1 = bound violated or go build failed
#   2 = usage / file error

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
log_ok() { echo -e "${GREEN}[OK]${NC} $*"; }

DEFAULT_LIMIT_BYTES=20971520  # 20 MiB — the README/claims.yml bound
LIMIT_BYTES="${G8S_BINARY_SIZE_LIMIT_BYTES:-$DEFAULT_LIMIT_BYTES}"

# Release matrix — must mirror the `builds:` entries in .goreleaser.yaml.
RELEASE_TARGETS=(darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64)

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PACKAGE_PATH="./cmd/g8s"

MODE=""
TARGET=""
BINARY_PATH=""

usage() {
    sed -n '3,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

WORK_DIR="$(mktemp -d)"
trap 'chmod -R u+rwX "$WORK_DIR" 2>/dev/null; rm -rf "$WORK_DIR"' EXIT

while [ $# -gt 0 ]; do
    case "$1" in
        --binary)
            [ $# -lt 2 ] && { log_error "--binary requires a path"; exit 2; }
            BINARY_PATH="$2"; MODE="binary"; shift 2 ;;
        --target)
            [ $# -lt 2 ] && { log_error "--target requires os/arch"; exit 2; }
            TARGET="$2"; MODE="target"; shift 2 ;;
        --all-targets)
            MODE="all"; shift ;;
        --limit-bytes)
            [ $# -lt 2 ] && { log_error "--limit-bytes requires a number"; exit 2; }
            if ! printf '%s' "$2" | grep -Eq '^[0-9]+$'; then
                log_error "--limit-bytes must be a non-negative integer, got '$2'"
                exit 2
            fi
            LIMIT_BYTES="$2"; shift 2 ;;
        --help|-h)
            usage; exit 0 ;;
        *)
            log_error "unknown argument: $1"
            usage >&2
            exit 2 ;;
    esac
done

[ -z "$MODE" ] && MODE="host"

valid_target() {
    local t="$1" want_os want_arch
    want_os="${t%%/*}"
    want_arch="${t#*/}"
    for known in "${RELEASE_TARGETS[@]}"; do
        if [ "$known" = "$want_os/$want_arch" ]; then
            return 0
        fi
    done
    return 1
}

mib() {
    awk -v b="$1" 'BEGIN { printf "%.2f", b / 1048576 }'
}

check_size() {
    local label="$1" file="$2"
    local size
    if ! size=$(wc -c < "$file" | tr -d '[:space:]'); then
        log_error "cannot measure size of $file"
        return 1
    fi
    if [ "$size" -lt "$LIMIT_BYTES" ]; then
        log_ok "$label: $size bytes ($(mib "$size") MiB) < $LIMIT_BYTES bytes ($(mib "$LIMIT_BYTES") MiB) bound"
        return 0
    fi
    log_error "binary size bound exceeded: $label is $size bytes ($(mib "$size") MiB), bound is $LIMIT_BYTES bytes ($(mib "$LIMIT_BYTES") MiB) — binary must be strictly smaller (D-6 #986)"
    return 1
}

build_target() {
    local target="$1" out="$2"
    local os arch
    os="${target%%/*}"
    arch="${target#*/}"
    # Mirror .goreleaser.yaml build flags exactly: CGO_ENABLED=0, ldflags "-s -w"
    # (goreleaser also injects -X main.* version strings, which do not
    # materially change size).
    if ! (cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
            go build -ldflags "-s -w" -o "$out" "$PACKAGE_PATH"); then
        log_error "go build failed for target $target"
        return 1
    fi
}

FAILED=0

case "$MODE" in
    binary)
        if [ ! -f "$BINARY_PATH" ]; then
            log_error "binary file not found: $BINARY_PATH"
            exit 2
        fi
        check_size "$BINARY_PATH" "$BINARY_PATH" || FAILED=1
        ;;
    host)
        HOST_OS="$(go env GOHOSTOS)"
        HOST_ARCH="$(go env GOHOSTARCH)"
        if ! valid_target "$HOST_OS/$HOST_ARCH"; then
            log_error "host platform $HOST_OS/$HOST_ARCH is not a release target; use --target or --binary"
            exit 2
        fi
        TMP_BIN="$WORK_DIR/g8s"
        if ! build_target "$HOST_OS/$HOST_ARCH" "$TMP_BIN"; then
            exit 1
        fi
        check_size "$HOST_OS/$HOST_ARCH" "$TMP_BIN" || FAILED=1
        ;;
    target)
        if ! valid_target "$TARGET"; then
            log_error "unknown release target '$TARGET' — valid targets: ${RELEASE_TARGETS[*]}"
            exit 2
        fi
        TMP_BIN="$WORK_DIR/g8s"
        if ! build_target "$TARGET" "$TMP_BIN"; then
            exit 1
        fi
        check_size "$TARGET" "$TMP_BIN" || FAILED=1
        ;;
    all)
        TMP_BIN_DIR="$WORK_DIR"
        for target in "${RELEASE_TARGETS[@]}"; do
            TMP_BIN="$TMP_BIN_DIR/$(printf '%s' "$target" | tr '/' '-')"
            if ! build_target "$target" "$TMP_BIN"; then
                FAILED=1
                continue
            fi
            check_size "$target" "$TMP_BIN" || FAILED=1
        done
        ;;
esac

if [ "$FAILED" -ne 0 ]; then
    log_error "binary size gate FAILED — see docs/claims.yml claim-binary-size-per-arch"
    exit 1
fi
exit 0
