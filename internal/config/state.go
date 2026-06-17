package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func DefaultStateDir() (string, error) {
	if override := os.Getenv("AROK_STATE_DIR"); override != "" {
		return ResolveStateDir(override)
	}

	if xdgStateHome := os.Getenv("XDG_STATE_HOME"); xdgStateHome != "" {
		return filepath.Join(xdgStateHome, "arok"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(home, ".local", "state", "arok"), nil
}

func ResolveStateDir(override string) (string, error) {
	if override == "" {
		return DefaultStateDir()
	}

	if !filepath.IsAbs(override) {
		return "", errors.New("AROK_STATE_DIR must be an absolute path")
	}

	return filepath.Clean(override), nil
}

func EnsureLayout(stateDir string) error {
	dirs := []string{
		stateDir,
		HooksDir(stateDir),
		LogsDir(stateDir),
		ReconcileDir(stateDir),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	return nil
}

func DBPath(stateDir string) string {
	return filepath.Join(stateDir, "usage.db")
}

func HooksDir(stateDir string) string {
	return filepath.Join(stateDir, "hooks")
}

func LogsDir(stateDir string) string {
	return filepath.Join(stateDir, "logs")
}

func ReconcileDir(stateDir string) string {
	return filepath.Join(stateDir, "reconcile")
}

func CaptureLogPath(stateDir string) string {
	return filepath.Join(LogsDir(stateDir), "copilot-hook-events.jsonl")
}

func IngestLogPath(stateDir string) string {
	return filepath.Join(LogsDir(stateDir), "ingest-errors.log")
}

func ReconcileLogPath(stateDir string) string {
	return filepath.Join(LogsDir(stateDir), "reconcile.log")
}
