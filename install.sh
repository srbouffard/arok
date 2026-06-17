#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./install.sh [--prefix DIR] [--state-dir ABSOLUTE_PATH] [--copilot-home PATH] [--no-copilot]

Options:
  --prefix DIR              Install directory for the arok binary. Default: ~/.local/bin
  --state-dir ABSOLUTE_PATH Override the canonical AROK state dir for generated hook config.
  --copilot-home PATH       Override the Copilot home directory. Default: ${COPILOT_HOME:-$HOME/.copilot}
  --no-copilot              Install only the binary; skip Copilot hook setup.
EOF
}

PREFIX_DIR="${HOME}/.local/bin"
STATE_DIR=""
COPILOT_HOME_DIR="${COPILOT_HOME:-$HOME/.copilot}"
INSTALL_COPILOT=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)
      [[ $# -ge 2 ]] || { echo "Missing value for --prefix" >&2; exit 1; }
      PREFIX_DIR="$2"
      shift 2
      ;;
    --state-dir)
      [[ $# -ge 2 ]] || { echo "Missing value for --state-dir" >&2; exit 1; }
      STATE_DIR="$2"
      shift 2
      ;;
    --copilot-home)
      [[ $# -ge 2 ]] || { echo "Missing value for --copilot-home" >&2; exit 1; }
      COPILOT_HOME_DIR="$2"
      shift 2
      ;;
    --no-copilot)
      INSTALL_COPILOT=0
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

echo "Installed binary: $PREFIX_DIR/arok"

if [[ "$INSTALL_COPILOT" -eq 1 ]]; then
  INSTALL_ARGS=(install copilot --copilot-home "$COPILOT_HOME_DIR")
  if [[ -n "$STATE_DIR" ]]; then
    INSTALL_ARGS+=(--state-dir "$STATE_DIR")
  fi
  "$PREFIX_DIR/arok" "${INSTALL_ARGS[@]}"
fi

case ":$PATH:" in
  *":$PREFIX_DIR:"*) ;;
  *)
    echo
    echo "Add $PREFIX_DIR to PATH if it is not already present."
    ;;
esac
