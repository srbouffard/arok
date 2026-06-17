package vscode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultUserDataDirUsesEnvOverride(t *testing.T) {
	t.Setenv("VSCODE_USER_DATA_DIR", "/custom/vscode-user")

	if got := DefaultUserDataDir(); got != "/custom/vscode-user" {
		t.Fatalf("DefaultUserDataDir() = %q, want %q", got, "/custom/vscode-user")
	}
}

func TestReadChatSessionReconstructsState(t *testing.T) {
	root := t.TempDir()
	storageDir := filepath.Join(root, "workspaceStorage", "workspace-1")
	chatDir := filepath.Join(storageDir, "chatSessions")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	workspacePath := filepath.Join(storageDir, "workspace.json")
	if err := os.WriteFile(workspacePath, []byte(`{"folder":"file:///home/test/my%40repo/feature%20branch"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(workspace.json) returned error: %v", err)
	}

	// Simulate the real VS Code pattern:
	// kind:0 — initial snapshot with one partial request (streaming in progress)
	// kind:2 — splice: append response items into requests[0].response
	// kind:1 — update nested field: requests[0].completionTokens = 1996 (final value)
	// kind:1 — update nested: requests[0].result.metadata with promptTokens + resolvedModel
	// kind:2 — splice: add a second request to requests[]
	sessionPath := filepath.Join(chatDir, "sess-1.jsonl")
	payload := []byte(
		// kind:0: snapshot with one in-progress request (stale completionTokens=10)
		`{"kind":0,"v":{"sessionId":"sess-1","creationDate":1781700310818,"requests":[{"requestId":"request-1","timestamp":1781700328855,"modelId":"copilot/auto","completionTokens":10,"elapsedMs":50}]}}` + "\n" +
			// kind:1: update requests[0].completionTokens to final value
			`{"kind":1,"k":["requests",0,"completionTokens"],"v":1996}` + "\n" +
			// kind:1: update requests[0].result with metadata including promptTokens + resolvedModel
			`{"kind":1,"k":["requests",0,"result"],"v":{"metadata":{"promptTokens":500,"outputTokens":1996,"resolvedModel":"claude-haiku-4.5"}}}` + "\n" +
			// kind:2: splice a second request into requests[]
			`{"kind":2,"k":["requests"],"v":[{"requestId":"request-2","timestamp":1781700330000,"modelId":"gpt-5","completionTokens":25,"elapsedMs":60}]}` + "\n",
	)
	if err := os.WriteFile(sessionPath, payload, 0o644); err != nil {
		t.Fatalf("WriteFile(session) returned error: %v", err)
	}

	session, err := ReadChatSession(sessionPath)
	if err != nil {
		t.Fatalf("ReadChatSession returned error: %v", err)
	}

	if session.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want %q", session.SessionID, "sess-1")
	}
	if session.WorkspaceFolder != "/home/test/my@repo/feature branch" {
		t.Fatalf("WorkspaceFolder = %q", session.WorkspaceFolder)
	}
	if session.CreationDate != time.UnixMilli(1781700310818).UTC() {
		t.Fatalf("CreationDate = %v", session.CreationDate)
	}
	if len(session.Requests) != 2 {
		t.Fatalf("len(Requests) = %d, want 2", len(session.Requests))
	}

	// First request: kind:1 patches must have overridden the stale snapshot value (10 → 1996)
	req0 := session.Requests[0]
	if req0.CompletionTokens != 1996 {
		t.Fatalf("request-1 CompletionTokens = %d, want 1996 (kind:1 patch must override snapshot)", req0.CompletionTokens)
	}
	// result.metadata.promptTokens should be captured
	if req0.PromptTokens != 500 {
		t.Fatalf("request-1 PromptTokens = %d, want 500", req0.PromptTokens)
	}
	// result.metadata.resolvedModel should be preferred over modelId
	if req0.ModelID != "claude-haiku-4.5" {
		t.Fatalf("request-1 ModelID = %q, want %q (resolvedModel)", req0.ModelID, "claude-haiku-4.5")
	}

	// Second request: added via kind:2 splice
	req1 := session.Requests[1]
	if req1.CompletionTokens != 25 {
		t.Fatalf("request-2 CompletionTokens = %d, want 25", req1.CompletionTokens)
	}
}

func TestReadChatSessionSkipsRemoteWorkspaceFolder(t *testing.T) {
	root := t.TempDir()
	storageDir := filepath.Join(root, "workspaceStorage", "workspace-2")
	chatDir := filepath.Join(storageDir, "chatSessions")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(storageDir, "workspace.json"), []byte(`{"folder":"vscode-remote://ssh-remote%2Bhost/home/test/project"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(workspace.json) returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chatDir, "sess-2.jsonl"), []byte(`{"kind":0,"v":{"sessionId":"sess-2","creationDate":1781700310818}}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(session) returned error: %v", err)
	}

	session, err := ReadChatSession(filepath.Join(chatDir, "sess-2.jsonl"))
	if err != nil {
		t.Fatalf("ReadChatSession returned error: %v", err)
	}
	if session.WorkspaceFolder != "" {
		t.Fatalf("WorkspaceFolder = %q, want empty", session.WorkspaceFolder)
	}
}

func TestScanSessionsSkipsInvalidFiles(t *testing.T) {
	root := t.TempDir()
	validDir := filepath.Join(root, "workspaceStorage", "workspace-1", "chatSessions")
	invalidDir := filepath.Join(root, "workspaceStorage", "workspace-2", "chatSessions")
	for _, dir := range []string{validDir, invalidDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
	}

	if err := os.WriteFile(filepath.Join(root, "workspaceStorage", "workspace-1", "workspace.json"), []byte(`{"folder":"file:///repo"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(workspace.json) returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(validDir, "sess-1.jsonl"), []byte(`{"kind":0,"v":{"sessionId":"sess-1","creationDate":1781700310818}}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(valid session) returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(invalidDir, "sess-2.jsonl"), []byte(`{"kind":`), 0o644); err != nil {
		t.Fatalf("WriteFile(invalid session) returned error: %v", err)
	}

	sessions, err := ScanSessions(root)
	if err != nil {
		t.Fatalf("ScanSessions returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
}
