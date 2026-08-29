package supervisor

import (
	"bytes"
	"context"
	"errors"
	"log"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cleanup"
)

type mockSupervisorProcessManager struct {
	mu         sync.Mutex
	ghosts     []cleanup.ProcessInfo
	killedWith map[int][]syscall.Signal
}

func (m *mockSupervisorProcessManager) FindGhostProcesses(ctx context.Context, heartbeatDir string, maxAge time.Duration, clock func() time.Time) ([]cleanup.ProcessInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ghosts, nil
}

func (m *mockSupervisorProcessManager) KillProcess(pid int, sig syscall.Signal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.killedWith == nil {
		m.killedWith = make(map[int][]syscall.Signal)
	}
	m.killedWith[pid] = append(m.killedWith[pid], sig)
	return nil
}

func (m *mockSupervisorProcessManager) IsProcessAlive(pid int) bool {
	return false
}

type mockSupervisorGitRunner struct {
	mu          sync.Mutex
	pruneCalled bool
	pruneErr    error
}

func (m *mockSupervisorGitRunner) WorktreeListPorcelain(ctx context.Context, repoDir string) (string, error) {
	return `worktree /path/to/main
HEAD 1111111111111111111111111111111111111111
branch refs/heads/main

worktree /path/to/orphan
HEAD 2222222222222222222222222222222222222222
branch refs/heads/agy/sup-orphan-sub-1
prunable gitdir points to non-existent location
`, nil
}

func (m *mockSupervisorGitRunner) WorktreePrune(ctx context.Context, repoDir string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneCalled = true
	return "pruned", m.pruneErr
}

func (m *mockSupervisorGitRunner) WorktreeRemove(ctx context.Context, repoDir, wtPath string) error {
	return nil
}
func (m *mockSupervisorGitRunner) MergedBranches(ctx context.Context, repoDir string) ([]string, error) {
	return nil, nil
}
func (m *mockSupervisorGitRunner) RemoteBranches(ctx context.Context, repoDir string) ([]string, error) {
	return nil, nil
}
func (m *mockSupervisorGitRunner) DeleteBranch(ctx context.Context, repoDir, branch string, force bool) error {
	return nil
}
func (m *mockSupervisorGitRunner) ClosedPRBranches(ctx context.Context, repoDir string) ([]string, error) {
	return nil, nil
}
func (m *mockSupervisorGitRunner) LocalTags(ctx context.Context, repoDir string) ([]cleanup.TagInfo, error) {
	return nil, nil
}
func (m *mockSupervisorGitRunner) RemoteTags(ctx context.Context, repoDir string) ([]string, error) {
	return nil, nil
}
func (m *mockSupervisorGitRunner) DeleteTag(ctx context.Context, repoDir, tag string) error {
	return nil
}

func validTestRequest(desc string) RunRequest {
	return RunRequest{
		TaskDescription: desc,
		Role:            "collector",
		Permission:      "read_only",
		Model:           "gemini-3.7-flash-high",
		AddDirs:         []string{"/tmp"},
		SelfTestMode:    true,
	}
}

func TestSupervisorAutoCleanup_OnSuccess(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(goodReceipt("task-1"))
	reviewer := NewStubReviewer()

	gitRunner := &mockSupervisorGitRunner{}

	sup := NewSelfTestSupervisor(store, worker, reviewer)
	sup.Config.AutoCleanup = true
	sup.Config.GitRunner = gitRunner

	res, err := sup.Run(context.Background(), validTestRequest("build feature"))
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	if res.Outcome != RunSucceeded {
		t.Fatalf("expected RunSucceeded, got %v", res.Outcome)
	}

	if !gitRunner.pruneCalled {
		t.Errorf("expected auto-cleanup to prune orphan worktrees on success")
	}
}

func TestSupervisorAutoCleanup_OnEscalateWithGhostProcesses(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(failingReceipt("task-1"))
	reviewer := NewStubReviewer()

	gitRunner := &mockSupervisorGitRunner{}
	procMgr := &mockSupervisorProcessManager{
		ghosts: []cleanup.ProcessInfo{
			{PID: 9999, Binary: "agy", CommandLine: "agy stale", Reason: "dead"},
		},
	}

	sup := NewSelfTestSupervisor(store, worker, reviewer)
	sup.Config.MaxAttemptsPerApproach = 1
	sup.Config.MaxApproaches = 1
	sup.Config.AutoCleanup = true
	sup.Config.CleanupOnEscalate = true
	sup.Config.GitRunner = gitRunner
	sup.Config.ProcessManager = procMgr

	res, err := sup.Run(context.Background(), validTestRequest("unsolvable task"))
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	if res.Outcome != RunEscalated {
		t.Fatalf("expected RunEscalated, got %v", res.Outcome)
	}

	if !gitRunner.pruneCalled {
		t.Errorf("expected auto-cleanup to prune orphan worktrees on escalation")
	}
	if len(procMgr.killedWith[9999]) == 0 {
		t.Errorf("expected ghost process 9999 to be killed on escalation when CleanupOnEscalate is true")
	}
}

func TestSupervisorAutoCleanup_Disabled(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(goodReceipt("task-1"))
	reviewer := NewStubReviewer()

	gitRunner := &mockSupervisorGitRunner{}

	sup := NewSelfTestSupervisor(store, worker, reviewer)
	sup.Config.AutoCleanup = false
	sup.Config.GitRunner = gitRunner

	res, err := sup.Run(context.Background(), validTestRequest("build feature"))
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	if res.Outcome != RunSucceeded {
		t.Fatalf("expected RunSucceeded, got %v", res.Outcome)
	}

	if gitRunner.pruneCalled {
		t.Errorf("expected auto-cleanup NOT to run when AutoCleanup=false")
	}
}

func TestSupervisorAutoCleanup_ErrorDoesNotBlockRun(t *testing.T) {
	store := NewStubPersistence()
	worker := newStubWorker(goodReceipt("task-1"))
	reviewer := NewStubReviewer()

	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)

	gitRunner := &mockSupervisorGitRunner{
		pruneErr: errors.New("disk I/O error"),
	}

	sup := NewSelfTestSupervisor(store, worker, reviewer)
	sup.Config.AutoCleanup = true
	sup.Config.GitRunner = gitRunner
	sup.Logger = logger

	res, err := sup.Run(context.Background(), validTestRequest("build feature"))
	if err != nil {
		t.Fatalf("run should succeed despite cleanup error: %v", err)
	}
	if res.Outcome != RunSucceeded {
		t.Fatalf("expected RunSucceeded, got %v", res.Outcome)
	}
}
