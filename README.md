# arok

`arok` is a local-first CLI for capturing and querying LLM and agent usage across harnesses. The first production slice in this repo supports **GitHub Copilot CLI** with:

1. `sessionEnd`-driven capture
2. SQLite as the canonical local store
3. git metadata enrichment from `cwd`
4. idempotent session upserts for resumed Copilot sessions
5. detached post-hook reconciliation when `session.shutdown.modelMetrics` lands after the hook returns

## Current scope

This repository now contains a production-oriented Go implementation for the first Copilot vertical slice and the validated JavaScript POC under `poc/`.

The production CLI currently provides:

| Command | Purpose |
| --- | --- |
| `arok install copilot` | Generate and install the Copilot hook config that points at the installed `arok` binary |
| `arok capture --harness copilot --event sessionEnd` | Ingest one Copilot hook payload from stdin or a file |
| `arok reconcile --harness copilot` | Retry a known session log to upgrade provisional captures |
| `arok query ...` | Focused session and grouped usage reports |
| `arok analyze ...` | Lightweight diagnostics and usage discovery |
| `arok doctor` | Validate state and Copilot hook installation |

## Build and test

`Makefile` is the main entrypoint:

```bash
make build
make test
make lint
make fmt
make check
```

## Install from this checkout

The repo bootstrap flow builds the Go binary from source and installs it to `~/.local/bin` by default.

```bash
./install.sh
```

Useful options:

```bash
./install.sh --state-dir /absolute/shared/arok-state
./install.sh --prefix "$HOME/.local/bin" --copilot-home "$HOME/.copilot"
./install.sh --no-copilot
```

Re-running `install.sh` is the supported update path for this checkout: it refreshes the installed binary and rewrites the Copilot hook config without wiping collected state.

## State layout

By default `arok` uses:

```bash
${XDG_STATE_HOME:-$HOME/.local/state}/arok
```

You can override that with:

```bash
AROK_STATE_DIR=/absolute/path
```

The production slice writes:

| Path | Purpose |
| --- | --- |
| `usage.db` | Canonical SQLite session store |
| `hooks/` | Generated hook config fragments |
| `logs/` | Hook capture log, ingest failures, and reconcile logs |
| `reconcile/` | Temporary payload snapshots for detached reconciliation |

Mounted/shared state directories are supported as long as the override path is absolute.

## Copilot flow

The installed Copilot hook runs:

```bash
arok capture --harness copilot --event sessionEnd
```

`arok` then:

1. parses the raw hook payload
2. resolves the local Copilot `events.jsonl` session log
3. enriches repo metadata from `cwd` using local git inspection
4. persists a normalized session row in SQLite
5. briefly retries for `session.shutdown.modelMetrics`
6. if needed, schedules detached reconciliation to upgrade the same session row later

Authoritative overall totals come from `session.shutdown.modelMetrics` when present. If they are not visible yet, `arok` stores a provisional fallback based on assistant-message output tokens and later upgrades that same logical session.

## Query examples

```bash
arok query sessions --latest 10
arok query sessions --session-id <session-id>
arok query repos --since 168h
arok query models --since 24h
arok analyze overview --since 168h
arok analyze missing-finals
arok doctor
```

## Notes

1. The first production slice is intentionally small and low-dependency.
2. The Copilot implementation keeps harness-specific parsing inside the CLI rather than in external adapter scripts.
3. The POC remains in `poc/` as the validated reference for the behavior this implementation preserves.
