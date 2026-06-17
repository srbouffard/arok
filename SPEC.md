# Abstract

This initiative defines a local-first product for capturing and querying LLM and agent resource usage across harnesses such as GitHub Copilot CLI and OpenCode.

The working product direction is **`arok`**, short for **Agent Resource Observation Kit**.

The system uses harness hooks plus post-hook reconciliation to ingest normalized usage events into a single host-level SQLite database so usage can be analyzed later by session, task, repo, harness, model, host, branch, worktree, host, and timeframe.

# Rationale

Local agent workflows already span multiple sessions, repos, hosts, harnesses, and sometimes sub-agents. The useful data exists during execution, but it is easy to lose once a session ends.

Manual tracking is unreliable, and flat files alone make the interesting questions harder than they need to be:

1. how many tokens were used for a given session or task?
2. how much usage came from sub-agents?
3. what did a repo or harness cost over the last week?
4. which models are driving the highest usage?

The POC proved that the hard part is not a large UX surface, but reliable capture, metadata enrichment, and post-session reconciliation. The production design should therefore stay small and low-dependency, but it no longer needs to remain repo-scripts-only. A compact installable CLI/binary is now justified because it improves:

1. distribution and updates
2. cross-host consistency
3. autonomous background reconciliation
4. future multi-harness support

# Specification

## Goals

The system must:

1. capture usage from supported harnesses automatically via hooks
2. normalize those payloads into a common schema
3. persist the data locally in a query-friendly form
4. preserve enough parent/child linkage to separate main-agent and sub-agent usage when the harness exposes it
5. support lightweight reporting by session, task, repo, harness, model, branch, worktree, host, and timeframe
6. enrich sessions with stable repo/worktree metadata using local git inspection when the harness provides a cwd
7. reconcile late-arriving final usage data automatically without requiring the user to rerun commands manually
8. ship with a friction-light install and update story
9. minimize external runtime dependencies

## Non-goals

The first version does not aim to:

1. archive prompts, responses, or tool transcripts
2. ship a daemon or always-on background service
3. introduce a large application or complex CLI UX
4. support every harness immediately
5. compute authoritative pricing for every provider and model
6. require language runtimes such as Python on the target host just to use the product

## Initial scope

Version 1 should support:

1. GitHub Copilot CLI
2. OpenCode

Additional harnesses may be added later if they can map into the same normalized ingestion contract without changing the core storage design.

## Product naming

The recommended working product name is:

**`arok`** — **Agent Resource Observation Kit**

Why this name:

1. short enough for a CLI command and repo name
2. broad enough to cover tokens, cache, reasoning, tool usage, and future agent metrics
3. brandable without locking the product to today's token-centric terminology

Recommended naming:

| Surface | Recommendation |
| --- | --- |
| Product name | `arok` |
| Expanded name | `Agent Resource Observation Kit` |
| CLI command | `arok` |
| Default state dir | `${XDG_STATE_HOME:-$HOME/.local/state}/arok` |
| Candidate repo name | `arok` |

## High-level design

The design has five parts:

1. **Harness hook shims** capture the smallest reliable hook payload the harness can provide.
2. **A local metadata enricher** derives repo/worktree identity from the session cwd using local git inspection.
3. **An ingester** writes normalized usage and session metadata into SQLite.
4. **A post-hook reconciler** upgrades provisional rows when authoritative final usage arrives after the hook returns.
5. **A small CLI** installs hooks, triggers reconciliation, and provides query/analytics commands.

The product should be a **small self-contained CLI/binary** with generated hook shims, not a large application and not a pile of repo-local scripts. The POC proved that a tiny command surface is enough, but packaging the logic as a product is worthwhile.

## Storage location

The canonical state directory is:

`${XDG_STATE_HOME:-$HOME/.local/state}/arok`

Version 1 should also support an explicit override:

`AROK_STATE_DIR=/absolute/path`

The following files are expected:

| Path | Purpose |
| --- | --- |
| `usage.db` | Canonical SQLite database |
| `hooks/` | Generated or example hook fragments per harness |
| `logs/` | Optional local logs for ingestion failures and debugging |

Usage data is **global to the host by default**, not repo-local.

