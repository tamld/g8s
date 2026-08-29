// Package orchestrator — cleanup_hook.go implements post-terminal lifecycle
// cleanup hooks per DEBT-32 (#124).
package orchestrator

import (
	"context"
	"log"
	"time"

	"github.com/tamld/g8s/internal/cleanup"
)

// CleanupHookOptions configures the post-terminal cleanup hook.
type CleanupHookOptions struct {
	Targets         []string
	RepoDir         string
	HeartbeatDir    string
	DBPath          string
	WorktreeBaseDir string
	GracePeriod     time.Duration
	Clock           func() time.Time
	Logger          *log.Logger
	ProcessManager  cleanup.ProcessManager
	GitRunner       cleanup.CleanupGitRunner
}

// RunCleanupHook executes lifecycle cleanup in-process after an orchestration run reaches terminal state.
func RunCleanupHook(ctx context.Context, opts CleanupHookOptions) error {
	if len(opts.Targets) == 0 {
		return nil
	}

	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	cfg := cleanup.CleanupConfig{
		RepoDir:         opts.RepoDir,
		HeartbeatDir:    opts.HeartbeatDir,
		DBPath:          opts.DBPath,
		WorktreeBaseDir: opts.WorktreeBaseDir,
		Targets:         opts.Targets,
		DryRun:          false, // Auto-cleanup applies force removal
		GracePeriod:     opts.GracePeriod,
		Clock:           clock,
		GitRunner:       opts.GitRunner,
		ProcessManager:  opts.ProcessManager,
	}

	_, err := cleanup.RunCleanupSweep(ctx, cfg)
	if err != nil && opts.Logger != nil {
		opts.Logger.Printf("auto-cleanup: %v", err)
	}
	return err
}
