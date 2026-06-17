package cli

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	sessionpkg "github.com/srbouffard/arok/internal/session"
	"github.com/srbouffard/arok/internal/store"
)

func TestRunCaptureVSCodeScanImportsSessions(t *testing.T) {
	userDataDir := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "project one")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(projectDir) returned error: %v", err)
	}

	writeVSCodeSessionFixture(t, userDataDir, "workspace-1", "sess-1", mustFileURL(projectDir), []string{
		`{"requestId":"request-1","timestamp":1781700328855,"modelId":"copilot/auto","completionTokens":11,"elapsedMs":30}`,
	})
	writeVSCodeSessionFixture(t, userDataDir, "workspace-2", "sess-2", mustFileURL(filepath.Join(t.TempDir(), "project-two")), nil)
	writeVSCodeSessionFixture(t, userDataDir, "workspace-3", "sess-3", mustFileURL(filepath.Join(t.TempDir(), "project-three")), []string{
		`{"requestId":"request-3","timestamp":1781700328855,"modelId":"copilot/auto","completionTokens":0,"elapsedMs":30}`,
	})

	stateDir := t.TempDir()
	t.Setenv("VSCODE_USER_DATA_DIR", userDataDir)

	app := New(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if err := app.Run([]string{"capture", "--harness", "vscode", "--event", "scan", "--state-dir", stateDir}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	db, err := store.Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db.Close()

	count, err := db.CountSessions()
	if err != nil {
		t.Fatalf("CountSessions returned error: %v", err)
	}
	if count != 2 {
		t.Fatalf("CountSessions = %d, want 2", count)
	}

	summary, err := db.GetSession("sess-1")
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if summary.Harness != sessionpkg.HarnessVSCodeCopilot {
		t.Fatalf("Harness = %q, want %q", summary.Harness, sessionpkg.HarnessVSCodeCopilot)
	}
	if summary.CaptureState != sessionpkg.CaptureStateFinal {
		t.Fatalf("CaptureState = %q, want %q", summary.CaptureState, sessionpkg.CaptureStateFinal)
	}
	if summary.TotalOutputTokens == nil || *summary.TotalOutputTokens != 11 {
		t.Fatalf("TotalOutputTokens = %#v, want 11", summary.TotalOutputTokens)
	}
	if summary.CWD != projectDir {
		t.Fatalf("CWD = %q, want %q", summary.CWD, projectDir)
	}

	emptySummary, err := db.GetSession("sess-2")
	if err != nil {
		t.Fatalf("GetSession(sess-2) returned error: %v", err)
	}
	if emptySummary.TotalOutputTokens == nil || *emptySummary.TotalOutputTokens != 0 {
		t.Fatalf("sess-2 TotalOutputTokens = %#v, want 0", emptySummary.TotalOutputTokens)
	}
}

func TestRunCaptureVSCodeStopImportsDerivedChatSession(t *testing.T) {
	userDataDir := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "project-stop")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(projectDir) returned error: %v", err)
	}

	sessionPath := writeVSCodeSessionFixture(t, userDataDir, "workspace-1", "sess-stop", mustFileURL(projectDir), []string{
		`{"requestId":"request-1","timestamp":1781700328855,"modelId":"copilot/auto","completionTokens":17,"elapsedMs":30}`,
	})

	transcriptPath := filepath.Join(userDataDir, "workspaceStorage", "workspace-1", "GitHub.copilot-chat", "transcripts", "sess-stop.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(transcripts) returned error: %v", err)
	}

	payload := fmt.Sprintf(`{"hook_event_name":"Stop","session_id":"sess-stop","transcript_path":%q,"cwd":%q}`, transcriptPath, projectDir)
	stateDir := t.TempDir()

	app := New(bytes.NewBufferString(payload), &bytes.Buffer{}, &bytes.Buffer{})
	if err := app.Run([]string{"capture", "--harness", "vscode", "--event", "Stop", "--state-dir", stateDir}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	db, err := store.Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db.Close()

	summary, err := db.GetSession("sess-stop")
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if summary.TranscriptPath != transcriptPath {
		t.Fatalf("TranscriptPath = %q, want %q", summary.TranscriptPath, transcriptPath)
	}
	if summary.EventLogPath != sessionPath {
		t.Fatalf("EventLogPath = %q, want %q", summary.EventLogPath, sessionPath)
	}
	if summary.TotalOutputTokens == nil || *summary.TotalOutputTokens != 17 {
		t.Fatalf("TotalOutputTokens = %#v, want 17", summary.TotalOutputTokens)
	}
}

func writeVSCodeSessionFixture(t *testing.T, userDataDir, workspaceHash, sessionID, folderURL string, requests []string) string {
	t.Helper()

	storageDir := filepath.Join(userDataDir, "workspaceStorage", workspaceHash)
	chatDir := filepath.Join(storageDir, "chatSessions")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(chatDir) returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storageDir, "workspace.json"), []byte(fmt.Sprintf(`{"folder":%q}`, folderURL)), 0o644); err != nil {
		t.Fatalf("WriteFile(workspace.json) returned error: %v", err)
	}

	requestsJSON := "[]"
	if len(requests) > 0 {
		requestsJSON = "[" + requests[0]
		for _, req := range requests[1:] {
			requestsJSON += "," + req
		}
		requestsJSON += "]"
	}

	sessionPath := filepath.Join(chatDir, sessionID+".jsonl")
	content := fmt.Sprintf(`{"kind":0,"v":{"sessionId":%q,"creationDate":1781700310818,"requests":%s}}`+"\n", sessionID, requestsJSON)
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(session) returned error: %v", err)
	}
	return sessionPath
}

func mustFileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}
