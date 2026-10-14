package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/search"
)

func TestSearchJSONAndFixedStrings(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	file1 := filepath.Join(tempDir, "sample.go")
	content := "package main\n\nfunc Target() {\n\tprintln(\"hello\")\n}\n"
	if err := os.WriteFile(file1, []byte(content), 0o600); err != nil {
		t.Fatalf("write sample.go: %v", err)
	}

	cmd := exec.Command(binPath, "search", "-F", "func Target(", "-g", "*.go", tempDir, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("search command failed: %v\nOutput: %s", err, string(out))
	}

	var env struct {
		Kind string     `json:"kind"`
		Data searchData `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal search output: %v\nOutput: %s", err, string(out))
	}

	if len(env.Data.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(env.Data.Matches), env.Data.Matches)
	}

	match := env.Data.Matches[0]
	if match.Line != 3 {
		t.Errorf("expected line 3, got %d", match.Line)
	}
	if match.Column != 1 {
		t.Errorf("expected column 1, got %d", match.Column)
	}
	if match.File != file1 {
		t.Errorf("expected file %q, got %q", file1, match.File)
	}
	if match.Text != "func Target() {" {
		t.Errorf("expected text %q, got %q", "func Target() {", match.Text)
	}
}

func TestSearchFilesModeAndQuiet(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	docFile := filepath.Join(tempDir, "README.md")
	if err := os.WriteFile(docFile, []byte("# Documentation\n"), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	goFile := filepath.Join(tempDir, "sample.go")
	if err := os.WriteFile(goFile, []byte("func Target() {}\n"), 0o600); err != nil {
		t.Fatalf("write sample.go: %v", err)
	}

	// 1. Files mode with glob
	cmd := exec.Command(binPath, "search", "--files", "-g", "*.md", tempDir, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("search --files failed: %v\nOutput: %s", err, string(out))
	}

	var env struct {
		Data searchData `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal files mode output: %v\nOutput: %s", err, string(out))
	}
	if len(env.Data.Files) != 1 || env.Data.Files[0] != docFile {
		t.Fatalf("expected [README.md], got %+v", env.Data.Files)
	}

	// 2. Quiet mode: match found -> exit 0
	cmdQuietHit := exec.Command(binPath, "search", "-q", "-F", "func Target(", tempDir)
	if err := cmdQuietHit.Run(); err != nil {
		t.Fatalf("quiet mode with match expected exit 0, got: %v", err)
	}

	// 3. Quiet mode: match not found -> exit 1
	cmdQuietMiss := exec.Command(binPath, "search", "-q", "-F", "nonexistent_token", tempDir)
	err = cmdQuietMiss.Run()
	if err == nil {
		t.Fatal("quiet mode with no match expected non-zero exit, got exit 0")
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}
}

func TestSearchReceiptScopeFilter(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	allowedDir := filepath.Join(tempDir, "allowed")
	disallowedDir := filepath.Join(tempDir, "disallowed")
	if err := os.MkdirAll(allowedDir, 0o700); err != nil {
		t.Fatalf("mkdir allowed: %v", err)
	}
	if err := os.MkdirAll(disallowedDir, 0o700); err != nil {
		t.Fatalf("mkdir disallowed: %v", err)
	}

	fileAllowed := filepath.Join(allowedDir, "item.txt")
	fileDisallowed := filepath.Join(disallowedDir, "item.txt")
	if err := os.WriteFile(fileAllowed, []byte("allowed content\n"), 0o600); err != nil {
		t.Fatalf("write fileAllowed: %v", err)
	}
	if err := os.WriteFile(fileDisallowed, []byte("disallowed content\n"), 0o600); err != nil {
		t.Fatalf("write fileDisallowed: %v", err)
	}

	stateDir := filepath.Join(tempDir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("mkdir stateDir: %v", err)
	}
	rcDbPath := filepath.Join(stateDir, "receipts.db")
	rcID := issueTestReceipt(t, rcDbPath, []string{"allowed/**"})

	cmd := exec.Command(binPath, "search", "--files", "--receipt-id", rcID, tempDir, "--json")
	cmd.Env = append(os.Environ(), "G8S_STATE_DIR="+stateDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("search with receipt-id failed: %v\nOutput: %s", err, string(out))
	}

	var env struct {
		Data searchData `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal receipt search output: %v\nOutput: %s", err, string(out))
	}

	if len(env.Data.Files) != 1 {
		t.Fatalf("expected exactly 1 allowed file, got %d: %+v", len(env.Data.Files), env.Data.Files)
	}
	if env.Data.Files[0] != fileAllowed {
		t.Errorf("expected allowed file %q, got %q", fileAllowed, env.Data.Files[0])
	}
}

func TestSearchInvalidRegexExitUsage(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	cmd := exec.Command(binPath, "search", "[unclosed", tempDir, "--json")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error on invalid regex, got exit 0 with output: %s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("expected exit code 2 (E_USAGE), got %d\nOutput: %s", exitErr.ExitCode(), string(out))
	}
}

// Suppress unused imports check
var _ = search.Match{}
