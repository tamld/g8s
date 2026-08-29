package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cleanup"
	"github.com/tamld/g8s/internal/cli"
	_ "modernc.org/sqlite"
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
	fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, false)
	_ = actor
	dryRunFlag := fs.Bool("dry-run", true, "show resources that would be cleaned up without removing them")
	forceFlag := fs.Bool("force", false, "apply cleanup and remove detected ghost/orphan resources")
	forceMissingFlag := fs.Bool("force-missing", false, "allow killing ghost processes that have no heartbeat file (requires confirmation)")
	yesFlag := fs.Bool("yes", false, "skip interactive confirmation prompt for --force-missing")
	targetFlag := fs.String("target", "", "comma-separated targets (ghost-process,orphan-wt,orphan-dir,orphan-branch,stale-receipt,closed-pr-branch,old-tag)")
	repoDir := fs.String("repo", ".", "target git repository directory")
	gracePeriod := fs.Duration("grace-period", 10*time.Second, "grace period before SIGKILL for ghost processes")
	if err := fs.Parse(args); err != nil {
		exitUsage("cleanup", "", *traceID, err.Error(), "", *jsonl)
	}

	dryRun := *dryRunFlag
	if *forceFlag {
		dryRun = false
	}

	if *forceFlag && *forceMissingFlag && !dryRun && !*yesFlag {
		if !confirmForceMissing(os.Stdin, os.Stdout) {
			pterm.Warning.Println("Aborted --force-missing ghost process termination.")
			return
		}
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
		ForceMissing:   *forceMissingFlag,
		GracePeriod:    *gracePeriod,
		Clock:          time.Now,
		GitRunner:      &DefaultCleanupGitRunner{},
		ProcessManager: &DefaultProcessManager{},
		Writer:         os.Stdout,
	}

	report, err := RunCleanupSweep(context.Background(), cfg)
	if err != nil {
		exitRuntime("cleanup", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("cleanup_report", "cleanup", "", report)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
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

func confirmForceMissing(r io.Reader, w io.Writer) bool {
	if r == nil {
		r = os.Stdin
	}
	if w == nil {
		w = os.Stdout
	}
	pterm.Warning.WithWriter(w).Println("WARNING: Terminating processes without heartbeat files may kill foreign or unrelated binaries.")
	_, _ = fmt.Fprint(w, "Are you sure you want to proceed with terminating processes with no heartbeats? [y/N]: ")
	var resp string
	_, err := fmt.Fscanln(r, &resp)
	if err != nil {
		return false
	}
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes"
}
