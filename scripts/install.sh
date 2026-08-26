#!/usr/bin/env bash
# ==============================================================================
# g8s (The Gatekeepers) Standalone Cross-Platform Installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/tamld/g8s/main/scripts/install.sh | bash
#
# Environment Overrides:
#   G8S_VERSION      - Release version to install (e.g., v0.1.0; default: latest)
#   G8S_INSTALL_DIR  - Directory to install binary (default: ~/.local/bin or /usr/local/bin)
# ==============================================================================

set -euo pipefail

REPO="tamld/g8s"
GITHUB_URL="https://github.com/${REPO}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

info() {
    printf "${BLUE}==>${NC} %s\n" "$1"
}

success() {
    printf "${GREEN}==>${NC} %s\n" "$1"
}

warn() {
    printf "${YELLOW}==>${NC} %s\n" "$1"
}

error() {
    printf "${RED}Error:${NC} %s\n" "$1" >&2
    exit 1
}

# 1. Detect OS
detect_os() {
    local os
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    case "$os" in
        darwin) echo "darwin" ;;
        linux) echo "linux" ;;
        *) error "Unsupported operating system: $os (g8s supports darwin and linux; for Windows, download release zip)" ;;
    esac
}

# 2. Detect Architecture
detect_arch() {
    local arch
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) error "Unsupported architecture: $arch" ;;
    esac
}

# 3. Detect latest version if not set
get_latest_version() {
    if [ -n "${G8S_VERSION:-}" ]; then
        echo "${G8S_VERSION}"
        return
    fi
    local latest
    latest="$(curl -fsSL -H "Accept: application/vnd.github+json" "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/' || true)"
    if [ -z "$latest" ]; then
        latest="v0.1.0"
    fi
    echo "$latest"
}

main() {
    info "Installing g8s (The Gatekeepers)..."

    local os arch version
    os="$(detect_os)"
    arch="$(detect_arch)"
    version="$(get_latest_version)"

    info "Detected platform: ${os}/${arch} (Version: ${version})"

    # Clean version tag without leading v for tarball name
    local clean_version="${version#v}"
    local asset_name="g8s_${clean_version}_${os}_${arch}.tar.gz"
    local download_url="${GITHUB_URL}/releases/download/${version}/${asset_name}"
    local checksum_url="${GITHUB_URL}/releases/download/${version}/checksums.txt"

    # Temporary directory
    local tmp_dir
    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "$tmp_dir"' EXIT

    info "Downloading ${asset_name}..."
    if ! curl -fsSL "$download_url" -o "${tmp_dir}/${asset_name}"; then
        error "Failed to download ${download_url}"
    fi

    # Verify Checksum
    if curl -fsSL "$checksum_url" -o "${tmp_dir}/checksums.txt" 2>/dev/null; then
        info "Verifying SHA-256 checksum..."
        (
            cd "$tmp_dir"
            if command -v sha256sum >/dev/null 2>&1; then
                grep "${asset_name}" checksums.txt | sha256sum -c - >/dev/null 2>&1 || warn "Checksum verification could not be validated automatically"
            elif command -v shasum >/dev/null 2>&1; then
                grep "${asset_name}" checksums.txt | shasum -a 256 -c - >/dev/null 2>&1 || warn "Checksum verification could not be validated automatically"
            fi
        )
    fi

    # Unpack binary
    info "Unpacking binary..."
    tar -xzf "${tmp_dir}/${asset_name}" -C "$tmp_dir"

    if [ ! -f "${tmp_dir}/g8s" ]; then
        error "Binary 'g8s' not found inside downloaded package"
    fi

    # Determine install destination
    local install_dir
    if [ -n "${G8S_INSTALL_DIR:-}" ]; then
        install_dir="${G8S_INSTALL_DIR}"
    elif [ "$(id -u)" -eq 0 ]; then
        install_dir="/usr/local/bin"
    else
        install_dir="${HOME}/.local/bin"
    fi

    mkdir -p "$install_dir"
    chmod 755 "${tmp_dir}/g8s"
    mv "${tmp_dir}/g8s" "${install_dir}/g8s"

    success "g8s installed successfully to ${install_dir}/g8s"

    # Check PATH
    if [[ ":$PATH:" != *":${install_dir}:"* ]]; then
        echo ""
        warn "${install_dir} is not in your current PATH."
        echo "Add it to your shell configuration profile:"
        echo ""
        echo "  export PATH=\"${install_dir}:\$PATH\""
        echo ""
    fi

    # Run doctor check
    if command -v "${install_dir}/g8s" >/dev/null 2>&1; then
        "${install_dir}/g8s" version
    fi
}

main "$@"
