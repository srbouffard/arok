# arok

**Agent Resource Observation Kit** — a local-first CLI for capturing and querying LLM and agent usage.

Version 1 provides production-ready **GitHub Copilot CLI** support with:

1. Automatic session-end capture via Copilot hooks
2. SQLite-backed local storage for all usage data
3. Git metadata enrichment (repo, branch, commit)
4. Idempotent session tracking for resumed sessions
5. Autonomous reconciliation for late-arriving usage totals
6. Focused query and analytics commands

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

Install from this repository:

```bash
./install.sh
```

This builds the binary from source and installs to `~/.local/bin` by default.

Then configure Copilot CLI hooks:

```bash
arok install copilot
```

The `install copilot` command:
- Creates hook configuration at `~/.copilot/hooks/arok-copilot.json`
- Initializes the state directory and SQLite database
- Validates that the Copilot CLI will invoke the hooks

Options:
- `install.sh --prefix DIR` — Install directory (default: `~/.local/bin`)
- `arok install copilot --state-dir PATH` — Override state directory
- `arok install copilot --copilot-home PATH` — Override Copilot home directory

To update: re-run `install.sh` to refresh the binary. Hook config persists across updates.

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
