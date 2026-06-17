package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStateDirRequiresAbsoluteOverride(t *testing.T) {
	t.Setenv("AROK_STATE_DIR", "")
	if _, err := ResolveStateDir("relative/path"); err == nil {
		t.Fatal("expected relative override to fail")
	}
}

func TestDefaultStateDirUsesXDGStateHome(t *testing.T) {
	t.Setenv("AROK_STATE_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	got, err := DefaultStateDir()
	if err != nil {
		t.Fatalf("DefaultStateDir returned error: %v", err)
	}
	if got != "/tmp/xdg-state/arok" {
		t.Fatalf("unexpected default state dir: %s", got)
	}
}

func TestEnsureLayoutCreatesExpectedDirectories(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	if err := EnsureLayout(stateDir); err != nil {
		t.Fatalf("EnsureLayout returned error: %v", err)
	}

	for _, path := range []string{
		stateDir,
		HooksDir(stateDir),
		LogsDir(stateDir),
		ReconcileDir(stateDir),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}
