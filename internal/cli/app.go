package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/srbouffard/arok/internal/config"
	"github.com/srbouffard/arok/internal/copilot"
	"github.com/srbouffard/arok/internal/gitmeta"
	"github.com/srbouffard/arok/internal/install"
	sessionpkg "github.com/srbouffard/arok/internal/session"
	"github.com/srbouffard/arok/internal/store"
	"github.com/srbouffard/arok/internal/update"
	"github.com/srbouffard/arok/internal/version"
	"github.com/srbouffard/arok/internal/vscode"
)

type App struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func New(stdin io.Reader, stdout, stderr io.Writer) *App {
	return &App{stdin: stdin, stdout: stdout, stderr: stderr}
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		a.printRootUsage()
		return nil
	}

	switch args[0] {
	case "install":
		return a.runInstall(args[1:])
	case "capture":
		return a.runCapture(args[1:])
	case "reconcile":
		return a.runReconcile(args[1:])
	case "query":
		return a.runQuery(args[1:])
	case "analyze":
		return a.runAnalyze(args[1:])
	case "doctor":
		return a.runDoctor(args[1:])
	case "update", "upgrade":
		return a.runUpdate(args[1:])
	case "version", "--version":
		fmt.Fprintf(a.stdout, "%s\n", version.Version)
		return nil
	case "help", "--help", "-h":
		a.printRootUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) runInstall(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: arok install <harness> [flags]\nAvailable harnesses: copilot")
	}
	switch args[0] {
	case "copilot":
		return a.runInstallCopilot(args[1:])
	default:
		return fmt.Errorf("unknown harness %q — available: copilot", args[0])
	}
}

func (a *App) runInstallCopilot(args []string) error {
	fs := flag.NewFlagSet("install copilot", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		stateDirOverride string
		copilotHome      = install.DefaultCopilotHome()
		binaryPath       string
		printConfig      bool
	)
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&copilotHome, "copilot-home", copilotHome, "Override the Copilot home directory.")
	fs.StringVar(&binaryPath, "binary-path", "", "Override the binary path written into the Copilot hook config.")
	fs.BoolVar(&printConfig, "print-config", false, "Print the generated Copilot hook config instead of installing it.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	stateDir, err := config.ResolveStateDir(stateDirOverride)
	if err != nil {
		return err
	}
	if err := config.EnsureLayout(stateDir); err != nil {
		return err
	}

	if binaryPath == "" {
		binaryPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
	}

	db, err := store.Open(stateDir)
	if err != nil {
		return err
	}
	defer db.Close()

	if printConfig {
		raw, err := install.RenderCopilotConfig(binaryPath, stateDir)
		if err != nil {
			return err
		}
		_, err = a.stdout.Write(append(raw, '\n'))
		return err
	}

	result, err := install.InstallCopilot(binaryPath, stateDir, copilotHome)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "Installed Copilot hooks for Copilot CLI (sessionEnd) and VS Code (Stop).\nConfig: %s\nHook fragment: %s\nBinary: %s\nState dir: %s\nImport existing VS Code sessions with: arok capture --harness vscode --event scan\n", result.ConfigPath, result.FragmentPath, result.BinaryPath, result.StateDir)
	return nil
}

func (a *App) runCapture(args []string) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		harness          string
		eventName        string
		stateDirOverride string
		payloadFile      string
		noReconcile      bool
	)
	fs.StringVar(&harness, "harness", "", "Harness name.")
	fs.StringVar(&eventName, "event", "", "Hook event name.")
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&payloadFile, "payload-file", "", "Read the hook payload from a file instead of stdin.")
	fs.BoolVar(&noReconcile, "no-reconcile", false, "Skip detached reconciliation scheduling.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	switch harness {
	case "copilot":
		return a.runCaptureCopilot(eventName, stateDirOverride, payloadFile, noReconcile)
	case "vscode":
		return a.runCaptureVSCode(eventName, stateDirOverride, payloadFile)
	default:
		return fmt.Errorf("unsupported harness %q", harness)
	}
}

