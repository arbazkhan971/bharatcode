#!/usr/bin/env sh
# BharatCode installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.sh | sh
#
# Downloads the prebuilt binary for this OS/arch from the latest GitHub release
# (or a specific version via BHARATCODE_VERSION / --version) and installs it to
# a bin directory on PATH. No build toolchain required.
#
# Environment:
#   BHARATCODE_VERSION   release tag to install (e.g. v0.2.0); default: latest
#   BHARATCODE_INSTALL_DIR  install directory; default: $HOME/.local/bin
set -eu

REPO="arbazkhan971/bharatcode"
BINARY="bharatcode"
INSTALL_DIR="${BHARATCODE_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${BHARATCODE_VERSION:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --dir) INSTALL_DIR="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 1 ;;
  esac
done

err() { echo "bharatcode-install: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"; }

need uname
need tar
if command -v curl >/dev/null 2>&1; then DL="curl -fsSL -o"; DLO="curl -fsSL";
elif command -v wget >/dev/null 2>&1; then DL="wget -qO"; DLO="wget -qO-";
else err "need curl or wget"; fi

# Map uname to the GoReleaser asset tokens: {Os}_{Arch}.
os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Darwin) OS_TOKEN="Darwin" ;;
  Linux)  OS_TOKEN="Linux" ;;
  *) err "unsupported OS: $os (use the npm install or build from source)" ;;
esac
case "$arch" in
  x86_64|amd64) ARCH_TOKEN="x86_64" ;;
  arm64|aarch64) ARCH_TOKEN="arm64" ;;
  *) err "unsupported architecture: $arch" ;;
esac

# Resolve the latest tag if none was requested.
if [ -z "$VERSION" ]; then
  VERSION="$($DLO "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -1 | cut -d '"' -f 4)"
  [ -n "$VERSION" ] || err "could not determine latest release; set BHARATCODE_VERSION"
fi

ASSET="${BINARY}_${OS_TOKEN}_${ARCH_TOKEN}.tar.gz"
URL="https://github.com/$REPO/releases/download/$VERSION/$ASSET"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $BINARY $VERSION ($OS_TOKEN/$ARCH_TOKEN)..."
# shellcheck disable=SC2086
$DL "$tmp/$ASSET" "$URL" || err "download failed: $URL"

tar -xzf "$tmp/$ASSET" -C "$tmp" || err "extract failed"
[ -f "$tmp/$BINARY" ] || err "archive did not contain $BINARY"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/$BINARY" "$INSTALL_DIR/$BINARY" 2>/dev/null \
  || { cp "$tmp/$BINARY" "$INSTALL_DIR/$BINARY" && chmod 0755 "$INSTALL_DIR/$BINARY"; }

echo "Installed $BINARY to $INSTALL_DIR/$BINARY"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "Note: $INSTALL_DIR is not on your PATH. Add it, e.g.:"
     echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.profile && . ~/.profile" ;;
esac
"$INSTALL_DIR/$BINARY" version || true
