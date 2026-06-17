#!/usr/bin/env node

import fs from 'fs';
import path from 'path';
import { execFileSync } from 'child_process';

function parseArgs(argv) {
  const out = {};
  for (let i = 2; i < argv.length; i += 1) {
    const arg = argv[i];
    const value = argv[i + 1];
    if (!arg.startsWith('--')) {
      throw new Error(`Unexpected argument: ${arg}`);
    }
    if (typeof value === 'undefined') {
      throw new Error(`Missing value for argument: ${arg}`);
    }
    out[arg.slice(2)] = value;
    i += 1;
  }
  return out;
}

function sleepMs(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

function summarize(binDir, eventName, payloadFile, sessionFile, stateDir) {
  return JSON.parse(execFileSync(process.execPath, [
    path.join(binDir, 'summarize-copilot-session.mjs'),
    '--event-name', eventName,
    '--payload-file', payloadFile,
    '--session-file', sessionFile,
    '--state-dir', stateDir
  ], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe']
  }));
}

function writeSummary(summaryFile, summary) {
  fs.writeFileSync(summaryFile, `${JSON.stringify(summary, null, 2)}\n`);
}

function writeDb(binDir, stateDir, summaryFile) {
  execFileSync(process.execPath, [
    '--no-warnings',
    path.join(binDir, 'write-copilot-usage-db.mjs'),
    '--state-dir', stateDir,
    '--summary-file', summaryFile
  ], {
    stdio: ['ignore', 'pipe', 'pipe']
  });
}

const args = parseArgs(process.argv);
const stateDir = args['state-dir'];
const binDir = args['bin-dir'];
const eventName = args['event-name'] || 'sessionEnd';
const payloadFile = args['payload-file'];
const sessionFile = args['session-file'];
const sessionId = args['session-id'];
const attempts = Number(args.attempts || 12);
const delaySec = Number(args['delay-sec'] || 1);
const initialDelaySec = Number(args['initial-delay-sec'] || 2);

if (!stateDir || !binDir || !payloadFile || !sessionFile || !sessionId) {
  throw new Error('Missing required arguments for reconciliation');
}

const summaryFile = path.join(stateDir, 'sessions', `${sessionId}.summary.json`);

if (initialDelaySec > 0) {
  sleepMs(Math.round(initialDelaySec * 1000));
}

let latestSummary = null;
for (let attempt = 1; attempt <= attempts; attempt += 1) {
  latestSummary = summarize(binDir, eventName, payloadFile, sessionFile, stateDir);

  if (latestSummary.usage_source === 'session.shutdown.modelMetrics' || attempt === attempts) {
    writeSummary(summaryFile, latestSummary);
    writeDb(binDir, stateDir, summaryFile);
    break;
  }

  sleepMs(Math.round(delaySec * 1000));
}

try {
  fs.unlinkSync(payloadFile);
} catch {
  // Best-effort cleanup for the persisted payload snapshot.
}
