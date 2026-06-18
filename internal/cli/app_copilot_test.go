package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sessionpkg "github.com/srbouffard/arok/internal/session"
	"github.com/srbouffard/arok/internal/store"
)

// sessionFinalJSONL is a minimal events.jsonl containing a session.shutdown event
// with modelMetrics — the authoritative source for final token totals.
const sessionFinalJSONL = "" +
	`{"timestamp":"2026-01-01T00:00:00Z","type":"assistant.message","data":{"model":"claude-sonnet","outputTokens":150,"interactionId":"i-1","toolRequests":[{}]}}` + "\n" +
	`{"timestamp":"2026-01-01T00:00:01Z","type":"tool.execution_complete","data":{"success":true}}` + "\n" +
	`{"timestamp":"2026-01-01T00:00:02Z","type":"session.shutdown","data":{"modelMetrics":{"claude-sonnet":{"usage":{"inputTokens":500,"outputTokens":150,"cacheReadTokens":200},"requests":{"count":1}}}}}` + "\n"

// sessionProvisionalJSONL is a minimal events.jsonl without a session.shutdown event,
// simulating a session that ended before metrics were written.
const sessionProvisionalJSONL = "" +
	`{"timestamp":"2026-01-01T00:00:00Z","type":"assistant.message","data":{"model":"claude-sonnet","outputTokens":150,"interactionId":"i-1","toolRequests":[{}]}}` + "\n" +
	`{"timestamp":"2026-01-01T00:00:01Z","type":"tool.execution_complete","data":{"success":true}}` + "\n"