When sessions run inside Multipass instances, containers, or other isolated environments, the preferred deployment is a **shared mounted state directory**. In that case, `AROK_STATE_DIR` should point at a mounted path that is stable across the relevant environments, so usage from guest sessions can be written into the same dataset instead of fragmenting across isolated filesystems.

Rationale:

1. the same task may span multiple repos or worktrees
2. the same repo may be used from multiple sessions and harnesses
3. cross-repo and cross-harness reporting becomes straightforward

Rules:

1. the default remains host-local XDG state storage
2. `AROK_STATE_DIR` takes precedence when set
3. the override path must be absolute
4. records written to a shared mounted directory must include `host_name` so sessions from different instances remain distinguishable
5. shared-state support must not depend on identical mount paths across hosts, because repo identity is already captured separately from filesystem paths

Every session and usage event must still record repo metadata so queries can be narrowed back down to a single project when needed.

## Product packaging and distribution

The production implementation should be packaged as a **single small CLI/binary** with an optional `install.sh` bootstrap flow.

Preferred distribution order:

1. **`install.sh` bootstrap** that downloads the current release binary, places it in a standard user-local location, and sets up PATH/hooks
2. **Direct tarball/binary releases** for manual environments

Rationale:

1. an `install.sh` flow keeps the tool as easy to try as `rtk` and Copilot CLI
2. a single binary keeps security review and dependency sprawl small
3. avoiding snap confinement sidesteps isolation issues that could interfere with reading harness-owned local logs and session data

Version 1 should still keep the implementation simple enough that manual installation remains possible without special packaging.

## CLI inventory

Version 1 should define a small command surface similar to:

| Command | Responsibility |
| --- | --- |
| `arok install copilot` | Install or print Copilot hook configuration and helper shims |
| `arok install opencode` | Install or print OpenCode hook configuration |
| `arok capture --harness <name>` | Ingest one hook payload using the CLI's built-in harness logic |
| `arok reconcile --harness <name>` | Re-read a known session source and upgrade a provisional capture |
| `arok query` | Run focused reports against the database |
| `arok analyze` | Run higher-level usage and savings analytics |
| `arok doctor` | Validate state dir, DB health, hook config, and runtime assumptions |

Rules:

1. the CLI should stay intentionally small
2. hook-time shell shims are allowed, but they should be thin wrappers around the CLI
3. the preferred implementation should avoid requiring Python, Node, or sqlite3 CLI on the target host
4. a static or near-static binary implementation is preferred to minimize security and ops concerns

## Ingestion model

For version 1, the canonical unit of persistence is a **session row**, not a permanently stored stream of usage events.

The working POC showed that the main value comes from maintaining one current session record that may move through states such as:

1. initial provisional capture
2. upgraded capture with authoritative final totals
3. later refresh if the same harness session is resumed and ended again

This keeps the schema small and matches how the current Copilot logic actually works.

Each CLI harness implementation must map raw harness data into:

1. a session identity
2. timestamps
3. token metrics exposed by the harness
4. parent session linkage when available
5. repo and host context
6. optional human-supplied task metadata

If a future harness truly requires durable per-event storage to answer useful questions, that can be added later. It is not required for v1.

For GitHub Copilot CLI specifically, the preferred capture path is:

1. use the `sessionEnd` hook as the canonical trigger
2. read the local session log referenced by that session
3. treat `session.shutdown` as the authoritative overall-session usage record when present
4. treat `subagent.completed` as an optional breakdown source, not the canonical total
5. if `session.shutdown` is not yet available, persist a provisional capture immediately and let reconciliation upgrade it later

For Copilot session resume flows such as `copilot --continue`, `copilot --resume`, or reuse of the same explicit `--session-id`, later captures for the same `session_id` must be treated as updates to the same logical session rather than as new sessions. Each repeated `sessionEnd` capture should therefore re-read the full local session log and upsert the normalized session record so the tracker reflects the latest known usage after the continued work ends.

For Copilot CLI, the shutdown usage record and the context record come from different places:

1. `session.shutdown.modelMetrics` provides the richest final token totals
2. session folder, repo, remote, and branch context should still come from the hook payload plus local git inspection
3. the design must not assume the shutdown event itself includes cwd, repo root, remote, or branch

## Canonical schema

