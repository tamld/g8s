package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/search"
)

func cleanTestEnv(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_SCOPE_ROOT", "")
	t.Setenv("G8S_RECEIPT_ID", "")
	t.Setenv("G8S_PERMISSION", "")
}

func TestSearchJSONAndFixedStrings(t *testing.T) {
	cleanTestEnv(t)
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
	wantFile1 := filepath.ToSlash(file1)
	if filepath.ToSlash(match.File) != wantFile1 {
		t.Errorf("expected file %q, got %q", wantFile1, match.File)
	}
	if match.Text != "func Target() {" {
		t.Errorf("expected text %q, got %q", "func Target() {", match.Text)
	}
}

func TestSearchFilesModeAndQuiet(t *testing.T) {
	cleanTestEnv(t)
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
	wantDocFile := filepath.ToSlash(docFile)
	if len(env.Data.Files) != 1 || filepath.ToSlash(env.Data.Files[0]) != wantDocFile {
		t.Fatalf("expected [%s], got %+v", wantDocFile, env.Data.Files)
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
	cleanTestEnv(t)
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
	wantAllowed := filepath.ToSlash(fileAllowed)
	if filepath.ToSlash(env.Data.Files[0]) != wantAllowed {
		t.Errorf("expected allowed file %q, got %q", wantAllowed, env.Data.Files[0])
	}
}

func TestSearchInvalidRegexExitUsage(t *testing.T) {
	cleanTestEnv(t)
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

func TestSearchErgonomicFlags(t *testing.T) {
	cleanTestEnv(t)
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	file1 := filepath.Join(tempDir, "file1.txt")
	file2 := filepath.Join(tempDir, "file2.txt")
	file3 := filepath.Join(tempDir, "file3.txt")

	content1 := "hello world\nalpha\nhello again\n"
	content2 := "beta\nhello third\n"
	content3 := "gamma\ndelta\n"

	if err := os.WriteFile(file1, []byte(content1), 0o600); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte(content2), 0o600); err != nil {
		t.Fatalf("write file2: %v", err)
	}
	if err := os.WriteFile(file3, []byte(content3), 0o600); err != nil {
		t.Fatalf("write file3: %v", err)
	}

	// 1. Verify -n and --line-number work cleanly
	for _, flag := range []string{"-n", "--line-number"} {
		cmd := exec.Command(binPath, "search", flag, "hello", tempDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("search %s failed: %v\nOutput: %s", flag, err, string(out))
		}
		outStr := string(out)
		if !strings.Contains(outStr, "hello world") || !strings.Contains(outStr, ":1:") {
			t.Errorf("expected line number output for %s, got: %s", flag, outStr)
		}
	}

	// 2. Verify -l and --files-with-matches work cleanly
	for _, flag := range []string{"-l", "--files-with-matches"} {
		cmd := exec.Command(binPath, "search", flag, "hello", tempDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("search %s failed: %v\nOutput: %s", flag, err, string(out))
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 files with matches for %s, got %d: %q", flag, len(lines), lines)
		}
		if filepath.ToSlash(lines[0]) != filepath.ToSlash(file1) || filepath.ToSlash(lines[1]) != filepath.ToSlash(file2) {
			t.Errorf("expected [%s, %s], got %v", file1, file2, lines)
		}
	}

	// Test -l with --json mode
	{
		cmd := exec.Command(binPath, "search", "-l", "hello", tempDir, "--json")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("search -l --json failed: %v\nOutput: %s", err, string(out))
		}
		var env struct {
			Data searchData `json:"data"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("unmarshal search -l output: %v\nOutput: %s", err, string(out))
		}
		if len(env.Data.Files) != 2 {
			t.Fatalf("expected 2 files in json data.files, got %d: %+v", len(env.Data.Files), env.Data.Files)
		}
		if filepath.ToSlash(env.Data.Files[0]) != filepath.ToSlash(file1) || filepath.ToSlash(env.Data.Files[1]) != filepath.ToSlash(file2) {
			t.Errorf("expected [%s, %s], got %v", file1, file2, env.Data.Files)
		}
	}

	// 3. Verify -c and --count work cleanly
	for _, flag := range []string{"-c", "--count"} {
		cmd := exec.Command(binPath, "search", flag, "hello", tempDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("search %s failed: %v\nOutput: %s", flag, err, string(out))
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 count lines for %s, got %d: %q", flag, len(lines), lines)
		}
		want1 := filepath.ToSlash(file1) + ":2"
		want2 := filepath.ToSlash(file2) + ":1"
		if filepath.ToSlash(lines[0]) != want1 || filepath.ToSlash(lines[1]) != want2 {
			t.Errorf("expected [%s, %s], got %v", want1, want2, lines)
		}
	}

	// 4. Verify context flags (-C 2, -A 1, -B 1, --C 2, -C=2) parse cleanly without flag errors
	for _, args := range [][]string{
		{"-C", "2", "hello", tempDir},
		{"-A", "1", "hello", tempDir},
		{"-B", "1", "hello", tempDir},
		{"--C", "2", "hello", tempDir},
		{"-C=2", "hello", tempDir},
	} {
		cmd := exec.Command(binPath, append([]string{"search"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("search with args %v failed: %v\nOutput: %s", args, err, string(out))
		}
		if !strings.Contains(string(out), "hello world") {
			t.Errorf("expected match in output for args %v, got: %s", args, string(out))
		}
	}
}

// Suppress unused imports check
var _ = search.Match{}
