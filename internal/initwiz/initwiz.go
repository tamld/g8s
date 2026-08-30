// Package initwiz provides interactive and headless onboarding wizards for g8s,
// auto-detecting installed AI IDEs and generating MCP configuration blocks.
package initwiz

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Supported IDE identifiers.
const (
	IDECursor        = "cursor"
	IDEClaudeDesktop = "claude"
	IDEWindsurf      = "windsurf"
	IDEAntigravity   = "antigravity"
)

// SupportedIDEs enumerates recognized IDEs.
var SupportedIDEs = []string{IDECursor, IDEClaudeDesktop, IDEWindsurf, IDEAntigravity}

// DetectedIDE represents an IDE found on the local filesystem.
type DetectedIDE struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ConfigPath string `json:"config_path"`
	Installed  bool   `json:"installed"`
	Configured bool   `json:"configured"`
}

// InitResult summarizes the outcome of the initialization process.
type InitResult struct {
	StateDir        string        `json:"state_dir"`
	EvidenceDir     string        `json:"evidence_dir"`
	BinaryPath      string        `json:"binary_path"`
	ConfiguredIDEs  []DetectedIDE `json:"configured_ides"`
	ProvidersConfig string        `json:"providers_config,omitempty"`
	CreatedDirs     []string      `json:"created_dirs"`
}

// DetectIDEs inspects the operating system and user directories for supported IDEs.
func DetectIDEs(homeDir string) ([]DetectedIDE, error) {
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home directory: %w", err)
		}
	}

	var results []DetectedIDE

	// 1. Cursor
	cursorPath := filepath.Join(homeDir, ".cursor", "mcp.json")
	if runtime.GOOS == "darwin" {
		cursorPath = filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "globalStorage", "cursor.mcp", "mcp.json")
	} else if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		cursorPath = filepath.Join(appData, "Cursor", "User", "globalStorage", "cursor.mcp", "mcp.json")
	}
	results = append(results, checkIDE(IDECursor, "Cursor IDE", cursorPath))

	// 2. Claude Desktop
	claudePath := filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json")
	if runtime.GOOS == "darwin" {
		claudePath = filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	} else if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		claudePath = filepath.Join(appData, "Claude", "claude_desktop_config.json")
	}
	results = append(results, checkIDE(IDEClaudeDesktop, "Claude Desktop", claudePath))

	// 3. Windsurf
	windsurfPath := filepath.Join(homeDir, ".codeium", "windsurf", "mcp_config.json")
	results = append(results, checkIDE(IDEWindsurf, "Windsurf IDE", windsurfPath))

	// 4. Antigravity CLI
	antigravityPath := filepath.Join(homeDir, ".gemini", "antigravity-cli", "mcp", "g8s.json")
	results = append(results, checkIDE(IDEAntigravity, "Google Antigravity", antigravityPath))

	return results, nil
}

func checkIDE(id, name, configPath string) DetectedIDE {
	ide := DetectedIDE{
		ID:         id,
		Name:       name,
		ConfigPath: configPath,
	}

	dir := filepath.Dir(configPath)
	if _, err := os.Stat(dir); err == nil {
		ide.Installed = true
	}

	if data, err := os.ReadFile(configPath); err == nil {
		var cfg map[string]any
		if json.Unmarshal(data, &cfg) == nil {
			if servers, ok := cfg["mcpServers"].(map[string]any); ok {
				if _, exists := servers["g8s"]; exists {
					ide.Configured = true
				}
			}
		}
	}

	return ide
}

// ConfigureMCP writes or merges the g8s MCP server entry into an IDE config file.
func ConfigureMCP(configPath, binaryPath string) error {
	if binaryPath == "" {
		var err error
		binaryPath, err = os.Executable()
		if err != nil {
			binaryPath = "g8s"
		}
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	var root map[string]any
	if data, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(data, &root)
	}
	if root == nil {
		root = make(map[string]any)
	}

	mcpServers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		mcpServers = make(map[string]any)
		root["mcpServers"] = mcpServers
	}

	mcpServers["g8s"] = map[string]any{
		"command": binaryPath,
		"args":    []string{"mcp"},
	}

	formatted, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal updated config: %w", err)
	}

	// Write atomically via temp file
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, formatted, 0o600); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename config: %w", err)
	}

	return nil
}

// RunInit performs system onboarding, creates required directories, and configures target IDEs.
func RunInit(targetIDEs []string, homeDir, binaryPath string) (*InitResult, error) {
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home directory: %w", err)
		}
	}

	if binaryPath == "" {
		if exe, err := os.Executable(); err == nil {
			binaryPath = exe
		} else {
			binaryPath = "g8s"
		}
	}

	result := &InitResult{
		BinaryPath: binaryPath,
	}

	// 1. Initialize State & Evidence directories
	stateDir := filepath.Join(homeDir, ".local", "state", "g8s")
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		stateDir = filepath.Join(xdg, "g8s")
	}
	evidenceDir := filepath.Join(stateDir, "evidence")

	for _, dir := range []string{stateDir, evidenceDir} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("create directory %s: %w", dir, err)
			}
			result.CreatedDirs = append(result.CreatedDirs, dir)
		}
	}
	result.StateDir = stateDir
	result.EvidenceDir = evidenceDir

	// 2. Initialize default providers.json if missing
	configDir := filepath.Join(homeDir, ".config", "g8s")
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		configDir = filepath.Join(xdg, "g8s")
	}
	_ = os.MkdirAll(configDir, 0o700)
	providersPath := filepath.Join(configDir, "providers.json")
	if _, err := os.Stat(providersPath); os.IsNotExist(err) {
		defaultConfig := map[string]any{
			"version": "1.0",
			"providers": []map[string]any{
				{
					"name":  "agy",
					"class": "platform_dispatch",
					"models": []map[string]any{
						{
							"id": "gemini-3.7-flash-high",
						},
					},
					"slots": 8,
					"args": []string{
						"agy",
						"--model",
						"{model}",
						"--mode",
						"accept-edits",
						"{prompt}",
					},
				},
				{
					"name":  "claude",
					"class": "platform_dispatch",
					"models": []map[string]any{
						{
							"id": "claude-3-7-sonnet-latest",
						},
						{
							"id": "claude-haiku-4-5",
						},
					},
					"slots": 2,
					"args": []string{
						"claude",
						"-p",
						"{prompt}",
					},
				},
			},
		}
		if data, err := json.MarshalIndent(defaultConfig, "", "  "); err == nil {
			_ = os.WriteFile(providersPath, data, 0o600)
			result.ProvidersConfig = providersPath
		}
	}

	// 3. Configure IDEs
	detected, err := DetectIDEs(homeDir)
	if err != nil {
		return nil, err
	}

	targetSet := make(map[string]bool)
	for _, id := range targetIDEs {
		targetSet[strings.ToLower(strings.TrimSpace(id))] = true
	}

	for _, ide := range detected {
		// If specific IDEs were requested, only configure those. Otherwise configure installed ones.
		shouldConfigure := false
		if len(targetIDEs) > 0 {
			if targetSet["all"] || targetSet[ide.ID] {
				shouldConfigure = true
			}
		} else if ide.Installed {
			shouldConfigure = true
		}

		if shouldConfigure {
			if err := ConfigureMCP(ide.ConfigPath, binaryPath); err != nil {
				return nil, fmt.Errorf("configure MCP for %s: %w", ide.Name, err)
			}
			ide.Configured = true
			result.ConfiguredIDEs = append(result.ConfiguredIDEs, ide)
		}
	}

	return result, nil
}