func (a *App) runCaptureCopilot(eventName, stateDirOverride, payloadFile string, noReconcile bool) error {
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
	payload, err := copilot.ParsePayload(payloadRaw)
	if err != nil {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s capture failed: %v\n", time.Now().UTC().Format(time.RFC3339Nano), err))
		return err
	}
	if payload.SessionID() == "" {
		return errors.New("Copilot payload is missing sessionId")
	}

	if err := appendCaptureEvent(stateDir, eventName, payload); err != nil {
		return err
	}

	meta := metadataFromEnv()
	sessionFile := copilot.ResolveSessionFile(payload)
	summary, err := summarizeWithRetry(copilot.SummarizeOptions{
		EventName:   eventName,
		StateDir:    stateDir,
		Payload:     payload,
		SessionFile: sessionFile,
		Meta:        meta,
	}, shutdownRetryAttempts(), shutdownRetryDelay())
	if err != nil {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s summarize failed for %s: %v\n", time.Now().UTC().Format(time.RFC3339Nano), payload.SessionID(), err))
		return err
	}

	db, err := store.Open(stateDir)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.UpsertSession(summary); err != nil {
		_ = appendLog(config.IngestLogPath(stateDir), fmt.Sprintf("%s database write failed for %s: %v\n", time.Now().UTC().Format(time.RFC3339Nano), payload.SessionID(), err))
		return err
	}

	if eventName == "sessionEnd" && summary.CaptureState != sessionpkg.CaptureStateFinal && !noReconcile {
		if err := spawnDetachedReconcile(stateDir, payloadRaw, payload.SessionID(), eventName, sessionFile); err != nil {
			_ = appendLog(config.ReconcileLogPath(stateDir), fmt.Sprintf("%s failed to schedule reconcile for %s: %v\n", time.Now().UTC().Format(time.RFC3339Nano), payload.SessionID(), err))
			return err
		}
	}

	return nil
}

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

func (a *App) runReconcile(args []string) error {
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		harness          string
		eventName        string
		stateDirOverride string
		payloadFile      string
		sessionFile      string
		sessionID        string
		attempts         int
		delay            time.Duration
		initialDelay     time.Duration
	)
	fs.StringVar(&harness, "harness", "", "Harness name.")
	fs.StringVar(&eventName, "event", "sessionEnd", "Hook event name.")
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&payloadFile, "payload-file", "", "Persisted hook payload file.")
	fs.StringVar(&sessionFile, "session-file", "", "Explicit session log path.")
	fs.StringVar(&sessionID, "session-id", "", "Session identifier.")
	fs.IntVar(&attempts, "attempts", asyncReconcileAttempts(), "Number of reconciliation attempts.")
	fs.DurationVar(&delay, "delay", asyncReconcileDelay(), "Delay between reconciliation attempts.")
	fs.DurationVar(&initialDelay, "initial-delay", asyncReconcileInitialDelay(), "Delay before the first reconciliation attempt.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if harness != "copilot" {
		return fmt.Errorf("unsupported harness %q", harness)
	}
	if payloadFile == "" || sessionFile == "" || sessionID == "" {
		return errors.New("reconcile requires --payload-file, --session-file, and --session-id")
	}

	stateDir, err := config.ResolveStateDir(stateDirOverride)
	if err != nil {
		return err
	}
	if err := config.EnsureLayout(stateDir); err != nil {
		return err
	}

	payloadRaw, err := os.ReadFile(payloadFile)
	if err != nil {
		return fmt.Errorf("read payload snapshot: %w", err)
	}
	payload, err := copilot.ParsePayload(payloadRaw)
	if err != nil {
		return err
	}

	if initialDelay > 0 {
		time.Sleep(initialDelay)
	}

	meta := metadataFromEnv()
	summary, err := summarizeWithRetry(copilot.SummarizeOptions{
		EventName:   eventName,
		StateDir:    stateDir,
		Payload:     payload,
		SessionFile: sessionFile,
		Meta:        meta,
	}, attempts, delay)
	if err != nil {
		return err
	}

	db, err := store.Open(stateDir)
	if err != nil {
		return err
	}
	defer db.Close()

	// If reconcile exhausted without finding shutdown metrics, mark as best_effort
	// rather than leaving it provisional forever. This is expected for sessions that
	// end without emitting session.shutdown.modelMetrics (e.g. abrupt exits).
	reconcileExhausted := summary.CaptureState != sessionpkg.CaptureStateFinal
	if reconcileExhausted {
		summary.CaptureState = sessionpkg.CaptureStateBestEffort
	}

	if err := db.UpsertSession(summary); err != nil {
		return err
	}

	if err := os.Remove(payloadFile); err != nil && !os.IsNotExist(err) {
		_ = appendLog(config.ReconcileLogPath(stateDir), fmt.Sprintf("%s cleanup failed for %s: %v\n", time.Now().UTC().Format(time.RFC3339Nano), sessionID, err))
	}

	if reconcileExhausted {
		message := fmt.Sprintf("%s reconcile exhausted before final totals for %s; marked best_effort\n", time.Now().UTC().Format(time.RFC3339Nano), sessionID)
		_ = appendLog(config.ReconcileLogPath(stateDir), message)
		return errors.New(strings.TrimSpace(message))
	}

	return nil
}

