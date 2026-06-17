# SPEC.md V1 Validation Report

## Executive Summary

The implementation **SUBSTANTIALLY COMPLIES** with the SPEC.md V1 requirements but has **ONE CRITICAL GAP**: OpenCode support is not implemented. This is explicitly listed in the spec as part of V1 scope (line 56-60).

## Detailed Validation

### ✅ FULLY IMPLEMENTED

#### Goals (Lines 29-42)
- ✅ Capture usage from supported harnesses automatically via hooks (Copilot)
- ✅ Normalize payloads into common schema
- ✅ Persist data locally in SQLite
- ✅ Preserve parent/child linkage for sub-agents
- ✅ Support lightweight reporting by session, task, repo, harness, model, branch, worktree, host, timeframe
- ✅ Enrich sessions with stable repo/worktree metadata using local git inspection
- ✅ Reconcile late-arriving final usage data automatically
- ✅ Ship with friction-light install and update story
- ✅ Minimize external runtime dependencies (pure Go + modernc.org/sqlite)

#### Non-goals (Lines 43-52)
- ✅ Does NOT archive prompts/responses/transcripts
- ✅ Does NOT ship daemon or always-on service
- ✅ Kept CLI small and intentional
- ✅ Does NOT require Python/Node on target host
- ✅ Does NOT compute authoritative pricing

#### Product Naming (Lines 63-83)
- ✅ Product name: `arok`
- ✅ CLI command: `arok`
- ✅ Default state dir: `${XDG_STATE_HOME:-$HOME/.local/state}/arok` (config/state.go:10-16)
- ✅ Expanded name documented

#### High-level Design (Lines 85-95)
- ✅ Harness hook shims (install/copilot.go generates hooks)
- ✅ Local metadata enricher (gitmeta/gitmeta.go)
- ✅ Ingester writes to SQLite (store/store.go)
- ✅ Post-hook reconciler (cli/app.go:runReconcile + detached spawn)
- ✅ Small CLI (cli/app.go)

#### Storage Location (Lines 97-133)
- ✅ Default: `${XDG_STATE_HOME:-$HOME/.local/state}/arok`
- ✅ Override: `AROK_STATE_DIR` environment variable
- ✅ Absolute path requirement enforced (config/state.go:19-22)
- ✅ Records include `host_name` for multi-instance deployments
- ✅ Files: `usage.db`, `hooks/`, `logs/`, `reconcile/` (config/state.go:38-49)

#### Product Packaging (Lines 135-150)
- ✅ Single binary CLI
- ✅ `install.sh` bootstrap from checkout
- ⚠️ PARTIAL: install.sh builds from source, does NOT download release binary yet (future enhancement)
- ✅ No Python/Node/sqlite3 CLI required

#### CLI Inventory (Lines 152-165)
- ✅ `arok install copilot`
- ❌ **MISSING**: `arok install opencode`
- ✅ `arok capture --harness copilot`
- ✅ `arok reconcile --harness copilot`
- ✅ `arok query [sessions|repos|branches|worktrees|harnesses|tasks|models]`
- ✅ `arok analyze [overview|missing-finals]`
- ✅ `arok doctor`

#### Ingestion Model (Lines 173-211)
- ✅ Session-row persistence (not event stream)
- ✅ Provisional → final → refresh states
- ✅ `sessionEnd` as canonical trigger
- ✅ `session.shutdown` as authoritative source
- ✅ `subagent.completed` as optional breakdown
- ✅ Repeated `sessionEnd` upserts same session (store/store.go:79-95)

#### Canonical Schema (Lines 212-251)
- ✅ `sessions` table with all required columns
- ✅ `capture_state` field (provisional/final)
- ✅ Nullable token fields
- ✅ `models_json` and `subagents_json` for breakdowns
- ✅ `task_id`, `title`, `summary`, `tags_json` optional metadata
- ✅ `raw_summary_json` for debugging/migration
- ⚠️ NOTE: POC had separate `subagent_summaries` table, production stores in JSON (acceptable simplification)

#### Context Capture (Lines 254-289)
- ✅ All metadata fields collected
- ✅ Environment variables supported:
  - ✅ `LLM_USAGE_TASK_ID` (cli/app.go:727)
  - ✅ `LLM_USAGE_TITLE` (cli/app.go:728)
  - ✅ `LLM_USAGE_SUMMARY` (cli/app.go:729)
  - ✅ `LLM_USAGE_TAGS` (cli/app.go:730)
  - ✅ `AROK_STATE_DIR` (config/state.go:9-13)
- ✅ State dir stored in session metadata

#### Repo Identification (Lines 291-306)
- ✅ git enrichment from `cwd`
- ✅ `repo_root`, `worktree_root`, `git_common_dir` captured
- ✅ Remote URL normalization (gitmeta/gitmeta.go:49-82)
- ✅ Branch and HEAD commit captured
- ✅ Ingestion succeeds even without git metadata

