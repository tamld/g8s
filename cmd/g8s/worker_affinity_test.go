package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tamld/g8s/internal/pathutil"
)

// workerAffinityEnv builds the g8s binary and sets up a mock environment
// with multiple providers and multiple models in providers.json.
func workerAffinityEnv(t *testing.T) (string, func(dir string, args ...string) *exec.Cmd, string) {
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
      "name": "provider-alpha",
      "class": "platform_dispatch",
      "models": [
        {"id": "model-1"},
        {"id": "model-2"}
      ],
      "args": ` + mockArgs + `
    },
    {
      "name": "provider-beta",
      "class": "platform_dispatch",
      "models": [
        {"id": "model-1"},
        {"id": "model-2"}
      ],
      "args": ` + mockArgs + `
    }
  ]
}`
	if err := os.WriteFile(providersPath, []byte(providerJSON), 0o600); err != nil {
		t.Fatalf("write mock providers: %v", err)
	}

	// Filter out any ambient G8S_* vars from test runner
	var cleanEnv []string
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "G8S_") {
			cleanEnv = append(cleanEnv, env)
		}
	}
	cleanEnv = append(cleanEnv,
		"G8S_DB="+dbPath,
		"G8S_PROVIDERS="+providersPath,
	)

	run := func(dir string, args ...string) *exec.Cmd {
		cmd := exec.Command(binPath, args...)
		cmd.Env = cleanEnv
		cmd.Dir = dir
		return cmd
	}
	return binPath, run, dbPath
}

func submitAffinityTaskCLI(t *testing.T, run func(dir string, args ...string) *exec.Cmd, key, provider, model string, priority int) string {
	t.Helper()
	args := []string{
		"submit",
		"--idempotency-key", key,
		"--prompt", "execute task",
		"--priority", strconv.Itoa(priority),
		"--json",
	}
	if provider != "" {
		args = append(args, "--provider", provider)
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := run("", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("submit %s failed: %v\n%s", key, err, out)
	}

	var env struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil || env.Data.TaskID == "" {
		t.Fatalf("parse submit output for %s: %v\n%s", key, err, out)
	}
	return env.Data.TaskID
}

func checkTaskUntouched(t *testing.T, dbPath, taskID string) {
	t.Helper()
	db, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=ro"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var state string
	var attempts int
	var leaseOwner, leaseToken sql.NullString
	err = db.QueryRowContext(context.Background(),
		"SELECT state, attempts, lease_owner, lease_token FROM tasks WHERE task_id = ?",
		taskID).Scan(&state, &attempts, &leaseOwner, &leaseToken)
	if err != nil {
		t.Fatalf("query task %s: %v", taskID, err)
	}
	if state != "QUEUED" {
		t.Errorf("task %s state = %s, want QUEUED", taskID, state)
	}
	if attempts != 0 {
		t.Errorf("task %s attempts = %d, want 0", taskID, attempts)
	}
	if leaseOwner.Valid {
		t.Errorf("task %s lease_owner = %s, want NULL", taskID, leaseOwner.String)
	}
	if leaseToken.Valid {
		t.Errorf("task %s lease_token = %s, want NULL", taskID, leaseToken.String)
	}
}

// TestWorkerCLIAffinity_SerialOnce tests that --once with --provider and --model claims
// only matching tasks and leaves non-matching tasks queued with 0 attempts and no lease.
func TestWorkerCLIAffinity_SerialOnce(t *testing.T) {
	_, run, dbPath := workerAffinityEnv(t)
	repoDir := initGitRepo(t)

	idA1 := submitAffinityTaskCLI(t, run, "k-a1", "provider-alpha", "model-1", 10)
	idA2 := submitAffinityTaskCLI(t, run, "k-a2", "provider-alpha", "model-2", 20)
	idB1 := submitAffinityTaskCLI(t, run, "k-b1", "provider-beta", "model-1", 30)
	idB2 := submitAffinityTaskCLI(t, run, "k-b2", "provider-beta", "model-2", 40)

	// Worker runs with provider-alpha and model-1 -> must claim idA1
	cmd := run(repoDir, "worker", "--once", "--provider", "provider-alpha", "--model", "model-1", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("worker --once failed: %v\n%s", err, out)
	}
	stdout := string(out)
	if !strings.Contains(stdout, idA1) {
		t.Fatalf("expected claimed task %s in output:\n%s", idA1, stdout)
	}

	// Verify all other 3 tasks remain QUEUED with attempt 0 and no lease
	checkTaskUntouched(t, dbPath, idA2)
	checkTaskUntouched(t, dbPath, idB1)
	checkTaskUntouched(t, dbPath, idB2)
}

// TestWorkerCLIAffinity_MismatchRemainsQueued tests that when a worker starts with
// affinity matching zero queued tasks, it immediately exits drained without claiming
// or touching existing queued tasks.
func TestWorkerCLIAffinity_MismatchRemainsQueued(t *testing.T) {
	_, run, dbPath := workerAffinityEnv(t)
	repoDir := initGitRepo(t)

	idA1 := submitAffinityTaskCLI(t, run, "k-mismatch-target", "provider-alpha", "model-1", 10)

	// Worker asks for provider-alpha with model-2 (mismatch)
	cmd := run(repoDir, "worker", "--once", "--provider", "provider-alpha", "--model", "model-2", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("worker --once failed: %v\n%s", err, out)
	}
	stdout := string(out)
	if !strings.Contains(stdout, "drained") {
		t.Fatalf("expected queue drained envelope, got:\n%s", stdout)
	}
	if strings.Contains(stdout, idA1) {
		t.Fatalf("mismatched task %s should not appear in worker output:\n%s", idA1, stdout)
	}

	// Task must remain untouched
	checkTaskUntouched(t, dbPath, idA1)
}

// TestWorkerCLIAffinity_SerialDrain tests serial draining with --once=false and affinity flags.
func TestWorkerCLIAffinity_SerialDrain(t *testing.T) {
	_, run, dbPath := workerAffinityEnv(t)
	repoDir := initGitRepo(t)

	idMatch1 := submitAffinityTaskCLI(t, run, "k-s-m1", "provider-alpha", "model-1", 10)
	idMatch2 := submitAffinityTaskCLI(t, run, "k-s-m2", "provider-alpha", "model-1", 20)
	idMismatch := submitAffinityTaskCLI(t, run, "k-s-mis", "provider-alpha", "model-2", 30)

	cmd := run(repoDir, "worker", "--once=false", "--concurrency", "1", "--provider", "provider-alpha", "--model", "model-1", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("worker drain failed: %v\n%s", err, out)
	}
	stdout := string(out)

	if !strings.Contains(stdout, idMatch1) || !strings.Contains(stdout, idMatch2) {
		t.Fatalf("missing matching tasks in output:\n%s", stdout)
	}
	if strings.Contains(stdout, idMismatch) {
		t.Fatalf("mismatched task %s claimed:\n%s", idMismatch, stdout)
	}
	if !strings.Contains(stdout, "drained") {
		t.Fatalf("expected drained envelope:\n%s", stdout)
	}

	checkTaskUntouched(t, dbPath, idMismatch)
}

// TestWorkerCLIAffinity_ConcurrentDrain tests concurrent drain with affinity flags.
func TestWorkerCLIAffinity_ConcurrentDrain(t *testing.T) {
	_, run, dbPath := workerAffinityEnv(t)
	repoDir := initGitRepo(t)

	idMatch1 := submitAffinityTaskCLI(t, run, "k-c-m1", "provider-beta", "model-2", 10)
	idMatch2 := submitAffinityTaskCLI(t, run, "k-c-m2", "provider-beta", "model-2", 20)
	idMatch3 := submitAffinityTaskCLI(t, run, "k-c-m3", "provider-beta", "model-2", 30)
	idMismatch1 := submitAffinityTaskCLI(t, run, "k-c-mis1", "provider-beta", "model-1", 40)
	idMismatch2 := submitAffinityTaskCLI(t, run, "k-c-mis2", "provider-alpha", "model-2", 50)

	cmd := run(repoDir, "worker", "--once=false", "--concurrency", "2", "--provider", "provider-beta", "--model", "model-2", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("worker concurrent drain failed: %v\n%s", err, out)
	}
	stdout := string(out)

	for _, id := range []string{idMatch1, idMatch2, idMatch3} {
		if !strings.Contains(stdout, id) {
			t.Fatalf("missing matching task %s in output:\n%s", id, stdout)
		}
	}
	for _, id := range []string{idMismatch1, idMismatch2} {
		if strings.Contains(stdout, id) {
			t.Fatalf("mismatched task %s claimed:\n%s", id, stdout)
		}
		checkTaskUntouched(t, dbPath, id)
	}
}

// TestWorkerCLIAffinity_SerialConcurrentEquivalence tests that serial and concurrent
// worker drains produce identical claim sets across all filter permutations:
// empty, provider-only, model-only, both-set.
func TestWorkerCLIAffinity_SerialConcurrentEquivalence(t *testing.T) {
	testCases := []struct {
		name     string
		provider string
		model    string
		wantKeys []string
	}{
		{
			name:     "empty-unrestricted",
			provider: "",
			model:    "",
			wantKeys: []string{"k-a1", "k-a2", "k-b1", "k-b2"},
		},
		{
			name:     "provider-only",
			provider: "provider-alpha",
			model:    "",
			wantKeys: []string{"k-a1", "k-a2"},
		},
		{
			name:     "model-only",
			provider: "",
			model:    "model-1",
			wantKeys: []string{"k-a1", "k-b1"},
		},
		{
			name:     "both-set",
			provider: "provider-alpha",
			model:    "model-1",
			wantKeys: []string{"k-a1"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Helper to setup and run a drain mode
			runDrain := func(concurrency int) map[string]bool {
				_, run, _ := workerAffinityEnv(t)
				repoDir := initGitRepo(t)

				taskMap := make(map[string]string) // key -> taskID
				taskMap["k-a1"] = submitAffinityTaskCLI(t, run, "k-a1", "provider-alpha", "model-1", 10)
				taskMap["k-a2"] = submitAffinityTaskCLI(t, run, "k-a2", "provider-alpha", "model-2", 20)
				taskMap["k-b1"] = submitAffinityTaskCLI(t, run, "k-b1", "provider-beta", "model-1", 30)
				taskMap["k-b2"] = submitAffinityTaskCLI(t, run, "k-b2", "provider-beta", "model-2", 40)

				args := []string{"worker", "--once=false", "--concurrency", strconv.Itoa(concurrency), "--json"}
				if tc.provider != "" {
					args = append(args, "--provider", tc.provider)
				}
				if tc.model != "" {
					args = append(args, "--model", tc.model)
				}

				cmd := run(repoDir, args...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("drain (concurrency %d) failed: %v\n%s", concurrency, err, out)
				}
				stdout := string(out)

				claimedKeys := make(map[string]bool)
				for key, id := range taskMap {
					if strings.Contains(stdout, id) {
						claimedKeys[key] = true
					}
				}
				return claimedKeys
			}

			serialClaimed := runDrain(1)
			concurrentClaimed := runDrain(2)

			if !reflect.DeepEqual(serialClaimed, concurrentClaimed) {
				t.Fatalf("serial claimed keys %v != concurrent claimed keys %v for %s", serialClaimed, concurrentClaimed, tc.name)
			}

			// Verify claimed keys match expected wantKeys
			expectedMap := make(map[string]bool)
			for _, k := range tc.wantKeys {
				expectedMap[k] = true
			}
			if !reflect.DeepEqual(serialClaimed, expectedMap) {
				t.Fatalf("claimed keys %v != expected %v for %s", serialClaimed, expectedMap, tc.name)
			}
		})
	}
}

// TestWorkerCLIAffinity_SingleWinningClaimConcurrency tests that multiple concurrent
// workers claiming tasks from a shared filtered queue elect a single winner per task.
func TestWorkerCLIAffinity_SingleWinningClaimConcurrency(t *testing.T) {
	_, run, dbPath := workerAffinityEnv(t)
	repoDir := initGitRepo(t)

	// Submit 8 tasks with provider-alpha and model-1
	const totalTasks = 8
	taskIDs := make([]string, 0, totalTasks)
	for i := 0; i < totalTasks; i++ {
		id := submitAffinityTaskCLI(t, run, "k-race-"+strconv.Itoa(i), "provider-alpha", "model-1", i)
		taskIDs = append(taskIDs, id)
	}

	// Run 2 worker processes simultaneously with --once=false --concurrency 2
	var wg sync.WaitGroup
	var mu sync.Mutex
	allOutputs := make([]string, 0, 2)

	for p := 0; p < 2; p++ {
		wg.Add(1)
		go func(workerIndex int) {
			defer wg.Done()
			cmd := run(repoDir, "worker", "--once=false", "--concurrency", "2",
				"--provider", "provider-alpha", "--model", "model-1",
				"--actor", "worker-"+strconv.Itoa(workerIndex), "--json")
			out, _ := cmd.CombinedOutput()
			mu.Lock()
			allOutputs = append(allOutputs, string(out))
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	// Parse claimed task occurrences across all worker process outputs
	db, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "mode=ro"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, id := range taskIDs {
		var state string
		var attempts int
		if err := db.QueryRow("SELECT state, attempts FROM tasks WHERE task_id = ?", id).Scan(&state, &attempts); err != nil {
			t.Fatalf("query task %s: %v", id, err)
		}
		if state != "WORKER_COMPLETED" {
			t.Errorf("task %s state = %s, want WORKER_COMPLETED", id, state)
		}
		if attempts != 1 {
			t.Errorf("task %s attempts = %d, want exactly 1 attempt (single winning claim)", id, attempts)
		}
	}
}

// TestWorkerCLIAffinity_ReadonlyAndIsolationFailClosed verifies fail-closed isolation
// for concurrent mode when worktree isolation is unavailable.
func TestWorkerCLIAffinity_ReadonlyAndIsolationFailClosed(t *testing.T) {
	_, run, _ := workerAffinityEnv(t)
	nonGitDir := t.TempDir()

	cmd := run(nonGitDir, "worker", "--once=false", "--concurrency", "2",
		"--provider", "provider-alpha", "--model", "model-1", "--json")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected usage error when concurrency > 1 outside git repo, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "requires worktree isolation") {
		t.Fatalf("expected fail-closed isolation message, got:\n%s", string(out))
	}
}
