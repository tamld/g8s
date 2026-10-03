package initwiz

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

func TestDetectIDEsCoverage(t *testing.T) {
	tempDir := t.TempDir()

	cursorDir := filepath.Join(tempDir, ".cursor")
	_ = os.MkdirAll(cursorDir, 0o755)

	mcpPath := filepath.Join(cursorDir, "mcp.json")
	_ = os.WriteFile(mcpPath, []byte(`{"mcpServers": {"g8s": {}}}`), 0o644)

	ides, err := DetectIDEs(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ides) != 4 {
		t.Errorf("Expected 4 ides, got %d", len(ides))
	}

	// Test with empty homeDir (uses os.UserHomeDir())
	_, _ = DetectIDEs("")
}

func TestCheckIDECoverage(t *testing.T) {
	tempDir := t.TempDir()

	// Create invalid json
	mcpPath := filepath.Join(tempDir, "mcp.json")
	_ = os.WriteFile(mcpPath, []byte(`{invalid`), 0o644)

	ide := checkIDE(IDECursor, "Cursor IDE", mcpPath)
	if ide.Configured {
		t.Errorf("expected Configured=false for invalid JSON")
	}

	// Create valid json without g8s
	_ = os.WriteFile(mcpPath, []byte(`{"mcpServers": {"other": {}}}`), 0o644)
	ide = checkIDE(IDECursor, "Cursor IDE", mcpPath)
	if ide.Configured {
		t.Errorf("expected Configured=false without g8s")
	}
}

func TestConfigureMCPBranches(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Default binary path (empty binaryPath)
	cfgPath := filepath.Join(tempDir, "subdir1", "mcp.json")
	err := ConfigureMCP(cfgPath, "")
	if err != nil {
		t.Fatalf("ConfigureMCP with empty binPath failed: %v", err)
	}

	// 2. Existing config without mcpServers key
	cfgPath2 := filepath.Join(tempDir, "subdir2", "mcp.json")
	_ = os.MkdirAll(filepath.Dir(cfgPath2), 0o700)
	_ = os.WriteFile(cfgPath2, []byte(`{"otherKey": 123}`), 0o600)
	err = ConfigureMCP(cfgPath2, "/bin/g8s")
	if err != nil {
		t.Fatalf("ConfigureMCP failed on config without mcpServers: %v", err)
	}

	// 3. Existing config with invalid JSON (should start fresh root map)
	cfgPath3 := filepath.Join(tempDir, "subdir3", "mcp.json")
	_ = os.MkdirAll(filepath.Dir(cfgPath3), 0o700)
	_ = os.WriteFile(cfgPath3, []byte(`{not-json}`), 0o600)
	err = ConfigureMCP(cfgPath3, "/bin/g8s")
	if err != nil {
		t.Fatalf("ConfigureMCP failed on invalid JSON: %v", err)
	}

	// 4. Invalid directory path (MkdirAll failure)
	// Create a regular file and try to create a config inside it as a directory
	blockerFile := filepath.Join(tempDir, "blocker")
	_ = os.WriteFile(blockerFile, []byte("blocker"), 0o600)
	badPath := filepath.Join(blockerFile, "sub", "mcp.json")
	err = ConfigureMCP(badPath, "/bin/g8s")
	if err == nil {
		t.Errorf("expected error when MkdirAll fails")
	}
}

