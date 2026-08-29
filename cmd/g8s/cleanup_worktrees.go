package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cleanup"
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
	isMainWorktree             = cleanup.IsMainWorktree
	checkWorktreeAge           = cleanup.CheckWorktreeAge
	hasUncommittedChanges      = cleanup.HasUncommittedChanges
)

func runCleanupWorktrees(args []string) {
	fs := flag.NewFlagSet("cleanup-worktrees", flag.ExitOnError)
	olderThanStr := fs.String("older-than", "1h", "remove worktrees older than duration (e.g. 1h, 24h, 30m)")
	dryRun := fs.Bool("dry-run", false, "show worktrees that would be removed without removing them")
	jsonFlag := fs.Bool("json", false, "output report as JSON")
	repoDir := fs.String("repo", ".", "target git repository directory")
	failIf(fs.Parse(args))

	olderThan, err := time.ParseDuration(*olderThanStr)
	failIf(err)
	if olderThan < 0 {
		failIf(fmt.Errorf("--older-than duration must be non-negative"))
	}

	var w io.Writer = os.Stdout
	if *jsonFlag {
		w = io.Discard
	}
	report, err := executeCleanupWorktrees(w, &DefaultGitRunner{}, time.Now, *repoDir, olderThan, *dryRun)
	failIf(err)

	if *jsonFlag {
		out, err := json.MarshalIndent(report, "", "  ")
		failIf(err)
		fmt.Println(string(out))
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
