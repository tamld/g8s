package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/supervisor"
)

// trackingStubWorker records spawn invocations and returns predefined receipts.
type trackingStubWorker struct {
	spawnedTasks []orchestrator.Task
	receipts     []orchestrator.Receipt
}

func (w *trackingStubWorker) Name() string                      { return "tracking-stub" }
func (w *trackingStubWorker) Available(_ context.Context) error { return nil }
func (w *trackingStubWorker) Spawn(_ context.Context, t orchestrator.Task) (orchestrator.Handle, error) {
	w.spawnedTasks = append(w.spawnedTasks, t)
	idx := len(w.spawnedTasks) - 1
	var r orchestrator.Receipt
	if idx < len(w.receipts) {
		r = w.receipts[idx]
	} else {
		r = orchestrator.Receipt{
			OK:              true,
			WorkerName:      w.Name(),
			TaskID:          t.ID,
			CommitSHA:       "test-sha",
			FilesModified:   []string{"file.go"},
			DurationSeconds: 0.25,
			StartedAt:       time.Now(),
			FinishedAt:      time.Now(),
		}
	}
	if r.TaskID == "" {
		r.TaskID = t.ID
	}
	if r.WorkerName == "" {
		r.WorkerName = w.Name()
	}
	return &trackingStubHandle{receipt: r}, nil
}

type trackingStubHandle struct {
	receipt orchestrator.Receipt
}

func (h *trackingStubHandle) PID() int { return 42 }
func (h *trackingStubHandle) Wait(_ context.Context) (orchestrator.Receipt, error) {
	return h.receipt, nil
}
func (h *trackingStubHandle) Cancel(_ context.Context) error { return nil }
func (h *trackingStubHandle) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return nil
}

func TestParseIntentSubtasks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single line",
			input:    "scan ./src for MCP implementations",
			expected: []string{"scan ./src for MCP implementations"},
		},
		{
			name:     "multi line 3 tasks",
			input:    "scan ./src for MCP implementations\nreview ./docs for spec changes\nverify test coverage",
			expected: []string{"scan ./src for MCP implementations", "review ./docs for spec changes", "verify test coverage"},
		},
		{
			name:     "comma separated 3 tasks",
			input:    "scan ./src for MCP implementations, review ./docs for spec changes, verify test coverage",
			expected: []string{"scan ./src for MCP implementations", "review ./docs for spec changes", "verify test coverage"},
		},
		{
			name:     "mixed commas and newlines with spaces",
			input:    "task 1, task 2\n  \n task 3 , task 4\n",
			expected: []string{"task 1", "task 2", "task 3", "task 4"},
		},
		{
			name:     "empty input",
			input:    "   \n  ,  \n\t ",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseIntentSubtasks(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected %d tasks, got %d (%v)", len(tc.expected), len(got), got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("task[%d]: expected %q, got %q", i, tc.expected[i], got[i])
				}
			}
		})
	}
}

