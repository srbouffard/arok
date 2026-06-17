#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./install.sh [--prefix DIR] [--from-source]

Options:
  --prefix DIR              Install directory for the arok binary. Default: ~/.local/bin
  --from-source             Build from source instead of downloading release binary

By default, downloads the latest release binary from GitHub.
Use --from-source to build from the local checkout (requires Go).

After installation, run:
  arok install copilot      Set up Copilot CLI hooks
EOF
}

PREFIX_DIR="${HOME}/.local/bin"
FROM_SOURCE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)
      [[ $# -ge 2 ]] || { echo "Missing value for --prefix" >&2; exit 1; }
      PREFIX_DIR="$2"
      shift 2
      ;;
    --from-source)
      FROM_SOURCE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$PREFIX_DIR"

# Detect platform
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    echo "Supported: amd64, arm64" >&2
    exit 1
    ;;
esac

# Build from source if requested or if we're in a git checkout without releases
if [[ "$FROM_SOURCE" -eq 1 ]] || [[ -d "$ROOT_DIR/.git" && ! -f "$ROOT_DIR/dist/arok" ]]; then
  command -v go >/dev/null 2>&1 || {
    echo "Error: Building from source requires Go." >&2
    echo "Install Go or remove --from-source to download a release binary." >&2
    exit 1
  }
  
  echo "Building from source..."
  (
    cd "$ROOT_DIR"
    GOFLAGS='' go build -o "$TMP_DIR/arok" ./cmd/arok
  )
  install -m 0755 "$TMP_DIR/arok" "$PREFIX_DIR/arok"
  
elif [[ -f "$ROOT_DIR/dist/arok" ]]; then
  # Use pre-built binary from local dist/ directory
  echo "Installing from local dist/ directory..."
  install -m 0755 "$ROOT_DIR/dist/arok" "$PREFIX_DIR/arok"
  
else
  # Download latest release from GitHub
  REPO="srbouffard/arok"
  
  command -v curl >/dev/null 2>&1 || {
    echo "Error: curl is required to download release binaries." >&2
    echo "Install curl or use --from-source to build locally." >&2
    exit 1
  }
  
  echo "Fetching latest release from GitHub..."
  
  # Get latest release tag
  LATEST_TAG="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v?([^"]+)".*/\1/')"
  
  if [[ -z "$LATEST_TAG" ]]; then
    echo "Error: Could not fetch latest release." >&2
    echo "Use --from-source to build from this checkout." >&2
    exit 1
  fi
  
  BINARY_NAME="arok-${OS}-${ARCH}"
  DOWNLOAD_URL="https://github.com/$REPO/releases/download/v${LATEST_TAG}/${BINARY_NAME}.tar.gz"
  
  echo "Downloading arok v${LATEST_TAG} for ${OS}/${ARCH}..."
  
  curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/${BINARY_NAME}.tar.gz" || {
    echo "Error: Failed to download ${DOWNLOAD_URL}" >&2
    echo "Use --from-source to build from this checkout." >&2
    exit 1
  }
  
  tar -xzf "$TMP_DIR/${BINARY_NAME}.tar.gz" -C "$TMP_DIR"
  install -m 0755 "$TMP_DIR/${BINARY_NAME}" "$PREFIX_DIR/arok"
fi

echo "✓ Installed binary: $PREFIX_DIR/arok"
echo

"$PREFIX_DIR/arok" version || {
  echo "Error: Installed binary failed to run." >&2
  exit 1
}

echo
echo "Next steps:"
echo "  1. Ensure $PREFIX_DIR is in your PATH"
echo "  2. Run: arok install copilot"
echo

case ":$PATH:" in
  *":$PREFIX_DIR:"*) ;;
  *)
    echo "Note: Add $PREFIX_DIR to PATH if it is not already present."
    echo
    ;;
esac
