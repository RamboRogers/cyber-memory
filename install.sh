#!/usr/bin/env sh
# cyber-memory installer
# https://github.com/RamboRogers/cyber-memory
#
# Supported: macOS arm64, Linux amd64
# Usage: curl -fsSL https://raw.githubusercontent.com/RamboRogers/cyber-memory/master/install.sh | sh

set -e

REPO="RamboRogers/cyber-memory"
INSTALL_DIR="/usr/local/bin"
BINARY="cyber-memory"

die() { echo "error: $1" >&2; exit 1; }

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS/$ARCH" in
  Darwin/arm64)  ASSET="cyber-memory-darwin-arm64" ;;
  Linux/x86_64)  ASSET="cyber-memory-linux-amd64" ;;
  Darwin/x86_64) die "macOS Intel (x86_64) is not yet supported. See https://github.com/$REPO for updates." ;;
  Linux/aarch64) die "Linux arm64 is not yet supported. See https://github.com/$REPO for updates." ;;
  *)             die "Unsupported platform: $OS/$ARCH. See https://github.com/$REPO for supported platforms." ;;
esac

# Resolve download URL from latest release
LATEST_URL="https://api.github.com/repos/${REPO}/releases/latest"
if command -v curl >/dev/null 2>&1; then
  DOWNLOAD_URL="$(curl -fsSL "$LATEST_URL" | grep '"browser_download_url"' | grep "\"${ASSET}\"" | head -1 | sed 's/.*"browser_download_url": "\(.*\)".*/\1/')"
elif command -v wget >/dev/null 2>&1; then
  DOWNLOAD_URL="$(wget -qO- "$LATEST_URL" | grep '"browser_download_url"' | grep "\"${ASSET}\"" | head -1 | sed 's/.*"browser_download_url": "\(.*\)".*/\1/')"
else
  die "curl or wget is required"
fi

[ -z "$DOWNLOAD_URL" ] && die "Could not resolve download URL for $ASSET. Check https://github.com/$REPO/releases"

TMPFILE="$(mktemp)"
trap 'rm -f "$TMPFILE"' EXIT

echo ""
echo "  cyber-memory installer"
echo "  ----------------------"
echo "  Platform : $OS/$ARCH"
echo "  Asset    : $ASSET"
echo "  Install  : $INSTALL_DIR/$BINARY"
echo ""

if command -v curl >/dev/null 2>&1; then
  curl -fsSL --progress-bar "$DOWNLOAD_URL" -o "$TMPFILE"
else
  wget -q --show-progress "$DOWNLOAD_URL" -O "$TMPFILE"
fi

chmod +x "$TMPFILE"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMPFILE" "$INSTALL_DIR/$BINARY"
else
  echo "  Installing to $INSTALL_DIR requires sudo..."
  sudo mv "$TMPFILE" "$INSTALL_DIR/$BINARY"
fi

echo "  Installed: $($INSTALL_DIR/$BINARY --version 2>/dev/null || echo 'done')"

cat <<'EOF'

  Add to your MCP config (claude_desktop_config.json or equivalent):

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