Version 1 only requires one mandatory table:

### `sessions`

One row per logical harness session, updated in place as better information becomes available.

| Column | Type | Meaning |
| --- | --- | --- |
| `session_id` | text primary key | Harness-native session identifier |
| `harness` | text | Source harness, such as `copilot-cli` or `opencode` |
| `parent_session_id` | text nullable | Parent session when the harness exposes it |
| `started_at` | text nullable | Earliest known session timestamp |
| `ended_at` | text nullable | Latest known session timestamp |
| `capture_state` | text | `provisional`, `final`, `partial`, or `unknown` |
| `usage_source` | text | Where the current usage totals came from |
| `stop_reason` | text nullable | Harness stop reason when available |
| `host_name` | text | Local host or VM name |
| `cwd` | text nullable | Working directory seen by the harness |
| `repo_root` | text nullable | Git top-level path when available |
| `worktree_root` | text nullable | Worktree root path when available; often equal to `repo_root` |
| `git_common_dir` | text nullable | Shared git common-dir path for multi-worktree repos when available |
| `repo_remote` | text nullable | Normalized Git remote URL when available |
| `repo_branch` | text nullable | Git branch at capture time when available |
| `repo_head` | text nullable | Full HEAD commit SHA when available |
| `total_input_tokens` | integer nullable | Final or best-known input tokens |
| `total_output_tokens` | integer nullable | Final or best-known output tokens |
| `total_cache_read_tokens` | integer nullable | Final or best-known cache-read tokens |
| `total_cache_write_tokens` | integer nullable | Final or best-known cache-write tokens |
| `total_reasoning_tokens` | integer nullable | Final or best-known reasoning tokens |
| `models_json` | text nullable | Per-model breakdown when the harness exposes it |
| `subagents_json` | text nullable | Supplementary sub-agent breakdown when exposed |
| `task_id` | text nullable | Stable task identifier spanning sessions |
| `title` | text nullable | Human-friendly session title |
| `summary` | text nullable | Short human-friendly session summary |
| `tags_json` | text nullable | JSON array of tags |
| `raw_summary_json` | text nullable | Stored normalized session payload for debugging and migration |
| `updated_at` | text | Last time this row was refreshed |

This is enough for v1 because the working design already updates the session row directly. A separate `session_rollups` table is unnecessary if the session row itself is the canonical rollup, and a separate `usage_events` table is unnecessary unless a harness later forces per-event persistence for correctness or analytics.

## Context capture

The system should collect these metadata fields whenever they are safely available:

1. session ID
2. harness
3. host name
4. working directory
5. git repo root
6. git worktree root
7. git common-dir
8. normalized git remote
9. git branch
10. HEAD commit SHA
11. timestamps
12. optional parent session ID

Human-friendly metadata should be optional and explicit.

Version 1 should support these environment variables:

| Variable | Meaning |
| --- | --- |
| `LLM_USAGE_TASK_ID` | Shared task identifier across sessions |
| `LLM_USAGE_TITLE` | Human-readable title |
| `LLM_USAGE_SUMMARY` | Short summary |
| `LLM_USAGE_TAGS` | Comma-separated tags |
| `AROK_STATE_DIR` | Absolute override for the canonical state directory, intended especially for mounted shared storage |

Rules:

1. if the harness already exposes equivalent fields, prefer the explicit harness value
2. do not infer titles by persisting prompt or response text
3. absence of optional metadata must not block ingestion
4. when `AROK_STATE_DIR` is set, store the resolved path in session metadata or ingestion diagnostics so later troubleshooting can tell where the record was written
5. when `cwd` is available, the CLI should enrich the record with local git inspection from that directory instead of depending on harness-specific repo metadata formats

## Repo identification

Because usage may come from a local machine, container, or VM with the same repo mounted in different paths, repo context must include both path and remote identity when available.

The CLI should resolve repo metadata in this order:

1. `cwd` from the harness payload
2. `repo_root` / `worktree_root` from `git rev-parse --path-format=absolute --show-toplevel`
3. `git_common_dir` from `git rev-parse --path-format=absolute --git-common-dir`
4. `repo_remote` from the primary fetch remote, normalized to a stable URL form
5. `repo_branch` from the current symbolic HEAD if available
6. `repo_head` from `git rev-parse HEAD`

