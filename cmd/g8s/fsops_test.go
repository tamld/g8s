package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIReadEditAndSelect(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	target := filepath.Join(tempDir, "hello.go")
	initial := "package main\n\nfunc Hello() string {\n\treturn \"v1\"\n}\n"
	if err := os.WriteFile(target, []byte(initial), 0o600); err != nil {
		t.Fatalf("write hello.go: %v", err)
	}

	// 1. Read with --lines and --select .data.sha256
	readCmd := exec.Command(binPath, "read", "--lines", "3:5", "--scope-root", tempDir, target, "--select", ".data.sha256")
	shaOut, err := readCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s read --select failed: %v\nOutput: %s", err, string(shaOut))
	}
	origSHA := strings.TrimSpace(string(shaOut))
	if len(origSHA) != 64 {
		t.Fatalf("expected 64-char sha256 hex, got %q", origSHA)
	}

	// 2. Edit with --if-sha256 and --select .data.replacements
	editCmd := exec.Command(binPath, "edit",
		"--old", `return "v1"`,
		"--new", `return "v2"`,
		"--if-sha256", origSHA,
		"--scope-root", tempDir,
		target,
		"--select", ".data.replacements",
	)
	editOut, err := editCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s edit failed: %v\nOutput: %s", err, string(editOut))
	}
	if strings.TrimSpace(string(editOut)) != "1" {
		t.Fatalf("expected 1 replacement, got %q", string(editOut))
	}

	// 3. Standalone g8s select from stdin
	selectCmd := exec.Command(binPath, "select", ".data.items[].name")
	selectCmd.Stdin = strings.NewReader(`{"data":{"items":[{"name":"alpha"},{"name":"beta"}]}}`)
	selOut, err := selectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s select failed: %v\nOutput: %s", err, string(selOut))
	}
	if strings.TrimSpace(string(selOut)) != "alpha\nbeta" {
		t.Fatalf("expected alpha\\nbeta, got %q", string(selOut))
	}
}