func (a *App) runQuery(args []string) error {
	subcommand := "sessions"
	if len(args) > 0 {
		subcommand = args[0]
		args = args[1:]
	}

	switch subcommand {
	case "sessions":
		return a.runQuerySessions(args)
	case "repos", "branches", "worktrees", "harnesses", "tasks":
		return a.runQueryGroups(subcommand, args)
	case "models":
		return a.runQueryModels(args)
	default:
		return fmt.Errorf("unknown query mode %q", subcommand)
	}
}

func (a *App) runQuerySessions(args []string) error {
	fs := flag.NewFlagSet("query sessions", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		stateDirOverride string
		sessionID        string
		latest           int
	)
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&sessionID, "session-id", "", "Show a single session as JSON.")
	fs.IntVar(&latest, "latest", 10, "How many sessions to list.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := a.openStore(stateDirOverride)
	if err != nil {
		return err
	}
	defer db.Close()

	if sessionID != "" {
		summary, err := db.GetSession(sessionID)
		if err != nil {
			return err
		}
		return writeJSON(a.stdout, summary)
	}

	rows, err := db.ListSessions(latest)
	if err != nil {
		return err
	}
	writeTable(a.stdout, []string{"SESSION", "HARNESS", "STATE", "USAGE_SOURCE", "HOST", "BRANCH", "WORKTREE", "INPUT", "OUTPUT", "ENDED_AT"}, func() [][]string {
		out := make([][]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, []string{
				row.SessionID,
				row.Harness,
				row.CaptureState,
				row.UsageSource,
				row.HostName,
				row.RepoBranch,
				row.WorktreeRoot,
				fmt.Sprintf("%d", row.TotalInputTokens),
				fmt.Sprintf("%d", row.TotalOutputTokens),
				row.EndedAt,
			})
		}
		return out
	}())
	return nil
}

func (a *App) runQueryGroups(kind string, args []string) error {
	fs := flag.NewFlagSet("query groups", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		stateDirOverride string
		sinceRaw         string
		limit            int
	)
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&sinceRaw, "since", "", "Only include sessions newer than now-duration (for example 168h).")
	fs.IntVar(&limit, "limit", 10, "Limit results.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	since, err := parseSince(sinceRaw)
	if err != nil {
		return err
	}

	groupBy := map[string]string{
		"repos":     "repo",
		"branches":  "branch",
		"worktrees": "worktree",
		"harnesses": "harness",
		"tasks":     "task",
	}[kind]

	db, err := a.openStore(stateDirOverride)
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.GroupTotals(groupBy, since, limit)
	if err != nil {
		return err
	}

	writeTable(a.stdout, []string{"KEY", "SESSIONS", "INPUT", "OUTPUT", "CACHE_READ", "CACHE_WRITE", "REASONING"}, func() [][]string {
		out := make([][]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, []string{
				row.Key,
				fmt.Sprintf("%d", row.Sessions),
				fmt.Sprintf("%d", row.TotalInputTokens),
				fmt.Sprintf("%d", row.TotalOutputTokens),
				fmt.Sprintf("%d", row.TotalCacheReadTokens),
				fmt.Sprintf("%d", row.TotalCacheWriteTokens),
				fmt.Sprintf("%d", row.TotalReasoningTokens),
			})
		}
		return out
	}())
	return nil
}

func (a *App) runQueryModels(args []string) error {
	fs := flag.NewFlagSet("query models", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		stateDirOverride string
		sinceRaw         string
		limit            int
	)
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&sinceRaw, "since", "", "Only include sessions newer than now-duration.")
	fs.IntVar(&limit, "limit", 10, "Limit results.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	since, err := parseSince(sinceRaw)
	if err != nil {
		return err
	}

	db, err := a.openStore(stateDirOverride)
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.AggregateModels(since, limit)
	if err != nil {
		return err
	}

	writeTable(a.stdout, []string{"MODEL", "SESSIONS", "ASSISTANT_MSGS", "REQUESTS", "INPUT", "OUTPUT", "CACHE_READ", "CACHE_WRITE", "REASONING"}, func() [][]string {
		out := make([][]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, []string{
				row.Model,
				fmt.Sprintf("%d", row.Sessions),
				fmt.Sprintf("%d", row.AssistantMessages),
				fmt.Sprintf("%d", row.RequestCount),
				fmt.Sprintf("%d", row.TotalInputTokens),
				fmt.Sprintf("%d", row.TotalOutputTokens),
				fmt.Sprintf("%d", row.TotalCacheReadTokens),
				fmt.Sprintf("%d", row.TotalCacheWriteTokens),
				fmt.Sprintf("%d", row.TotalReasoningTokens),
			})
		}
		return out
	}())
	return nil
}

