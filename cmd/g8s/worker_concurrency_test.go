package main

// #394 PR-B: `g8s worker --concurrency N` — bounded concurrent drain behind
// the session-isolation hard gate. RED-first against the ratified design:
// default N=1 stays byte-identical; N>1 without worktree isolation is a
// usage error, never a warning.

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// workerConcurrencyEnv builds the binary plus a mock-provider environment
// (mirroring TestSubmitAndWorkerE2E) and returns the binary path, a command
// runner bound to one shared control-plane DB, and that DB path.
func workerConcurrencyEnv(t *testing.T) (string, func(dir string, args ...string) *exec.Cmd, string) {
	t.Helper()
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "config")
	dbPath := filepath.Join(tempDir, "cp.db")
	_ = os.MkdirAll(configDir, 0o700)

	providersPath := filepath.Join(configDir, "providers.json")
	mockArgs := `["sh", "-c", "echo '{\"status\":\"succeeded\"}'"]`
	if runtime.GOOS == "windows" {
		mockCmd := filepath.Join(tempDir, "mock_success.cmd")
		if err := os.WriteFile(mockCmd, []byte("@echo off\r\necho {\"status\":\"succeeded\"}\r\n"), 0o755); err != nil {
			t.Fatalf("write mock success: %v", err)
		}
		mockArgs = "[" + strconv.Quote(filepath.ToSlash(mockCmd)) + "]"
	}
	providerJSON := `{
  "version": "1.0",
  "providers": [
    {
      "name": "mock-success",
      "class": "platform_dispatch",
      "models": [{"id": "gemini-3.8-flash-high"}],
      "args": ` + mockArgs + `
    }
  ]
}`
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0o600); err != nil {
		t.Fatalf("write mock providers: %v", err)
	}
	env := append(os.Environ(),
		"G8S_DB="+dbPath,
		"G8S_PROVIDERS="+providersPath,
	)
	run := func(dir string, args ...string) *exec.Cmd {
		cmd := exec.Command(binPath, args...)
		cmd.Env = env
		cmd.Dir = dir
		return cmd
	}
	return binPath, run, dbPath
}

// envelopeMessage extracts the error envelope message, undoing Go's HTML
// escaping of `>` so assertions can match the ratified wording.
func envelopeMessage(t *testing.T, out []byte) string {
	t.Helper()
	return strings.ReplaceAll(string(out), `\u003e`, ">")
}

func submitTasks(t *testing.T, binPath string, run func(dir string, args ...string) *exec.Cmd, idemPrefix string, n int) []string {
	t.Helper()
	var ids []string
	for i := 0; i < n; i++ {
		submit := run("", "submit", "--idempotency-key", idemPrefix+"-"+strconv.Itoa(i),
			"--prompt", "scan repo", "--model", "gemini-3.8-flash-high", "--json")
		out, err := submit.CombinedOutput()
		if err != nil {
			t.Fatalf("submit %d failed: %v\n%s", i, err, out)
		}
		compact := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
				return -1
			}
			return r
		}, string(out))
		if idx := strings.Index(compact, `"task_id":"`); idx >= 0 {
			rest := compact[idx+len(`"task_id":"`):]
			if end := strings.Index(rest, `"`); end >= 0 {
				ids = append(ids, rest[:end])
			}
		}
	}
	if len(ids) != n {
		t.Fatalf("expected %d submitted tasks, got %d", n, len(ids))
	}
	return ids
}

// N>1 in a directory that cannot provide worktree isolation must be a hard
// usage error (exit 2) — ratified wording, never a warning.
func TestWorkerConcurrencyRequiresWorktreeIsolation(t *testing.T) {
	_, run, _ := workerConcurrencyEnv(t)
	plainDir := t.TempDir() // not a git repo

	cmd := run(plainDir, "worker", "--once=false", "--concurrency", "2", "--json")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected usage error outside a git checkout, got success: %s", out)
	}
	if !strings.Contains(envelopeMessage(t, out), "concurrency>1 requires worktree isolation") {
		t.Fatalf("expected ratified hard-gate message, got: %s", out)
	}
}

