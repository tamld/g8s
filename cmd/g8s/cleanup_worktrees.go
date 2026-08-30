package main

import (
	"context"
	"flag"
	"io"
	"os"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cleanup"
	"github.com/tamld/g8s/internal/cli"
)

var SubagentBranchPattern = cleanup.SubagentBranchPattern

type (
	WorktreeEntry     = cleanup.WorktreeEntry
	WorktreeCandidate = cleanup.WorktreeCandidate
	SkippedWorktree   = cleanup.SkippedWorktree
	CleanupReport     = cleanup.CleanupReport
	GitRunner         = cleanup.GitRunner
	DefaultGitRunner  = cleanup.DefaultGitRunner
	CleanupOptions    = cleanup.CleanupOptions
)

var (
	CleanupWorktrees           = cleanup.CleanupWorktrees
	parseWorktreeListPorcelain = cleanup.ParseWorktreeListPorcelain
)

func runCleanupWorktrees(args []string) {
	fs := flag.NewFlagSet("cleanup-worktrees", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	olderThanStr := fs.String("older-than", "1h", "remove worktrees older than duration (e.g. 1h, 24h, 30m)")
	dryRun := fs.Bool("dry-run", false, "show worktrees that would be removed without removing them")
	repoDir := fs.String("repo", ".", "target git repository directory")
	if err := fs.Parse(args); err != nil {
		exitUsage("cleanup-worktrees", "", *traceID, err.Error(), "", *jsonl)
	}

	olderThan, err := time.ParseDuration(*olderThanStr)
	if err != nil {
		exitRuntime("cleanup-worktrees", "", *traceID, cli.CodeInvalid, err, "Use valid duration format (e.g. 1h, 24h)", *jsonl)
	}
	if olderThan < 0 {
		exitUsage("cleanup-worktrees", "", *traceID, "--older-than duration must be non-negative", "", *jsonl)
	}

	var w io.Writer = os.Stdout
	if *jsonMode || *jsonl {
		w = io.Discard
	}
	report, err := executeCleanupWorktrees(w, &DefaultGitRunner{}, time.Now, *repoDir, olderThan, *dryRun)
	if err != nil {
		exitRuntime("cleanup-worktrees", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("cleanup_report", "cleanup-worktrees", "", report)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
		return
	}

	if *dryRun {
		pterm.Info.Printf("Dry-run complete: %d worktree(s) would be removed, %d skipped.\n", len(report.Removed), len(report.Skipped))
	} else {
		pterm.Success.Printf("Cleanup complete: %d worktree(s) removed, %d skipped.\n", len(report.Removed), len(report.Skipped))
	}
}

func executeCleanupWorktrees(w io.Writer, runner GitRunner, clock func() time.Time, repoDir string, olderThan time.Duration, dryRun bool) (*CleanupReport, error) {
	return CleanupWorktrees(context.Background(), CleanupOptions{
		RepoDir:   repoDir,
		OlderThan: olderThan,
		DryRun:    dryRun,
		Clock:     clock,
		Runner:    runner,
		Writer:    w,
	})
}
