package store

import (
	"testing"
	"time"

	sessionpkg "github.com/srbouffard/arok/internal/session"
)

func TestUpsertSessionRefreshesExistingRow(t *testing.T) {
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db.Close()

	first := sessionpkg.SessionSummary{
		SchemaVersion:         1,
		Source:                sessionpkg.HarnessCopilotCLI,
		Harness:               sessionpkg.HarnessCopilotCLI,
		CollectedAt:           "2026-01-01T00:00:00Z",
		SessionID:             "sess-1",
		EventName:             "sessionEnd",
		CaptureState:          sessionpkg.CaptureStateProvisional,
		UsageSource:           "assistant.message.outputTokens",
		HostName:              "host-a",
		StateDir:              stateDir,
		AssistantMessageCount: 1,
		AssistantOutputTokens: 10,
		TotalOutputTokens:     sessionpkg.PtrInt64(10),
	}
	second := first
	second.CaptureState = sessionpkg.CaptureStateFinal
	second.UsageSource = sessionpkg.UsageSourceShutdown
	second.TotalInputTokens = sessionpkg.PtrInt64(20)
	second.TotalOutputTokens = sessionpkg.PtrInt64(30)

	if err := db.UpsertSession(first); err != nil {
		t.Fatalf("UpsertSession(first) returned error: %v", err)
	}
	if err := db.UpsertSession(second); err != nil {
		t.Fatalf("UpsertSession(second) returned error: %v", err)
	}

	summary, err := db.GetSession("sess-1")
	if err != nil {
		t.Fatalf("GetSession returned error: %v", err)
	}
	if summary.CaptureState != sessionpkg.CaptureStateFinal {
		t.Fatalf("expected refreshed capture_state, got %s", summary.CaptureState)
	}
	if summary.TotalInputTokens == nil || *summary.TotalInputTokens != 20 {
		t.Fatalf("unexpected total_input_tokens: %#v", summary.TotalInputTokens)
	}

	count, err := db.CountSessions()
	if err != nil {
		t.Fatalf("CountSessions returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one session row, got %d", count)
	}
}

func TestOverviewFiltersMissingFinalSessionsByTimeWindow(t *testing.T) {
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer db.Close()

	oldSummary := sessionpkg.SessionSummary{
		SchemaVersion:         1,
		Source:                sessionpkg.HarnessCopilotCLI,
		Harness:               sessionpkg.HarnessCopilotCLI,
		CollectedAt:           "2026-01-01T00:00:00Z",
		SessionID:             "old-session",
		EventName:             "sessionEnd",
		CaptureState:          sessionpkg.CaptureStateProvisional,
		UsageSource:           "assistant.message.outputTokens",
		HostName:              "host-a",
		StateDir:              stateDir,
		AssistantMessageCount: 1,
		AssistantOutputTokens: 10,
		TotalOutputTokens:     sessionpkg.PtrInt64(10),
		EndedAt:               "2026-01-01T00:00:00Z",
	}
	newSummary := oldSummary
	newSummary.SessionID = "new-session"
	newSummary.EndedAt = "2026-06-17T00:00:00Z"

	if err := db.UpsertSession(oldSummary); err != nil {
		t.Fatalf("UpsertSession(oldSummary) returned error: %v", err)
	}
	if err := db.UpsertSession(newSummary); err != nil {
		t.Fatalf("UpsertSession(newSummary) returned error: %v", err)
	}

	cutoff := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	overview, err := db.Overview(&cutoff, 10)
	if err != nil {
		t.Fatalf("Overview returned error: %v", err)
	}
	if len(overview.MissingFinals) != 1 || overview.MissingFinals[0].SessionID != "new-session" {
		t.Fatalf("unexpected missing finals: %+v", overview.MissingFinals)
	}
}