func TestRunVerificationTaskFullSuccess(t *testing.T) {
	tempHome := t.TempDir()
	binPath := filepath.Join(tempHome, "g8s")
	_ = os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755)

	// Create state dir and evidence dir
	stateDir := filepath.Join(tempHome, ".local", "state", "g8s")
	evidenceDir := filepath.Join(stateDir, "evidence")
	_ = os.MkdirAll(evidenceDir, 0o700)

	// Create valid tasks.db
	dbPath := filepath.Join(stateDir, "tasks.db")
	store, err := controlplane.NewControlPlane(dbPath, time.Now)
	if err != nil {
		t.Fatalf("create controlplane: %v", err)
	}
	_ = store.Close()

	// Create config dir and valid providers.json
	configDir := filepath.Join(tempHome, ".config", "g8s")
	_ = os.MkdirAll(configDir, 0o700)
	providersPath := filepath.Join(configDir, "providers.json")
	_ = os.WriteFile(providersPath, []byte(`{"version": "1.0"}`), 0o600)

	// Test with zero timeout
	res, err := runVerificationTask(tempHome, binPath, 0)
	if err != nil {
		t.Fatalf("runVerificationTask failed: %v", err)
	}
	if !res.Verified {
		t.Errorf("expected verification to pass, error: %s", res.Error)
	}

	// Test with corrupt tasks.db
	_ = os.WriteFile(dbPath, []byte("not a sqlite db"), 0o600)
	res, err = runVerificationTask(tempHome, binPath, 30)
	if err != nil {
		t.Fatalf("runVerificationTask error: %v", err)
	}
	if res.Verified {
		t.Errorf("expected verification to fail on corrupt db")
	}

	// Test with invalid providers.json
	_ = os.WriteFile(providersPath, []byte("invalid json"), 0o600)
	res, err = runVerificationTask(tempHome, binPath, 30)
	if err != nil {
		t.Fatalf("runVerificationTask error: %v", err)
	}
	if res.Verified {
		t.Errorf("expected verification to fail on invalid providers.json")
	}

	// Test with XDG_STATE_HOME and XDG_CONFIG_HOME
	xdgState := filepath.Join(tempHome, "xdg_state")
	t.Setenv("XDG_STATE_HOME", xdgState)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempHome, "xdg_config"))
	res, _ = runVerificationTask(tempHome, binPath, 30)
	if res.Verified {
		t.Errorf("expected failure when XDG_STATE_HOME is missing required dirs")
	}
}

func TestRunInitBranches(t *testing.T) {
	tempHome := t.TempDir()
	binPath := filepath.Join(tempHome, "g8s")
	_ = os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755)

	// Pre-create installed IDE directory for windsurf
	windsurfDir := filepath.Join(tempHome, ".codeium", "windsurf")
	_ = os.MkdirAll(windsurfDir, 0o755)

	// 1. Run with targetIDEs = nil (should configure installed IDEs: windsurf)
	res, err := RunInit(nil, tempHome, binPath)
	if err != nil {
		t.Fatalf("RunInit nil targetIDEs failed: %v", err)
	}
	foundWindsurf := false
	for _, ide := range res.ConfiguredIDEs {
		if ide.ID == IDEWindsurf {
			foundWindsurf = true
			break
		}
	}
	if !foundWindsurf {
		t.Errorf("expected Windsurf to be configured when targetIDEs is nil")
	}

	// 2. Run with targetIDEs containing "all"
	resAll, err := RunInit([]string{"all"}, tempHome, binPath)
	if err != nil {
		t.Fatalf("RunInit 'all' failed: %v", err)
	}
	if len(resAll.ConfiguredIDEs) != 4 {
		t.Errorf("expected 4 configured IDEs for 'all', got %d", len(resAll.ConfiguredIDEs))
	}

	// 3. Run with SKIP_VERIFICATION=1
	t.Setenv("SKIP_VERIFICATION", "1")
	resSkip, err := RunInit([]string{IDECursor}, tempHome, binPath)
	if err != nil {
		t.Fatalf("RunInit with SKIP_VERIFICATION failed: %v", err)
	}
	if resSkip.Verification != nil {
		t.Errorf("expected nil Verification when SKIP_VERIFICATION=1")
	}

	// 4. Run with empty binaryPath
	t.Setenv("SKIP_VERIFICATION", "1")
	_, err = RunInit([]string{IDECursor}, tempHome, "")
	if err != nil {
		t.Fatalf("RunInit with empty binaryPath failed: %v", err)
	}

	// 5. Run with XDG_STATE_HOME set
	xdgState := filepath.Join(tempHome, "custom_xdg_state")
	t.Setenv("XDG_STATE_HOME", xdgState)
	resXDG, err := RunInit([]string{IDECursor}, tempHome, binPath)
	if err != nil {
		t.Fatalf("RunInit with XDG_STATE_HOME failed: %v", err)
	}
	if resXDG.StateDir != filepath.Join(xdgState, "g8s") {
		t.Errorf("expected StateDir %s, got %s", filepath.Join(xdgState, "g8s"), resXDG.StateDir)
	}

	// 6. Test empty homeDir
	_, _ = RunInit([]string{IDECursor}, "", binPath)
}
