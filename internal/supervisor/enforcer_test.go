package supervisor

import (
	"strings"
	"testing"
)

func goodRequest() RunRequest {
	return RunRequest{
		TaskDescription: "scan src for MCP server candidates",
		Role:            "scout",
		Permission:      "read_only",
		Model:           "gemini-3.8-flash-high",
		AddDirs:         []string{"./src"},
		AllowedFiles:    nil,
	}
}

func TestEnforcerHappyPath(t *testing.T) {
	e := NewEnforcer()
	if err := e.Validate(SelectEnvelope(nil), goodRequest()); err != nil {
		t.Fatalf("happy path: unexpected error: %v", err)
	}
}

func TestEnforcerMissingPrompt(t *testing.T) {
	e := NewEnforcer()
	req := goodRequest()
	req.TaskDescription = "   "
	if err := e.Validate(SelectEnvelope(nil), req); err == nil {
		t.Fatalf("expected error for missing prompt")
	}
}

func TestEnforcerBadRole(t *testing.T) {
	e := NewEnforcer()
	req := goodRequest()
	req.Role = "ghost"
	err := e.Validate(SelectEnvelope(nil), req)
	if err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("expected unknown-role error, got %v", err)
	}
}

func TestEnforcerBadPermission(t *testing.T) {
	e := NewEnforcer()
	req := goodRequest()
	req.Permission = "superuser"
	err := e.Validate(SelectEnvelope(nil), req)
	if err == nil || !strings.Contains(err.Error(), "unknown permission") {
		t.Fatalf("expected unknown-permission error, got %v", err)
	}
}

func TestEnforcerMissingAddDirs(t *testing.T) {
	e := NewEnforcer()
	req := goodRequest()
	req.AddDirs = nil
	err := e.Validate(SelectEnvelope(nil), req)
	if err == nil || !strings.Contains(err.Error(), "AddDirs") {
		t.Fatalf("expected AddDirs error, got %v", err)
	}
}

func TestEnforcerWorkspaceWriteRequiresAllowedFiles(t *testing.T) {
	e := NewEnforcer()
	req := goodRequest()
	req.Permission = "workspace_write"
	req.AllowedFiles = nil
	err := e.Validate(SelectEnvelope(nil), req)
	if err == nil || !strings.Contains(err.Error(), "AllowedFiles") {
		t.Fatalf("expected AllowedFiles error, got %v", err)
	}
}

func TestEnforcerMissingModel(t *testing.T) {
	e := NewEnforcer()
	req := goodRequest()
	req.Model = ""
	err := e.Validate(SelectEnvelope(nil), req)
	if err == nil || !strings.Contains(err.Error(), "Model") {
		t.Fatalf("expected Model error, got %v", err)
	}
}