// --concurrency N>1 is meaningless in --once mode: hard usage error instead
// of silently ignoring the flag.
func TestWorkerConcurrencyOnceIsUsageError(t *testing.T) {
	_, run, _ := workerConcurrencyEnv(t)
	repoDir := initGitRepo(t)

	cmd := run(repoDir, "worker", "--once", "--concurrency", "2", "--json")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected usage error for --once with --concurrency, got success: %s", out)
	}
	if !strings.Contains(envelopeMessage(t, out), "--once=false") {
		t.Fatalf("expected remediation hint pointing at --once=false, got: %s", out)
	}
}

// The happy path: a git checkout, 3 queued tasks, 2 concurrent workers —
// everything drains, the session is registered and finished, and every task
// reaches a completed state.
func TestWorkerConcurrentDrainE2E(t *testing.T) {
	binPath, run, dbPath := workerConcurrencyEnv(t)
	repoDir := initGitRepo(t)
	taskIDs := submitTasks(t, binPath, run, "conc-e2e", 3)

	cmd := run(repoDir, "worker", "--once=false", "--concurrency", "2", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("concurrent drain failed: %v\n%s", err, out)
	}
	stdout := string(out)
	if !strings.Contains(stdout, `"worker_status"`) || !strings.Contains(stdout, `"drained"`) {
		t.Fatalf("expected drained status envelope, got: %s", stdout)
	}
	for _, id := range taskIDs {
		if !strings.Contains(stdout, id) {
			t.Fatalf("drain output missing task %s:\n%s", id, stdout)
		}
		gout, gerr := run("", "get", id, "--json").CombinedOutput()
		if gerr != nil || !strings.Contains(string(gout), "WORKER_COMPLETED") {
			t.Fatalf("task %s not completed: err=%v\n%s", id, gerr, gout)
		}
	}

	// Session provenance (#393): exactly one registered session, finished dead.
	db, derr := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if derr != nil {
		t.Fatalf("open control plane: %v", derr)
	}
	defer db.Close()
	var sessions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE status = 'dead' AND mode = 'worktree'`).Scan(&sessions); err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	if sessions != 1 {
		t.Fatalf("expected exactly 1 finished session row, got %d", sessions)
	}
}

// Default N=1 keeps today's serial drain behavior (same envelopes, same
// exit contract).
func TestWorkerDefaultDrainUnchanged(t *testing.T) {
	binPath, run, _ := workerConcurrencyEnv(t)
	repoDir := initGitRepo(t)
	submitTasks(t, binPath, run, "serial-drain", 1)

	cmd := run(repoDir, "worker", "--once=false", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("serial drain failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"drained"`) {
		t.Fatalf("expected drained envelope, got: %s", out)
	}
}

// After the concurrent drain, pool worktrees are released (no leftover
// checkout dirs or branches). Windows is skipped for the filesystem
// assertion due to handle-sharing lag documented in #400/#401.
func TestWorkerConcurrentDrainReleasesWorktrees(t *testing.T) {
	binPath, run, _ := workerConcurrencyEnv(t)
	repoDir := initGitRepo(t)
	submitTasks(t, binPath, run, "wt-release", 2)

	cmd := run(repoDir, "worker", "--once=false", "--concurrency", "2", "--json")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("concurrent drain failed: %v\n%s", err, out)
	}

	if runtime.GOOS == "windows" {
		t.Skip("worktree removal assertion skipped on windows (handle-sharing lag, #400)")
	}
	out, err := exec.Command("git", "-C", repoDir, "worktree", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v (%s)", err, out)
	}
	// Only the main working tree should remain.
	if n := strings.Count(string(out), "\n"); n > 1 {
		t.Fatalf("expected only the main worktree to remain, got:\n%s", out)
	}
}