func TestRunCaptureCopilotFinalSession(t *testing.T) {
	stateDir := t.TempDir()
	copilotHome := t.TempDir()
	sessionID := "sess-copilot-final"

	eventsDir := filepath.Join(copilotHome, "session-state", sessionID)
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(eventsDir) returned error: %v", err)
	}
	eventsFile := filepath.Join(eventsDir, "events.jsonl")
	if err := os.WriteFile(eventsFile, []byte(sessionFinalJSONL), 0o644); err != nil {
		t.Fatalf("WriteFile(events.jsonl) returned error: %v", err)
	}

	payload := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionID, t.TempDir())
	payloadFile := writeTempFile(t, "payload.json", payload)
	t.Setenv("COPILOT_HOME", copilotHome)

	app := New(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if err := app.Run([]string{
		"capture", "--harness", "copilot", "--event", "sessionEnd",
		"--state-dir", stateDir,
		"--payload-file", payloadFile,
		"--no-reconcile",
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	db, err := store.Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db.Close()

	summary, err := db.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if summary.Harness != sessionpkg.HarnessCopilotCLI {
		t.Errorf("Harness = %q, want %q", summary.Harness, sessionpkg.HarnessCopilotCLI)
	}
	if summary.CaptureState != sessionpkg.CaptureStateFinal {
		t.Errorf("CaptureState = %q, want %q", summary.CaptureState, sessionpkg.CaptureStateFinal)
	}
	if summary.TotalInputTokens == nil || *summary.TotalInputTokens != 500 {
		t.Errorf("TotalInputTokens = %#v, want 500", summary.TotalInputTokens)
	}
	if summary.TotalOutputTokens == nil || *summary.TotalOutputTokens != 150 {
		t.Errorf("TotalOutputTokens = %#v, want 150", summary.TotalOutputTokens)
	}
	if summary.TotalCacheReadTokens == nil || *summary.TotalCacheReadTokens != 200 {
		t.Errorf("TotalCacheReadTokens = %#v, want 200", summary.TotalCacheReadTokens)
	}
	if summary.EventName != "sessionEnd" {
		t.Errorf("EventName = %q, want sessionEnd", summary.EventName)
	}
}

func TestRunCaptureCopilotProvisionalSession(t *testing.T) {
	stateDir := t.TempDir()
	copilotHome := t.TempDir()
	sessionID := "sess-copilot-provisional"

	eventsDir := filepath.Join(copilotHome, "session-state", sessionID)
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(eventsDir) returned error: %v", err)
	}
	eventsFile := filepath.Join(eventsDir, "events.jsonl")
	if err := os.WriteFile(eventsFile, []byte(sessionProvisionalJSONL), 0o644); err != nil {
		t.Fatalf("WriteFile(events.jsonl) returned error: %v", err)
	}

	payload := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionID, t.TempDir())
	payloadFile := writeTempFile(t, "payload.json", payload)
	t.Setenv("COPILOT_HOME", copilotHome)
	// Disable retry so the test doesn't spin waiting for shutdown metrics.
	t.Setenv("AROK_COPILOT_SHUTDOWN_RETRY_ATTEMPTS", "1")

	app := New(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if err := app.Run([]string{
		"capture", "--harness", "copilot", "--event", "sessionEnd",
		"--state-dir", stateDir,
		"--payload-file", payloadFile,
		"--no-reconcile",
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	db, err := store.Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db.Close()

	summary, err := db.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if summary.CaptureState != sessionpkg.CaptureStateProvisional {
		t.Errorf("CaptureState = %q, want %q", summary.CaptureState, sessionpkg.CaptureStateProvisional)
	}
	// Without shutdown metrics, TotalOutputTokens falls back to assistant.message counts.
	if summary.TotalOutputTokens == nil || *summary.TotalOutputTokens != 150 {
		t.Errorf("TotalOutputTokens = %#v, want 150 (fallback from assistant.message)", summary.TotalOutputTokens)
	}
}

func TestRunCaptureCopilotMissingSessionID(t *testing.T) {
	stateDir := t.TempDir()
	payloadFile := writeTempFile(t, "payload.json", `{"cwd":"/tmp"}`)

	app := New(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Run([]string{
		"capture", "--harness", "copilot", "--event", "sessionEnd",
		"--state-dir", stateDir,
		"--payload-file", payloadFile,
		"--no-reconcile",
	})
	if err == nil {
		t.Fatal("expected error for missing sessionId, got nil")
	}
}

func TestRunCaptureCopilotUnknownHarness(t *testing.T) {
	app := New(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	err := app.Run([]string{"capture", "--harness", "unknown-harness", "--event", "sessionEnd"})
	if err == nil {
		t.Fatal("expected error for unknown harness, got nil")
	}
}

func TestRunInstallCopilotPrintConfig(t *testing.T) {
	stateDir := t.TempDir()
	var out bytes.Buffer
	app := New(bytes.NewReader(nil), &out, &bytes.Buffer{})
	if err := app.Run([]string{
		"install", "copilot",
		"--print-config",
		"--state-dir", stateDir,
		"--binary-path", "/usr/local/bin/arok",
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "sessionEnd") {
		t.Error("expected config to contain sessionEnd hook")
	}
	if !strings.Contains(output, "/usr/local/bin/arok") {
		t.Error("expected config to contain binary path")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Errorf("config is not valid JSON: %v", err)
	}
}

func TestRunReconcileUpgradesProvisionalToFinal(t *testing.T) {
	stateDir := t.TempDir()
	copilotHome := t.TempDir()
	sessionID := "sess-reconcile-final"

	eventsDir := filepath.Join(copilotHome, "session-state", sessionID)
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	eventsFile := filepath.Join(eventsDir, "events.jsonl")

	// Stage 1: capture with no shutdown event → provisional.
	if err := os.WriteFile(eventsFile, []byte(sessionProvisionalJSONL), 0o644); err != nil {
		t.Fatalf("WriteFile(provisional) returned error: %v", err)
	}
	payload := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionID, t.TempDir())
	payloadFile := writeTempFile(t, "payload.json", payload)
	t.Setenv("COPILOT_HOME", copilotHome)
	t.Setenv("AROK_COPILOT_SHUTDOWN_RETRY_ATTEMPTS", "1")

	app := New(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if err := app.Run([]string{
		"capture", "--harness", "copilot", "--event", "sessionEnd",
		"--state-dir", stateDir, "--payload-file", payloadFile, "--no-reconcile",
	}); err != nil {
		t.Fatalf("capture returned error: %v", err)
	}

	db, err := store.Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	before, err := db.GetSession(sessionID)
	db.Close()
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if before.CaptureState != sessionpkg.CaptureStateProvisional {
		t.Fatalf("pre-condition: CaptureState = %q, want provisional", before.CaptureState)
	}

	// Stage 2: shutdown event arrives → reconcile upgrades to final.
	if err := os.WriteFile(eventsFile, []byte(sessionFinalJSONL), 0o644); err != nil {
		t.Fatalf("WriteFile(final) returned error: %v", err)
	}
	reconcilePayload := writeTempFile(t, "reconcile_payload.json", payload)

	if err := app.Run([]string{
		"reconcile", "--harness", "copilot", "--event", "sessionEnd",
		"--state-dir", stateDir,
		"--payload-file", reconcilePayload,
		"--session-file", eventsFile,
		"--session-id", sessionID,
		"--attempts", "1",
	}); err != nil {
		t.Fatalf("reconcile returned error: %v", err)
	}

	db2, err := store.Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db2.Close()
	after, err := db2.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if after.CaptureState != sessionpkg.CaptureStateFinal {
		t.Errorf("CaptureState = %q, want final", after.CaptureState)
	}
	if after.TotalInputTokens == nil || *after.TotalInputTokens != 500 {
		t.Errorf("TotalInputTokens = %#v, want 500", after.TotalInputTokens)
	}
}

func TestRunReconcileMarksBestEffortWhenExhausted(t *testing.T) {
	stateDir := t.TempDir()
	copilotHome := t.TempDir()
	sessionID := "sess-reconcile-best-effort"

	eventsDir := filepath.Join(copilotHome, "session-state", sessionID)
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	eventsFile := filepath.Join(eventsDir, "events.jsonl")
	if err := os.WriteFile(eventsFile, []byte(sessionProvisionalJSONL), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	payload := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionID, t.TempDir())
	payloadFile := writeTempFile(t, "payload.json", payload)
	t.Setenv("COPILOT_HOME", copilotHome)
	t.Setenv("AROK_COPILOT_SHUTDOWN_RETRY_ATTEMPTS", "1")

	app := New(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if err := app.Run([]string{
		"capture", "--harness", "copilot", "--event", "sessionEnd",
		"--state-dir", stateDir, "--payload-file", payloadFile, "--no-reconcile",
	}); err != nil {
		t.Fatalf("capture returned error: %v", err)
	}

	// Reconcile against the still-provisional events file — should exhaust and mark best_effort.
	reconcilePayload := writeTempFile(t, "reconcile_payload.json", payload)
	err := app.Run([]string{
		"reconcile", "--harness", "copilot", "--event", "sessionEnd",
		"--state-dir", stateDir,
		"--payload-file", reconcilePayload,
		"--session-file", eventsFile,
		"--session-id", sessionID,
		"--attempts", "1",
	})
	if err == nil {
		t.Fatal("expected error when reconcile exhausts without final metrics")
	}

	db, err := store.Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db.Close()
	after, err := db.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if after.CaptureState != sessionpkg.CaptureStateBestEffort {
		t.Errorf("CaptureState = %q, want best_effort", after.CaptureState)
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) returned error: %v", name, err)
	}
	return path
}
