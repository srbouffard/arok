#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./install.sh [--prefix DIR]

Options:
  --prefix DIR              Install directory for the arok binary. Default: ~/.local/bin

After installation, run:
  arok install copilot      Set up Copilot CLI hooks
EOF
}

PREFIX_DIR="${HOME}/.local/bin"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)
      [[ $# -ge 2 ]] || { echo "Missing value for --prefix" >&2; exit 1; }
      PREFIX_DIR="$2"
      shift 2
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

command -v go >/dev/null 2>&1 || {
  echo "go must be available to build arok from this checkout." >&2
  exit 1
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$PREFIX_DIR"
(
  cd "$ROOT_DIR"
  GOFLAGS='' go build -o "$TMP_DIR/arok" ./cmd/arok
)
install -m 0755 "$TMP_DIR/arok" "$PREFIX_DIR/arok"

echo "✓ Installed binary: $PREFIX_DIR/arok"
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
