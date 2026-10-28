package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIReadEditAndSelect(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_SCOPE_ROOT", "")
	t.Setenv("G8S_RECEIPT_ID", "")
	t.Setenv("G8S_PERMISSION", "")

	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	target := filepath.Join(tempDir, "hello.go")
	initial := "package main\n\nfunc Hello() string {\n\treturn \"v1\"\n}\n"
	if err := os.WriteFile(target, []byte(initial), 0o600); err != nil {
		t.Fatalf("write hello.go: %v", err)
	}

	// 1. Read with --lines and --select .data.sha256
	readCmd := exec.Command(binPath, "read", "--lines", "3:5", "--scope-root", tempDir, target, "--select", ".data.sha256")
	readCmd.Env = os.Environ()
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
	editCmd.Env = os.Environ()
	editOut, err := editCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s edit failed: %v\nOutput: %s", err, string(editOut))
	}
	if strings.TrimSpace(string(editOut)) != "1" {
		t.Fatalf("expected 1 replacement, got %q", string(editOut))
	}

	// 3. Standalone g8s select from stdin
	selectCmd := exec.Command(binPath, "select", ".data.items[].name")
	selectCmd.Env = os.Environ()
	selectCmd.Stdin = strings.NewReader(`{"data":{"items":[{"name":"alpha"},{"name":"beta"}]}}`)
	selOut, err := selectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s select failed: %v\nOutput: %s", err, string(selOut))
	}
	if strings.TrimSpace(string(selOut)) != "alpha\nbeta" {
		t.Fatalf("expected alpha\\nbeta, got %q", string(selOut))
	}
}

func TestCLISearchAndReadScopeEscapeBlocked_Issue598(t *testing.T) {
	binPath := buildG8sBinary(t)
	tmpDir := t.TempDir()
	scopeDir := filepath.Join(tmpDir, "scope")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	outsideSecret := filepath.Join(outsideDir, "secret.txt")
	secretContent := "SECRET_TOKEN=topsecret_token_val\n"
	if err := os.WriteFile(outsideSecret, []byte(secretContent), 0o600); err != nil {
		t.Fatal(err)
	}

	symlinkPath := filepath.Join(scopeDir, "symlink_secret.txt")
	if err := os.Symlink(outsideSecret, symlinkPath); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	t.Setenv("G8S_TIER", "F1")
	t.Setenv("G8S_SCOPE_ROOT", scopeDir)

	runRead := func(args []string) int {
		cmd := exec.Command(binPath, append([]string{"read"}, args...)...)
		cmd.Env = os.Environ()
		err := cmd.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return exitErr.ExitCode()
			}
			return 1
		}
		return 0
	}

	runSearch := func(args []string) int {
		cmd := exec.Command(binPath, append([]string{"search"}, args...)...)
		cmd.Env = os.Environ()
		err := cmd.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return exitErr.ExitCode()
			}
			return 1
		}
		return 0
	}

	// 1. Verify runRead returns non-zero exit code when widening scope via --scope-root
	code := runRead([]string{"--scope-root", filepath.Dir(scopeDir), filepath.Join(outsideDir, "secret.txt")})
	if code == 0 {
		t.Fatalf("expected non-zero exit code for read with scope-root escaping G8S_SCOPE_ROOT, got 0")
	}

	// 2. Verify reading the symlink that escapes scopeRoot returns non-zero exit code
	symCode := runRead([]string{symlinkPath})
	if symCode == 0 {
		t.Fatalf("expected non-zero exit code for read on symlink escaping G8S_SCOPE_ROOT, got 0")
	}

	// 3. Verify runSearch returns non-zero exit code when G8S_SCOPE_ROOT is scopeDir and search root is outsideDir
	searchCode := runSearch([]string{"SECRET_TOKEN", outsideDir})
	if searchCode == 0 {
		t.Fatalf("expected non-zero exit code for search outside G8S_SCOPE_ROOT, got 0")
	}
}

func TestCLIReadPrintsJITToolboxAndBatonAtEndOfRound(t *testing.T) {
	t.Setenv("G8S_TIER", "")
	t.Setenv("G8S_SCOPE_ROOT", "")
	t.Setenv("G8S_RECEIPT_ID", "")
	t.Setenv("G8S_PERMISSION", "")
	t.Setenv("G8S_JIT_WHISPER", "1")

	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	g8sDir := filepath.Join(tempDir, ".g8s")
	if err := os.MkdirAll(g8sDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fmYAML := `schema_version: "2.0"
features:
  - id: "FEAT-PLATFORM-CLI-01"
    title: "CLI & Agent File I/O Surface"
    persona_story: "As an agent, I need deterministic file I/O and CLI envelopes."
    srs_reqs: ["FR-1"]
    openspec_deltas: ["DELTA-23"]
    code_paths: ["cmd/g8s/**", "internal/fsops/**"]
    collision_domain: "cli-surface"
    owned_paths: ["cmd/g8s/**", "internal/fsops/**"]
    interfaces:
      cli: ["read", "edit", "search", "select"]
      go_packages: ["internal/fsops", "internal/cli", "internal/lockfile"]
    invariants:
      - "Reuse canonical fsops/lockfile/cli helpers instead of reinventing boilerplate"
    visible_tests: ["internal/fsops/fsops_test.go"]
    verifier_class: "test"
`
	if err := os.WriteFile(filepath.Join(g8sDir, "feature-map.yml"), []byte(fmYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cmdDir := filepath.Join(tempDir, "cmd", "g8s")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cmdDir, "app.go")
	if err := os.WriteFile(target, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readCmd := exec.Command(binPath, "read", "--scope-root", tempDir, target)
	readCmd.Env = os.Environ()
	out, err := readCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s read failed: %v\nOutput: %s", err, string(out))
	}
	outStr := string(out)
	if !strings.Contains(outStr, "[jit:toolbox:FEAT-PLATFORM-CLI-01]") || !strings.Contains(outStr, "[jit:baton:EXPLORING]") {
		t.Fatalf("expected text-mode g8s read to print JIT toolbox and baton at end of round, got:\n%s", outStr)
	}

	// Round 2: Follow baton to g8s edit -> verify [jit:baton:MUTATED]
	editCmd := exec.Command(binPath, "edit",
		"--scope-root", tempDir,
		"--old", "func main() {}",
		"--new", "func main() { _ = 42 }",
		target,
	)
	editCmd.Env = os.Environ()
	editOut, err := editCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s edit failed: %v\nOutput: %s", err, string(editOut))
	}
	editStr := string(editOut)
	if !strings.Contains(editStr, "[jit:baton:MUTATED]") || !strings.Contains(editStr, "verify --feature FEAT-PLATFORM-CLI-01") {
		t.Fatalf("expected g8s edit to emit [jit:baton:MUTATED] next step, got:\n%s", editStr)
	}
}
