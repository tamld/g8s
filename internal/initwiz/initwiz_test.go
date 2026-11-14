package initwiz

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

func TestDetectAndConfigureMCP(t *testing.T) {
	tempHome := t.TempDir()

	// 1. Pre-create Cursor config dir with existing config
	cursorDir := filepath.Join(tempHome, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o700); err != nil {
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
	if err := os.WriteFile(cursorFile, raw, 0o600); err != nil {
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
	t.Setenv("XDG_CONFIG_HOME", "")
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

func TestInitwizConfigPassesValidator(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	binPath := "/opt/bin/g8s"

	res, err := RunInit([]string{IDECursor}, tempHome, binPath)
	if err != nil {
		t.Fatalf("RunInit: %v", err)
	}

	if res.ProvidersConfig == "" {
		t.Fatal("expected ProvidersConfig to be set")
	}

	cfg, err := config.Load(res.ProvidersConfig)
	if err != nil {
		t.Fatalf("config.Load failed for initwiz providers.json: %v", err)
	}

	if len(cfg.Providers) != 4 {
		t.Fatalf("expected 4 providers, got %d", len(cfg.Providers))
	}

	// Provider 1: 9router (api_call)
	nineRouter := cfg.Providers[0]
	if nineRouter.Class != "api_call" || nineRouter.Name != "9router" || nineRouter.Protocol != "9router" || nineRouter.BaseURL != "http://127.0.0.1:20128/v1" || nineRouter.BaseURLEnv != "NINEROUTER_URL" || nineRouter.AuthEnv != "OPENAI_API_KEY" || !nineRouter.AllowUnlistedModels || len(nineRouter.Models) != 2 || nineRouter.Models[0].ID != "reasoning-high" || nineRouter.Models[1].ID != "fast-agent" {
		t.Errorf("unexpected 9router provider config: %+v", nineRouter)
	}

	// Provider 2: opencode (platform_dispatch)
	opencode := cfg.Providers[1]
	if opencode.Class != "platform_dispatch" || opencode.Name != "opencode" || len(opencode.Models) != 1 || opencode.Models[0].ID != "9router/reasoning-high" || len(opencode.Args) != 5 || opencode.Args[0] != "opencode" || opencode.Args[1] != "run" || opencode.Args[2] != "-m" || opencode.Args[3] != "{model}" || opencode.Args[4] != "{prompt}" {
		t.Errorf("unexpected opencode provider config: %+v", opencode)
	}

	// Provider 3: agy (platform_dispatch)
	agy := cfg.Providers[2]
	if agy.Class != "platform_dispatch" || agy.Name != "agy" || len(agy.Models) != 2 || agy.Models[0].ID != "gemini-4-hydrogen-low" || agy.Models[1].ID != "gemini-4-hydrogen-high" || len(agy.Args) != 8 || agy.Args[0] != "agy" || agy.Args[1] != "--print" || agy.Args[2] != "--model" || agy.Args[3] != "{model}" || agy.Args[4] != "--mode" || agy.Args[5] != "agent" || agy.Args[6] != "--prompt" || agy.Args[7] != "{prompt}" {
		t.Errorf("unexpected agy provider config: %+v", agy)
	}

	// Provider 4: claude (platform_dispatch)
	claude := cfg.Providers[3]
	if claude.Class != "platform_dispatch" || claude.Name != "claude" || len(claude.Models) != 2 || claude.Models[0].ID != "claude-sonnet-4-6" || claude.Models[1].ID != "claude-haiku-4-5" || len(claude.Args) != 3 || claude.Args[0] != "claude" || claude.Args[1] != "-p" || claude.Args[2] != "{prompt}" {
		t.Errorf("unexpected claude provider config: %+v", claude)
	}

	// Ensure no gemini-3.8- strings exist
	for _, p := range cfg.Providers {
		for _, m := range p.Models {
			if strings.Contains(m.ID, "gemini-3.8-") {
				t.Errorf("found deprecated gemini-3.8- model ID: %s", m.ID)
			}
		}
	}
}
