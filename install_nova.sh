#!/bin/bash
# Installs the nova cli from GitHub releases.
#
#   ./install_nova.sh
#
# Optional env vars:
#   NOVA_VERSION  release to install (default: latest)
#   INSTALL_DIR   target directory (default: /usr/local/bin)
set -euo pipefail

REPO="wandelbotsgmbh/nova-cli"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Function to detect OS and architecture, mapped to the release asset names
detect_os_arch() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$ARCH" in
        x86_64|amd64)
            ARCH=amd64
            ;;
        arm64|aarch64)
            ARCH=arm64
            ;;
        *)
            echo "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    case "$OS" in
        linux)
            ;;
        darwin)
            OS=macos
            ;;
        *)
            echo "Unsupported OS: $OS (on Windows, download the zip from https://github.com/${REPO}/releases)"
            exit 1
            ;;
    esac
}

# Resolves "latest" via the releases/latest redirect (no API token / rate limit)
resolve_version() {
    if [ -n "${NOVA_VERSION:-}" ]; then
        VERSION="$NOVA_VERSION"
        return
    fi
    VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
    VERSION="${VERSION##*/}"
    if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
        echo "Could not determine the latest release of ${REPO}"
        exit 1
    fi
}

install_nova() {
    URL="https://github.com/${REPO}/releases/download/${VERSION}/novacli_${OS}_${ARCH}-${VERSION}.tar.gz"
    TMP=$(mktemp -d)
    trap 'rm -rf "$TMP"' EXIT

    echo "Downloading nova ${VERSION} from: $URL"
    curl -fL -o "$TMP/nova.tar.gz" "$URL"
    tar -xzf "$TMP/nova.tar.gz" -C "$TMP" nova
    chmod +x "$TMP/nova"

    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP/nova" "$INSTALL_DIR/nova"
    else
        sudo mv "$TMP/nova" "$INSTALL_DIR/nova"
    fi

    "$INSTALL_DIR/nova" version
    echo "Nova CLI has been installed successfully to $INSTALL_DIR/nova."
}

# Main script execution
detect_os_arch
resolve_version
install_nova
