#!/usr/bin/env node

import fs from 'fs';
import path from 'path';
import { DatabaseSync } from 'node:sqlite';

function parseArgs(argv) {
  const out = {};
  for (let i = 2; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--state-dir') {
      out.stateDir = argv[i + 1];
      i += 1;
    } else if (arg === '--summary-file') {
      out.summaryFile = argv[i + 1];
      i += 1;
    } else if (arg === '--init-only') {
      out.initOnly = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  return out;
}

function ensureSchema(db) {
  db.exec(`
    PRAGMA journal_mode = WAL;

    CREATE TABLE IF NOT EXISTS sessions (
      session_id TEXT PRIMARY KEY,
      event_name TEXT,
      state_dir TEXT,
      source TEXT,
      collected_at TEXT,
      stop_reason TEXT,
      transcript_path TEXT,
      event_log_path TEXT,
      cwd TEXT,
      repo_root TEXT,
      worktree_root TEXT,
      git_common_dir TEXT,
      repo_remote TEXT,
      repo_branch TEXT,
      repo_head TEXT,
      host_name TEXT,
      started_at TEXT,
      ended_at TEXT,
      interaction_count INTEGER,
      assistant_message_count INTEGER,
      assistant_output_tokens INTEGER,
      total_input_tokens INTEGER,
      total_output_tokens INTEGER,
      total_cache_read_tokens INTEGER,
      total_cache_write_tokens INTEGER,
      total_reasoning_tokens INTEGER,
      usage_source TEXT,
      tool_request_count INTEGER,
      successful_tool_execution_count INTEGER,
      subagent_breakdown_source TEXT,
      is_subagent INTEGER,
      models_json TEXT NOT NULL,
      notes_json TEXT NOT NULL,
      raw_summary_json TEXT NOT NULL,
      updated_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS subagent_summaries (
      session_id TEXT NOT NULL,
      subagent_key TEXT NOT NULL,
      tool_call_id TEXT,
      agent_id TEXT,
      agent_name TEXT,
      agent_display_name TEXT,
      model TEXT,
      total_tool_calls INTEGER,
      total_tokens INTEGER,
      duration_ms INTEGER,
      completed_at TEXT,
      PRIMARY KEY (session_id, subagent_key)
    );

  `);

  const existingColumns = new Set(
    db.prepare('PRAGMA table_info(sessions)').all().map((column) => column.name)
  );
  const columnsToAdd = [
    ['worktree_root', 'TEXT'],
    ['git_common_dir', 'TEXT'],
    ['repo_head', 'TEXT']
  ];

  for (const [name, type] of columnsToAdd) {
    if (!existingColumns.has(name)) {
      db.exec(`ALTER TABLE sessions ADD COLUMN ${name} ${type}`);
    }
  }

  db.exec(`
    CREATE INDEX IF NOT EXISTS idx_sessions_ended_at ON sessions (ended_at DESC);
    CREATE INDEX IF NOT EXISTS idx_sessions_repo_remote ON sessions (repo_remote);
    CREATE INDEX IF NOT EXISTS idx_sessions_repo_branch ON sessions (repo_branch);
    CREATE INDEX IF NOT EXISTS idx_sessions_worktree_root ON sessions (worktree_root);
    CREATE INDEX IF NOT EXISTS idx_subagent_session_id ON subagent_summaries (session_id);
  `);
}

const args = parseArgs(process.argv);
const stateDir = args.stateDir || process.env.LLM_USAGE_POC_STATE_DIR;

if (!stateDir) {
  throw new Error('Missing --state-dir');
}

const dbPath = path.join(stateDir, 'usage.db');
fs.mkdirSync(stateDir, { recursive: true });
const db = new DatabaseSync(dbPath);
ensureSchema(db);

if (args.initOnly) {
  db.close();
  process.exit(0);
}

if (!args.summaryFile) {
  db.close();
  throw new Error('Missing --summary-file');
}

const summary = JSON.parse(fs.readFileSync(args.summaryFile, 'utf8'));
const now = new Date().toISOString();

const upsertSession = db.prepare(`
  INSERT INTO sessions (
    session_id, event_name, state_dir, source, collected_at, stop_reason,
    transcript_path, event_log_path, cwd, repo_root, worktree_root, git_common_dir,
    repo_remote, repo_branch, repo_head,
    host_name, started_at, ended_at, interaction_count, assistant_message_count,
    assistant_output_tokens, total_input_tokens, total_output_tokens,
    total_cache_read_tokens, total_cache_write_tokens, total_reasoning_tokens,
    usage_source, tool_request_count, successful_tool_execution_count,
    subagent_breakdown_source, is_subagent, models_json, notes_json,
    raw_summary_json, updated_at
  ) VALUES (
    @session_id, @event_name, @state_dir, @source, @collected_at, @stop_reason,
    @transcript_path, @event_log_path, @cwd, @repo_root, @worktree_root, @git_common_dir,
    @repo_remote, @repo_branch, @repo_head,
    @host_name, @started_at, @ended_at, @interaction_count, @assistant_message_count,
    @assistant_output_tokens, @total_input_tokens, @total_output_tokens,
    @total_cache_read_tokens, @total_cache_write_tokens, @total_reasoning_tokens,
    @usage_source, @tool_request_count, @successful_tool_execution_count,
    @subagent_breakdown_source, @is_subagent, @models_json, @notes_json,
    @raw_summary_json, @updated_at
  )
  ON CONFLICT(session_id) DO UPDATE SET
    event_name = excluded.event_name,
    state_dir = excluded.state_dir,
    source = excluded.source,
    collected_at = excluded.collected_at,
    stop_reason = excluded.stop_reason,
    transcript_path = excluded.transcript_path,
    event_log_path = excluded.event_log_path,
    cwd = excluded.cwd,
    repo_root = excluded.repo_root,
    worktree_root = excluded.worktree_root,
    git_common_dir = excluded.git_common_dir,
    repo_remote = excluded.repo_remote,
    repo_branch = excluded.repo_branch,
    repo_head = excluded.repo_head,
    host_name = excluded.host_name,
    started_at = excluded.started_at,
    ended_at = excluded.ended_at,
    interaction_count = excluded.interaction_count,
    assistant_message_count = excluded.assistant_message_count,
    assistant_output_tokens = excluded.assistant_output_tokens,
    total_input_tokens = excluded.total_input_tokens,
    total_output_tokens = excluded.total_output_tokens,
    total_cache_read_tokens = excluded.total_cache_read_tokens,
    total_cache_write_tokens = excluded.total_cache_write_tokens,
    total_reasoning_tokens = excluded.total_reasoning_tokens,
    usage_source = excluded.usage_source,
    tool_request_count = excluded.tool_request_count,
    successful_tool_execution_count = excluded.successful_tool_execution_count,
    subagent_breakdown_source = excluded.subagent_breakdown_source,
    is_subagent = excluded.is_subagent,
    models_json = excluded.models_json,
    notes_json = excluded.notes_json,
    raw_summary_json = excluded.raw_summary_json,
    updated_at = excluded.updated_at
`);

const deleteSubagents = db.prepare('DELETE FROM subagent_summaries WHERE session_id = ?');
const insertSubagent = db.prepare(`
  INSERT INTO subagent_summaries (
    session_id, subagent_key, tool_call_id, agent_id, agent_name, agent_display_name,
    model, total_tool_calls, total_tokens, duration_ms, completed_at
  ) VALUES (
    @session_id, @subagent_key, @tool_call_id, @agent_id, @agent_name, @agent_display_name,
    @model, @total_tool_calls, @total_tokens, @duration_ms, @completed_at
  )
`);

upsertSession.run({
  session_id: summary.session_id,
  event_name: summary.event_name,
  state_dir: summary.state_dir,
  source: summary.source,
  collected_at: summary.collected_at,
  stop_reason: summary.stop_reason,
  transcript_path: summary.transcript_path,
  event_log_path: summary.event_log_path,
  cwd: summary.cwd,
  repo_root: summary.repo_root,
  worktree_root: summary.worktree_root,
  git_common_dir: summary.git_common_dir,
  repo_remote: summary.repo_remote,
  repo_branch: summary.repo_branch,
  repo_head: summary.repo_head,
  host_name: summary.host_name,
  started_at: summary.started_at,
  ended_at: summary.ended_at,
  interaction_count: summary.interaction_count,
  assistant_message_count: summary.assistant_message_count,
  assistant_output_tokens: summary.assistant_output_tokens,
  total_input_tokens: summary.total_input_tokens,
  total_output_tokens: summary.total_output_tokens,
  total_cache_read_tokens: summary.total_cache_read_tokens,
  total_cache_write_tokens: summary.total_cache_write_tokens,
  total_reasoning_tokens: summary.total_reasoning_tokens,
  usage_source: summary.usage_source,
  tool_request_count: summary.tool_request_count,
  successful_tool_execution_count: summary.successful_tool_execution_count,
  subagent_breakdown_source: summary.subagent_breakdown_source,
  is_subagent: summary.is_subagent ? 1 : 0,
  models_json: JSON.stringify(summary.models || []),
  notes_json: JSON.stringify(summary.notes || []),
  raw_summary_json: JSON.stringify(summary),
  updated_at: now
});

deleteSubagents.run(summary.session_id);
for (const [index, subagent] of (summary.subagent_summaries || []).entries()) {
  insertSubagent.run({
    session_id: summary.session_id,
    subagent_key:
      subagent.tool_call_id ||
      subagent.agent_id ||
      `${subagent.completed_at || 'unknown'}-${index}`,
    tool_call_id: subagent.tool_call_id,
    agent_id: subagent.agent_id,
    agent_name: subagent.agent_name,
    agent_display_name: subagent.agent_display_name,
    model: subagent.model,
    total_tool_calls: subagent.total_tool_calls,
    total_tokens: subagent.total_tokens,
    duration_ms: subagent.duration_ms,
    completed_at: subagent.completed_at
  });
}

db.close();
