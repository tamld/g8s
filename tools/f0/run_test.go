package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRewriteArgv(t *testing.T) {
	t.Run("go-test injects -json", func(t *testing.T) {
		args := []string{"go", "test", "./..."}
		rewritten := rewriteArgv("go-test", args)
		if len(rewritten) != 4 || rewritten[2] != "-json" {
			t.Fatalf("expected -json injected after test, got: %v", rewritten)
		}

		// Already has -json
		argsWithJSON := []string{"go", "test", "-json", "./..."}
		rewrittenSame := rewriteArgv("go-test", argsWithJSON)
		if len(rewrittenSame) != 4 {
			t.Fatalf("expected no extra injection, got: %v", rewrittenSame)
		}

		// With leading env and KEY=VALUE
		argsWithEnv := []string{"env", "CGO_ENABLED=0", "go", "test", "./..."}
		rewrittenEnv := rewriteArgv("go-test", argsWithEnv)
		if len(rewrittenEnv) != 6 || rewrittenEnv[4] != "-json" {
			t.Fatalf("expected -json injected after test with env, got: %v", rewrittenEnv)
		}
	})

	t.Run("g8s-deliver injects --json", func(t *testing.T) {
		args := []string{"g8s", "deliver", "--task-id", "123"}
		rewritten := rewriteArgv("g8s-deliver", args)
		if len(rewritten) != 5 || rewritten[2] != "--json" {
			t.Fatalf("expected --json injected after deliver, got: %v", rewritten)
		}

		// Already has --json
		argsWithJSON := []string{"g8s", "deliver", "--json", "--task-id", "123"}
		rewrittenSame := rewriteArgv("g8s-deliver", argsWithJSON)
		if len(rewrittenSame) != 5 {
			t.Fatalf("expected no extra injection, got: %v", rewrittenSame)
		}

		// With leading env
		argsWithEnv := []string{"env", "FOO=1", "g8s", "deliver", "--task-id", "123"}
		rewrittenEnv := rewriteArgv("g8s-deliver", argsWithEnv)
		if len(rewrittenEnv) != 7 || rewrittenEnv[4] != "--json" {
			t.Fatalf("expected --json injected after deliver with env, got: %v", rewrittenEnv)
		}
	})
}

func TestDetectKind(t *testing.T) {
	tests := []struct {
		argv     []string
		expected string
	}{
		{[]string{"go", "test", "./..."}, "go-test"},
		{[]string{"env", "CGO_ENABLED=0", "go", "test", "./..."}, "go-test"},
		{[]string{"env", "FOO=bar", "BAR=baz", "go", "test", "./..."}, "go-test"},
		{[]string{"CGO_ENABLED=0", "go", "test", "./..."}, "go-test"},
		{[]string{"node", "--test", "foo.test.js"}, "node-test"},
		{[]string{"npm", "test"}, "npm-test"},
		{[]string{"cargo", "test"}, "cargo-test"},
		{[]string{"did-verify", "--files"}, "did-verify"},
		{[]string{"defense-in-depth", "verify"}, "did-verify"},
		{[]string{"node", "./dist/cli/index.js", "verify", "--files"}, "did-verify"},
		{[]string{"env", "node", "./dist/cli/index.js", "verify", "--files"}, "did-verify"},
		{[]string{"g8s", "verify", "--files"}, "g8s-verify"},
		{[]string{"g8s", "offer", "check"}, "g8s-offer-check"},
		{[]string{"g8s", "deliver"}, "g8s-deliver"},
		{[]string{"ls", "-la"}, "generic"},
	}

	for _, tc := range tests {
		got := detectKind(tc.argv)
		if got != tc.expected {
			t.Errorf("detectKind(%v) = %q, expected %q", tc.argv, got, tc.expected)
		}
	}
}

