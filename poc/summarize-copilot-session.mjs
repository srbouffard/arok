#!/usr/bin/env node

import fs from 'fs';
import os from 'os';
import path from 'path';
import { execFileSync } from 'child_process';

function parseArgs(argv) {
  const args = {};
  for (let i = 2; i < argv.length; i += 1) {
    const key = argv[i];
    const value = argv[i + 1];
    if (!key.startsWith('--')) {
      throw new Error(`Unexpected argument: ${key}`);
    }
    if (typeof value === 'undefined') {
      throw new Error(`Missing value for argument: ${key}`);
    }
    args[key.slice(2)] = value;
    i += 1;
  }
  return args;
}

function safeJsonParse(text, fallback) {
  try {
    return JSON.parse(text);
  } catch {
    return fallback;
  }
}

function readJsonFile(filePath, fallback = {}) {
  return safeJsonParse(fs.readFileSync(filePath, 'utf8'), fallback);
}

function readJsonLines(filePath) {
  if (!fs.existsSync(filePath)) {
    return [];
  }
  return fs
    .readFileSync(filePath, 'utf8')
    .split('\n')
    .filter(Boolean)
    .map((line) => safeJsonParse(line, null))
    .filter(Boolean);
}

function gitValue(cwd, ...args) {
  try {
    return execFileSync('git', ['-C', cwd, ...args], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore']
    }).trim();
  } catch {
    return null;
  }
}

function gitMetadata(cwd) {
  if (!cwd) {
    return {
      repoRoot: null,
      worktreeRoot: null,
      gitCommonDir: null,
      repoRemote: null,
      repoBranch: null,
      repoHead: null
    };
  }

  const repoRoot = gitValue(cwd, 'rev-parse', '--path-format=absolute', '--show-toplevel');
  const worktreeRoot = repoRoot;
  const gitCommonDir = repoRoot
    ? gitValue(repoRoot, 'rev-parse', '--path-format=absolute', '--git-common-dir')
    : null;
  const repoRemote = repoRoot ? gitValue(repoRoot, 'remote', 'get-url', 'origin') : null;
  const repoBranch = repoRoot ? gitValue(repoRoot, 'symbolic-ref', '--quiet', '--short', 'HEAD') : null;
  const repoHead = repoRoot ? gitValue(repoRoot, 'rev-parse', 'HEAD') : null;

  return {
    repoRoot,
    worktreeRoot,
    gitCommonDir,
    repoRemote,
    repoBranch,
    repoHead
  };
}

const args = parseArgs(process.argv);
const eventName = args['event-name'] || 'unknown';
const payloadFile = args['payload-file'];
const sessionFile = args['session-file'];
const stateDir = args['state-dir'] || null;

if (!payloadFile || !sessionFile) {
  throw new Error('Missing required arguments: --payload-file and --session-file');
}

const payload = readJsonFile(payloadFile, {});
const events = readJsonLines(sessionFile);

const sessionId = payload.sessionId || payload.session_id || path.basename(path.dirname(sessionFile));
const cwd = payload.cwd || null;
const { repoRoot, worktreeRoot, gitCommonDir, repoRemote, repoBranch, repoHead } = gitMetadata(cwd);

let startedAt = null;
let endedAt = null;
let assistantMessageCount = 0;
let assistantOutputTokens = 0;
let toolRequestCount = 0;
let successfulToolExecutions = 0;
const interactionIds = new Set();
const modelStats = new Map();
let shutdownMetrics = null;
let shutdownTimestamp = null;
let shutdownEventPresent = false;
const subagentSummaries = [];

for (const event of events) {
  const timestamp = event?.timestamp || null;
  if (timestamp && (!startedAt || timestamp < startedAt)) {
    startedAt = timestamp;
  }
  if (timestamp && (!endedAt || timestamp > endedAt)) {
    endedAt = timestamp;
  }

  if (event?.type === 'assistant.message' && event.data) {
    const model = event.data.model || 'unknown';
    const outputTokens = Number(event.data.outputTokens || 0);
    assistantMessageCount += 1;
    assistantOutputTokens += outputTokens;
    toolRequestCount += Array.isArray(event.data.toolRequests) ? event.data.toolRequests.length : 0;
    if (event.data.interactionId) {
      interactionIds.add(event.data.interactionId);
    }

    const current = modelStats.get(model) || {
      model,
      assistant_message_count: 0,
      assistant_output_tokens: 0
    };
    current.assistant_message_count += 1;
    current.assistant_output_tokens += outputTokens;
    modelStats.set(model, current);
  }

  if (event?.type === 'session.shutdown' && event.data) {
    shutdownEventPresent = true;
    shutdownMetrics = event.data.modelMetrics || null;
    shutdownTimestamp = event.timestamp || shutdownTimestamp;
  }

  if (event?.type === 'subagent.completed' && event.data) {
    subagentSummaries.push({
      agent_id: event.agentId || null,
      tool_call_id: event.data.toolCallId || null,
      agent_name: event.data.agentName || null,
      agent_display_name: event.data.agentDisplayName || null,
      model: event.data.model || null,
      total_tool_calls: Number(event.data.totalToolCalls || 0),
      total_tokens: Number(event.data.totalTokens || 0),
      duration_ms: Number(event.data.durationMs || 0),
      completed_at: event.timestamp || null
    });
  }

  if (event?.type === 'tool.execution_complete' && event.data?.success) {
    successfulToolExecutions += 1;
  }
}

