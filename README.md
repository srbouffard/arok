# arok

**Agent Resource Observation Kit** — a local-first CLI for capturing and querying LLM and agent usage.

Version 1 provides production-ready **GitHub Copilot** support across the CLI and VS Code with:

1. Automatic Copilot CLI session-end capture via hooks
2. Automatic VS Code Copilot stop-hook capture plus historical session import
3. SQLite-backed local storage for all usage data
4. Git metadata enrichment (repo, branch, commit)
5. Idempotent session tracking for resumed sessions
6. Autonomous reconciliation for late-arriving usage totals
7. Focused query and analytics commands

## Features

The CLI provides:

| Command | Purpose |
| --- | --- |
| `arok install copilot` | Install Copilot hook configuration |
| `arok capture` | Ingest hook payloads (called automatically by hooks) |
| `arok reconcile` | Background reconciliation (called automatically by capture) |
| `arok query` | Session and grouped usage reports |
| `arok analyze` | Usage analytics and diagnostics |
| `arok doctor` | Validate installation and database health |

## Installation

**Option 1: Install from GitHub releases** (recommended for users):

```bash
curl -fsSL https://raw.githubusercontent.com/srbouffard/arok/main/install.sh | bash
```

This downloads the latest pre-built binary for your platform.

**Option 2: Build from source** (for developers):

```bash
git clone https://github.com/srbouffard/arok.git
cd arok
./install.sh --from-source
```

Requires Go 1.26+ to build.

**Add to PATH** (if not already present):

```bash
# Add to ~/.bashrc or ~/.zshrc
export PATH="$HOME/.local/bin:$PATH"

# Or for current session only
export PATH="$HOME/.local/bin:$PATH"
```

**Then configure Copilot CLI hooks:**

```bash
arok install copilot
```

The `install copilot` command:
- Creates hook configuration at `~/.copilot/hooks/arok-copilot.json`
- Installs hooks for both Copilot CLI (`sessionEnd`) and VS Code (`Stop`)
- Initializes the state directory and SQLite database
- Validates that the Copilot CLI will invoke the hooks

To import existing VS Code Copilot sessions:

```bash
arok capture --harness vscode --event scan
```

**Installation options:**
- `arok install copilot --state-dir PATH` — Override state directory
- `arok install copilot --copilot-home PATH` — Override Copilot home directory

**To update:** Re-run the installation command to get the latest version.

## State directory

Default location:
```bash
${XDG_STATE_HOME:-$HOME/.local/state}/arok
```

Override with:
```bash
export AROK_STATE_DIR=/absolute/path
```

Contents:

| Path | Purpose |
| --- | --- |
| `usage.db` | SQLite database with all session data |
| `hooks/` | Generated hook configurations |
| `logs/` | Capture events and error logs |
| `reconcile/` | Temporary files for background reconciliation |

Shared mounted directories are supported for multi-host deployments.

## How it works

When a Copilot session ends, the installed hook automatically captures:

1. Session ID and timestamps
2. Token usage (input, output, cache, reasoning)
3. Model and sub-agent breakdowns
4. Git context (repo, branch, commit)
5. Tool execution counts

The CLI:
- Reads Copilot's local `events.jsonl` session log
- Enriches with git metadata from the working directory
- Stores normalized data in SQLite
- Handles late-arriving usage totals via background reconciliation

Resumed sessions (via `--continue` or `--resume`) update the same logical session record.

## Usage examples

List recent sessions:
```bash
arok query sessions --latest 10
```

Show session details:
```bash
arok query sessions --session-id <session-id>
```

Usage by repository (last 7 days):
```bash
arok query repos --since 168h
```

Model breakdown (last 24 hours):
```bash
arok query models --since 24h
```

Overview analytics:
```bash
arok analyze overview --since 168h
```

Check installation:
```bash
arok doctor
```

## Development

Build and test:
```bash
make build    # Build binary
make test     # Run tests
make lint     # Run linters
make check    # Full verification
```

## Design

`arok` is built with:
- **Go** — single-binary CLI with minimal dependencies
- **modernc.org/sqlite** — pure-Go SQLite driver (no cgo)
- **Local-first** — all data stays on your machine
- **Hook-driven** — automatic capture, no manual tracking

The POC in `poc/` contains the validated JavaScript prototype that informed this implementation.

## Roadmap

**Version 2** will add:
- OpenCode harness support
- Binary release downloads
- Prompt-cache savings estimates

## License

See LICENSE file for details.
