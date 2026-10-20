package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubmitSuppliedText(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "supplied_text.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "supplied-text-task",
		"--prompt", "Analyze architecture without tools",
		"--permission", "supplied_text",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "supplied-text-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["permission"] != "supplied_text" {
		t.Errorf("permission = %v, want supplied_text", payload["permission"])
	}
	if payload["tool_policy"] != "none" {
		t.Errorf("tool_policy = %v, want none", payload["tool_policy"])
	}
}

func TestSubmitToolPolicyNone(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tool_policy_none.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "tool-policy-none-task",
		"--prompt", "Read-only task with tool policy none",
		"--permission", "read_only",
		"--tool-policy", "none",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "tool-policy-none-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["tool_policy"] != "none" {
		t.Errorf("tool_policy = %v, want none", payload["tool_policy"])
	}
}

func TestSubmitToolPolicyDefault(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tool_policy_default.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	env, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "tool-policy-default-task",
		"--prompt", "Read-only task with tool policy default",
		"--permission", "read_only",
		"--tool-policy", "default",
	)
	if code != 0 {
		t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
	}

	reqJSON := getTaskRequestJSON(t, dbPath, "tool-policy-default-task")
	var payload map[string]any
	if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload["tool_policy"] != "default" {
		t.Errorf("tool_policy = %v, want default", payload["tool_policy"])
	}
}

func TestSubmitToolPolicyInvalid_UsageError(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tool_policy_invalid.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	_, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "tool-policy-invalid-task",
		"--prompt", "Task with invalid tool policy",
		"--tool-policy", "invalid_policy",
	)
	if code == 0 {
		t.Fatal("expected non-zero exit code for invalid --tool-policy")
	}
	if !strings.Contains(raw, "invalid --tool-policy") {
		t.Errorf("expected error mentioning invalid --tool-policy, got: %s", raw)
	}
}

func TestSubmitSuppliedTextUnsupportedProvider_FailsClosed(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "unsupported_provider.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
	}

	_, code, raw := runSubmitCLI(t, binPath, envVars,
		"--idempotency-key", "unsupported-provider-task",
		"--prompt", "Task prompt",
		"--permission", "supplied_text",
		"--provider", "codex",
	)
	if code == 0 {
		t.Fatal("expected submit to fail closed when provider does not support tool_policy=none")
	}
	if !strings.Contains(raw, "does not support tool_policy=none") {
		t.Errorf("expected error mentioning tool_policy=none unsupported, got: %s", raw)
	}
}
