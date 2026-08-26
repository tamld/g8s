package initwiz

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectAndConfigureMCP(t *testing.T) {
	tempHome := t.TempDir()

	// 1. Pre-create Cursor config dir with existing config
	cursorDir := filepath.Join(tempHome, ".cursor")
	if err := os.MkdirAll(cursorDir, 0700); err != nil {
		t.Fatalf("mkdir cursor: %v", err)
	}
	existingConfig := map[string]any{
		"customKey": "preservedValue",
		"mcpServers": map[string]any{
			"otherServer": map[string]any{
				"command": "other-bin",
			},
		},
	}
	raw, _ := json.Marshal(existingConfig)
	cursorFile := filepath.Join(cursorDir, "mcp.json")
	if err := os.WriteFile(cursorFile, raw, 0600); err != nil {
		t.Fatalf("write cursor config: %v", err)
	}

	// 2. Configure MCP for Cursor
	binPath := "/usr/local/bin/g8s"
	if err := ConfigureMCP(cursorFile, binPath); err != nil {
		t.Fatalf("ConfigureMCP: %v", err)
	}

	// 3. Verify merged configuration
	data, err := os.ReadFile(cursorFile)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}

	var updated map[string]any
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatalf("unmarshal updated config: %v", err)
	}

	if updated["customKey"] != "preservedValue" {
		t.Errorf("expected customKey to be preserved, got %v", updated["customKey"])
	}

	servers, ok := updated["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers not a map: %T", updated["mcpServers"])
	}

	if _, exists := servers["otherServer"]; !exists {
		t.Errorf("expected otherServer to be preserved")
	}

	g8sConfig, ok := servers["g8s"].(map[string]any)
	if !ok {
		t.Fatalf("g8s config missing or not a map")
	}
	if g8sConfig["command"] != binPath {
		t.Errorf("command = %v, want %v", g8sConfig["command"], binPath)
	}
}

func TestRunInitFullLifecycle(t *testing.T) {
	tempHome := t.TempDir()
	binPath := "/opt/bin/g8s"

	res, err := RunInit([]string{IDECursor, IDEClaudeDesktop}, tempHome, binPath)
	if err != nil {
		t.Fatalf("RunInit: %v", err)
	}

	// Verify state and evidence directories
	if _, err := os.Stat(res.StateDir); err != nil {
		t.Errorf("state dir does not exist: %v", err)
	}
	if _, err := os.Stat(res.EvidenceDir); err != nil {
		t.Errorf("evidence dir does not exist: %v", err)
	}

	// Verify configured IDEs
	if len(res.ConfiguredIDEs) != 2 {
		t.Fatalf("configured %d IDEs, want 2", len(res.ConfiguredIDEs))
	}
}
