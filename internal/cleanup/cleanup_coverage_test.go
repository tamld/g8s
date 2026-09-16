package cleanup

import (
	"context"
	"testing"
	"time"
    "syscall"
)

func TestMockProcessManager_FindGhostProcesses(t *testing.T) {
	pm := &MockProcessManager{}
	pm.mu.Lock()
	pm.processes = []ProcessInfo{
		{PID: 1, Binary: "test"},
	}
	pm.mu.Unlock()

	ghosts, err := pm.FindGhostProcesses(context.Background(), "", 0, time.Now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ghosts) != 1 {
		t.Errorf("expected 1 ghost, got %d", len(ghosts))
	}
}

func TestDefaultProcessManager_KillAndAlive(t *testing.T) {
	lister := &mockCleanupProcessLister{}
	pm := &DefaultProcessManager{Lister: lister}

	_ = pm.KillProcess(1, syscall.SIGTERM)
	_ = pm.KillProcess(1, syscall.SIGKILL)
	_ = pm.IsProcessAlive(1)
}

func TestDefaultCleanupGitRunner(t *testing.T) {
	runner := &DefaultCleanupGitRunner{}
	ctx := context.Background()

	_, _ = runner.WorktreeListPorcelain(ctx, ".")
	_, _ = runner.WorktreePrune(ctx, ".")
	_ = runner.WorktreeRemove(ctx, ".", "somepath")
	_, _ = runner.MergedBranches(ctx, ".")
	_, _ = runner.RemoteBranches(ctx, ".")
	_ = runner.DeleteBranch(ctx, ".", "somebranch", false)
	_ = runner.DeleteBranch(ctx, ".", "somebranch", true)
	_, _ = runner.ClosedPRBranches(ctx, ".")
	_, _ = runner.LocalTags(ctx, ".")
	_, _ = runner.RemoteTags(ctx, ".")
	_ = runner.DeleteTag(ctx, ".", "sometag")
}

func TestDefaultGitRunner(t *testing.T) {
	ctx := context.Background()

	runner := &DefaultGitRunner{}

	_, _ = runner.WorktreeListPorcelain(ctx, ".")
	_ = runner.WorktreeRemove(ctx, ".", "somepath")
	_, _ = runner.WorktreePrune(ctx, ".")
	_, _ = runner.StatusPorcelain(ctx, ".")
}