#### Sub-agent Handling (Lines 308-323)
- ✅ `parent_session_id` supported in schema
- ✅ Sub-agent totals stored as supplementary metadata
- ✅ `subagent.completed` events parsed (copilot/copilot.go:156-168)
- ✅ Does not depend on `subagentStop` hooks

#### Hook Integration (Lines 324-359)
- ✅ Harness logic inside CLI (copilot/copilot.go)
- ✅ `sessionEnd` primary hook
- ✅ Does NOT require sessionStart
- ✅ Does NOT depend on agentStop/subagentStop
- ✅ Repeated captures upsert (store/store.go:79-95)
- ✅ Retry window for shutdown metrics (cli/app.go:747-762, configurable via env)
- ✅ Detached reconciliation (cli/app.go:771-808)
- ✅ Install generates config automatically (install/copilot.go:21-56)

#### Query and Analytics (Lines 361-385)
- ✅ Totals for session ID
- ✅ Totals grouped by task ID
- ✅ Totals grouped by repo
- ✅ Totals grouped by branch
- ✅ Totals grouped by worktree
- ✅ Totals grouped by harness
- ✅ Totals grouped by model
- ✅ Totals over time window (--since flag)
- ✅ Cache analysis (reports cache_read/cache_write tokens)
- ⚠️ PARTIAL: Savings estimates not yet implemented (acceptable - data present for future enhancement)
- ✅ Highest-usage repos/branches/models
- ✅ Provisional vs final counts (analyze overview)
- ✅ Sessions missing final totals (analyze missing-finals)

#### Failure Handling (Lines 387-398)
- ✅ Malformed payloads fail loudly
- ✅ Database write failures propagate
- ✅ Failures logged to `logs/ingest-errors.log` and `logs/reconcile.log`
- ✅ Duplicate session IDs handled idempotently (UPSERT)
- ✅ Reconcile failures logged and visible

#### Privacy and Retention (Lines 399-410)
- ✅ Does NOT store prompts by default
- ✅ Does NOT store model responses by default
- ✅ Does NOT store tool payloads by default
- ✅ Only stores normalized usage and metadata in `raw_summary_json`

#### Deployment and Update (Lines 425-440)
- ✅ Binary installed to user-local location (~/.local/bin by default)
- ✅ Generated hook config invokes installed binary
- ✅ State directory separate from binary
- ✅ Hook config points at binary + state dir
- ✅ Re-running installer refreshes binary and hook config
- ✅ Does NOT wipe collected state on reinstall

#### POC Findings Preservation (Lines 467-479)
- ✅ `session.shutdown.modelMetrics` as authoritative
- ✅ Context from hook payload + git inspection
- ✅ `subagent.completed` for breakdown only
- ✅ Repeated captures upsert
- ✅ Brief retry before fallback (configurable)
- ✅ Async reconcile for late shutdown
- ✅ Shared mounted state supported
- ✅ Git inspection from cwd

### ❌ NOT IMPLEMENTED

#### Initial Scope (Lines 54-61)
- ❌ **CRITICAL GAP**: OpenCode support missing
  - Spec explicitly lists "Version 1 should support: 1. GitHub Copilot CLI 2. OpenCode"
  - Only Copilot implemented

#### CLI Inventory
- ❌ `arok install opencode` - not present
- ❌ `arok capture --harness opencode` - harness check rejects non-copilot

#### Analytics
- ⚠️ Prompt-cache savings estimates - data present but no calculation logic yet (low priority)

### ⚠️ MINOR DEVIATIONS (Acceptable)

1. **Subagent storage**: POC used separate `subagent_summaries` table, production stores in `subagents_json`. This is a reasonable simplification since sub-agents are supplementary.

2. **install.sh**: Currently builds from source instead of downloading release binary. This is acceptable for V1 since the spec says "simple enough that manual installation remains possible" and building from source is documented.

3. **Savings estimates**: Cache token data is captured but savings calculation not implemented. Data is present for future enhancement.

## Recommendation

### For V1 Release
**BLOCK on OpenCode support** OR **update SPEC.md** to defer OpenCode to V2 if product direction has changed.

Current implementation is otherwise production-ready for Copilot-only V1 with:
- Complete Copilot vertical slice
- All core functionality working
- Tests passing
- Documentation complete
- Install/update flow working

### Priority Order
1. **MUST**: Decide on OpenCode for V1 (implement or defer)
2. **SHOULD**: Add release binary download to install.sh (can be post-V1)
3. **NICE**: Add savings estimate calculations (can be post-V1)

## Testing Coverage

The implementation was validated with:
- ✅ Unit tests for all packages
- ✅ End-to-end Copilot capture test
- ✅ Large JSONL line handling test
- ✅ Time-scoped analytics test
- ✅ Session upsert test
- ✅ `make check` passes

## Conclusion

The implementation is **high-quality and production-ready for Copilot**. The only material gap is OpenCode support, which the spec explicitly lists as V1 scope. If OpenCode support has been deferred to V2, the spec should be updated accordingly and the current implementation can be released as V1.
