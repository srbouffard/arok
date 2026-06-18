package cli

// VS Code Copilot harness — capture support (scan and per-session Stop events).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/srbouffard/arok/internal/config"
	"github.com/srbouffard/arok/internal/gitmeta"
	sessionpkg "github.com/srbouffard/arok/internal/session"
	"github.com/srbouffard/arok/internal/store"
	"github.com/srbouffard/arok/internal/vscode"
)

// runCaptureVSCode handles: arok capture --harness vscode --event <event>
//
// Supported events:
//   - scan  : scans the VS Code workspaceStorage directory and imports all sessions
//   - Stop  : handles a single VS Code Copilot Stop hook payload
func (a *App) runCaptureVSCode(eventName, stateDirOverride, payloadFile string) error {
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

	db, err := store.Open(stateDir)
	if err != nil {
		return err
	}
	defer db.Close()

	if strings.EqualFold(eventName, "scan") {
		return a.captureVSCodeScan(stateDir, db)
	}

	payloadRaw, err := readPayload(a.stdin, payloadFile)
	if err != nil {
		return err
	}

	payload, ok := parseVSCodeStopPayload(payloadRaw)
	if !ok {
		if len(strings.TrimSpace(string(payloadRaw))) > 0 {
			_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s VS Code capture skipped: invalid Stop payload\n", time.Now().UTC().Format(time.RFC3339Nano)))
		}
		return nil
	}

	sessionPath := deriveVSCodeChatSessionPath(payload.TranscriptPath)
	if sessionPath == "" {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s VS Code capture skipped for %s: unable to derive chatSessions path\n", time.Now().UTC().Format(time.RFC3339Nano), payload.SessionID))
		return nil
	}

	sessionData, err := vscode.ReadChatSession(sessionPath)
	if err != nil {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s VS Code capture skipped for %s: %v\n", time.Now().UTC().Format(time.RFC3339Nano), payload.SessionID, err))
		return nil
	}
	if payload.SessionID != "" {
		sessionData.SessionID = payload.SessionID
	}

	summary := buildVSCodeSummary(sessionData, stateDir, eventName, sessionPath, payload.TranscriptPath)
	if summary.SessionID == "" {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s VS Code capture skipped: missing session_id\n", time.Now().UTC().Format(time.RFC3339Nano)))
		return nil
	}

	if err := upsertVSCodeSummary(db, summary); err != nil {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s VS Code database write failed for %s: %v\n", time.Now().UTC().Format(time.RFC3339Nano), summary.SessionID, err))
		return nil
	}
	return nil
}

func (a *App) captureVSCodeScan(stateDir string, db *store.Store) error {
	userDataDir := vscode.DefaultUserDataDir()
	if userDataDir == "" {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s VS Code scan skipped: user data directory not determinable\n", time.Now().UTC().Format(time.RFC3339Nano)))
		return nil
	}

	sessions, err := vscode.ScanSessions(userDataDir)
	if err != nil {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s VS Code scan skipped: %v\n", time.Now().UTC().Format(time.RFC3339Nano), err))
		return nil
	}

	for _, sessionData := range sessions {
		if !shouldImportScannedVSCodeSession(sessionData) {
			continue
		}
		summary := buildVSCodeSummary(sessionData, stateDir, "scan", "", "")
		if summary.SessionID == "" {
			continue
		}
		if err := upsertVSCodeSummary(db, summary); err != nil {
			_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s VS Code scan write failed for %s: %v\n", time.Now().UTC().Format(time.RFC3339Nano), summary.SessionID, err))
		}
	}
	return nil
}

type vscodeStopPayload struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
}

func parseVSCodeStopPayload(raw []byte) (vscodeStopPayload, bool) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return vscodeStopPayload{}, false
	}
	var payload vscodeStopPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return vscodeStopPayload{}, false
	}
	if payload.SessionID == "" || payload.TranscriptPath == "" {
		return vscodeStopPayload{}, false
	}
	return payload, true
}

func deriveVSCodeChatSessionPath(transcriptPath string) string {
	if strings.TrimSpace(transcriptPath) == "" {
		return ""
	}
	sessionFile := filepath.Base(transcriptPath)
	storageDir := filepath.Dir(filepath.Dir(filepath.Dir(transcriptPath)))
	if sessionFile == "." || sessionFile == string(filepath.Separator) {
		return ""
	}
	return filepath.Join(storageDir, "chatSessions", sessionFile)
}

