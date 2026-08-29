package orchestrator

import (
	"context"
	"testing"
)

func TestFanOutReceiptPropagation(t *testing.T) {
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	mustRunGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	mustRunGit(t, dir, "checkout", "-q", "-b", "main")

	worker := &stubWorker{
		name:    "agy",
		receipt: Receipt{OK: true, CommitSHA: "headsha"},
	}
	reg := NewRegistry()
	reg.Register("agy", func() Worker { return worker })

	pool, err := NewPool(PoolOptions{Repo: dir, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	plan := []TaskSpec{
		{
			TaskID:         "task-1",
			OrchestratorID: "orch-alpha",
			WorktreeID:     "custom-wt-1",
			WorkerName:     "custom-agy",
			Iter:           1,
			Task: Task{
				ID:   "task-1",
				Iter: 1,
			},
		},
		{
			TaskID:         "task-2",
			OrchestratorID: "orch-alpha",
			WorktreeID:     "custom-wt-2",
			WorkerName:     "custom-agy",
			Iter:           2,
			Task: Task{
				ID:   "task-2",
				Iter: 2,
			},
		},
		{
			TaskID:         "task-3",
			OrchestratorID: "orch-beta",
			// WorktreeID and WorkerName left empty to test fallback auto-population
			Iter: 3,
			Task: Task{
				ID:   "task-3",
				Iter: 3,
			},
		},
	}

	receipts, err := FanOut(context.Background(), plan, FanOutOptions{
		Registry: reg,
		Pool:     pool,
	})
	if err != nil {
		t.Fatalf("FanOut: %v", err)
	}

	if len(receipts) != 3 {
		t.Fatalf("got %d receipts, want 3", len(receipts))
	}

	// Task 1 assertions (explicit values)
	r1 := receipts[0]
	if r1.TaskID != "task-1" {
		t.Errorf("r1.TaskID = %q, want %q", r1.TaskID, "task-1")
	}
	if r1.OrchestratorID != "orch-alpha" {
		t.Errorf("r1.OrchestratorID = %q, want %q", r1.OrchestratorID, "orch-alpha")
	}
	if r1.WorktreeID != "custom-wt-1" {
		t.Errorf("r1.WorktreeID = %q, want %q", r1.WorktreeID, "custom-wt-1")
	}
	if r1.WorkerName != "custom-agy" {
		t.Errorf("r1.WorkerName = %q, want %q", r1.WorkerName, "custom-agy")
	}
	if r1.Iter != 1 {
		t.Errorf("r1.Iter = %d, want 1", r1.Iter)
	}

	// Task 2 assertions (explicit values)
	r2 := receipts[1]
	if r2.TaskID != "task-2" {
		t.Errorf("r2.TaskID = %q, want %q", r2.TaskID, "task-2")
	}
	if r2.OrchestratorID != "orch-alpha" {
		t.Errorf("r2.OrchestratorID = %q, want %q", r2.OrchestratorID, "orch-alpha")
	}
	if r2.WorktreeID != "custom-wt-2" {
		t.Errorf("r2.WorktreeID = %q, want %q", r2.WorktreeID, "custom-wt-2")
	}
	if r2.WorkerName != "custom-agy" {
		t.Errorf("r2.WorkerName = %q, want %q", r2.WorkerName, "custom-agy")
	}
	if r2.Iter != 2 {
		t.Errorf("r2.Iter = %d, want 2", r2.Iter)
	}

	// Task 3 assertions (fallback to pool worktree ID and worker name)
	r3 := receipts[2]
	if r3.TaskID != "task-3" {
		t.Errorf("r3.TaskID = %q, want %q", r3.TaskID, "task-3")
	}
	if r3.OrchestratorID != "orch-beta" {
		t.Errorf("r3.OrchestratorID = %q, want %q", r3.OrchestratorID, "orch-beta")
	}
	if r3.WorktreeID == "" {
		t.Errorf("r3.WorktreeID was not auto-populated from acquired worktree")
	}
	if r3.WorkerName != "agy" {
		t.Errorf("r3.WorkerName = %q, want fallback %q", r3.WorkerName, "agy")
	}
	if r3.Iter != 3 {
		t.Errorf("r3.Iter = %d, want 3", r3.Iter)
	}
}

func TestDriveReceiptLakePropagation(t *testing.T) {
	dir := t.TempDir()
	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	mustRunGit(t, dir, "commit", "--allow-empty", "-q", "-m", "init")
	mustRunGit(t, dir, "checkout", "-q", "-b", "main")

	worker := &stubWorker{
		name:    "agy",
		receipt: Receipt{OK: true, CommitSHA: "headsha"},
	}
	reg := NewRegistry()
	reg.Register("agy", func() Worker { return worker })

	pool, err := NewPool(PoolOptions{Repo: dir})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	fsm := NewFSM()
	plan := []TaskSpec{
		{
			TaskID:         "task-drive-1",
			OrchestratorID: "orch-drive",
			WorktreeID:     "wt-drive-1",
			WorkerName:     "agy",
			Iter:           5,
		},
	}

	result, err := fsm.Drive(context.Background(), plan, FanOutOptions{
		Registry: reg,
		Pool:     pool,
	})
	if err != nil {
		t.Fatalf("Drive: %v", err)
	}

	if result.FinalState != StateMerge {
		t.Fatalf("result.FinalState = %s, want MERGE", result.FinalState)
	}
	if len(result.Receipts) != 1 {
		t.Fatalf("got %d receipts, want 1", len(result.Receipts))
	}

	r := result.Receipts[0]
	if r.TaskID != "task-drive-1" {
		t.Errorf("r.TaskID = %q, want task-drive-1", r.TaskID)
	}
	if r.OrchestratorID != "orch-drive" {
		t.Errorf("r.OrchestratorID = %q, want orch-drive", r.OrchestratorID)
	}
	if r.WorktreeID != "wt-drive-1" {
		t.Errorf("r.WorktreeID = %q, want wt-drive-1", r.WorktreeID)
	}
	if r.WorkerName != "agy" {
		t.Errorf("r.WorkerName = %q, want agy", r.WorkerName)
	}
	if r.Iter != 5 {
		t.Errorf("r.Iter = %d, want 5", r.Iter)
	}
}
