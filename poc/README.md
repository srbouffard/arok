# Copilot CLI usage-tracking POC

This POC gives you a **real Copilot CLI hook setup you can try now** on your multipass instance before the full implementation exists.

It is intentionally small and pragmatic:

1. install a user-level Copilot hook
2. parse Copilot's local `events.jsonl` session log at session end
3. write raw hook payloads, scripts, per-session JSON summaries, and a minimal SQLite DB into a shared-capable state directory

## What it captures

Today, this POC extracts:

1. session ID and event type
2. cwd plus git-derived repo/worktree metadata when available
3. host name
4. assistant message count
5. session-level **input/output/cache/reasoning token** totals from Copilot's local `session.shutdown.modelMetrics` when present
6. per-model totals when present in `session.shutdown.modelMetrics`
7. optional sub-agent breakdown reconstructed from local `subagent.completed` events
8. tool-request counts
9. raw hook payloads for debugging
10. a minimal SQLite session store for later querying

The metadata enrichment strategy is intentionally simple: use the harness-provided `cwd` as the anchor, then run local git inspection there to derive `repo_root`, `worktree_root`, `git_common_dir`, `repo_remote`, `repo_branch`, and `repo_head`.

## Current limitation

Copilot's hook payloads themselves do **not** appear to include the token totals this POC needs.

The useful metrics come from Copilot's local session log instead:

1. `session.shutdown.modelMetrics` can include `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWriteTokens`, and `reasoningTokens` at session shutdown
2. `subagent.completed` can provide per-sub-agent `model`, `totalToolCalls`, `totalTokens`, and `durationMs`
3. cwd/repo/branch context still comes from the hook payload plus local git inspection, not from `session.shutdown.modelMetrics`

That means:

1. `sessionEnd` is the canonical capture point for overall session usage
2. sub-agent breakdown can be derived from the same parent session log, without relying on `subagentStop` hooks
3. hook payloads themselves are not the primary metrics source for this POC
4. if a Copilot session is resumed later with `--continue`, `--resume`, or the same explicit `--session-id`, the POC should refresh that same session's tracked summary on the next `sessionEnd`
5. the hook should briefly retry after `sessionEnd` before accepting fallback totals, because final shutdown metrics may appear slightly after the hook fires
6. if fallback totals are still used at hook time, the POC launches an autonomous post-hook reconcile step to upgrade the stored session once `session.shutdown` lands

So this POC is useful for:

1. validating the hook flow
2. validating state placement and shared mounts
3. validating end-of-session totals and optional sub-agent breakdown

But it is still **not yet a complete implementation** of the final spec because it depends on local Copilot log structure rather than a stable first-class usage API from hooks.

## Recommended multipass setup

If you want the captured state to survive inside/outside the instance and be inspectable from the host, point the POC at a **mounted shared path**.

Example:

```bash
bash specs/llm-usage-tracking/poc/install-copilot-poc.sh \
  --state-dir /path/to/your/mounted/llm-usage-tracking-poc-state
```

That state dir can be a mounted host path inside the instance.

If you do not set `--state-dir`, the default is:

```bash
${XDG_STATE_HOME:-$HOME/.llm-usage-tracker-poc}
```

## Install

From this repository:

```bash
bash specs/llm-usage-tracking/poc/install-copilot-poc.sh \
  --state-dir /path/to/your/mounted/llm-usage-tracking-poc-state
```

The installer will:

1. copy the POC scripts, including the installer itself, into `STATE_DIR/bin/`
2. generate `~/.copilot/hooks/llm-usage-tracking-poc.json`
3. embed the selected state directory in that generated hook config
4. initialize `STATE_DIR/usage.db`

This layout is deliberate:

1. the actual hook config still lives where Copilot expects it: `~/.copilot/hooks/`
2. the executable scripts live beside the captured state in `STATE_DIR/bin/`
3. the SQLite DB lives in the same state directory
4. a mounted Multipass path can therefore carry both the POC logic and the captured data together

