package install

import (
	"encoding/json"
	"testing"
)

func TestRenderCopilotConfigIncludesCaptureCommand(t *testing.T) {
	raw, err := RenderCopilotConfig("/home/test/.local/bin/arok", "/tmp/arok-state")
	if err != nil {
		t.Fatalf("RenderCopilotConfig returned error: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	hooks := config["hooks"].(map[string]any)
	sessionEnd := hooks["sessionEnd"].([]any)
	entry := sessionEnd[0].(map[string]any)
	if entry["bash"] != "'/home/test/.local/bin/arok' capture --harness copilot --event sessionEnd" {
		t.Fatalf("unexpected command: %v", entry["bash"])
	}
}
