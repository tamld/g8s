package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/watch"
)

// Supported milestone modes for watch (#436).
const (
	MilestoneAccept         = "accept"
	MilestoneWorkerComplete = "worker-complete"

	// StateWorkerComplete indicates worker finished; awaiting supervisor acceptance (#436).
	StateWorkerComplete watch.VerdictState = "worker-complete"

	// DetailWorkerComplete is the detail message when WORKER_COMPLETED milestone is reached.
	DetailWorkerComplete = "worker finished; awaiting supervisor acceptance"
)

// TaskVerdict represents the mapped resolution of a task observation under a milestone.
type TaskVerdict struct {
	Done     bool
	State    watch.VerdictState
	Failed   []string
	Detail   string
	ExitCode int
}

// resolveTaskVerdict maps a CheckResult and milestone into a TaskVerdict.
func resolveTaskVerdict(res watch.CheckResult, milestone string) TaskVerdict {
	if !res.Done {
		return TaskVerdict{
			Done:     false,
			Detail:   res.Detail,
			ExitCode: -1,
		}
	}
	if !res.Passed {
		return TaskVerdict{
			Done:     true,
			State:    watch.StateFailed,
			Failed:   res.Failed,
			Detail:   res.Detail,
			ExitCode: 1,
		}
	}
	if milestone == MilestoneWorkerComplete && res.Detail == DetailWorkerComplete {
		return TaskVerdict{
			Done:     true,
			State:    StateWorkerComplete,
			Detail:   DetailWorkerComplete,
			ExitCode: 0,
		}
	}
	return TaskVerdict{
		Done:     true,
		State:    watch.StatePassed,
		Detail:   res.Detail,
		ExitCode: 0,
	}
}

