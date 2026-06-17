package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sessionpkg "github.com/srbouffard/arok/internal/session"
)

func TestSummarizeUsesShutdownMetricsWhenPresent(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "events.jsonl")
	payloadRaw := []byte(`{"sessionId":"sess-1","cwd":"` + root + `"}`)
	payload, err := ParsePayload(payloadRaw)
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	content := "" +
		"{\"timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"assistant.message\",\"data\":{\"model\":\"gpt-5\",\"outputTokens\":12,\"interactionId\":\"i-1\",\"toolRequests\":[{}]}}\n" +
		"{\"timestamp\":\"2026-01-01T00:00:01Z\",\"type\":\"tool.execution_complete\",\"data\":{\"success\":true}}\n" +
		"{\"timestamp\":\"2026-01-01T00:00:02Z\",\"type\":\"subagent.completed\",\"agentId\":\"agent-1\",\"data\":{\"toolCallId\":\"tool-1\",\"agentName\":\"research\",\"agentDisplayName\":\"Research\",\"model\":\"gpt-5\",\"totalToolCalls\":3,\"totalTokens\":44,\"durationMs\":1200}}\n" +
		"{\"timestamp\":\"2026-01-01T00:00:03Z\",\"type\":\"session.shutdown\",\"data\":{\"modelMetrics\":{\"gpt-5\":{\"usage\":{\"inputTokens\":30,\"outputTokens\":40,\"cacheReadTokens\":50,\"cacheWriteTokens\":60,\"reasoningTokens\":70},\"requests\":{\"count\":2}}}}}\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	summary, err := Summarize(SummarizeOptions{
		EventName:   "sessionEnd",
		StateDir:    root,
		Payload:     payload,
		SessionFile: sessionFile,
		Meta:        Meta{TaskID: "task-1", Title: "Title", Summary: "Summary", Tags: []string{"alpha", "beta"}},
	})
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}

	if summary.CaptureState != sessionpkg.CaptureStateFinal {
		t.Fatalf("unexpected capture state: %s", summary.CaptureState)
	}
	if summary.UsageSource != sessionpkg.UsageSourceShutdown {
		t.Fatalf("unexpected usage source: %s", summary.UsageSource)
	}
	if summary.TotalInputTokens == nil || *summary.TotalInputTokens != 30 {
		t.Fatalf("unexpected total input tokens: %#v", summary.TotalInputTokens)
	}
	if summary.TotalOutputTokens == nil || *summary.TotalOutputTokens != 40 {
		t.Fatalf("unexpected total output tokens: %#v", summary.TotalOutputTokens)
	}
	if summary.TaskID != "task-1" || summary.Title != "Title" || summary.Summary != "Summary" {
		t.Fatalf("expected metadata from environment fallback, got %+v", summary)
	}
	if len(summary.Subagents) != 1 {
		t.Fatalf("expected one subagent summary, got %d", len(summary.Subagents))
	}
}

func TestResolveSessionFileFallsBackToCopilotHome(t *testing.T) {
	t.Setenv("COPILOT_HOME", "/tmp/copilot-home")
	path := ResolveSessionFile(Payload{"sessionId": "sess-2"})
	want := "/tmp/copilot-home/session-state/sess-2/events.jsonl"
	if path != want {
		t.Fatalf("ResolveSessionFile returned %q, want %q", path, want)
	}
}

func TestSummarizeHandlesLargeJSONLLines(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "events.jsonl")
	payload, err := ParsePayload([]byte(`{"sessionId":"sess-large","cwd":"` + root + `"}`))
	if err != nil {
		t.Fatalf("ParsePayload returned error: %v", err)
	}

	largePayload := strings.Repeat("x", 80*1024)
	content := "{\"timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"assistant.message\",\"data\":{\"model\":\"gpt-5\",\"outputTokens\":7,\"toolRequests\":[{\"payload\":\"" + largePayload + "\"}]}}\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	summary, err := Summarize(SummarizeOptions{
		EventName:   "sessionEnd",
		StateDir:    root,
		Payload:     payload,
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if summary.TotalOutputTokens == nil || *summary.TotalOutputTokens != 7 {
		t.Fatalf("unexpected total output tokens: %#v", summary.TotalOutputTokens)
	}
}
