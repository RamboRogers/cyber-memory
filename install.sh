#!/usr/bin/env sh
# cyber-memory installer
# https://github.com/RamboRogers/cyber-memory
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/RamboRogers/cyber-memory/master/install.sh | sh

set -e

REPO="RamboRogers/cyber-memory"
BINARY="cyber-memory"

# ---- detect platform ----

OS="$(uname -s 2>/dev/null || echo "windows")"
ARCH="$(uname -m 2>/dev/null || echo "unknown")"

case "$OS" in
  Darwin)
    case "$ARCH" in
      arm64)  ASSET="cyber-memory-darwin-arm64" ;;
      x86_64) ASSET="cyber-memory-darwin-amd64" ;;
      *)      die "Unsupported macOS architecture: $ARCH" ;;
    esac
    INSTALL_DIR="/usr/local/bin"
    ;;
  Linux)
    case "$ARCH" in
      x86_64)  ASSET="cyber-memory-linux-amd64" ;;
      aarch64) ASSET="cyber-memory-linux-arm64" ;;
      arm64)   ASSET="cyber-memory-linux-arm64" ;;
      *)       die "Unsupported Linux architecture: $ARCH" ;;
    esac
    INSTALL_DIR="/usr/local/bin"
    ;;
  MINGW*|MSYS*|CYGWIN*|Windows_NT)
    ASSET="cyber-memory-windows-amd64.exe"
    BINARY="cyber-memory.exe"
    INSTALL_DIR="$LOCALAPPDATA/cyber-memory"
    ;;
  *)
    die "Unsupported OS: $OS"
    ;;
esac

die() { echo "error: $1" >&2; exit 1; }

# ---- resolve latest release ----

LATEST_URL="https://api.github.com/repos/${REPO}/releases/latest"

if command -v curl >/dev/null 2>&1; then
  DOWNLOAD_URL="$(curl -fsSL "$LATEST_URL" | grep '"browser_download_url"' | grep "$ASSET\"" | head -1 | sed 's/.*"browser_download_url": "\(.*\)".*/\1/')"
elif command -v wget >/dev/null 2>&1; then
  DOWNLOAD_URL="$(wget -qO- "$LATEST_URL" | grep '"browser_download_url"' | grep "$ASSET\"" | head -1 | sed 's/.*"browser_download_url": "\(.*\)".*/\1/')"
else
  die "curl or wget is required"
fi

[ -z "$DOWNLOAD_URL" ] && die "Could not find release asset: $ASSET — check https://github.com/${REPO}/releases"

# ---- download ----

TMPFILE="$(mktemp)"
trap 'rm -f "$TMPFILE"' EXIT

echo ""
echo "  cyber-memory installer"
echo "  ----------------------"
echo "  Asset  : $ASSET"
echo "  URL    : $DOWNLOAD_URL"
echo "  Install: $INSTALL_DIR/$BINARY"
echo ""

if command -v curl >/dev/null 2>&1; then
  curl -fsSL --progress-bar "$DOWNLOAD_URL" -o "$TMPFILE"
else
  wget -q --show-progress "$DOWNLOAD_URL" -O "$TMPFILE"
fi

chmod +x "$TMPFILE"

# ---- install ----

if [ ! -d "$INSTALL_DIR" ]; then
  mkdir -p "$INSTALL_DIR" 2>/dev/null || { echo "  Creating $INSTALL_DIR requires sudo..."; sudo mkdir -p "$INSTALL_DIR"; }
fi

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMPFILE" "$INSTALL_DIR/$BINARY"
else
  echo "  Installing to $INSTALL_DIR requires sudo..."
  sudo mv "$TMPFILE" "$INSTALL_DIR/$BINARY"
fi

# ---- verify ----

if command -v "$BINARY" >/dev/null 2>&1 || [ -x "$INSTALL_DIR/$BINARY" ]; then
  VERSION="$("$INSTALL_DIR/$BINARY" --version 2>/dev/null || echo "unknown")"
  echo "  Installed: $VERSION"
else
  echo "  Installed to $INSTALL_DIR/$BINARY"
fi

# ---- print MCP config ----

cat <<'EOF'

  Done. Add to your MCP config (claude_desktop_config.json or equivalent):

  {
    "mcpServers": {
      "memory": {
        "command": "cyber-memory"
      }
    }
  }

  On first use, cyber-memory will download the embedding model (~300 MB).
  All data is stored locally in ~/.local/share/cyber-memory/

EOF

# Windows PATH hint
case "$OS" in
  MINGW*|MSYS*|CYGWIN*|Windows_NT)
    echo "  Windows: add $INSTALL_DIR to your PATH if it isn't already."
    echo ""
    ;;
esac
