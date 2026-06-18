# Adding a New Harness to arok

A **harness** is an AI agent tool that fires hooks at the end of a session — for example,
GitHub Copilot CLI, VS Code Copilot, Hermes, or OpenCode. This guide walks you through
adding support for a new harness.

## Overview

The capture pipeline for every harness looks the same:

```
agent tool fires hook
  → runs: arok capture --harness <name> --event <event>
    → parses the hook payload
    → reads session data from the harness-specific source
    → builds a session.SessionSummary
    → stores it in the arok SQLite database
```

Adding a harness means implementing steps 2–4 for your tool.

## File structure

The codebase organises harness-specific code into two places:

```
internal/<harness>/         ← payload parsing and session summarizing logic
internal/cli/app_<harness>.go  ← CLI plumbing: runCapture<Harness>(), helpers
```

Look at the existing copilot harness for reference:

```
internal/copilot/copilot.go          ← Summarize(), ParsePayload(), ResolveSessionFile()
internal/cli/app_copilot.go          ← runCaptureCopilot(), runReconcile(), etc.
internal/cli/app_copilot_test.go     ← integration tests
internal/copilot/testdata/           ← fixture files for unit tests
```

## Step-by-step: adding "myharness"

### 1. Create the parsing package

Create `internal/myharness/myharness.go`. Its job is to convert a raw hook payload
and whatever session log/API the tool provides into a `session.SessionSummary`.

```go
package myharness

import (
    sessionpkg "github.com/srbouffard/arok/internal/session"
    "time"
)

// Payload holds the data delivered by the hook runner.
type Payload struct {
    SessionID string `json:"session_id"`
    CWD       string `json:"cwd"`
    // ... add fields from your tool's hook payload
}

// Summarize converts payload and session data into a SessionSummary.
func Summarize(eventName, stateDir string, p Payload) (sessionpkg.SessionSummary, error) {
    // Read session data from wherever your tool stores it.
    // Build and return a SessionSummary.
    return sessionpkg.SessionSummary{
        SchemaVersion: 1,
        Source:        "myharness",
        Harness:       "myharness",           // lowercase kebab-case identifier
        CollectedAt:   time.Now().UTC().Format(time.RFC3339Nano),
        SessionID:     p.SessionID,
        EventName:     eventName,
        CaptureState:  sessionpkg.CaptureStateFinal,
        // ... populate remaining fields
    }, nil
}
```

Key `SessionSummary` fields to fill in:

| Field | Description |
|-------|-------------|
| `Harness` | Lowercase kebab-case name stored in the database (e.g. `"myharness"`) |
| `SessionID` | Unique session identifier from the hook payload |
| `CaptureState` | `"final"` if you have complete data now; `"provisional"` if you need a reconcile pass |
| `TotalInputTokens` / `TotalOutputTokens` | `*int64` — use `session.PtrInt64(n)` |
| `Models` | Per-model breakdown as `[]session.ModelUsage` |
| `CWD` / `RepoRoot` / `RepoBranch` | Use `gitmeta.Inspect(cwd)` to fill these from the working directory |

### 2. Create the CLI plumbing

Create `internal/cli/app_myharness.go`:

```go
package cli

import (
    "errors"
    "time"

    "github.com/srbouffard/arok/internal/config"
    "github.com/srbouffard/arok/internal/myharness"
    "github.com/srbouffard/arok/internal/store"
)

func (a *App) runCaptureMyHarness(eventName, stateDirOverride, payloadFile string) error {
    if eventName == "" {
        return errors.New("missing --event")
    }

    stateDir, err := config.ResolveStateDir(stateDirOverride)
    if err != nil {
        return err
    }
    if err := config.EnsureLayout(stateDir); err != nil {
        return err
    }

    payloadRaw, err := readPayload(a.stdin, payloadFile)
    if err != nil {
        return err
    }

    // Parse the hook payload.
    var p myharness.Payload
    if err := json.Unmarshal(payloadRaw, &p); err != nil {
        return err
    }

    summary, err := myharness.Summarize(eventName, stateDir, p)
    if err != nil {
        _ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s myharness capture failed: %v\n", time.Now().UTC().Format(time.RFC3339Nano), err))
        return err
    }

    db, err := store.Open(stateDir)
    if err != nil {
        return err
    }
    defer db.Close()

    return db.UpsertSession(summary)
}
```

