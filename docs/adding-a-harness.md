# Adding a New Harness to arok

A **harness** is an AI agent tool that fires hooks at the end of a session — for example,
GitHub Copilot CLI, VS Code Copilot, Hermes, or OpenCode. This guide explains the concepts
you need to understand and the files you need to create. For concrete code structure, refer
to the existing harness implementations as living examples.

## How capture works

When an agent session ends, the harness fires a hook that runs:

```
arok capture --harness <name> --event <event>
```

arok receives a JSON payload from the hook, parses it, reads any additional session data
from the tool's own files or APIs, assembles a `session.SessionSummary`, and writes it to
the SQLite database.

Your job as a harness author is to implement that payload→summary translation for your tool.

## File structure

Each harness lives in two places:

```
internal/<harness>/            ← payload parsing and session summarizing logic
internal/cli/app_<harness>.go  ← CLI plumbing: runCapture<Harness>() and helpers
```

**Read the copilot harness before writing any code.** It is the canonical, fully-tested
reference implementation:

```
internal/copilot/copilot.go          ← Summarize(), ParsePayload(), ResolveSessionFile()
internal/cli/app_copilot.go          ← runCaptureCopilot(), runReconcile(), and helpers
internal/cli/app_copilot_test.go     ← integration tests via App.Run()
internal/copilot/testdata/           ← fixture JSONL files used by unit tests
```

The VS Code harness (`internal/vscode/`, `internal/cli/app_vscode.go`) is a simpler
example without reconcile logic — useful if your harness delivers complete data at hook time.

## Key concepts

### The hook payload may not contain everything you need

Most harnesses pass only a thin payload (session ID, working directory, maybe an event
name). The real metrics — token counts, model names, tool calls — often live in a separate
log file or API that the tool writes during the session.

Use the session ID and working directory from the payload as keys to locate and read that
richer data source. See how `ResolveSessionFile` in `internal/copilot/copilot.go` uses the
`COPILOT_HOME` environment variable and the session ID to find the right `events.jsonl`
file.

### Final metrics may not be available when the hook fires

Some tools emit usage metrics asynchronously — the hook fires to signal session end, but the
final token count is written to disk only moments later (or is computed by a background
process). If you try to read the metrics immediately, you will get incomplete data.

**Do not block the hook.** The hook runner expects a fast exit. Instead:

1. Capture whatever is available immediately and store it with
   `CaptureState: "provisional"`.
2. Spawn a short-lived background process (`arok reconcile --harness <name>`) that polls
   until the final data appears, then upgrades the record to `CaptureState: "final"`.
3. If the background process exhausts its retries without finding complete data (e.g. the
   process was killed), mark the record `CaptureState: "best_effort"` so the data is still
   usable but clearly flagged as incomplete.

This is exactly what the copilot harness does — see `spawnDetachedReconcile` and
`runReconcile` in `internal/cli/app_copilot.go`.

If your tool delivers complete metrics synchronously at hook time, you can skip all of this
and always return `CaptureState: "final"`. The VS Code harness is an example of this
simpler path.

### Use git metadata to enrich sessions

The hook payload rarely includes repository context. Use `gitmeta.Inspect(cwd)` (see
`internal/gitmeta/`) to populate `RepoRoot`, `RepoBranch`, and `RepoRemote` on the
`SessionSummary` from the working directory that the payload provides.

## Adding your harness: the steps

1. **Create `internal/<harness>/`** — implement `Summarize()`, which takes the parsed
   payload and returns a `session.SessionSummary`. Model it on
   `internal/copilot/copilot.go`.

2. **Create `internal/cli/app_<harness>.go`** — implement `runCapture<Harness>()` as a
   method on `*App`. It reads the payload, calls `Summarize()`, opens the store, and calls
   `db.UpsertSession()`. Model it on `internal/cli/app_copilot.go` (or the simpler
   `app_vscode.go` if you don't need reconcile).

3. **Wire the dispatcher** — add a `case "<harness>":` to the `switch` in `runCapture()`
   in `internal/cli/app.go`. That single line is the only change needed in shared code.

4. **Update the usage string** — add your harness name to the `--harness` list in
   `printRootUsage()` in `app.go`.

5. **Add `arok install` support (optional)** — if your tool uses a config file that arok
   can write, follow the pattern in `runInstallCopilot` / `internal/install/copilot.go`.

## Writing tests

**Unit tests** — `internal/<harness>/<harness>_test.go`

Test `Summarize()` directly using fixture files in `internal/<harness>/testdata/`. Your
fixture files are also living documentation of what the raw session data looks like — make
them representative. See `internal/copilot/copilot_test.go` and its testdata for the
pattern.

**Integration tests** — `internal/cli/app_<harness>_test.go`

Test the end-to-end path through `App.Run()`. Every code path should be covered: a final
capture, a provisional capture (if applicable), and the failure cases (missing session ID,
unknown event, etc.). See `internal/cli/app_copilot_test.go` for the full pattern.

Run `make check` before opening a PR. All tests must pass.

## Checklist

- [ ] `internal/<harness>/<harness>.go` — `Summarize()` returns a valid `SessionSummary`
- [ ] `internal/cli/app_<harness>.go` — `runCapture<Harness>()` method on `*App`
- [ ] `internal/cli/app.go` — one `case` added to the `runCapture()` switch
- [ ] `internal/<harness>/<harness>_test.go` — unit tests for `Summarize()`
- [ ] `internal/<harness>/testdata/` — fixture files covering final and (if needed) provisional states
- [ ] `internal/cli/app_<harness>_test.go` — integration tests via `App.Run()`
- [ ] `make check` passes
