package orchestrator

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cleanup"
)

type mockHookProcessManager struct {
	ghosts     []cleanup.ProcessInfo
	killedWith map[int][]syscall.Signal
}

func (m *mockHookProcessManager) FindGhostProcesses(ctx context.Context, heartbeatDir string, maxAge time.Duration, clock func() time.Time) ([]cleanup.ProcessInfo, error) {
	return m.ghosts, nil
}

func (m *mockHookProcessManager) KillProcess(pid int, sig syscall.Signal) error {
	if m.killedWith == nil {
		m.killedWith = make(map[int][]syscall.Signal)
	}
	m.killedWith[pid] = append(m.killedWith[pid], sig)
	return nil
}

func (m *mockHookProcessManager) IsProcessAlive(pid int) bool {
	return false
}

func TestRunCleanupHook(t *testing.T) {
	t.Run("empty targets is a no-op", func(t *testing.T) {
		err := RunCleanupHook(context.Background(), CleanupHookOptions{
			Targets: []string{},
		})
		if err != nil {
			t.Fatalf("expected nil error for empty targets, got %v", err)
		}
	})

	t.Run("executes targets with process manager", func(t *testing.T) {
		pm := &mockHookProcessManager{
			ghosts: []cleanup.ProcessInfo{
				{PID: 1234, Binary: "agy", CommandLine: "agy", Reason: "no live heartbeat"},
			},
		}

		now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
		err := RunCleanupHook(context.Background(), CleanupHookOptions{
			Targets:        []string{cleanup.TargetGhostProcess},
			ProcessManager: pm,
			Clock:          func() time.Time { return now },
			GracePeriod:    10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("unexpected hook error: %v", err)
		}

		if len(pm.killedWith[1234]) == 0 {
			t.Errorf("expected process 1234 to be killed by hook")
		}
	})
}
