package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/srbouffard/arok/internal/config"
	"github.com/srbouffard/arok/internal/copilot"
	"github.com/srbouffard/arok/internal/install"
	sessionpkg "github.com/srbouffard/arok/internal/session"
	"github.com/srbouffard/arok/internal/store"
	"github.com/srbouffard/arok/internal/version"
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
	if len(args) == 0 || args[0] != "copilot" {
		return errors.New("usage: arok install copilot [--state-dir ABSOLUTE_PATH] [--copilot-home PATH] [--binary-path PATH] [--print-config]")
	}

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
	if err := fs.Parse(args[1:]); err != nil {
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

	fmt.Fprintf(a.stdout, "Installed Copilot hook.\nConfig: %s\nHook fragment: %s\nBinary: %s\nState dir: %s\n", result.ConfigPath, result.FragmentPath, result.BinaryPath, result.StateDir)
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

	if harness != "copilot" {
		return fmt.Errorf("unsupported harness %q", harness)
	}
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
	}

	fmt.Fprintf(a.stdout, "state_dir\t%s\ndatabase\t%s\ncopilot_hook\t%s\nsessions\t%d\nnon_final_sessions\t%d\nbest_effort_sessions\t%d\n",
		stateDir,
		statusString(dbErr == nil),
		statusString(cfgErr == nil),
		sessionCount,
		nonFinals,
		bestEffort,
	)
	if dbErr != nil || cfgErr != nil {
		return errors.New("doctor found missing installation pieces")
	}
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
	fmt.Fprintf(a.stdout, "arok %s\n\nCommands:\n  install copilot\n  capture --harness copilot --event sessionEnd\n  reconcile --harness copilot\n  query [sessions|repos|branches|worktrees|harnesses|tasks|models]\n  analyze [overview|missing-finals]\n  doctor\n  version\n", version.Version)
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