If git metadata is unavailable, ingestion should still succeed with `cwd` and `host_name`.

This separation is important for shared mounted state directories: repo path differences between a host and a guest instance must not prevent grouping records that belong to the same repository remote.

## Sub-agent handling

Sub-agent tracking is useful when the harness exposes enough metadata to support it, but version 1 should keep the model minimal.

Rules:

1. record `parent_session_id` when the harness exposes it
2. if the harness only exposes sub-agent totals as part of a parent session log, store that breakdown as supplementary session metadata rather than inventing extra required tables
3. if a harness does not expose parent/child linkage, ingest the usage without guessing

For Copilot CLI, this means:

1. overall usage should come from the parent session's `session.shutdown` totals
2. sub-agent breakdown should be treated as supplementary when `subagent.completed` data is present in the same session log
3. the design must not assume hook-time `subagentStop` payloads carry full token accounting

## Hook integration

Each supported harness requires harness-specific parsing logic, but that logic should live **inside the `arok` CLI**, not in a separate external adapter process.

The hook contract is:

1. capture the raw usage payload from the hook environment or hook input
2. invoke `arok capture --harness <name> --event <type>` with that raw payload

The CLI contract is:

1. parse the harness-specific payload
2. determine the session identity and timestamps
3. gather repo and host metadata
4. write or update the canonical session row

The hook set should stay as small as the harness allows while still capturing the canonical metrics.

For Copilot CLI version 1:

1. prefer `sessionEnd` as the primary hook because it aligns with `session.shutdown`
2. do not require `sessionStart` for normal accounting
3. do not depend on `agentStop` for canonical usage totals
4. do not depend on `subagentStop` for canonical usage totals, because the richer overall totals are better captured from the parent session log
5. treat repeated `sessionEnd` captures for the same Copilot `session_id` as idempotent upserts, so resumed sessions refresh the stored summary instead of creating duplicates
6. if raw hook captures are also stored for debugging, they may remain append-only, but the normalized session summary must still converge to one latest view per `session_id`
7. after `sessionEnd`, allow a short retry window before falling back, because `session.shutdown.modelMetrics` may land in the local session log slightly after the hook fires
8. if fallback totals are still used at hook time, schedule an autonomous post-hook reconciliation step that re-reads the same session log and upgrades the stored row once `session.shutdown` is persisted
9. prefer a detached reconcile command over long hook blocking, because at least one real session wrote `session.shutdown` only after the hook completed

The install command should either:

1. install the necessary hook configuration automatically, or
2. print the exact configuration snippets the user must add manually

The spec does not require fully automatic editing of third-party config files if that is brittle. Correctness and clarity are more important than automation here.

## Query and analytics behavior

The CLI should support focused reporting and lightweight analytics rather than a sprawling command tree.

The initial report modes should cover:

1. totals for a session ID
2. totals grouped by task ID
3. totals grouped by repo
4. totals grouped by branch
5. totals grouped by worktree
6. totals grouped by harness
7. totals grouped by model
8. totals over a time window

Version 1 should also provide analytics-oriented commands inspired by token-usage discovery workflows such as:

1. cache-read and cache-write analysis
2. prompt-cache savings estimates where the harness exposes enough information
3. highest-usage repos, branches, worktrees, and models
4. provisional vs reconciled capture counts
5. sub-agent share of total usage when available
6. sessions missing final authoritative totals

The requirement is useful discovery and reporting, not a rich command hierarchy.

## Failure handling

Ingestion failures must be visible and recoverable.

Rules:

1. malformed payloads should fail loudly and emit an actionable error
2. database write failures should not silently discard data
3. the CLI may log failures under `logs/`, but it must not pretend ingestion succeeded
4. duplicate event IDs should be handled idempotently
5. if a session is captured provisionally and reconciliation fails, that failure must be visible in logs and queryable in diagnostics

## Privacy and retention

The tracker is for usage accounting, not transcript storage.

Rules:

1. do not store full prompts by default
2. do not store full model responses by default
3. do not store tool input/output payloads by default
4. raw JSON retained in the database should be limited to usage and metadata payloads required for debugging or schema migration

