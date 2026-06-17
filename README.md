# arok

**Agent Resource Observation Kit** — a local-first CLI for capturing and querying LLM and agent usage.

`arok` automatically captures **GitHub Copilot** usage across the CLI and VS Code, storing everything in a local SQLite database with git metadata enrichment. No accounts, no telemetry, no cloud.

## Features

| Command | Purpose |
| --- | --- |
| `arok install copilot` | Install Copilot hook configuration |
| `arok capture` | Ingest hook payloads (called automatically by hooks) |
| `arok reconcile` | Background reconciliation (called automatically by capture) |
| `arok query` | Session and grouped usage reports |
| `arok analyze` | Usage analytics and diagnostics |
| `arok doctor` | Validate installation and database health |

**What gets captured:**

- Session ID and timestamps
- Token usage (input, output, cache read/write, reasoning)
- Model and sub-agent breakdowns per session
- Git context (repo, branch, commit) from the working directory
- Tool execution counts

## Installation

**Install from GitHub releases:**

```bash
curl -fsSL https://raw.githubusercontent.com/srbouffard/arok/main/install.sh | bash
```

**Add to PATH** (if not already present):

```bash
# Add to ~/.bashrc or ~/.zshrc
export PATH="$HOME/.local/bin:$PATH"
```

**Configure Copilot hooks:**

```bash
arok install copilot
```

This creates `~/.copilot/hooks/arok-copilot.json` with hooks for both Copilot CLI (`sessionEnd`) and VS Code (`Stop`), initializes the state directory, and opens the SQLite database.

**Import existing VS Code sessions:**

```bash
arok capture --harness vscode --event scan
```

**Build from source** (requires Go 1.26+):

```bash
git clone https://github.com/srbouffard/arok.git
cd arok
./install.sh --from-source
```

**Update:** Re-run the install command to get the latest release.

## Usage

List recent sessions:
```bash
arok query sessions --latest 20
```

Show full session detail:
```bash
arok query sessions --session-id <session-id>
```

Usage by repository:
```bash
arok query repos --since 168h
```

Model breakdown:
```bash
arok query models --since 24h
```

Overview analytics:
```bash
arok analyze overview --since 168h
```

Check installation health:
```bash
arok doctor
```

## How it works

### Copilot CLI

When a Copilot CLI session ends, the `sessionEnd` hook fires. arok reads the local `events.jsonl` session log, extracts token totals and model usage, enriches with git metadata, and writes a normalized record to SQLite. If usage totals aren't available yet (common when the session ends before the model finishes reporting), a background reconcile process retries until the data arrives.

### VS Code Copilot

The `Stop` hook fires after each turn. arok reads the session's `chatSessions` JSONL file from VS Code's local workspace storage, replaying the patch-log to reconstruct the final session state with accurate token counts. Workspace folder metadata maps to git context.

Both harnesses write to the same database with the same schema — `arok query` reports across all of them uniformly.

### Resumed sessions

Sessions continued via `--continue` or `--resume` update the same logical record. The session ID is stable across continuations.

## State directory

Default location:
```bash
${XDG_STATE_HOME:-$HOME/.local/state}/arok
```

Override with:
```bash
export AROK_STATE_DIR=/absolute/path
```

| Path | Purpose |
| --- | --- |
| `usage.db` | SQLite database with all session data |
| `hooks/` | Generated hook configurations |
| `logs/` | Capture events and error logs |
| `reconcile/` | Temporary files for background reconciliation |

Shared mounted directories are supported for multi-host deployments.

## Development

```bash
make build    # Build binary
make test     # Run tests
make lint     # Run linters
make check    # Full verification (fmt + vet + test + build)
```

## Design

- **Go** — single-binary CLI with no cgo dependencies
- **modernc.org/sqlite** — pure-Go SQLite driver
- **Local-first** — all data stays on your machine
- **Hook-driven** — automatic capture, no manual tracking

## License

See LICENSE file for details.
