package harness

import (
	"strings"
	"testing"
)

func TestRolesAndPermissions(t *testing.T) {
	roles := RoleNames()
	if len(roles) != 6 {
		t.Fatalf("expected 6 roles, got %d", len(roles))
	}

	perms := PermissionNames()
	if len(perms) != 3 {
		t.Fatalf("expected 3 permissions, got %d", len(perms))
	}
}

func TestValidateRequestBlockedPatterns(t *testing.T) {
	err := ValidateRequest(
		"Please run rm -rf /tmp/test to clean up",
		"collector",
		"read_only",
		[]string{"/workspace"},
		false,
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "blocked task pattern") {
		t.Fatalf("expected blocked pattern error, got %v", err)
	}
}

func TestValidateRequestDeniedPaths(t *testing.T) {
	err := ValidateRequest(
		"Summarize keys",
		"collector",
		"read_only",
		[]string{"/Users/test/.ssh"},
		false,
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "denied path fragment") {
		t.Fatalf("expected denied path error, got %v", err)
	}
}

func TestValidateRequestWorkspaceWriteWithoutReceipt(t *testing.T) {
	err := ValidateRequest(
		"Refactor main.go",
		"collector",
		"workspace_write",
		[]string{"/workspace"},
		false,
		"", // No receipt
	)
	if err == nil || !strings.Contains(err.Error(), "requires --receipt-id") {
		t.Fatalf("expected receipt requirement error, got %v", err)
	}
}

func TestBuildContractPrompt(t *testing.T) {
	prompt, err := BuildContractPrompt(
		"Count files in src",
		"collector",
		"read_only",
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "Role: collector") {
		t.Fatalf("expected role in prompt, got %s", prompt)
	}
	if !strings.Contains(prompt, "Mutation policy: This task is read-only") {
		t.Fatalf("expected read-only policy in prompt, got %s", prompt)
	}
}
