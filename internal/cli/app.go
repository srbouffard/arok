package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/srbouffard/arok/internal/config"
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

// runCapture dispatches to the harness-specific capture implementation.
//
// To add a new harness:
//  1. Add a case below calling a.runCapture<Harness>().
//  2. Create internal/cli/app_<harness>.go with that method.
//  3. Create internal/<harness>/ for the data parsing logic.
//
// See docs/adding-a-harness.md for a full walkthrough.
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

func (a *App) runQuery(args []string) error {
	subcommand := "sessions"
	if len(args) > 0 {
		subcommand = args[0]
		args = args[1:]
	}

	switch subcommand {
	case "sessions":
		return a.runQuerySessions(args)
	case "repos", "branches", "worktrees", "harnesses", "tasks", "hosts":
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
		filterHost       string
		filterRepo       string
		filterBranch     string
		sinceRaw         string
	)
	fs.StringVar(&stateDirOverride, "state-dir", "", "Override the AROK state directory.")
	fs.StringVar(&sessionID, "session-id", "", "Show a single session as JSON.")
	fs.IntVar(&latest, "latest", 20, "How many sessions to list.")
	fs.StringVar(&filterHost, "host", "", "Filter by host name.")
	fs.StringVar(&filterRepo, "repo", "", "Filter by repo remote.")
	fs.StringVar(&filterBranch, "branch", "", "Filter by branch name.")
	fs.StringVar(&sinceRaw, "since", "", "Only include sessions newer than now-duration (e.g. 168h).")
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

	since, err := parseSince(sinceRaw)
	if err != nil {
		return err
	}

	hasFilter := filterHost != "" || filterRepo != "" || filterBranch != "" || since != nil
	if hasFilter {
		filter := sessionpkg.SessionFilter{
			Host:   filterHost,
			Repo:   filterRepo,
			Branch: filterBranch,
			Since:  since,
		}
		rows, totals, err := db.FilteredSessions(filter, latest)
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
		fmt.Fprintf(a.stdout, "\ntotals  sessions=%d  input=%d  output=%d  cache_read=%d  cache_write=%d  reasoning=%d\n",
			totals.Sessions,
			totals.TotalInputTokens,
			totals.TotalOutputTokens,
			totals.TotalCacheReadTokens,
			totals.TotalCacheWriteTokens,
			totals.TotalReasoningTokens,
		)
		return nil
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
		"hosts":     "host",
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
	writeGroupTable(a.stdout, "top hosts", overview.TopHosts)
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
	fmt.Fprintf(a.stdout, "arok %s\n\nCommands:\n  install copilot\n  capture --harness [copilot|vscode] --event <event>\n  reconcile --harness copilot\n  query [sessions|hosts|repos|branches|worktrees|harnesses|tasks|models]\n  analyze [overview|missing-finals]\n  doctor\n  update\n  version\n", version.Version)
}

func readPayload(stdin io.Reader, payloadFile string) ([]byte, error) {
	if payloadFile != "" {
		return os.ReadFile(payloadFile)
	}
	return io.ReadAll(stdin)
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

func statusString(ok bool) string {
	if ok {
		return "ok"
	}
	return "missing"
}

func displayString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
