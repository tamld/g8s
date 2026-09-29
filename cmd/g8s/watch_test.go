package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/watch"
	_ "modernc.org/sqlite"
)

// setupTaskWithState creates a task in the controlplane store and updates its state directly.
func setupTaskWithState(t *testing.T, store *controlplane.Store, dbPath, state string) string {
	t.Helper()
	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: "idem-" + t.Name() + "-" + state,
		Priority:       10,
		MaxAttempts:    3,
		Role:           "scout",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{filepath.Dir(dbPath)},
		Payload:        json.RawMessage(`{"prompt":"test"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("UPDATE tasks SET state = ? WHERE task_id = ?", state, task.TaskID); err != nil {
		t.Fatalf("update task state to %s: %v", state, err)
	}
	return task.TaskID
}

// TestWatchTaskMilestonesTable tests the milestone mapping table-driven:
// 1. --milestone accept (default, flag absent): task at WORKER_COMPLETED -> still polling (no resolution) — assert mapping, not a sleep.
// 2. --milestone worker-complete: WORKER_COMPLETED -> resolved with the exact state string "worker-complete".
// 3. --milestone accept: SUCCEEDED -> resolved as today; FAILED -> failure as today (regression guard).
// 4. --milestone worker-complete: FAILED -> still failure (exit nonzero path).
func TestWatchTaskMilestonesTable(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("create control plane: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	tests := []struct {
		name         string
		taskState    string
		milestone    string // empty string simulates default (flag absent)
		wantDone     bool
		wantPassed   bool
		wantState    watch.VerdictState
		wantDetail   string
		wantExitCode int
	}{
		// 1. --milestone accept (default, flag absent): task at WORKER_COMPLETED -> still polling (no resolution)
		{
			name:         "flag absent (default) with task at WORKER_COMPLETED is still polling",
			taskState:    "WORKER_COMPLETED",
			milestone:    "",
			wantDone:     false,
			wantExitCode: -1,
		},
		{
			name:         "milestone accept with task at WORKER_COMPLETED is still polling",
			taskState:    "WORKER_COMPLETED",
			milestone:    MilestoneAccept,
			wantDone:     false,
			wantExitCode: -1,
		},
		// 2. --milestone worker-complete: WORKER_COMPLETED -> resolved with exact state string worker-complete
		{
			name:         "milestone worker-complete with task at WORKER_COMPLETED resolves",
			taskState:    "WORKER_COMPLETED",
			milestone:    MilestoneWorkerComplete,
			wantDone:     true,
			wantPassed:   true,
			wantState:    StateWorkerComplete,
			wantDetail:   DetailWorkerComplete,
			wantExitCode: 0,
		},
		// 3. --milestone accept: SUCCEEDED -> resolved as today; FAILED -> failure as today (regression guard)
		{
			name:         "milestone accept with task at SUCCEEDED resolves as today",
			taskState:    "SUCCEEDED",
			milestone:    MilestoneAccept,
			wantDone:     true,
			wantPassed:   true,
			wantState:    watch.StatePassed,
			wantExitCode: 0,
		},
		{
			name:         "milestone accept with task at FAILED is failure as today",
			taskState:    "FAILED",
			milestone:    MilestoneAccept,
			wantDone:     true,
			wantPassed:   false,
			wantState:    watch.StateFailed,
			wantExitCode: 1,
		},
		{
			name:         "milestone accept with task at CANCELLED is failure as today",
			taskState:    "CANCELLED",
			milestone:    MilestoneAccept,
			wantDone:     true,
			wantPassed:   false,
			wantState:    watch.StateFailed,
			wantExitCode: 1,
		},
		// 4. --milestone worker-complete: FAILED -> still failure (exit nonzero path)
		{
			name:         "milestone worker-complete with task at FAILED still failure",
			taskState:    "FAILED",
			milestone:    MilestoneWorkerComplete,
			wantDone:     true,
			wantPassed:   false,
			wantState:    watch.StateFailed,
			wantExitCode: 1,
		},
		{
			name:         "milestone worker-complete with task at CANCELLED still failure",
			taskState:    "CANCELLED",
			milestone:    MilestoneWorkerComplete,
			wantDone:     true,
			wantPassed:   false,
			wantState:    watch.StateFailed,
			wantExitCode: 1,
		},
		{
			name:         "milestone worker-complete with task at TIMED_OUT still failure",
			taskState:    "TIMED_OUT",
			milestone:    MilestoneWorkerComplete,
			wantDone:     true,
			wantPassed:   false,
			wantState:    watch.StateFailed,
			wantExitCode: 1,
		},
		{
			name:         "milestone worker-complete with task at SUCCEEDED resolves",
			taskState:    "SUCCEEDED",
			milestone:    MilestoneWorkerComplete,
			wantDone:     true,
			wantPassed:   true,
			wantState:    watch.StatePassed,
			wantExitCode: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			taskID := setupTaskWithState(t, store, dbPath, tc.taskState)

			var res watch.CheckResult
			var err error
			if tc.milestone == "" {
				res, err = checkTask(ctx, store, taskID)
			} else {
				res, err = checkTask(ctx, store, taskID, tc.milestone)
			}
			if err != nil {
				t.Fatalf("checkTask failed: %v", err)
			}

			if res.Done != tc.wantDone {
				t.Fatalf("checkTask Done = %v, want %v", res.Done, tc.wantDone)
			}

			if tc.wantDone {
				if res.Passed != tc.wantPassed {
					t.Errorf("checkTask Passed = %v, want %v", res.Passed, tc.wantPassed)
				}
				if tc.wantDetail != "" && res.Detail != tc.wantDetail {
					t.Errorf("checkTask Detail = %q, want %q", res.Detail, tc.wantDetail)
				}
			}

			verdict := resolveTaskVerdict(res, tc.milestone)
			if verdict.Done != tc.wantDone {
				t.Errorf("resolveTaskVerdict Done = %v, want %v", verdict.Done, tc.wantDone)
			}
			if tc.wantDone {
				if verdict.State != tc.wantState {
					t.Errorf("resolveTaskVerdict State = %q, want %q", verdict.State, tc.wantState)
				}
				if tc.wantDetail != "" && verdict.Detail != tc.wantDetail {
					t.Errorf("resolveTaskVerdict Detail = %q, want %q", verdict.Detail, tc.wantDetail)
				}
			}
			if verdict.ExitCode != tc.wantExitCode {
				t.Errorf("resolveTaskVerdict ExitCode = %d, want %d", verdict.ExitCode, tc.wantExitCode)
			}
		})
	}
}

// TestWatchRunWithWorkerCompleteMilestone verifies that Watcher.Run immediately terminates
// without sleeping when a task is in WORKER_COMPLETED under worker-complete milestone.
func TestWatchRunWithWorkerCompleteMilestone(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("create control plane: %v", err)
	}
	defer store.Close()

	taskID := setupTaskWithState(t, store, dbPath, "WORKER_COMPLETED")

	checker := func(ctx context.Context) (watch.CheckResult, error) {
		return checkTask(ctx, store, taskID, MilestoneWorkerComplete)
	}

	sleepCalls := 0
	sleeper := func(ctx context.Context, d time.Duration) error {
		sleepCalls++
		return nil
	}

	w := watch.Watcher{
		Checker:  checker,
		Interval: time.Minute,
		Timeout:  10 * time.Minute,
		Sleeper:  sleeper,
	}

	verdict, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("w.Run failed: %v", err)
	}

	if sleepCalls != 0 {
		t.Errorf("expected 0 sleep calls for immediate resolution, got %d", sleepCalls)
	}
	if verdict.Polls != 1 {
		t.Errorf("expected 1 poll, got %d", verdict.Polls)
	}
	if verdict.Detail != DetailWorkerComplete {
		t.Errorf("expected detail %q, got %q", DetailWorkerComplete, verdict.Detail)
	}

	// Apply milestone as runWatch does
	if verdict.Detail == DetailWorkerComplete {
		verdict.State = StateWorkerComplete
	}
	if verdict.State != StateWorkerComplete {
		t.Errorf("verdict.State = %q, want %q", verdict.State, StateWorkerComplete)
	}
}

// TestWatchCLIIntegration builds the g8s binary and exercises watch with the milestone flag.
func TestWatchCLIIntegration(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("create control plane: %v", err)
	}
	defer store.Close()

	taskID := setupTaskWithState(t, store, dbPath, "WORKER_COMPLETED")

	t.Run("worker-complete milestone exits 0 with JSON envelope", func(t *testing.T) {
		cmd := exec.Command(binPath, "watch", "--task", taskID, "--milestone", "worker-complete", "--json")
		cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("watch failed with exit error: %v, output: %s", err, string(out))
		}

		var env struct {
			Kind string `json:"kind"`
			Data struct {
				Target string `json:"target"`
				State  string `json:"state"`
				Detail string `json:"detail"`
				Polls  int    `json:"polls"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("unmarshal watch JSON output: %v, raw: %s", err, string(out))
		}
		if env.Kind != "watch_verdict" {
			t.Errorf("env.Kind = %q, want watch_verdict", env.Kind)
		}
		if env.Data.State != "worker-complete" {
			t.Errorf("data.state = %q, want worker-complete", env.Data.State)
		}
		if env.Data.Detail != DetailWorkerComplete {
			t.Errorf("data.detail = %q, want %q", env.Data.Detail, DetailWorkerComplete)
		}
		if env.Data.Polls < 1 {
			t.Errorf("data.polls = %d, want >= 1", env.Data.Polls)
		}
	})

	t.Run("default accept milestone times out on WORKER_COMPLETED", func(t *testing.T) {
		cmd := exec.Command(binPath, "watch", "--task", taskID, "--interval", "10ms", "--timeout", "50ms", "--json")
		cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected nonzero exit on timeout, got exit 0, output: %s", string(out))
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("expected exit code 2 on timeout, got %v", err)
		}

		var env struct {
			Data struct {
				State string `json:"state"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("unmarshal watch JSON output: %v, raw: %s", err, string(out))
		}
		if env.Data.State != "timeout" {
			t.Errorf("data.state = %q, want timeout", env.Data.State)
		}
	})

	t.Run("invalid milestone exits 2 with usage error", func(t *testing.T) {
		cmd := exec.Command(binPath, "watch", "--task", taskID, "--milestone", "invalid-milestone", "--json")
		cmd.Env = append(cmd.Environ(), "G8S_DB="+dbPath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected nonzero exit for invalid milestone, got exit 0, output: %s", string(out))
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("expected exit code 2 on usage error, got %v", err)
		}
	})
}