func (a *App) runAnalyze(args []string) error {
	subcommand := "overview"
	if len(args) > 0 {
		subcommand = args[0]
		args = args[1:]
	}

	switch subcommand {
	case "overview":
		return a.runAnalyzeOverview(args)
	case "missing-finals":
		return a.runAnalyzeMissingFinals(args)
	default:
		return fmt.Errorf("unknown analyze mode %q", subcommand)
	}
}

func (a *App) runAnalyzeOverview(args []string) error {
	fs := flag.NewFlagSet("analyze overview", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		stateDirOverride string
		sinceRaw         string
		limit            int
	)
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&sinceRaw, "since", "", "Only include sessions newer than now-duration.")
	fs.IntVar(&limit, "limit", 5, "Limit grouped outputs.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	since, err := parseSince(sinceRaw)
	if err != nil {
		return err
	}

	db, err := a.openStore(stateDirOverride)
	if err != nil {
		return err
	}
	defer db.Close()

	overview, err := db.Overview(since, limit)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "sessions\t%d\nfinal_sessions\t%d\nprovisional_sessions\t%d\nbest_effort_sessions\t%d\ntotal_input_tokens\t%d\ntotal_output_tokens\t%d\ntotal_cache_read_tokens\t%d\ntotal_cache_write_tokens\t%d\ntotal_reasoning_tokens\t%d\n\n",
		overview.TotalSessions,
		overview.FinalSessions,
		overview.ProvisionalSessions,
		overview.BestEffortSessions,
		overview.TotalInputTokens,
		overview.TotalOutputTokens,
		overview.TotalCacheReadTokens,
		overview.TotalCacheWriteTokens,
		overview.TotalReasoningTokens,
	)
	writeGroupTable(a.stdout, "top repos", overview.TopRepos)
	writeGroupTable(a.stdout, "top branches", overview.TopBranches)
	writeModelTable(a.stdout, "top models", overview.TopModels)
	if len(overview.MissingFinals) > 0 {
		writeSessionTable(a.stdout, "sessions missing final totals", overview.MissingFinals)
	}
	return nil
}

func (a *App) runAnalyzeMissingFinals(args []string) error {
	fs := flag.NewFlagSet("analyze missing-finals", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		stateDirOverride string
		limit            int
	)
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.IntVar(&limit, "limit", 20, "Limit results.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := a.openStore(stateDirOverride)
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.ListNonFinalSessions(limit)
	if err != nil {
		return err
	}
	writeSessionTable(a.stdout, "sessions missing final totals", rows)
	return nil
}

func (a *App) runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	var (
		stateDirOverride string
		copilotHome      = install.DefaultCopilotHome()
	)
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&copilotHome, "copilot-home", copilotHome, "Override the Copilot home directory.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	stateDir, err := config.ResolveStateDir(stateDirOverride)
	if err != nil {
		return err
	}

	dbPath := config.DBPath(stateDir)
	configPath := filepath.Join(copilotHome, "hooks", "arok-copilot.json")
	_, dbErr := os.Stat(dbPath)
	_, cfgErr := os.Stat(configPath)

	var (
		sessionCount int64
		nonFinals    int64
		bestEffort   int64
		vscodeCount  int64
	)
	if dbErr == nil {
		db, err := store.Open(stateDir)
		if err != nil {
			return err
		}
		defer db.Close()
		sessionCount, err = db.CountSessions()
		if err != nil {
			return err
		}
		nonFinals, err = db.CountNonFinalSessions()
		if err != nil {
			return err
		}
		bestEffort, err = db.CountBestEffortSessions()
		if err != nil {
			return err
		}
		vscodeCount, err = db.CountSessionsByHarness(sessionpkg.HarnessVSCodeCopilot)
		if err != nil {
			return err
		}
	}

	// Non-blocking version check with short timeout.
	latestVersion := ""
	updateAvailable := false
	vctx, vcancel := context.WithTimeout(context.Background(), update.CheckTimeout)
	defer vcancel()
	if rel, err := update.LatestRelease(vctx); err == nil {
		latestVersion = rel.TagName
		updateAvailable = update.IsNewer(version.Version, latestVersion)
	}

	updateStatus := "up to date"
	if updateAvailable {
		updateStatus = fmt.Sprintf("yes (%s available — run: arok update)", latestVersion)
	} else if latestVersion == "" {
		updateStatus = "unknown (offline)"
	}

	fmt.Fprintf(a.stdout, "version\t%s\nupdate_available\t%s\nstate_dir\t%s\ndatabase\t%s\ncopilot_hook\t%s\nvscode_user_data_dir\t%s\nvscode_sessions_present\t%s\nvscode_sessions\t%d\nsessions\t%d\nnon_final_sessions\t%d\nbest_effort_sessions\t%d\n",
		version.Version,
		updateStatus,
		stateDir,
		statusString(dbErr == nil),
		statusString(cfgErr == nil),
		displayString(vscode.DefaultUserDataDir(), "(not determinable)"),
		statusString(vscodeCount > 0),
		vscodeCount,
		sessionCount,
		nonFinals,
		bestEffort,
	)
	if dbErr != nil || cfgErr != nil {
		return errors.New("doctor found missing installation pieces")
	}
	return nil
}