### 3. Wire it into the capture dispatcher

In `internal/cli/app.go`, add your harness to the `runCapture` switch:

```go
switch harness {
case "copilot":
    return a.runCaptureCopilot(eventName, stateDirOverride, payloadFile, noReconcile)
case "vscode":
    return a.runCaptureVSCode(eventName, stateDirOverride, payloadFile)
case "myharness":                                           // ← add this
    return a.runCaptureMyHarness(eventName, stateDirOverride, payloadFile)
default:
    return fmt.Errorf("unsupported harness %q", harness)
}
```

### 4. Wire it into the hook config (optional)

If your tool uses a JSON hook config file (like Copilot CLI does), add an
`arok install myharness` command by following the same pattern as
`runInstallCopilot` in `app_copilot.go` and `InstallCopilot` in
`internal/install/copilot.go`.

Then add a case in `runInstall` in `app.go`:

```go
case "myharness":
    return a.runInstallMyHarness(args[1:])
```

### 5. Update the usage string

In `printRootUsage()` in `app.go`, add your harness to the capture line:

```go
fmt.Fprintf(a.stdout, "... capture --harness [copilot|vscode|myharness] --event <event> ...")
```

## Reconcile (only needed for two-phase capture)

The copilot harness needs a reconcile pass because `session.shutdown.modelMetrics`
arrives asynchronously after the hook fires. Most harnesses can produce a final
`SessionSummary` immediately and do not need this.

If your harness fires a hook before all metrics are available, return
`CaptureState: sessionpkg.CaptureStateProvisional` from `Summarize()` and then
spawn a background `arok reconcile --harness myharness` process. See
`spawnDetachedReconcile` and `runReconcile` in `app_copilot.go` for the pattern.

## Writing tests

Add two test files:

**Unit tests** — `internal/myharness/myharness_test.go`

Test `Summarize()` in isolation using fixture JSONL files in
`internal/myharness/testdata/`. See `internal/copilot/copilot_test.go` for
examples.

**Integration tests** — `internal/cli/app_myharness_test.go`

Test the full `arok capture --harness myharness` flow through `App.Run()`.
See `internal/cli/app_copilot_test.go` for the pattern:

```go
func TestRunCaptureMyHarnessStoresSession(t *testing.T) {
    stateDir := t.TempDir()
    payloadFile := writeTempFile(t, "payload.json", `{"session_id":"sess-1","cwd":"/tmp"}`)

    app := New(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
    if err := app.Run([]string{
        "capture", "--harness", "myharness", "--event", "sessionEnd",
        "--state-dir", stateDir,
        "--payload-file", payloadFile,
    }); err != nil {
        t.Fatalf("Run returned error: %v", err)
    }

    db, _ := store.Open(stateDir)
    defer db.Close()
    summary, err := db.GetSession("sess-1")
    if err != nil {
        t.Fatalf("GetSession: %v", err)
    }
    if summary.Harness != "myharness" {
        t.Errorf("Harness = %q, want myharness", summary.Harness)
    }
    // ... assert token counts, capture state, etc.
}
```

## Checklist

- [ ] `internal/myharness/myharness.go` — `Summarize()` returns a valid `SessionSummary`
- [ ] `internal/cli/app_myharness.go` — `runCaptureMyHarness()` method
- [ ] `internal/cli/app.go` — case added to `runCapture()` switch
- [ ] `internal/myharness/myharness_test.go` — unit tests for `Summarize()`
- [ ] `internal/myharness/testdata/` — fixture JSONL files
- [ ] `internal/cli/app_myharness_test.go` — integration test via `App.Run()`
- [ ] `make check` passes
