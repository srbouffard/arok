package cli

// Copilot CLI harness — capture, reconcile, and install support.
//
// To add a new harness, follow the same pattern as this file:
//   1. Create internal/cli/app_<harness>.go with a runCapture<Harness>() method and helpers.
//   2. Create internal/<harness>/ for the payload parsing and summarizing logic.
//   3. Add a case in runCapture() in app.go that calls runCapture<Harness>().
//   4. Optionally add runInstall<Harness>() and a case in runInstall().
//
// See docs/adding-a-harness.md for the full walkthrough.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/srbouffard/arok/internal/config"
	"github.com/srbouffard/arok/internal/copilot"
	"github.com/srbouffard/arok/internal/install"
	sessionpkg "github.com/srbouffard/arok/internal/session"
	"github.com/srbouffard/arok/internal/store"
)

// runInstallCopilot handles: arok install copilot [flags]
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

// runCaptureCopilot handles: arok capture --harness copilot --event <event>
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

// runReconcile handles: arok reconcile --harness copilot [flags]
//
// Reconcile is a copilot-specific background process: it polls events.jsonl until
// session.shutdown emits final modelMetrics, then upgrades the stored record from
// provisional to final. Harnesses that produce final data at capture time do not
// need this command.
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

func summarizeWithRetry(opts copilot.SummarizeOptions, attempts int, delay time.Duration) (sessionpkg.SessionSummary, error) {
	if attempts < 1 {
		attempts = 1
	}
	var (
		summary sessionpkg.SessionSummary
		err     error
	)
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

func shutdownRetryAttempts() int { return envInt("AROK_COPILOT_SHUTDOWN_RETRY_ATTEMPTS", 6) }
func shutdownRetryDelay() time.Duration {
	return envDuration("AROK_COPILOT_SHUTDOWN_RETRY_DELAY", 500*time.Millisecond)
}
func asyncReconcileAttempts() int { return envInt("AROK_COPILOT_RECONCILE_ATTEMPTS", 12) }
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
