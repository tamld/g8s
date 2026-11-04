package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cli"
)

func TestSubmitProfile_FillsFields(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "profile_fill.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + filepath.Join(t.TempDir(), "absent-providers.json"),
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--profile", "scout-fast",
		"--idempotency-key", "profile-scout-fast",
		"--prompt", "Scout task",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "profile-scout-fast")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["profile"] != "scout-fast" {
		t.Errorf("profile = %v, want scout-fast", payload["profile"])
	}
	if payload["provider"] != "cursor" {
		t.Errorf("provider = %v, want cursor", payload["provider"])
	}
	if payload["model"] != "composer-2.5" {
		t.Errorf("model = %v, want composer-2.5", payload["model"])
	}
	if payload["role"] != "scout" {
		t.Errorf("role = %v, want scout", payload["role"])
	}
	if payload["permission"] != "read_only" {
		t.Errorf("permission = %v, want read_only", payload["permission"])
	}
	if payload["timeout"] != "180s" {
		t.Errorf("timeout = %v, want 180s", payload["timeout"])
	}
	if payload["effort_applied"] != "low" && payload["effort"] != "low" {
		t.Errorf("effort = %v / effort_applied = %v, want low", payload["effort"], payload["effort_applied"])
	}
}

func TestSubmitProfile_ExplicitOverrides(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "profile_override.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + filepath.Join(t.TempDir(), "absent-providers.json"),
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--profile", "scout-fast",
		"--effort", "high",
		"--idempotency-key", "profile-override-effort",
		"--prompt", "Scout with high effort",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "profile-override-effort")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["effort_applied"] != "high" && payload["effort"] != "high" {
		t.Errorf("effort = %v / effort_applied = %v, want high override", payload["effort"], payload["effort_applied"])
	}
	if payload["timeout"] != "180s" {
		t.Errorf("timeout = %v, want profile default 180s", payload["timeout"])
	}
}

func TestSubmitProfile_UnknownName(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "profile_unknown.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--profile", "no-such-profile",
		"--idempotency-key", "profile-unknown",
		"--prompt", "Task",
	)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d raw=%s", code, raw)
	}
	if env.Error == nil || env.Error.Code != cli.CodeUsage {
		t.Errorf("expected E_USAGE, got %+v", env.Error)
	}
	if !strings.Contains(raw, "docs-retriever") || !strings.Contains(raw, "scout-fast") {
		t.Errorf("expected sorted profile hint in output, got: %s", raw)
	}
	if _, err := os.Stat(dbPath); err == nil {
		t.Errorf("expected no database write before profile validation, but %s exists", dbPath)
	}
}

func TestSubmitProfile_TimeoutExceedsReceiptTTL(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "profile_clocks.db")
	rcDBPath := filepath.Join(tempDir, "receipts.db")
	rc := createTestReceipt(t, rcDBPath, 600*time.Second, []string{"*"})
	envVars := []string{
		"G8S_DB=" + dbPath,
		"AGY_MCP_ALLOW_WORKSPACE_WRITE=1",
		"G8S_PROVIDERS=" + filepath.Join(t.TempDir(), "absent-providers.json"),
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--profile", "impl-medium",
		"--allow-workspace-write",
		"--receipt-id", rc.ReceiptID,
		"--idempotency-key", "profile-timeout-eq-ttl",
		"--prompt", "Implement",
	)
	if code == 0 {
		t.Fatal("expected submit to fail when profile timeout == receipt TTL")
	}
	if env.Error == nil || env.Error.Code != cli.CodeUsage {
		t.Errorf("expected E_USAGE, got %+v (raw: %s)", env.Error, raw)
	}
	if !strings.Contains(raw, "600s") {
		t.Errorf("expected error to name profile timeout 600s, got: %s", raw)
	}
}

func TestSubmitProfile_WorkspaceWriteWithoutReceipt(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "profile_harness.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + filepath.Join(t.TempDir(), "absent-providers.json"),
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--profile", "impl-medium",
		"--idempotency-key", "profile-no-receipt",
		"--prompt", "Mutate without receipt",
	)
	if code == 0 {
		t.Fatal("expected harness rejection for workspace_write without receipt")
	}
	if env.Error == nil || env.Error.Code != cli.CodeHarness {
		t.Errorf("expected E_HARNESS, got %+v (raw: %s)", env.Error, raw)
	}
}
