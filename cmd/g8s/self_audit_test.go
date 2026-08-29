package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindDogfoodScript(t *testing.T) {
	script, err := findDogfoodScript()
	if err != nil {
		t.Fatalf("findDogfoodScript() failed: %v", err)
	}
	if !strings.HasSuffix(script, filepath.Join("tools", "dogfood_report.sh")) {
		t.Errorf("script path = %q, expected suffix tools/dogfood_report.sh", script)
	}
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		t.Errorf("script file not accessible: stat error=%v", err)
	}
}

func TestExecuteSelfAudit(t *testing.T) {
	tempDir := t.TempDir()
	mockScript := filepath.Join(tempDir, "mock_report.sh")

	// Script that outputs message and exits with 0 or code based on arg
	scriptContent := `#!/usr/bin/env bash
if [ "${1:-}" = "--fail" ]; then
    echo "mock failure" >&2
    exit 42
fi
echo "mock report output: $@"
exit 0
`
	if err := os.WriteFile(mockScript, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write mock script: %v", err)
	}

	// 1. Successful execution
	var stdout, stderr bytes.Buffer
	code, err := executeSelfAudit(&stdout, &stderr, mockScript, []string{"--json"})
	if err != nil {
		t.Fatalf("executeSelfAudit success test failed: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "mock report output: --json") {
		t.Errorf("stdout = %q, want it to contain mock report output", stdout.String())
	}

	// 2. Failing execution
	stdout.Reset()
	stderr.Reset()
	code, err = executeSelfAudit(&stdout, &stderr, mockScript, []string{"--fail"})
	if err != nil {
		t.Fatalf("executeSelfAudit fail test unexpected error: %v", err)
	}
	if code != 42 {
		t.Errorf("expected exit code 42, got %d", code)
	}
	if !strings.Contains(stderr.String(), "mock failure") {
		t.Errorf("stderr = %q, want 'mock failure'", stderr.String())
	}
}