func (a *App) runUpdate(_ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Fprintf(a.stdout, "Checking for updates...\n")

	release, err := update.LatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}

	if !update.IsNewer(version.Version, release.TagName) {
		fmt.Fprintf(a.stdout, "Already up to date (%s).\n", version.Version)
		return nil
	}

	fmt.Fprintf(a.stdout, "Updating %s → %s...\n", version.Version, release.TagName)

	newTag, err := update.SelfUpdate(ctx)
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Fprintf(a.stdout, "Updated to %s.\n", newTag)
	return nil
}

func (a *App) openStore(stateDirOverride string) (*store.Store, error) {
	stateDir, err := config.ResolveStateDir(stateDirOverride)
	if err != nil {
		return nil, err
	}
	if err := config.EnsureLayout(stateDir); err != nil {
		return nil, err
	}
	return store.Open(stateDir)
}

func (a *App) printRootUsage() {
	fmt.Fprintf(a.stdout, "arok %s\n\nCommands:\n  install copilot\n  capture --harness [copilot|vscode] --event <event>\n  reconcile --harness copilot\n  query [sessions|repos|branches|worktrees|harnesses|tasks|models]\n  analyze [overview|missing-finals]\n  doctor\n  update\n  version\n", version.Version)
}

func summarizeWithRetry(opts copilot.SummarizeOptions, attempts int, delay time.Duration) (sessionpkg.SessionSummary, error) {
	if attempts < 1 {
		attempts = 1
	}

	var summary sessionpkg.SessionSummary
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		summary, err = copilot.Summarize(opts)
		if err != nil {
			return sessionpkg.SessionSummary{}, err
		}
		if opts.EventName != "sessionEnd" || summary.CaptureState == sessionpkg.CaptureStateFinal || attempt == attempts {
			return summary, nil
		}
		time.Sleep(delay)
	}
	return summary, nil
}

func metadataFromEnv() copilot.Meta {
	return copilot.Meta{
		TaskID:  strings.TrimSpace(os.Getenv("LLM_USAGE_TASK_ID")),
		Title:   strings.TrimSpace(os.Getenv("LLM_USAGE_TITLE")),
		Summary: strings.TrimSpace(os.Getenv("LLM_USAGE_SUMMARY")),
		Tags:    splitCSV(os.Getenv("LLM_USAGE_TAGS")),
	}
}

