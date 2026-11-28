#!/usr/bin/env bash
#
# tools/winget_manifest_check.sh — fail-closed guard for the winget installer
# manifest (D-9, #986).
#
# Mirrors the chocolatey install-time throw in
# packaging/chocolatey/tools/chocolateyinstall.ps1: an installer manifest whose
# checksum still carries the '<from checksums.txt>' placeholder must never go
# out the door. Winget manifests are static YAML — nothing executes at install
# time — so the gate runs at release/shipping time instead:
#   - tools/pre_tag.sh invokes this script structurally (no placeholder, no
#     garbage sha, version-pinned URL) before a release tag is cut.
#   - When actually submitting the manifest to winget-pkgs, run with
#     --checksums-file <checksums.txt> or --verify-remote to also prove the
#     sha matches the pinned release artifact byte-for-byte.
#
# Usage:
#   tools/winget_manifest_check.sh [--manifest <path>] [--checksums-file <path>] [--verify-remote]
#
# Options:
#   --manifest <path>       Manifest to validate (default: packaging/winget/manifest.yaml)
#   --checksums-file <path> Local checksums.txt to verify InstallerSha256 against
#   --verify-remote         Fetch checksums.txt for the pinned version from the
#                           GitHub release derived from InstallerUrl and verify
#                           the sha. Fetch failure fails closed.
#
# Exit codes:
#   0 = gate passed
#   1 = gate violated (placeholder, malformed sha, version drift, checksum mismatch)
#   2 = usage / file error

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
log_ok() { echo -e "${GREEN}[OK]${NC} $*"; }

PLACEHOLDER='<from checksums.txt>'
DEFAULT_MANIFEST="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/packaging/winget/manifest.yaml"

MANIFEST=""
CHECKSUMS_FILE=""
VERIFY_REMOTE=0

usage() {
    cat <<'EOF'
tools/winget_manifest_check.sh — fail-closed guard for the winget installer manifest (D-9, #986)

Usage:
  tools/winget_manifest_check.sh [--manifest <path>] [--checksums-file <path>] [--verify-remote]

Options:
  --manifest <path>       Manifest to validate (default: packaging/winget/manifest.yaml)
  --checksums-file <path> Local checksums.txt to verify InstallerSha256 against
  --verify-remote         Fetch checksums.txt for the pinned version from the GitHub
                          release derived from InstallerUrl; fetch failure fails closed

Exit codes:
  0 = gate passed
  1 = gate violated (placeholder, malformed sha, version drift, checksum mismatch)
  2 = usage / file error
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --manifest)
            [ $# -lt 2 ] && { log_error "--manifest requires a path"; exit 2; }
            MANIFEST="$2"; shift 2 ;;
        --checksums-file)
            [ $# -lt 2 ] && { log_error "--checksums-file requires a path"; exit 2; }
            CHECKSUMS_FILE="$2"; shift 2 ;;
        --verify-remote)
            VERIFY_REMOTE=1; shift ;;
        --help|-h)
            usage; exit 0 ;;
        *)
            log_error "unknown argument: $1"; exit 2 ;;
    esac
done

[ -z "$MANIFEST" ] && MANIFEST="$DEFAULT_MANIFEST"

if [ ! -f "$MANIFEST" ]; then
    log_error "winget manifest not found: $MANIFEST"
    exit 2
fi

fail() {
    log_error "$1"
    log_error "winget manifest gate FAILED: $MANIFEST is not shippable (D-9 #986)"
    exit 1
}

# --- extract fields (first value wins; full-line comments ignored) ----------
PACKAGE_VERSION=$(grep -E '^[[:space:]]*PackageVersion:' "$MANIFEST" | head -1 | sed -E 's/^[[:space:]]*PackageVersion:[[:space:]]*//' | tr -d '"' || true)
if [ -z "$PACKAGE_VERSION" ]; then
    fail "cannot extract PackageVersion from manifest"
fi

INSTALLER_URLS=$(grep -E '^[[:space:]]*InstallerUrl:' "$MANIFEST" | sed -E 's/^[[:space:]]*InstallerUrl:[[:space:]]*//' | tr -d '"' || true)
if [ -z "$INSTALLER_URLS" ]; then
    fail "cannot extract InstallerUrl from manifest"
fi

INSTALLER_SHAS=$(grep -E '^[[:space:]]*InstallerSha256:' "$MANIFEST" | sed -E 's/^[[:space:]]*InstallerSha256:[[:space:]]*//' | tr -d '"' || true)
if [ -z "$INSTALLER_SHAS" ]; then
    fail "manifest has no InstallerSha256 field — winget cannot verify artifact integrity"
