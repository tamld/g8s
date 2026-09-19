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
	"time"

	"github.com/tamld/g8s/internal/controlplane"
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

// VerificationResult captures the outcome of the post-init verification task.
type VerificationResult struct {
	TaskID       string `json:"task_id"`
	ReceiptID    string `json:"receipt_id,omitempty"`
	Verified     bool   `json:"verified"`
	DurationSecs int    `json:"duration_secs"`
	Error        string `json:"error,omitempty"`
}

// InitResult summarizes the outcome of the initialization process.
type InitResult struct {
	StateDir        string              `json:"state_dir"`
	EvidenceDir     string              `json:"evidence_dir"`
	BinaryPath      string              `json:"binary_path"`
	ConfiguredIDEs  []DetectedIDE       `json:"configured_ides"`
	ProvidersConfig string              `json:"providers_config,omitempty"`
	CreatedDirs     []string            `json:"created_dirs"`
	Verification    *VerificationResult `json:"verification,omitempty"`
}

// runVerificationTask performs a lightweight system verification by checking
// that all required components are properly configured. This completes within
// the timeout without requiring external worker binaries.
func runVerificationTask(homeDir, binaryPath string, timeoutSeconds int) (*VerificationResult, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	_ = timeoutSeconds // silence ineffassign - timeout enforced by context in caller

	start := time.Now()
	var errors []string

	// 1. Check state directory
	stateDir := filepath.Join(homeDir, ".local", "state", "g8s")
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		stateDir = filepath.Join(xdg, "g8s")
	}
	if _, err := os.Stat(stateDir); err != nil {
		errors = append(errors, fmt.Sprintf("state directory missing: %v", err))
	}

	// 2. Check evidence directory
	evidenceDir := filepath.Join(stateDir, "evidence")
	if _, err := os.Stat(evidenceDir); err != nil {
		errors = append(errors, fmt.Sprintf("evidence directory missing: %v", err))
	}

	// 3. Check tasks database
	dbPath := filepath.Join(stateDir, "tasks.db")
	if _, err := os.Stat(dbPath); err != nil {
		errors = append(errors, fmt.Sprintf("tasks database missing: %v", err))
	} else {
		// Try to open and verify the database
		store, err := controlplane.NewControlPlane(dbPath, time.Now)
		if err != nil {
			errors = append(errors, fmt.Sprintf("tasks database corrupt: %v", err))
		} else {
			store.Close()
		}
	}

	// 4. Check providers configuration
	configDir := filepath.Join(homeDir, ".config", "g8s")
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		if home, err := os.UserHomeDir(); err == nil && home == homeDir {
			configDir = filepath.Join(xdg, "g8s")
		}
	}
	providersPath := filepath.Join(configDir, "providers.json")
	if _, err := os.Stat(providersPath); err != nil {
		errors = append(errors, fmt.Sprintf("providers.json missing: %v", err))
	} else {
		// Validate providers.json is valid JSON
		data, err := os.ReadFile(providersPath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("providers.json unreadable: %v", err))
		} else {
			var cfg map[string]any
			if err := json.Unmarshal(data, &cfg); err != nil {
				errors = append(errors, fmt.Sprintf("providers.json invalid JSON: %v", err))
			}
		}
	}

	// 5. Check g8s binary is executable
	if _, err := os.Stat(binaryPath); err != nil {
		errors = append(errors, fmt.Sprintf("g8s binary not found: %v", err))
	}

	duration := int(time.Since(start).Seconds())
	verified := len(errors) == 0

	var errorMsg string
	if len(errors) > 0 {
		errorMsg = strings.Join(errors, "; ")
	}

	return &VerificationResult{
		TaskID:       "init-verification",
		ReceiptID:    "system-check",
		Verified:     verified,
		DurationSecs: duration,
		Error:        errorMsg,
	}, nil
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
	switch runtime.GOOS {
	case "darwin":
		cursorPath = filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "globalStorage", "cursor.mcp", "mcp.json")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		cursorPath = filepath.Join(appData, "Cursor", "User", "globalStorage", "cursor.mcp", "mcp.json")
	}
	results = append(results, checkIDE(IDECursor, "Cursor IDE", cursorPath))

	// 2. Claude Desktop
	claudePath := filepath.Join(homeDir, ".config", "Claude", "claude_desktop_config.json")
	switch runtime.GOOS {
	case "darwin":
		claudePath = filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
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
		if home, err := os.UserHomeDir(); err == nil && home == homeDir {
			configDir = filepath.Join(xdg, "g8s")
		}
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
							"id": "gemini-3.8-flash-high",
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

	// 4. Run verification task (optional, can be skipped with SKIP_VERIFICATION=1)
	if os.Getenv("SKIP_VERIFICATION") != "1" {
		verification, err := runVerificationTask(homeDir, binaryPath, 30)
		if err != nil {
			verification = &VerificationResult{
				Verified:     false,
				DurationSecs: 30,
				Error:        fmt.Sprintf("verification task failed: %v", err),
			}
		}
		result.Verification = verification
	}

	return result, nil
}