func TestRunGoTestVacuous(t *testing.T) {
	// Running go test with -run NONE on tools/f0 must yield 0 tests and status VACUOUS
	repoDir := initTestGitRepo(t)

	// Copy tools/f0 parsers_test.go so there is a test file to run against
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldDir)
	}()

	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Create a minimal go.mod and test file in repoDir
	if err := os.WriteFile("go.mod", []byte("module example.com/testmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	testContent := `package testmod

import "testing"

func TestReal(t *testing.T) {}
`
	if err := os.WriteFile("mod_test.go", []byte(testContent), 0o644); err != nil {
		t.Fatalf("writing mod_test.go: %v", err)
	}
	_ = exec.Command("git", "add", "go.mod", "mod_test.go").Run()
	_ = exec.Command("git", "commit", "-m", "add test").Run()

	// 1. Run with -run NONE: exit code 0, items = 0, VACUOUS
	code := runCommand([]string{"--", "go", "test", "-run", "NONE", "."})
	if code != 0 {
		t.Fatalf("expected exit code 0 from go test -run NONE, got %d", code)
	}

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	last, err := ledger.LastEntry()
	if err != nil {
		t.Fatalf("read last entry: %v", err)
	}
	if last.Status != "VACUOUS" {
		t.Errorf("expected status VACUOUS, got %s", last.Status)
	}
	if last.ItemsChecked != 0 {
		t.Errorf("expected items_checked 0, got %d", last.ItemsChecked)
	}

	// 2. Run with -run TestReal: exit code 0, items = 1, PASS
	code = runCommand([]string{"--", "go", "test", "-run", "TestReal", "."})
	if code != 0 {
		t.Fatalf("expected exit code 0 from go test -run TestReal, got %d", code)
	}
	last, err = ledger.LastEntry()
	if err != nil {
		t.Fatalf("read last entry: %v", err)
	}
	if last.Status != "PASS" {
		t.Errorf("expected status PASS, got %s", last.Status)
	}
	if last.ItemsChecked != 1 {
		t.Errorf("expected items_checked 1, got %d", last.ItemsChecked)
	}
}

func buildMockHelper(t *testing.T, dir, baseName, stdoutText string) string {
	t.Helper()
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "main.go")
	src := fmt.Sprintf("package main\nimport \"fmt\"\nfunc main() { fmt.Print(%q) }\n", stdoutText)
	if err := os.WriteFile(srcFile, []byte(src), 0o600); err != nil {
		t.Fatalf("write mock helper src: %v", err)
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(baseName, ".exe") {
		baseName += ".exe"
	}
	binPath := filepath.Join(dir, baseName)
	cmd := exec.Command("go", "build", "-o", binPath, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock helper %s: %v (%s)", baseName, err, string(out))
	}
	return binPath
}

func TestRunDidVerify(t *testing.T) {
	repoDir := initTestGitRepo(t)
	oldDir, _ := os.Getwd()
	defer func() {
		_ = os.Chdir(oldDir)
	}()
	_ = os.Chdir(repoDir)

	mockScript := buildMockHelper(t, t.TempDir(), "did-verify", "")

	// 1. Empty --files => VACUOUS
	code := runCommand([]string{"--", mockScript, "--files"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	last, _ := ledger.LastEntry()
	if last.Status != "VACUOUS" {
		t.Errorf("expected status VACUOUS for empty --files, got %s", last.Status)
	}
	if last.ItemsChecked != 0 {
		t.Errorf("expected items_checked 0, got %d", last.ItemsChecked)
	}

	// 2. With files => PASS
	code = runCommand([]string{"--", mockScript, "--files", "file1.txt", "file2.txt"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	last, _ = ledger.LastEntry()
	if last.Status != "PASS" {
		t.Errorf("expected status PASS with files, got %s", last.Status)
	}
	if last.ItemsChecked != 2 {
		t.Errorf("expected items_checked 2, got %d", last.ItemsChecked)
	}

	// 3. node ./dist/cli/index.js verify --files with 0 files => VACUOUS
	mockNode := buildMockHelper(t, t.TempDir(), "node", "")
	code = runCommand([]string{"--", mockNode, "./dist/cli/index.js", "verify", "--files"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	last, _ = ledger.LastEntry()
	if last.Kind != "did-verify" {
		t.Errorf("expected kind did-verify, got %s", last.Kind)
	}
	if last.Status != "VACUOUS" {
		t.Errorf("expected status VACUOUS for node verify --files with 0 files, got %s", last.Status)
	}
	if last.ItemsChecked != 0 {
		t.Errorf("expected items_checked 0, got %d", last.ItemsChecked)
	}
}

func TestRunDeliverDryRun(t *testing.T) {
	repoDir := initTestGitRepo(t)
	oldDir, _ := os.Getwd()
	defer func() {
		_ = os.Chdir(oldDir)
	}()
	_ = os.Chdir(repoDir)

	// Create a real file in repo so git hash-object succeeds
	appliedFile := filepath.Join(repoDir, "applied.go")
	if err := os.WriteFile(appliedFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write applied.go: %v", err)
	}

	jsonEnvelope := `{
  "v": 1,
  "kind": "deliver",
  "cmd": "deliver",
  "data": {
    "task_id": "task-dry-run-123",
    "applied": ["applied.go"],
    "deleted": ["deleted.go"],
    "receipt_id": "rc-test-456"
  }
}`
	mockDeliver := buildMockHelper(t, t.TempDir(), "g8s", jsonEnvelope+"\n")

	// Run with --dry-run
	code := runCommand([]string{"--", mockDeliver, "deliver", "--dry-run"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	last, _ := ledger.LastEntry()
	if last == nil {
		t.Fatalf("no entry recorded")
	}

	if last.Extra == nil || last.Extra["dry_run"] != true {
		t.Errorf("expected extra.dry_run == true, got %v", last.Extra)
	}
	if last.Extra["task_id"] != "task-dry-run-123" {
		t.Errorf("expected task_id task-dry-run-123, got %v", last.Extra["task_id"])
	}
	if last.Extra["receipt_id"] != "rc-test-456" {
		t.Errorf("expected receipt_id rc-test-456, got %v", last.Extra["receipt_id"])
	}
	if last.ItemsChecked != 2 {
		t.Errorf("expected items_checked 2 (1 applied + 1 deleted), got %d", last.ItemsChecked)
	}

	// Blobs check
	blobs, ok := last.Extra["blobs"].(map[string]any)
	if !ok {
		// or map[string]string if unmarshaled as string
		blobsStr, okStr := last.Extra["blobs"].(map[string]string)
		if !okStr || blobsStr["applied.go"] == "" {
			t.Errorf("expected extra.blobs with applied.go hash, got %v", last.Extra["blobs"])
		}
	} else if blobs["applied.go"] == "" {
		t.Errorf("expected blobs for applied.go, got %v", blobs)
	}
}

func TestRunDirtyWorktree(t *testing.T) {
	repoDir := initTestGitRepo(t)
	oldDir, _ := os.Getwd()
	defer func() {
		_ = os.Chdir(oldDir)
	}()
	_ = os.Chdir(repoDir)

	// Clean worktree initially
	code := runCommand([]string{"--", "sh", "-c", "echo clean"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	ledger, err := OpenRepoLedger(repoDir)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	last, _ := ledger.LastEntry()
	if last.Dirty {
		t.Errorf("expected dirty=false on clean worktree, got dirty=true")
	}
	if last.DirtyHash != "" {
		t.Errorf("expected dirty_hash=\"\" on clean worktree, got %s", last.DirtyHash)
	}

	// Create dirty uncommitted file
	dirtyFile := filepath.Join(repoDir, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted change"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	code = runCommand([]string{"--", "sh", "-c", "echo dirty"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	last, _ = ledger.LastEntry()
	if !last.Dirty {
		t.Errorf("expected dirty=true on dirty worktree, got dirty=false")
	}
	if len(last.DirtyHash) != 64 {
		t.Errorf("expected 64-char sha256 dirty_hash, got %q", last.DirtyHash)
	}

	// Commit change and verify clean again
	_ = exec.Command("git", "add", "dirty.txt").Run()
	_ = exec.Command("git", "commit", "-m", "commit dirty file").Run()

	code = runCommand([]string{"--", "sh", "-c", "echo clean again"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	last, _ = ledger.LastEntry()
	if last.Dirty {
		t.Errorf("expected dirty=false after commit, got dirty=true")
	}
	if last.DirtyHash != "" {
		t.Errorf("expected dirty_hash=\"\" after commit, got %s", last.DirtyHash)
	}
}

func TestRunExitCodePassthrough(t *testing.T) {
	repoDir := initTestGitRepo(t)
	oldDir, _ := os.Getwd()
	defer func() {
		_ = os.Chdir(oldDir)
	}()
	_ = os.Chdir(repoDir)

	testCases := []int{0, 7, 42}
	for _, expectedCode := range testCases {
		cmdStr := fmt.Sprintf("exit %d", expectedCode)
		code := runCommand([]string{"--", "sh", "-c", cmdStr})
		if code != expectedCode {
			t.Errorf("expected exit code %d, got %d", expectedCode, code)
		}
		ledger, err := OpenRepoLedger(repoDir)
		if err != nil {
			t.Fatalf("open ledger: %v", err)
		}
		last, _ := ledger.LastEntry()
		if last.Exit != expectedCode {
			t.Errorf("expected recorded exit %d, got %d", expectedCode, last.Exit)
		}
		if expectedCode != 0 && last.Status != "FAIL" {
			t.Errorf("expected status FAIL for exit %d, got %s", expectedCode, last.Status)
		}
	}
}
