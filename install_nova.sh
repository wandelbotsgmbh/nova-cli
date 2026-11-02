#!/bin/bash
# Installs the nova cli from GitHub releases (macOS/Linux).
#
#   curl -fsSL https://raw.githubusercontent.com/wandelbotsgmbh/nova-cli/main/install_nova.sh | bash
#
# Optional env vars:
#   NOVA_VERSION  release to install (default: latest stable release)
#   INSTALL_DIR   target directory (default: /usr/local/bin)
#
# Uses sudo only when INSTALL_DIR (or its parent) is not writable.
set -euo pipefail

REPO="wandelbotsgmbh/nova-cli"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detects OS and architecture, mapped to the release asset names
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
            echo "Unsupported architecture: $ARCH" >&2
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
            echo "Unsupported OS: $OS (on Windows, use winget or download the zip from https://github.com/${REPO}/releases)" >&2
            exit 1
            ;;
    esac
}

# Resolves "latest" via the releases/latest redirect (no API token / rate limit).
# Prereleases (x.y.z-rc.N) are never "latest".
resolve_version() {
    if [ -n "${NOVA_VERSION:-}" ]; then
        VERSION="${NOVA_VERSION#v}"
        return
    fi
    VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
    VERSION="${VERSION##*/}"
    if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
        echo "Could not determine the latest release of ${REPO}" >&2
        exit 1
    fi
}

# Runs a command directly if possible, otherwise via sudo (if available)
run_privileged() {
    if command -v sudo >/dev/null 2>&1; then
        sudo "$@"
    else
        echo "Cannot write to $INSTALL_DIR and sudo is not available. Set INSTALL_DIR to a writable directory, e.g.:" >&2
        echo "  INSTALL_DIR=\$HOME/.local/bin bash install_nova.sh" >&2
        exit 1
    fi
}

ensure_install_dir() {
    [ -d "$INSTALL_DIR" ] && return
    # create the directory as the current user when the closest existing parent is writable
    PARENT="$INSTALL_DIR"
    while [ ! -d "$PARENT" ]; do
        PARENT=$(dirname "$PARENT")
    done
    if [ -w "$PARENT" ]; then
        mkdir -p "$INSTALL_DIR"
    else
        run_privileged mkdir -p "$INSTALL_DIR"
    fi
}

install_nova() {
    URL="https://github.com/${REPO}/releases/download/${VERSION}/novacli_${OS}_${ARCH}-${VERSION}.tar.gz"
    TMP=$(mktemp -d)
    trap 'rm -rf "$TMP"' EXIT

    ensure_install_dir
    if [ ! -w "$INSTALL_DIR" ] && ! command -v sudo >/dev/null 2>&1; then
        run_privileged true  # prints the hint and exits
    fi

    echo "Downloading nova ${VERSION} from: $URL"
    curl -fL --progress-bar -o "$TMP/nova.tar.gz" "$URL"
    tar -xzf "$TMP/nova.tar.gz" -C "$TMP" nova

    # install(1) sets mode and owner (tar would keep the uid from the archive)
    if [ -w "$INSTALL_DIR" ]; then
        install -m 0755 "$TMP/nova" "$INSTALL_DIR/nova"
    else
        run_privileged install -m 0755 "$TMP/nova" "$INSTALL_DIR/nova"
    fi

    "$INSTALL_DIR/nova" version
    echo "Nova CLI has been installed successfully to $INSTALL_DIR/nova."

    case ":$PATH:" in
        *":$INSTALL_DIR:"*) ;;
        *)
            echo
            echo "Note: $INSTALL_DIR is not in your PATH. Add it, e.g.:"
            echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc   # or ~/.zshrc"
            ;;
    esac
}

detect_os_arch
resolve_version
install_nova