func buildVSCodeSummary(sessionData vscode.Session, stateDir, eventName, eventLogPath, transcriptPath string) sessionpkg.SessionSummary {
	hostName, _ := os.Hostname()
	git := gitmeta.Inspect(sessionData.WorkspaceFolder)

	var (
		totalOutputTokens int64
		totalInputTokens  int64
		modelStats        = map[string]int64{}
		modelCounts       = map[string]int64{}
		endedAt           = sessionData.CreationDate
	)
	for _, req := range sessionData.Requests {
		totalOutputTokens += req.CompletionTokens
		totalInputTokens += req.PromptTokens
		model := req.ModelID
		if model == "" {
			model = "unknown"
		}
		modelStats[model] += req.CompletionTokens
		modelCounts[model]++
		if req.Timestamp.After(endedAt) {
			endedAt = req.Timestamp
		}
	}

	modelNames := make([]string, 0, len(modelStats))
	for model := range modelStats {
		modelNames = append(modelNames, model)
	}
	slices.Sort(modelNames)

	models := make([]sessionpkg.ModelUsage, 0, len(modelNames))
	for _, model := range modelNames {
		tokens := modelStats[model]
		count := modelCounts[model]
		models = append(models, sessionpkg.ModelUsage{
			Model:                 model,
			AssistantMessageCount: count,
			AssistantOutputTokens: tokens,
			OutputTokens:          sessionpkg.PtrInt64(tokens),
			RequestCount:          sessionpkg.PtrInt64(count),
		})
	}

	startedAt := ""
	if !sessionData.CreationDate.IsZero() {
		startedAt = sessionData.CreationDate.UTC().Format(time.RFC3339)
	}
	endedAtRaw := ""
	if !endedAt.IsZero() {
		endedAtRaw = endedAt.UTC().Format(time.RFC3339)
	}

	notes := []string{
		"VS Code Copilot usage is summarized from the local chatSessions JSONL transaction log.",
		"Token counts are sourced from result.metadata (primary) or completionTokens/usage fields (fallback).",
	}
	if sessionData.WorkspaceFolder == "" {
		notes = append(notes, "Workspace metadata is unavailable for missing or remote VS Code workspaces.")
	}

	var totalInputTokensPtr *int64
	if totalInputTokens > 0 {
		totalInputTokensPtr = sessionpkg.PtrInt64(totalInputTokens)
	}

	return sessionpkg.SessionSummary{
		SchemaVersion:         1,
		Source:                "vscode",
		Harness:               sessionpkg.HarnessVSCodeCopilot,
		CollectedAt:           time.Now().UTC().Format(time.RFC3339Nano),
		SessionID:             sessionData.SessionID,
		EventName:             eventName,
		CaptureState:          sessionpkg.CaptureStateFinal,
		UsageSource:           "chatSessions.completionTokens",
		TranscriptPath:        transcriptPath,
		EventLogPath:          eventLogPath,
		StateDir:              stateDir,
		CWD:                   sessionData.WorkspaceFolder,
		RepoRoot:              git.RepoRoot,
		WorktreeRoot:          git.WorktreeRoot,
		GitCommonDir:          git.GitCommonDir,
		RepoRemote:            git.RepoRemote,
		RepoBranch:            git.RepoBranch,
		RepoHead:              git.RepoHead,
		HostName:              hostName,
		StartedAt:             startedAt,
		EndedAt:               endedAtRaw,
		InteractionCount:      int64(len(sessionData.Requests)),
		AssistantMessageCount: int64(len(sessionData.Requests)),
		AssistantOutputTokens: totalOutputTokens,
		TotalInputTokens:      totalInputTokensPtr,
		TotalOutputTokens:     sessionpkg.PtrInt64(totalOutputTokens),
		Models:                models,
		Notes:                 notes,
	}
}

func shouldImportScannedVSCodeSession(sessionData vscode.Session) bool {
	if len(sessionData.Requests) == 0 {
		return true
	}
	for _, req := range sessionData.Requests {
		if req.CompletionTokens > 0 {
			return true
		}
	}
	return false
}

func upsertVSCodeSummary(db *store.Store, summary sessionpkg.SessionSummary) error {
	existing, err := db.GetSession(summary.SessionID)
	if err != nil && !errors.Is(err, store.ErrSessionNotFound) {
		return err
	}
	if err == nil && existing.CaptureState == sessionpkg.CaptureStateFinal &&
		derefInt64(existing.TotalOutputTokens) == derefInt64(summary.TotalOutputTokens) &&
		derefInt64(existing.TotalInputTokens) == derefInt64(summary.TotalInputTokens) {
		return nil
	}
	return db.UpsertSession(summary)
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