// runWatch blocks until a watched condition reaches a terminal state (#371).
// Run it as a background process: the process-exit notification is the push
// channel that wakes the supervisor without any sleep-polling.
//
//	g8s watch --pr 356 [--interval 60s] [--timeout 30m]
//	g8s watch --task <task-id> [--interval 10s] [--milestone <accept|worker-complete>]
func runWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlagsWithDefaults(fs, true)
	_ = actor
	prFlag := fs.Int("pr", 0, "GitHub PR number: watch its CI checks until terminal")
	taskFlag := fs.String("task", "", "g8s task id: watch its state until terminal")
	interval := fs.Duration("interval", 60*time.Second, "poll interval")
	timeout := fs.Duration("timeout", 30*time.Minute, "give up after this long (verdict=timeout)")
	milestone := fs.String("milestone", MilestoneAccept, "watch milestone: accept (default) or worker-complete")
	if err := fs.Parse(args); err != nil {
		exitUsage("watch", "", *traceID, err.Error(), "", *jsonl)
	}
	if (*prFlag == 0) == (*taskFlag == "") {
		exitUsage("watch", "", *traceID, "exactly one of --pr or --task is required", "g8s watch --pr 356 --interval 60s", *jsonl)
		return
	}
	if *milestone != MilestoneAccept && *milestone != MilestoneWorkerComplete {
		exitUsage("watch", "", *traceID, fmt.Sprintf("invalid --milestone %q (must be %s or %s)", *milestone, MilestoneAccept, MilestoneWorkerComplete), "g8s watch --task <task-id> --milestone worker-complete", *jsonl)
		return
	}

	var target string
	var checker watch.Checker
	switch {
	case *prFlag != 0:
		target = fmt.Sprintf("pr/%d", *prFlag)
		pr := *prFlag
		checker = func(ctx context.Context) (watch.CheckResult, error) {
			return checkPR(ctx, pr)
		}
	default:
		target = "task/" + *taskFlag
		taskID := *taskFlag
		dbPath, perr := databasePath()
		if perr != nil {
			exitRuntime("watch", "", *traceID, cli.CodeIO, perr, "", *jsonl)
			return
		}
		store, serr := controlplane.NewControlPlane(dbPath, nil)
		if serr != nil {
			exitRuntime("watch", "", *traceID, cli.CodeRuntime, serr, "", *jsonl)
			return
		}
		defer store.Close()
		checker = func(ctx context.Context) (watch.CheckResult, error) {
			return checkTask(ctx, store, taskID, *milestone)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := watch.Watcher{Checker: checker, Interval: *interval, Timeout: *timeout}
	verdict, err := w.Run(ctx)
	if err != nil {
		exitRuntime("watch", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
		return
	}

	if *milestone == MilestoneWorkerComplete && verdict.Detail == DetailWorkerComplete {
		verdict.State = StateWorkerComplete
	}

	data := map[string]any{
		"target":  target,
		"state":   string(verdict.State),
		"failed":  verdict.Failed,
		"detail":  verdict.Detail,
		"elapsed": verdict.Elapsed.String(),
		"polls":   verdict.Polls,
	}
	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("watch_verdict", "watch", "", data)
		env.TraceID = *traceID
		_ = cli.WriteResponse(os.Stdout, env, *jsonl)
	} else {
		pterm.Info.Printf("watch %s: %s (%s, %d polls)\n", target, verdict.State, verdict.Elapsed.Round(time.Second), verdict.Polls)
		if len(verdict.Failed) > 0 {
			pterm.Warning.Println("failed: " + strings.Join(verdict.Failed, ", "))
		}
	}

	switch verdict.State {
	case watch.StatePassed, StateWorkerComplete:
		return
	case watch.StateFailed:
		os.Exit(1)
	default:
		os.Exit(2)
	}
}

// checkPR observes one GitHub PR's CI checks via gh.
func checkPR(ctx context.Context, pr int) (watch.CheckResult, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "checks", fmt.Sprint(pr),
		"--repo", "tamld/g8s", "--json", "name,state")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// gh exits non-zero while checks are pending — treat as not-done.
		if strings.Contains(string(out), "no checks") || strings.Contains(err.Error(), "exit status 8") {
			return watch.CheckResult{Done: false}, nil
		}
		return watch.CheckResult{}, fmt.Errorf("gh pr checks: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	var checks []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if jerr := json.Unmarshal(out, &checks); jerr != nil {
		return watch.CheckResult{}, fmt.Errorf("decode gh pr checks: %w", jerr)
	}
	if len(checks) == 0 {
		return watch.CheckResult{Done: false}, nil
	}
	res := watch.CheckResult{Done: true, Passed: true}
	for _, c := range checks {
		switch c.State {
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			// healthy
		case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
			res.Failed = append(res.Failed, c.Name+" ("+c.State+")")
			res.Passed = false
		default:
			res.Done = false // still pending
		}
	}
	res.Detail = fmt.Sprintf("%d checks observed", len(checks))
	return res, nil
}

// checkTask observes one g8s task's state via the control plane (#436).
func checkTask(ctx context.Context, store *controlplane.Store, taskID string, milestone ...string) (watch.CheckResult, error) {
	m := MilestoneAccept
	if len(milestone) > 0 && milestone[0] != "" {
		m = milestone[0]
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return watch.CheckResult{}, fmt.Errorf("get task: %w", err)
	}
	if task == nil {
		return watch.CheckResult{}, fmt.Errorf("task %s not found", taskID)
	}
	switch task.State {
	case "SUCCEEDED":
		return watch.CheckResult{Done: true, Passed: true, Detail: "task " + taskID + " succeeded"}, nil
	case "WORKER_COMPLETED":
		if m == MilestoneWorkerComplete {
			return watch.CheckResult{
				Done:   true,
				Passed: true,
				Detail: DetailWorkerComplete,
			}, nil
		}
		return watch.CheckResult{Done: false, Detail: "state " + task.State}, nil
	case "FAILED", "CANCELLED", "TIMED_OUT":
		return watch.CheckResult{Done: true, Passed: false, Failed: []string{task.State}, Detail: "task " + taskID + " ended " + task.State}, nil
	default:
		return watch.CheckResult{Done: false, Detail: "state " + task.State}, nil
	}
}
