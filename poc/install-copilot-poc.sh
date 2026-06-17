#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bash specs/llm-usage-tracking/poc/install-copilot-poc.sh [--state-dir ABSOLUTE_PATH] [--copilot-home PATH]

Options:
  --state-dir ABSOLUTE_PATH  Override the POC state directory. If omitted and this installer is run from STATE_DIR/bin/, the parent of bin is used automatically.
  --copilot-home PATH        Override COPILOT_HOME. Default: ${COPILOT_HOME:-$HOME/.copilot}
EOF
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COPILOT_HOME_DIR="${COPILOT_HOME:-$HOME/.copilot}"
STATE_DIR="${XDG_STATE_HOME:-$HOME/.llm-usage-tracker-poc}"
STATE_DIR_EXPLICIT=0

install_script() {
  local mode="$1"
  local src="$2"
  local dest="$3"

  if [[ -e "$dest" ]] && [[ "$(readlink -f "$src")" == "$(readlink -f "$dest")" ]]; then
    return 0
  fi

  install -m "$mode" "$src" "$dest"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --state-dir)
      [[ $# -ge 2 ]] || { echo "Missing value for --state-dir" >&2; exit 1; }
      STATE_DIR="$2"
      STATE_DIR_EXPLICIT=1
      shift 2
      ;;
    --copilot-home)
      [[ $# -ge 2 ]] || { echo "Missing value for --copilot-home" >&2; exit 1; }
      COPILOT_HOME_DIR="$2"
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

if [[ "$STATE_DIR_EXPLICIT" -eq 0 ]] && [[ "$(basename "$SCRIPT_DIR")" == "bin" ]]; then
  if [[ -f "$SCRIPT_DIR/copilot-usage-hook.sh" ]] && [[ -f "$SCRIPT_DIR/show-copilot-usage.mjs" ]]; then
  STATE_DIR="$(dirname "$SCRIPT_DIR")"
  fi
fi

case "$STATE_DIR" in
  /*) ;;
  *)
    echo "--state-dir must be an absolute path: $STATE_DIR" >&2
    exit 1
    ;;
esac

command -v node >/dev/null 2>&1 || {
  echo "This POC requires node to be available in PATH." >&2
  exit 1
}

if ! node --no-warnings - >/dev/null <<'NODE'
require('node:sqlite');
NODE
then
  echo "This POC requires a Node runtime with built-in node:sqlite support." >&2
  exit 1
fi

HOOKS_DIR="$COPILOT_HOME_DIR/hooks"
BIN_DIR="$STATE_DIR/bin"
CONFIG_PATH="$HOOKS_DIR/llm-usage-tracking-poc.json"

mkdir -p "$BIN_DIR" "$STATE_DIR/raw" "$STATE_DIR/sessions"

install_script 0755 "$SCRIPT_DIR/install-copilot-poc.sh" "$BIN_DIR/install-copilot-poc.sh"
install_script 0755 "$SCRIPT_DIR/copilot-usage-hook.sh" "$BIN_DIR/copilot-usage-hook.sh"
install_script 0644 "$SCRIPT_DIR/summarize-copilot-session.mjs" "$BIN_DIR/summarize-copilot-session.mjs"
install_script 0644 "$SCRIPT_DIR/reconcile-copilot-session.mjs" "$BIN_DIR/reconcile-copilot-session.mjs"
install_script 0644 "$SCRIPT_DIR/write-copilot-usage-db.mjs" "$BIN_DIR/write-copilot-usage-db.mjs"
install_script 0644 "$SCRIPT_DIR/show-copilot-usage.mjs" "$BIN_DIR/show-copilot-usage.mjs"
node --no-warnings "$BIN_DIR/write-copilot-usage-db.mjs" --state-dir "$STATE_DIR" --init-only

node - "$BIN_DIR" "$STATE_DIR" "$CONFIG_PATH" <<'NODE'
const fs = require('fs');
const [binDir, stateDir, configPath] = process.argv.slice(2);

const makeHook = (eventName) => ({
  type: 'command',
  bash: `${binDir}/copilot-usage-hook.sh ${eventName}`,
  timeoutSec: 10,
  env: {
    LLM_USAGE_POC_STATE_DIR: stateDir,
    LLM_USAGE_POC_BIN_DIR: binDir
  }
});

const config = {
  version: 1,
  hooks: {
    sessionEnd: [makeHook('sessionEnd')]
  }
};

fs.mkdirSync(require('path').dirname(configPath), { recursive: true });
fs.writeFileSync(configPath, JSON.stringify(config, null, 2) + '\n');
NODE

echo "Installed Copilot POC hook."
echo "Hook config: $CONFIG_PATH"
echo "Hook scripts: $BIN_DIR"
echo "State dir: $STATE_DIR"
echo "SQLite DB: $STATE_DIR/usage.db"
echo
echo "Restart Copilot CLI before testing."