fi

# --- gate 1: no checksum placeholder (mirror of the chocolatey throw) -------
if printf '%s\n' "$INSTALLER_SHAS" | grep -qF "$PLACEHOLDER"; then
    fail "Chocolatey-style checksum placeholder '$PLACEHOLDER' must be replaced from release checksums.txt before the winget manifest ships"
fi

# --- gate 2: every InstallerSha256 is a well-formed SHA-256 ------------------
while IFS= read -r sha; do
    [ -z "$sha" ] && fail "empty InstallerSha256 value"
    if ! printf '%s' "$sha" | grep -Eq '^[0-9a-fA-F]{64}$'; then
        fail "InstallerSha256 '$sha' is not a 64-hex-character SHA-256"
    fi
done <<< "$INSTALLER_SHAS"

# --- gate 3: InstallerUrl pinned to PackageVersion (stale-pin drift) --------
while IFS= read -r url; do
    [ -z "$url" ] && fail "empty InstallerUrl value"
    case "$url" in
        *"/v${PACKAGE_VERSION}/"*) ;;
        *) fail "InstallerUrl '$url' is not pinned to PackageVersion ${PACKAGE_VERSION} (expected '/v${PACKAGE_VERSION}/' in path)" ;;
    esac
    case "$url" in
        *"g8s_${PACKAGE_VERSION}_"*) ;;
        *) fail "InstallerUrl artifact '$url' does not match PackageVersion ${PACKAGE_VERSION} (expected 'g8s_${PACKAGE_VERSION}_' in filename)" ;;
    esac
done <<< "$INSTALLER_URLS"

# --- checksum verification (optional, shipping-time) ------------------------
resolve_artifact_name() {
    # Last path segment of the URL (strip any query/fragment)
    printf '%s' "$1" | sed -E 's/[?#].*$//' | awk -F/ '{print $NF}'
}

verify_against_checksums() {
    local src_label="$1" src_path="$2"
    while IFS= read -r url; do
        local artifact expected
        artifact="$(resolve_artifact_name "$url")"
        expected=$(grep -E "(^|[[:space:]])${artifact}[[:space:]]*$" "$src_path" | head -1 | awk '{print $1}' || true)
        if [ -z "$expected" ]; then
            fail "checksums source ($src_label) has no entry for artifact '$artifact'"
        fi
        if ! printf '%s\n' "$INSTALLER_SHAS" | grep -qixF "$expected"; then
            fail "InstallerSha256 does not match $src_label entry for '$artifact' (expected $expected)"
        fi
        log_ok "InstallerSha256 matches $src_label entry for '$artifact'"
    done <<< "$INSTALLER_URLS"
}

if [ -n "$CHECKSUMS_FILE" ]; then
    if [ ! -f "$CHECKSUMS_FILE" ]; then
        log_error "checksums file not found: $CHECKSUMS_FILE"
        exit 2
    fi
    verify_against_checksums "--checksums-file" "$CHECKSUMS_FILE"
fi

if [ "$VERIFY_REMOTE" -eq 1 ]; then
    # Derive <owner>/<repo> from InstallerUrl; no hardcoded repo slug.
    FIRST_URL=$(printf '%s\n' "$INSTALLER_URLS" | head -1)
    SLUG=$(printf '%s' "$FIRST_URL" | sed -E 's|^https://github\.com/||' | cut -d/ -f1-2 || true)
    if ! printf '%s' "$SLUG" | grep -Eq '^[^/]+/[^/]+'; then
        fail "cannot derive GitHub owner/repo from InstallerUrl '$FIRST_URL'"
    fi
    TMP_CHECKSUMS="$(mktemp)"
    trap 'rm -f "$TMP_CHECKSUMS"' EXIT
    REMOTE_URL="https://github.com/${SLUG}/releases/download/v${PACKAGE_VERSION}/checksums.txt"
    if ! curl -fsSL --max-time 60 "$REMOTE_URL" -o "$TMP_CHECKSUMS"; then
        fail "cannot fetch checksums.txt for v${PACKAGE_VERSION} from ${REMOTE_URL} — refusing to pass the winget gate on an unverifiable checksum (fail-closed)"
    fi
    verify_against_checksums "remote checksums.txt ($REMOTE_URL)" "$TMP_CHECKSUMS"
fi

log_ok "winget manifest gate passed: $MANIFEST (v${PACKAGE_VERSION})"
exit 0