let totalInputTokens = null;
let totalOutputTokens = null;
let totalCacheReadTokens = null;
let totalCacheWriteTokens = null;
let totalReasoningTokens = null;
let usageSource = 'assistant.message.outputTokens';

if (shutdownMetrics && typeof shutdownMetrics === 'object') {
  totalInputTokens = 0;
  totalOutputTokens = 0;
  totalCacheReadTokens = 0;
  totalCacheWriteTokens = 0;
  totalReasoningTokens = 0;
  usageSource = 'session.shutdown.modelMetrics';

  for (const [model, metric] of Object.entries(shutdownMetrics)) {
    const usage = metric?.usage || {};
    const current = modelStats.get(model) || {
      model,
      assistant_message_count: 0,
      assistant_output_tokens: 0
    };

    current.input_tokens = Number(usage.inputTokens || 0);
    current.output_tokens = Number(usage.outputTokens || 0);
    current.cache_read_tokens = Number(usage.cacheReadTokens || 0);
    current.cache_write_tokens = Number(usage.cacheWriteTokens || 0);
    current.reasoning_tokens = Number(usage.reasoningTokens || 0);
    current.request_count = Number(metric?.requests?.count || 0);
    current.premium_cost_units = metric?.requests?.cost ?? null;

    totalInputTokens += current.input_tokens;
    totalOutputTokens += current.output_tokens;
    totalCacheReadTokens += current.cache_read_tokens;
    totalCacheWriteTokens += current.cache_write_tokens;
    totalReasoningTokens += current.reasoning_tokens;

    modelStats.set(model, current);
  }
} else {
  totalOutputTokens = assistantOutputTokens;
}

const summary = {
  schema_version: 1,
  source: 'copilot-cli-poc',
  collected_at: new Date().toISOString(),
  event_name: eventName,
  state_dir: stateDir,
  session_id: sessionId,
  is_subagent: eventName === 'subagentStop',
  agent_name: payload.agentName || payload.agent_name || null,
  stop_reason: payload.stopReason || payload.stop_reason || payload.reason || null,
  transcript_path: payload.transcriptPath || payload.transcript_path || null,
  event_log_path: sessionFile,
  cwd,
  repo_root: repoRoot,
  worktree_root: worktreeRoot,
  git_common_dir: gitCommonDir,
  repo_remote: repoRemote,
  repo_branch: repoBranch,
  repo_head: repoHead,
  host_name: os.hostname(),
  started_at: startedAt,
  ended_at: shutdownTimestamp || endedAt,
  interaction_count: interactionIds.size,
  assistant_message_count: assistantMessageCount,
  assistant_output_tokens: assistantOutputTokens,
  total_input_tokens: totalInputTokens,
  total_output_tokens: totalOutputTokens,
  total_cache_read_tokens: totalCacheReadTokens,
  total_cache_write_tokens: totalCacheWriteTokens,
  total_reasoning_tokens: totalReasoningTokens,
  usage_source: usageSource,
  shutdown_event_present: shutdownEventPresent,
  shutdown_metrics_present: usageSource === 'session.shutdown.modelMetrics',
  tool_request_count: toolRequestCount,
  successful_tool_execution_count: successfulToolExecutions,
  subagent_breakdown_source: subagentSummaries.length ? 'subagent.completed' : null,
  subagent_summaries: subagentSummaries,
  models: Array.from(modelStats.values()).sort((a, b) => a.model.localeCompare(b.model)),
  notes: [
    'This POC reads metrics from Copilot local events.jsonl rather than from hook payloads.',
    'When session.shutdown.modelMetrics is present, those totals are preferred.',
    'Sub-agent breakdown is reconstructed from subagent.completed events in the same session log when present.',
    'This POC is intentionally centered on final session accounting from session.shutdown.',
    'Repo and worktree metadata is derived from the captured cwd via local git inspection.'
  ]
};

process.stdout.write(JSON.stringify(summary, null, 2) + '\n');