Then restart Copilot CLI in the multipass instance.

If you later change the POC scripts in this repository, rerun the installer. `STATE_DIR/bin/` contains copied deployment artifacts, not live references back to the repo checkout.

If you install on the host first and then mount that same `STATE_DIR` into a Multipass instance, you can rerun the **mounted** installer from `STATE_DIR/bin/install-copilot-poc.sh` inside the instance. That updates the instance-local Copilot hook config while reusing the same mounted data directory.

When the installer is run from `STATE_DIR/bin/install-copilot-poc.sh`, it automatically infers `STATE_DIR` as the parent of that `bin/` directory unless you explicitly pass `--state-dir`.

If the guest sees the mounted directory at a different path than the host, that auto-detection is exactly what you want: the guest will write a hook config that points at the guest-visible mounted path.

Rerunning the installer against an existing mounted state dir is expected to be safe:

1. it refreshes the copied scripts in `STATE_DIR/bin/`
2. it rewrites the local `~/.copilot/hooks/llm-usage-tracking-poc.json` for the current environment
3. it reopens `STATE_DIR/usage.db` and applies `CREATE TABLE IF NOT EXISTS` schema setup
4. it does **not** wipe existing `usage.db`, `raw/`, or `sessions/` content

## Run it

1. restart `copilot`
2. run your discourse upgrade session as normal
3. exit the session cleanly so `sessionEnd` fires
4. if you later resume the same Copilot session and end it again, expect the same `session_id` row in `usage.db` to be refreshed with the latest totals
5. if shutdown metrics arrive only after the hook ends, the background reconcile step should update the same session row a few seconds later

## Validation result

This POC has been validated with two real non-interactive Copilot runs using the same explicit `--session-id`.

Observed behavior:

1. the first completed run inserted a tracked session row and raw `sessionEnd` hook record
2. the second completed run with the same `session_id` appended another raw hook record
3. the normalized SQLite session row was refreshed in place, so the tracker reflected the later session totals instead of creating a duplicate logical session
4. a captured guest session showed that repo and branch metadata were recorded correctly even when the final usage source fell back
5. the POC observed a real timing race where `sessionEnd` fired before `session.shutdown.modelMetrics` was visible
6. in one guest session, `session.shutdown` was logged only after the `sessionEnd` hook completed, so the POC now also launches an autonomous post-hook reconcile step

## Inspect captured data

Summaries:

```bash
node /path/to/your/mounted/llm-usage-tracking-poc-state/bin/show-copilot-usage.mjs \
  --state-dir /path/to/your/mounted/llm-usage-tracking-poc-state \
  --latest 10
```

Raw files:

1. `STATE_DIR/raw/copilot-hook-events.jsonl`
2. `STATE_DIR/sessions/<session-id>.summary.json`
3. `STATE_DIR/usage.db`
4. `STATE_DIR/logs/reconcile.log`

## Dependencies

This POC does **not** require extra host packages beyond Node.

It uses Node's built-in `node:sqlite` module, so you do not need to install:

1. `sqlite3` CLI
2. Python
3. extra npm packages

The tradeoff is that `node:sqlite` is still marked experimental in current Node releases, which is acceptable for this POC.

## Manual install

If you prefer to install manually, use `copilot-user-hooks.example.json` as the starting point and copy the scripts into:

`STATE_DIR/bin/`

Then copy the JSON file to:

`~/.copilot/hooks/llm-usage-tracking-poc.json`

Replace `__STATE_DIR__` in the example JSON with the absolute mounted/shared state path you want to use.

## Hook events used

The generated hook config wires:

1. `sessionEnd`

That keeps the POC focused on the main goal while still giving you:

1. per-session summaries
2. optional sub-agent summaries reconstructed from local `subagent.completed` events
3. raw debug payloads if the local event format changes
4. better end-of-session token totals when `session.shutdown` is present