func appendCaptureEvent(stateDir, eventName string, payload copilot.Payload) error {
	record := map[string]any{
		"captured_at": time.Now().UTC().Format(time.RFC3339Nano),
		"event_name":  eventName,
		"payload":     payload,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return appendLog(config.CaptureLogPath(stateDir), string(raw)+"\n")
}

func appendLog(path, line string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.WriteString(file, line)
	return err
}

func spawnDetachedReconcile(stateDir string, payloadRaw []byte, sessionID, eventName, sessionFile string) error {
	snapshot, err := os.CreateTemp(config.ReconcileDir(stateDir), sessionID+".payload.*.json")
	if err != nil {
		return err
	}
	if _, err := snapshot.Write(payloadRaw); err != nil {
		snapshot.Close()
		return err
	}
	if err := snapshot.Close(); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	logFile, err := os.OpenFile(config.ReconcileLogPath(stateDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(
		exe,
		"reconcile",
		"--harness", "copilot",
		"--event", eventName,
		"--state-dir", stateDir,
		"--payload-file", snapshot.Name(),
		"--session-file", sessionFile,
		"--session-id", sessionID,
		"--attempts", fmt.Sprintf("%d", asyncReconcileAttempts()),
		"--delay", asyncReconcileDelay().String(),
		"--initial-delay", asyncReconcileInitialDelay().String(),
	)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

func readPayload(stdin io.Reader, payloadFile string) ([]byte, error) {
	if payloadFile != "" {
		return os.ReadFile(payloadFile)
	}
	return io.ReadAll(stdin)
}

func parseSince(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return nil, fmt.Errorf("parse --since: %w", err)
	}
	cutoff := time.Now().UTC().Add(-duration)
	return &cutoff, nil
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeTable(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

func writeGroupTable(w io.Writer, title string, rows []sessionpkg.GroupTotal) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "%s\n", title)
	writeTable(w, []string{"KEY", "SESSIONS", "INPUT", "OUTPUT", "CACHE_READ", "CACHE_WRITE", "REASONING"}, func() [][]string {
		out := make([][]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, []string{
				row.Key,
				fmt.Sprintf("%d", row.Sessions),
				fmt.Sprintf("%d", row.TotalInputTokens),
				fmt.Sprintf("%d", row.TotalOutputTokens),
				fmt.Sprintf("%d", row.TotalCacheReadTokens),
				fmt.Sprintf("%d", row.TotalCacheWriteTokens),
				fmt.Sprintf("%d", row.TotalReasoningTokens),
			})
		}
		return out
	}())
	fmt.Fprintln(w)
}

func writeModelTable(w io.Writer, title string, rows []sessionpkg.ModelTotal) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "%s\n", title)
	writeTable(w, []string{"MODEL", "SESSIONS", "ASSISTANT_MSGS", "REQUESTS", "INPUT", "OUTPUT", "CACHE_READ", "CACHE_WRITE", "REASONING"}, func() [][]string {
		out := make([][]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, []string{
				row.Model,
				fmt.Sprintf("%d", row.Sessions),
				fmt.Sprintf("%d", row.AssistantMessages),
				fmt.Sprintf("%d", row.RequestCount),
				fmt.Sprintf("%d", row.TotalInputTokens),
				fmt.Sprintf("%d", row.TotalOutputTokens),
				fmt.Sprintf("%d", row.TotalCacheReadTokens),
				fmt.Sprintf("%d", row.TotalCacheWriteTokens),
				fmt.Sprintf("%d", row.TotalReasoningTokens),
			})
		}
		return out
	}())
	fmt.Fprintln(w)
}

func writeSessionTable(w io.Writer, title string, rows []sessionpkg.SessionListItem) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "%s\n", title)
	writeTable(w, []string{"SESSION", "STATE", "USAGE_SOURCE", "HOST", "BRANCH", "WORKTREE", "OUTPUT", "ENDED_AT"}, func() [][]string {
		out := make([][]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, []string{
				row.SessionID,
				row.CaptureState,
				row.UsageSource,
				row.HostName,
				row.RepoBranch,
				row.WorktreeRoot,
				fmt.Sprintf("%d", row.TotalOutputTokens),
				row.EndedAt,
			})
		}
		return out
	}())
	fmt.Fprintln(w)
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func shutdownRetryAttempts() int {
	return envInt("AROK_COPILOT_SHUTDOWN_RETRY_ATTEMPTS", 6)
}

func shutdownRetryDelay() time.Duration {
	return envDuration("AROK_COPILOT_SHUTDOWN_RETRY_DELAY", 500*time.Millisecond)
}

func asyncReconcileAttempts() int {
	return envInt("AROK_COPILOT_RECONCILE_ATTEMPTS", 12)
}

func asyncReconcileDelay() time.Duration {
	return envDuration("AROK_COPILOT_RECONCILE_DELAY", time.Second)
}

func asyncReconcileInitialDelay() time.Duration {
	return envDuration("AROK_COPILOT_RECONCILE_INITIAL_DELAY", 2*time.Second)
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func statusString(ok bool) string {
	if ok {
		return "ok"
	}
	return "missing"
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

	totalOutputTokens := int64(0)
	totalInputTokens := int64(0)
	modelStats := map[string]int64{}
	modelCounts := map[string]int64{}
	endedAt := sessionData.CreationDate
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

func displayString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
