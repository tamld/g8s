package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cleanup"
)

// Supported cleanup target identifiers aliased from internal/cleanup.
const (
	TargetGhostProcess   = cleanup.TargetGhostProcess
	TargetOrphanWT       = cleanup.TargetOrphanWT
	TargetOrphanDir      = cleanup.TargetOrphanDir
	TargetOrphanBranch   = cleanup.TargetOrphanBranch
	TargetStaleReceipt   = cleanup.TargetStaleReceipt
	TargetClosedPRBranch = cleanup.TargetClosedPRBranch
	TargetOldTag         = cleanup.TargetOldTag
)

// AllCleanupTargets lists all available cleanup target flags.
var AllCleanupTargets = cleanup.AllCleanupTargets

type (
	CleanupItem             = cleanup.CleanupItem
	FullCleanupReport       = cleanup.FullCleanupReport
	ProcessInfo             = cleanup.ProcessInfo
	TagInfo                 = cleanup.TagInfo
	HeartbeatData           = cleanup.HeartbeatData
	ProcessManager          = cleanup.ProcessManager
	CleanupGitRunner        = cleanup.CleanupGitRunner
	DefaultProcessManager   = cleanup.DefaultProcessManager
	DefaultCleanupGitRunner = cleanup.DefaultCleanupGitRunner
	CleanupConfig           = cleanup.CleanupConfig
)

var (
	RunCleanupSweep = cleanup.RunCleanupSweep
	loadHeartbeats  = cleanup.LoadHeartbeats
)

func runCleanup(args []string) {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	dryRunFlag := fs.Bool("dry-run", true, "show resources that would be cleaned up without removing them")
	forceFlag := fs.Bool("force", false, "apply cleanup and remove detected ghost/orphan resources")
	jsonFlag := fs.Bool("json", false, "output cleanup report as machine-readable JSON")
	targetFlag := fs.String("target", "", "comma-separated targets (ghost-process,orphan-wt,orphan-dir,orphan-branch,stale-receipt,closed-pr-branch,old-tag)")
	repoDir := fs.String("repo", ".", "target git repository directory")
	gracePeriod := fs.Duration("grace-period", 10*time.Second, "grace period before SIGKILL for ghost processes")
	failIf(fs.Parse(args))

	dryRun := *dryRunFlag
	if *forceFlag {
		dryRun = false
	}

	var targets []string
	if *targetFlag != "" {
		for _, t := range strings.Split(*targetFlag, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				targets = append(targets, t)
			}
		}
	}

	dbPath, _ := databasePath()

	cfg := CleanupConfig{
		RepoDir:        *repoDir,
		HeartbeatDir:   filepath.Join(*repoDir, ".heartbeat"),
		DBPath:         dbPath,
		Targets:        targets,
		DryRun:         dryRun,
		GracePeriod:    *gracePeriod,
		Clock:          time.Now,
		GitRunner:      &DefaultCleanupGitRunner{},
		ProcessManager: &DefaultProcessManager{},
		Writer:         os.Stdout,
	}

	report, err := RunCleanupSweep(context.Background(), cfg)
	failIf(err)

	if *jsonFlag {
		out, err := json.MarshalIndent(report, "", "  ")
		failIf(err)
		fmt.Println(string(out))
		return
	}

	renderCleanupReport(report)
}

func renderCleanupReport(report *FullCleanupReport) {
	pterm.DefaultHeader.WithFullWidth().Println("g8s Lifecycle Cleanup Sweep")

	if report.DryRun {
		pterm.Info.Println("Mode: DRY-RUN (no changes applied, use --force to execute)")
	} else {
		pterm.Success.Println("Mode: APPLY (active cleanup applied)")
	}
	fmt.Println()

	if len(report.Items) == 0 {
		pterm.Success.Println("System is clean! No ghost processes or orphan resources detected.")
		return
	}

	var td pterm.TableData
	td = append(td, []string{"Target", "Resource ID", "Action", "Detail"})
	for _, item := range report.Items {
		actionStr := item.Action
		switch {
		case strings.HasPrefix(actionStr, "would_"):
			actionStr = pterm.Yellow(actionStr)
		case actionStr == "killed" || actionStr == "removed" || actionStr == "deleted" || actionStr == "pruned":
			actionStr = pterm.Green(actionStr)
		default:
			actionStr = pterm.Red(actionStr)
		}
		td = append(td, []string{item.Target, item.ID, actionStr, item.Detail})
	}
	_ = pterm.DefaultTable.WithHasHeader().WithData(td).Render()

	fmt.Println()
	pterm.DefaultSection.Println("Summary:")
	for target, count := range report.Summary {
		fmt.Printf("  • %s: %d item(s)\n", target, count)
	}
}