If a future version introduces richer capture, it must be an explicit opt-in.

## Rollout plan

Implementation should proceed in this order:

1. finalize product name, command name, and state-dir naming
2. implement the single-binary CLI with SQLite storage and shared ingest path
3. add GitHub Copilot CLI hook support centered on `sessionEnd`, metadata enrichment, and autonomous reconciliation
4. add query and analytics commands
5. add OpenCode support through a separate CLI harness implementation that maps into the same normalized session schema
6. verify overall session totals from Copilot CLI `session.shutdown`
7. verify optional sub-agent breakdown against at least one parent session containing `subagent.completed` events
8. add the `install.sh` bootstrap flow and direct binary release packaging

## Deployment and update behavior

The preferred deployment model is:

1. install the `arok` binary in a standard user-local location such as `~/.local/bin/`
2. generate harness hook config that invokes that installed binary
3. keep the shared mounted state directory focused on data, logs, and reconciliation artifacts

That implies:

1. the harness-owned config location such as `~/.copilot/hooks/` can stay a small stable stub that points at the installed `arok` binary plus the chosen shared state dir
2. the mounted state directory should not need to contain the product binary itself
3. updates should replace the installed binary in its standard location rather than copying runnable logic into the shared state directory
4. rerunning the installer or updater should refresh both the installed binary and the generated hook config without wiping collected state

Version 1 does not need a background self-updater. Re-running `install.sh` is sufficient as long as that behavior is documented clearly.

# Further Information

## Why SQLite over JSON or markdown

JSON files or markdown notes are easy to write, but they are the wrong primary interface for the actual questions this initiative needs to answer. The queries are relational and aggregate-heavy.

SQLite keeps the system local and lightweight while making these questions trivial:

1. "show token totals for repo X over the last 7 days"
2. "compare Copilot CLI and OpenCode usage this month"
3. "sum usage for task ABC across three sessions and two hosts"

## Why a small CLI/binary instead of only scripts

The POC proved the workflow is valuable enough to justify a small product, but not a large one.

A compact CLI/binary is the right middle ground because it can:

1. install hooks and helper shims consistently
2. ingest and reconcile captures without extra host runtimes
3. run focused analytics commands
4. package cleanly as a downloadable binary with a simple `install.sh` flow

The key is to keep the implementation and dependency surface small even if the distribution becomes more polished.

## POC findings that should shape the full implementation

The Copilot POC surfaced several behavior details that should be preserved in the full implementation:

1. `session.shutdown.modelMetrics` is the best authoritative total when it is present
2. `session.shutdown` does **not** carry enough repo-context metadata by itself, so context capture must continue to use hook payloads and local git inspection
3. `subagent.completed` is useful for supplementary breakdown, but not as the canonical overall total
4. repeated captures for the same Copilot `session_id` are expected and must upsert rather than duplicate
5. `sessionEnd` can race slightly ahead of final `session.shutdown.modelMetrics` persistence, so the ingester should retry briefly before accepting a weaker fallback
6. in at least one real guest session, `session.shutdown` was written only after the `sessionEnd` hook completed, so a post-hook asynchronous reconcile step is more reliable than stretching the in-hook wait indefinitely
7. a shared mounted state directory is a practical deployment pattern, but the installed binary and hook config should be refreshed independently from the collected state
8. `cwd` plus local git inspection is a simple and harness-agnostic way to capture high-value repo, branch, worktree, and commit metadata

## Deferred items

The following are intentionally deferred:

1. a local pricing table for estimated costs when the harness does not provide cost
2. automatic task-title inference from prompts or transcripts
3. non-local sync to a remote service
4. dashboards or web UIs

# Spec History and Changelog

| Author(s) | Status | Date | Comment |
| :---- | :---- | :---- | :---- |
| Samuel Bouffard, Copilot | Drafting | 2026-06-17 | Reworked the spec around the proven Copilot POC: productized as `arok`, added low-dependency CLI packaging direction, autonomous post-hook reconciliation, git-based metadata enrichment, and analytics/reporting goals. |
| Samuel Bouffard, Copilot | Drafting | 2026-06-16 | Rebuilt the brainstorm into a script-first tracking spec with SQLite storage, normalized usage events, and initial hook/query scripts. |
