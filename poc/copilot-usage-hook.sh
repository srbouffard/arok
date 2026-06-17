#!/usr/bin/env bash
set -euo pipefail

EVENT_NAME="${1:?missing hook event name}"
STATE_DIR="${LLM_USAGE_POC_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.llm-usage-tracker-poc}}"
BIN_DIR="${LLM_USAGE_POC_BIN_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
SHUTDOWN_RETRY_ATTEMPTS="${LLM_USAGE_POC_SHUTDOWN_RETRY_ATTEMPTS:-6}"
SHUTDOWN_RETRY_DELAY_SEC="${LLM_USAGE_POC_SHUTDOWN_RETRY_DELAY_SEC:-0.5}"
ASYNC_RECONCILE_ATTEMPTS="${LLM_USAGE_POC_ASYNC_RECONCILE_ATTEMPTS:-12}"
ASYNC_RECONCILE_DELAY_SEC="${LLM_USAGE_POC_ASYNC_RECONCILE_DELAY_SEC:-1}"
ASYNC_RECONCILE_INITIAL_DELAY_SEC="${LLM_USAGE_POC_ASYNC_RECONCILE_INITIAL_DELAY_SEC:-2}"

mkdir -p "$STATE_DIR/raw" "$STATE_DIR/sessions" "$STATE_DIR/logs" "$STATE_DIR/reconcile"

PAYLOAD_FILE="$(mktemp)"
trap 'rm -f "$PAYLOAD_FILE"' EXIT
cat >"$PAYLOAD_FILE"

node - "$EVENT_NAME" "$PAYLOAD_FILE" >>"$STATE_DIR/raw/copilot-hook-events.jsonl" <<'NODE'
const fs = require('fs');
const [eventName, payloadPath] = process.argv.slice(2);
const raw = fs.readFileSync(payloadPath, 'utf8');
let payload;
try {
  payload = JSON.parse(raw);
} catch (error) {
  payload = { _parse_error: error.message, raw };
}
process.stdout.write(JSON.stringify({
  captured_at: new Date().toISOString(),
  event_name: eventName,
  payload
}) + '\n');
NODE

SESSION_ID="$(node - "$PAYLOAD_FILE" <<'NODE'
const fs = require('fs');
const raw = fs.readFileSync(process.argv[2], 'utf8');
try {
  const payload = JSON.parse(raw);
  process.stdout.write(String(payload.sessionId || payload.session_id || ''));
} catch {}
NODE
)"

if [[ -z "$SESSION_ID" ]]; then
  exit 0
fi

SESSION_FILE="$(node - "$PAYLOAD_FILE" "$SESSION_ID" <<'NODE'
const fs = require('fs');
const path = require('path');
const raw = fs.readFileSync(process.argv[2], 'utf8');
const sessionId = process.argv[3];
let transcriptPath = '';
try {
  const payload = JSON.parse(raw);
  transcriptPath = payload.transcriptPath || payload.transcript_path || '';
} catch {}
const copilotHome = process.env.COPILOT_HOME || path.join(process.env.HOME || '', '.copilot');
const defaultPath = path.join(copilotHome, 'session-state', sessionId, 'events.jsonl');
process.stdout.write(transcriptPath || defaultPath);
NODE
)"

summarize_once() {
  local output_file="$1"
  node "$BIN_DIR/summarize-copilot-session.mjs" \
    --event-name "$EVENT_NAME" \
    --payload-file "$PAYLOAD_FILE" \
    --session-file "$SESSION_FILE" \
    --state-dir "$STATE_DIR" >"$output_file"
}

summary_usage_source() {
  local summary_file="$1"
  node - "$summary_file" <<'NODE'
const fs = require('fs');
const summary = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
process.stdout.write(String(summary.usage_source || ''));
NODE
}

spawn_async_reconcile() {
  local payload_snapshot
  payload_snapshot="$(mktemp "$STATE_DIR/reconcile/${SESSION_ID}.payload.XXXXXX.json")"
  cp "$PAYLOAD_FILE" "$payload_snapshot"

  nohup node --no-warnings "$BIN_DIR/reconcile-copilot-session.mjs" \
    --event-name "$EVENT_NAME" \
    --payload-file "$payload_snapshot" \
    --session-file "$SESSION_FILE" \
    --session-id "$SESSION_ID" \
    --state-dir "$STATE_DIR" \
    --bin-dir "$BIN_DIR" \
    --attempts "$ASYNC_RECONCILE_ATTEMPTS" \
    --delay-sec "$ASYNC_RECONCILE_DELAY_SEC" \
    --initial-delay-sec "$ASYNC_RECONCILE_INITIAL_DELAY_SEC" \
    >>"$STATE_DIR/logs/reconcile.log" 2>&1 </dev/null &
}

FINAL_SUMMARY="$STATE_DIR/sessions/$SESSION_ID.summary.json"
attempt=1

while true; do
  TMP_SUMMARY="$(mktemp)"
  if ! summarize_once "$TMP_SUMMARY"; then
    rm -f "$TMP_SUMMARY"
    exit 1
  fi

  usage_source="$(summary_usage_source "$TMP_SUMMARY")"
  if [[ "$EVENT_NAME" != "sessionEnd" ]] || [[ "$usage_source" == "session.shutdown.modelMetrics" ]] || [[ "$attempt" -ge "$SHUTDOWN_RETRY_ATTEMPTS" ]]; then
    mv "$TMP_SUMMARY" "$FINAL_SUMMARY"
    break
  fi

  rm -f "$TMP_SUMMARY"
  attempt=$((attempt + 1))
  sleep "$SHUTDOWN_RETRY_DELAY_SEC"
done

node --no-warnings "$BIN_DIR/write-copilot-usage-db.mjs" \
  --state-dir "$STATE_DIR" \
  --summary-file "$FINAL_SUMMARY"

if [[ "$EVENT_NAME" == "sessionEnd" ]] && [[ "$usage_source" != "session.shutdown.modelMetrics" ]]; then
  spawn_async_reconcile
fi
