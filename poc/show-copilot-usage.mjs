#!/usr/bin/env node

import fs from 'fs';
import path from 'path';
import { DatabaseSync } from 'node:sqlite';

function parseArgs(argv) {
  const out = { latest: 10 };
  for (let i = 2; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--latest') {
      out.latest = Number(argv[i + 1] || 10);
      i += 1;
    } else if (arg === '--state-dir') {
      out.stateDir = argv[i + 1];
      i += 1;
    } else if (arg === '--session-id') {
      out.sessionId = argv[i + 1];
      i += 1;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  return out;
}

const args = parseArgs(process.argv);
const stateDir =
  args.stateDir ||
  process.env.LLM_USAGE_POC_STATE_DIR ||
  (process.env.XDG_STATE_HOME || path.join(process.env.HOME, '.llm-usage-tracker-poc'));
const sessionsDir = path.join(stateDir, 'sessions');
const dbPath = path.join(stateDir, 'usage.db');

function printFromDb() {
  const db = new DatabaseSync(dbPath);

  if (args.sessionId) {
    const row = db
      .prepare('SELECT raw_summary_json FROM sessions WHERE session_id = ?')
      .get(args.sessionId);
    if (!row) {
      console.error(`No summary found for session ${args.sessionId}`);
      db.close();
      process.exit(1);
    }
    const summary = JSON.parse(row.raw_summary_json);
    summary.subagent_summaries = db
      .prepare(`
        SELECT agent_id, tool_call_id, agent_name, agent_display_name, model,
               total_tool_calls, total_tokens, duration_ms, completed_at
        FROM subagent_summaries
        WHERE session_id = ?
        ORDER BY completed_at
      `)
      .all(args.sessionId);
    console.log(JSON.stringify(summary, null, 2));
    db.close();
    process.exit(0);
  }

  const rows = db
    .prepare(`
      SELECT session_id, event_name, host_name, repo_branch, worktree_root, total_input_tokens,
             COALESCE(total_output_tokens, assistant_output_tokens) AS total_output_tokens,
             usage_source, assistant_message_count,
             COALESCE(ended_at, collected_at) AS ended_at
      FROM sessions
      ORDER BY COALESCE(ended_at, collected_at) DESC
      LIMIT ?
    `)
    .all(args.latest);
  console.table(rows);
  db.close();
  process.exit(0);
}

if (fs.existsSync(dbPath)) {
  printFromDb();
}

if (!fs.existsSync(sessionsDir)) {
  console.error(`No session summaries found in ${sessionsDir}`);
  process.exit(1);
}

const summaries = fs
  .readdirSync(sessionsDir)
  .filter((name) => name.endsWith('.summary.json'))
  .map((name) => ({
    name,
    filePath: path.join(sessionsDir, name),
    stat: fs.statSync(path.join(sessionsDir, name))
  }))
  .sort((a, b) => b.stat.mtimeMs - a.stat.mtimeMs)
  .map(({ filePath }) => JSON.parse(fs.readFileSync(filePath, 'utf8')));

if (args.sessionId) {
  const found = summaries.find((summary) => summary.session_id === args.sessionId);
  if (!found) {
    console.error(`No summary found for session ${args.sessionId}`);
    process.exit(1);
  }
  console.log(JSON.stringify(found, null, 2));
  process.exit(0);
}

const rows = summaries.slice(0, args.latest).map((summary) => ({
  session_id: summary.session_id,
  event_name: summary.event_name,
  host_name: summary.host_name,
  repo_branch: summary.repo_branch || '',
  worktree_root: summary.worktree_root || summary.repo_root || '',
  total_input_tokens: summary.total_input_tokens ?? '',
  total_output_tokens: summary.total_output_tokens ?? summary.assistant_output_tokens,
  usage_source: summary.usage_source,
  assistant_message_count: summary.assistant_message_count,
  ended_at: summary.ended_at || summary.collected_at
}));

console.table(rows);