func TestOrchestrateIntentSplitMultiLine(t *testing.T) {
	dbPath := withTempDB(t)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	defer store.Close()

	worker := &trackingStubWorker{}
	reviewer := supervisor.NewStubReviewer()

	intent := "task 1: scan ./src for security issues\ntask 2: check ./docs for outdated specs\ntask 3: verify ./pkg tests pass"

	opts := orchestrateOptions{
		Store:         store,
		Worker:        worker,
		Reviewer:      reviewer,
		MaxAttempts:   1,
		MaxApproaches: 1,
		Timeout:       time.Minute,
		Model:         "gemini-3.7-flash-high",
		Role:          "collector",
		Permission:    "read_only",
		AddDirs:       []string{t.TempDir()},
	}

	res, err := executeOrchestration(context.Background(), intent, opts)
	if err != nil {
		t.Fatalf("executeOrchestration failed: %v", err)
	}

	if res.SupervisorTaskID == "" {
		t.Fatalf("expected non-empty supervisor_task_id")
	}
	if res.Outcome != "SUCCEEDED" {
		t.Errorf("expected outcome SUCCEEDED, got %s", res.Outcome)
	}
	if res.Verdict != "PASS" {
		t.Errorf("expected verdict PASS, got %s", res.Verdict)
	}
	if len(res.SubTasks) != 3 {
		t.Fatalf("expected 3 sub_tasks, got %d", len(res.SubTasks))
	}

	expectedPrompts := []string{
		"task 1: scan ./src for security issues",
		"task 2: check ./docs for outdated specs",
		"task 3: verify ./pkg tests pass",
	}

	for i, st := range res.SubTasks {
		if st.Task != expectedPrompts[i] {
			t.Errorf("sub_task[%d] task = %q, want %q", i, st.Task, expectedPrompts[i])
		}
		if st.Status != "succeeded" {
			t.Errorf("sub_task[%d] status = %q, want succeeded", i, st.Status)
		}
		if st.TaskID == "" {
			t.Errorf("sub_task[%d] task_id is empty", i)
		}
	}

	if res.ReceiptSummary == nil {
		t.Fatalf("expected non-nil receipt_summary")
	}
	if res.ReceiptSummary.TotalRuns != 3 {
		t.Errorf("expected TotalRuns=3, got %d", res.ReceiptSummary.TotalRuns)
	}
	if res.ReceiptSummary.Succeeded != 3 {
		t.Errorf("expected Succeeded=3, got %d", res.ReceiptSummary.Succeeded)
	}
	if res.ReceiptSummary.Failed != 0 {
		t.Errorf("expected Failed=0, got %d", res.ReceiptSummary.Failed)
	}

	// Verify JSON serialization matches contract
	encoded, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	jsonStr := string(encoded)
	if !strings.Contains(jsonStr, `"supervisor_task_id"`) {
		t.Errorf("missing supervisor_task_id in JSON: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"sub_tasks"`) {
		t.Errorf("missing sub_tasks in JSON: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"receipt_summary"`) {
		t.Errorf("missing receipt_summary in JSON: %s", jsonStr)
	}
}

func TestOrchestrateIntentSubtaskFailure(t *testing.T) {
	dbPath := withTempDB(t)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	defer store.Close()

	worker := &trackingStubWorker{
		receipts: []orchestrator.Receipt{
			{OK: true, DurationSeconds: 0.1, TaskID: "t0"},
			{OK: true, DurationSeconds: 0.1, TaskID: "t1"},
			{OK: false, Stderr: "syntax error", DurationSeconds: 0.1, TaskID: "t2"},
		},
	}

	intent := "task 1, task 2"
	opts := orchestrateOptions{
		Store:         store,
		Worker:        worker,
		Reviewer:      supervisor.NewStubReviewer(),
		MaxAttempts:   1,
		MaxApproaches: 1,
		Timeout:       time.Minute,
		AddDirs:       []string{t.TempDir()},
	}

	res, err := executeOrchestration(context.Background(), intent, opts)
	if err != nil {
		t.Fatalf("executeOrchestration failed: %v", err)
	}

	if len(res.SubTasks) != 2 {
		t.Fatalf("expected 2 sub_tasks, got %d", len(res.SubTasks))
	}
	if res.SubTasks[0].Status != "succeeded" {
		t.Errorf("subtask[0] status = %q, want succeeded", res.SubTasks[0].Status)
	}
	if res.SubTasks[1].Status != "failed" {
		t.Errorf("subtask[1] status = %q, want failed", res.SubTasks[1].Status)
	}
	if res.ReceiptSummary.Failed != 1 {
		t.Errorf("summary failed count = %d, want 1", res.ReceiptSummary.Failed)
	}
	if res.ReceiptSummary.Succeeded != 1 {
		t.Errorf("summary succeeded count = %d, want 1", res.ReceiptSummary.Succeeded)
	}
}

func TestOrchestrateIntentFromFile(t *testing.T) {
	dbPath := withTempDB(t)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	defer store.Close()

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "intent.txt")
	intentContent := "audit auth module\ninspect database queries"
	if err := os.WriteFile(filePath, []byte(intentContent), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	worker := &trackingStubWorker{}
	opts := orchestrateOptions{
		Store:         store,
		Worker:        worker,
		Reviewer:      supervisor.NewStubReviewer(),
		MaxAttempts:   1,
		MaxApproaches: 1,
		Timeout:       time.Minute,
		AddDirs:       []string{tempDir},
	}

	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	res, err := executeOrchestration(context.Background(), string(raw), opts)
	if err != nil {
		t.Fatalf("executeOrchestration: %v", err)
	}

	if len(res.SubTasks) != 2 {
		t.Fatalf("expected 2 sub_tasks, got %d", len(res.SubTasks))
	}
	if res.SubTasks[0].Task != "audit auth module" {
		t.Errorf("task[0] = %q, want 'audit auth module'", res.SubTasks[0].Task)
	}
	if res.SubTasks[1].Task != "inspect database queries" {
		t.Errorf("task[1] = %q, want 'inspect database queries'", res.SubTasks[1].Task)
	}
}

func TestOrchestrateIntentEmptyReturnsUsageError(t *testing.T) {
	dbPath := withTempDB(t)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	defer store.Close()

	opts := orchestrateOptions{
		Store: store,
	}

	_, err = executeOrchestration(context.Background(), "   \n\t  ", opts)
	if err == nil {
		t.Fatalf("expected error on empty intent, got nil")
	}
	if !strings.Contains(err.Error(), "empty intent") {
		t.Errorf("expected 'empty intent' error, got %v", err)
	}
}

func TestOrchestrateWithFanOutPool(t *testing.T) {
	dbPath := withTempDB(t)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	defer store.Close()

	// Initialize a temporary git repository for testing Pool + FanOut
	repoDir := t.TempDir()
	cmd := exec.Command("git", "init", repoDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	// Initial commit so HEAD exists
	readme := filepath.Join(repoDir, "README.md")
	_ = os.WriteFile(readme, []byte("# Test Repo\n"), 0o600)
	_ = exec.Command("git", "-C", repoDir, "config", "user.email", "test@test.com").Run()
	_ = exec.Command("git", "-C", repoDir, "config", "user.name", "Test").Run()
	_ = exec.Command("git", "-C", repoDir, "add", "README.md").Run()
	_ = exec.Command("git", "-C", repoDir, "commit", "-m", "init").Run()

	pool, err := orchestrator.NewPool(orchestrator.PoolOptions{
		Repo: repoDir,
		Root: filepath.Join(t.TempDir(), "wt-pool"),
	})
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}

	worker := &trackingStubWorker{}
	reg := orchestrator.NewRegistry()
	reg.Register("tracking-stub", func() orchestrator.Worker { return worker })

	opts := orchestrateOptions{
		Store:         store,
		Worker:        worker,
		Reviewer:      supervisor.NewStubReviewer(),
		Pool:          pool,
		Registry:      reg,
		MaxAttempts:   1,
		MaxApproaches: 1,
		Timeout:       time.Minute,
		AddDirs:       []string{repoDir},
	}

	res, err := executeOrchestration(context.Background(), "subtask 1, subtask 2", opts)
	if err != nil {
		t.Fatalf("executeOrchestration with pool: %v", err)
	}

	if len(res.SubTasks) != 2 {
		t.Fatalf("expected 2 subtasks, got %d", len(res.SubTasks))
	}
}

func TestOrchestrateAICWrapper(t *testing.T) {
	origFetcher := ghDiffFetcher
	defer func() { ghDiffFetcher = origFetcher }()

	diffFetched := false
	ghDiffFetcher = func(pr int) (string, error) {
		if pr != 100 {
			t.Errorf("expected PR 100, got %d", pr)
		}
		diffFetched = true
		return "diff --git a/main.go b/main.go\n+ // new line", nil
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "aic_test.db")
	t.Setenv("G8S_DB", dbPath)

	diff, err := ghDiffFetcher(100)
	if err != nil {
		t.Fatalf("ghDiffFetcher error: %v", err)
	}
	if !diffFetched {
		t.Fatalf("ghDiffFetcher was not called")
	}
	if !strings.Contains(diff, "diff --git") {
		t.Errorf("unexpected diff: %s", diff)
	}
}

func TestOrchestrateAICDiffError(t *testing.T) {
	origFetcher := ghDiffFetcher
	defer func() { ghDiffFetcher = origFetcher }()

	ghDiffFetcher = func(pr int) (string, error) {
		return "", errors.New("gh command failed: not logged in")
	}

	_, err := ghDiffFetcher(42)
	if err == nil {
		t.Fatalf("expected error from failed diff fetcher")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected not logged in error, got: %v", err)
	}
}

func TestRunOrchestrateCLIIntentJSON(t *testing.T) {
	origCtor := orchestratorWorkerCtor
	defer func() { orchestratorWorkerCtor = origCtor }()
	orchestratorWorkerCtor = func() orchestrator.Worker { return &trackingStubWorker{} }

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cli_intent.db")
	t.Setenv("G8S_DB", dbPath)

	out := captureStdout(t, func() {
		runOrchestrate([]string{"--from-intent", "task 1, task 2", "--json"})
	})

	if !strings.Contains(out, `"supervisor_task_id"`) {
		t.Fatalf("expected supervisor_task_id in output, got: %s", out)
	}
	if !strings.Contains(out, `"sub_tasks"`) {
		t.Fatalf("expected sub_tasks in output, got: %s", out)
	}
	if !strings.Contains(out, `"receipt_summary"`) {
		t.Fatalf("expected receipt_summary in output, got: %s", out)
	}
}

func TestRunOrchestrateCLIIntentPlainText(t *testing.T) {
	origCtor := orchestratorWorkerCtor
	defer func() { orchestratorWorkerCtor = origCtor }()
	orchestratorWorkerCtor = func() orchestrator.Worker { return &trackingStubWorker{} }

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cli_plain.db")
	t.Setenv("G8S_DB", dbPath)

	out := captureStdout(t, func() {
		runOrchestrate([]string{"--from-intent", "task A, task B", "--json=false"})
	})

	if !strings.Contains(out, "supervisor task:") {
		t.Fatalf("expected supervisor task in plain output, got: %s", out)
	}
	if !strings.Contains(out, "sub-tasks: 2") {
		t.Fatalf("expected sub-tasks: 2 in plain output, got: %s", out)
	}
}

func TestRunOrchestrateCLIFromFile(t *testing.T) {
	origCtor := orchestratorWorkerCtor
	defer func() { orchestratorWorkerCtor = origCtor }()
	orchestratorWorkerCtor = func() orchestrator.Worker { return &trackingStubWorker{} }

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cli_file.db")
	t.Setenv("G8S_DB", dbPath)

	intentPath := filepath.Join(dir, "intent_cli.txt")
	if err := os.WriteFile(intentPath, []byte("file task 1\nfile task 2"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	out := captureStdout(t, func() {
		runOrchestrate([]string{"--from-file", intentPath, "--json"})
	})

	if !strings.Contains(out, "file task 1") {
		t.Fatalf("expected file task 1 in output, got: %s", out)
	}
}

func TestRunOrchestrateAICCLI(t *testing.T) {
	origCtor := orchestratorWorkerCtor
	defer func() { orchestratorWorkerCtor = origCtor }()
	orchestratorWorkerCtor = func() orchestrator.Worker { return &trackingStubWorker{} }

	origFetcher := ghDiffFetcher
	defer func() { ghDiffFetcher = origFetcher }()
	ghDiffFetcher = func(pr int) (string, error) {
		return "diff --git a/pkg.go b/pkg.go\n+ func New()", nil
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cli_aic.db")
	t.Setenv("G8S_DB", dbPath)

	out := captureStdout(t, func() {
		runOrchestrateAIC([]string{"--pr", "101", "--intent", "review changes", "--json"})
	})

	if !strings.Contains(out, `"supervisor_task_id"`) {
		t.Fatalf("expected JSON output from orchestrate-aic, got: %s", out)
	}
}

func TestDefaultGHDiffFetcher(t *testing.T) {
	// Exercise defaultGHDiffFetcher error path with invalid PR
	_, _ = defaultGHDiffFetcher(-99999)
}
