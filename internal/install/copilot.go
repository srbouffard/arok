package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CopilotInstallResult struct {
	ConfigPath   string
	FragmentPath string
	BinaryPath   string
	StateDir     string
}

func InstallCopilot(binaryPath, stateDir, copilotHome string) (CopilotInstallResult, error) {
	binaryPath, err := filepath.Abs(binaryPath)
	if err != nil {
		return CopilotInstallResult{}, fmt.Errorf("resolve binary path: %w", err)
	}

	configBytes, err := RenderCopilotConfig(binaryPath, stateDir)
	if err != nil {
		return CopilotInstallResult{}, err
	}

	configPath := filepath.Join(copilotHome, "hooks", "arok-copilot.json")
	fragmentPath := filepath.Join(stateDir, "hooks", "copilot.json")

	for _, dir := range []string{filepath.Dir(configPath), filepath.Dir(fragmentPath)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return CopilotInstallResult{}, fmt.Errorf("create %s: %w", dir, err)
		}
	}

	for _, path := range []string{configPath, fragmentPath} {
		if err := os.WriteFile(path, append(configBytes, '\n'), 0o644); err != nil {
			return CopilotInstallResult{}, fmt.Errorf("write %s: %w", path, err)
		}
	}

	return CopilotInstallResult{
		ConfigPath:   configPath,
		FragmentPath: fragmentPath,
		BinaryPath:   binaryPath,
		StateDir:     stateDir,
	}, nil
}

func RenderCopilotConfig(binaryPath, stateDir string) ([]byte, error) {
	cliCommand := fmt.Sprintf("%s capture --harness copilot --event sessionEnd", shellQuote(binaryPath))
	vscodeCommand := fmt.Sprintf("%s capture --harness vscode --event Stop", shellQuote(binaryPath))
	config := map[string]any{
		"version": 1,
		"hooks": map[string]any{
			"sessionEnd": []map[string]any{
				{
					"type":       "command",
					"bash":       cliCommand,
					"timeoutSec": 10,
					"env": map[string]string{
						"AROK_STATE_DIR": stateDir,
					},
				},
			},
			"Stop": []map[string]any{
				{
					"type":       "command",
					"bash":       vscodeCommand,
					"timeoutSec": 15,
					"env": map[string]string{
						"AROK_STATE_DIR": stateDir,
					},
				},
			},
		},
	}

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal Copilot hook config: %w", err)
	}

	return out, nil
}

func DefaultCopilotHome() string {
	if home := os.Getenv("COPILOT_HOME"); home != "" {
		return home
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return ".copilot"
	}
	return filepath.Join(userHome, ".copilot")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
