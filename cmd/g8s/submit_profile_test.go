package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
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

func TestSubmitProfile_ExplicitModelOverride(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "profile_model_override.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + filepath.Join(t.TempDir(), "absent-providers.json"),
	}

	const overrideModel = "other-model"
	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--profile", "scout-fast",
		"--model", overrideModel,
		"--idempotency-key", "profile-override-model",
		"--prompt", "Scout with explicit model",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "profile-override-model")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["model"] != overrideModel {
		t.Errorf("model = %v, want %q from explicit flag", payload["model"], overrideModel)
	}
	if payload["provider"] != "cursor" {
		t.Errorf("provider = %v, want cursor from profile", payload["provider"])
	}
	if payload["role"] != "scout" {
		t.Errorf("role = %v, want scout from profile", payload["role"])
	}
	if payload["permission"] != "read_only" {
		t.Errorf("permission = %v, want read_only from profile", payload["permission"])
	}
	if payload["timeout"] != "180s" {
		t.Errorf("timeout = %v, want 180s from profile", payload["timeout"])
	}
	if payload["effort_applied"] != "low" && payload["effort"] != "low" {
		t.Errorf("effort = %v / effort_applied = %v, want low from profile", payload["effort"], payload["effort_applied"])
	}
}

func TestSubmitProfile_MissingCatalogFromCWD(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())
	binPath := buildG8sBinary(t)
	emptyWD := t.TempDir()
	dbPath := filepath.Join(emptyWD, "should-not-exist.db")

	cmd := exec.Command(binPath, "submit",
		"--profile", "scout-fast",
		"--idempotency-key", "no-catalog",
		"--prompt", "Task",
		"--json",
	)
	cmd.Dir = emptyWD
	cmd.Env = append(os.Environ(),
		"G8S_STATE_DIR="+t.TempDir(),
		"G8S_DB="+dbPath,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	if exitCode != 2 {
		t.Fatalf("expected exit 2, got %d stdout=%q stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	combined := stderr.String()
	if !strings.Contains(combined, "agent-models") || !strings.Contains(combined, "discoverable") {
		t.Errorf("expected catalog discovery hint, got stderr: %s", combined)
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		t.Errorf("expected no database write, but %s exists", dbPath)
	}
}

func TestSubmitBadFlagKeepsStdoutClean(t *testing.T) {
	binPath := buildG8sBinary(t)
	stateDir := t.TempDir()

	cases := []struct {
		name string
		args []string
	}{
		{name: "submit", args: []string{"submit", "--bogus-flag", "-json"}},
		{name: "worker", args: []string{"worker", "--bogus-flag"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binPath, tc.args...)
			cmd.Env = append(os.Environ(), "G8S_STATE_DIR="+stateDir)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatal("expected non-zero exit for unknown flag")
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q (stderr: %s)", stdout.String(), stderr.String())
			}
			if stderr.Len() == 0 {
				t.Fatalf("expected non-empty stderr")
			}
		})
	}
}
