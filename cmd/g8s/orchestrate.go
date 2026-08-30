// Package main — orchestrate.go runs the supervisor self-test (Concern A)
// and natural-language intent orchestration (DELTA-18) against agy workers.
// The flag-parsing surface mirrors the runSubmit / runResume conventions so
// operators and automation (AIC) can drive deterministic end-to-end fix loops
// and fan-out collections from the terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/supervisor"
)

// subTaskEnvelope represents the execution status and evidence for one
// sub-task derived from intent.
type subTaskEnvelope struct {
	TaskID          string   `json:"task_id,omitempty"`
	Task            string   `json:"task"`
	Status          string   `json:"status"`
	CommitSHA       string   `json:"commit_sha,omitempty"`
	FilesModified   []string `json:"files_modified,omitempty"`
	DurationSeconds float64  `json:"duration_seconds,omitempty"`
}

// receiptSummary aggregates metrics and file modifications across all sub-tasks.
type receiptSummary struct {
	TotalRuns            int      `json:"total_runs"`
	Succeeded            int      `json:"succeeded"`
	Failed               int      `json:"failed"`
	TotalDurationSeconds float64  `json:"total_duration_seconds"`
	FilesModified        []string `json:"files_modified,omitempty"`
}

// orchestrateResultJSON is the public machine-readable contract produced by
// orchestrate and self-test modes. The `approaches_tried` and `total_attempts`
// keys are load-bearing: the supervisor regression suite greps for them.
type orchestrateResultJSON struct {
	SupervisorTaskID string                 `json:"supervisor_task_id"`
	Outcome          string                 `json:"outcome"`
	Verdict          string                 `json:"verdict"`
	ApproachesTried  int                    `json:"approaches_tried"`
	TotalAttempts    int                    `json:"total_attempts"`
	Escalated        bool                   `json:"escalated"`
	Escalation       *supervisor.Escalation `json:"escalation,omitempty"`
	SubTasks         []subTaskEnvelope      `json:"sub_tasks,omitempty"`
	ReceiptSummary   *receiptSummary        `json:"receipt_summary,omitempty"`
}

// parseIntentSubtasks splits a natural language intent string into sub-tasks
// by splitting on commas and newlines, trimming whitespace, and filtering empty items.
func parseIntentSubtasks(intent string) []string {
	var tasks []string
	lines := strings.Split(intent, "\n")
	for _, line := range lines {
		parts := strings.Split(line, ",")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				tasks = append(tasks, trimmed)
			}
		}
	}
	return tasks
}

// orchestratorWorkerCtor is the worker factory seam for orchestrate runs.
var orchestratorWorkerCtor = func() orchestrator.Worker { return orchestrator.NewAgyWorker() }

// orchestrateOptions carries dependencies and configuration for orchestration runs.
type orchestrateOptions struct {
	Store         supervisor.Persistence
	Worker        orchestrator.Worker
	Reviewer      supervisor.Reviewer
	Pool          *orchestrator.Pool
	Registry      *orchestrator.Registry
	MaxAttempts   int
	MaxApproaches int
	Timeout       time.Duration
	Model         string
	Role          string
	Permission    string
	AddDirs       []string
	ParentTaskID  *string
	SelfTest      bool
	AutoCleanup   bool
}

// executeOrchestration drives the supervisor fix loop and sub-task fan-out
// according to the provided intent.
func executeOrchestration(ctx context.Context, intent string, opts orchestrateOptions) (orchestrateResultJSON, error) {
	subtasks := parseIntentSubtasks(intent)
	if len(subtasks) == 0 {
		return orchestrateResultJSON{}, errors.New("orchestrate: empty intent")
	}

	worker := opts.Worker
	if worker == nil {
		worker = orchestrator.NewAgyWorker()
	}
	reviewer := opts.Reviewer
	if reviewer == nil {
		reviewer = supervisor.NewStubReviewer()
	}

	sup := supervisor.NewSelfTestSupervisor(opts.Store, worker, reviewer)
	sup.Config.MaxAttemptsPerApproach = opts.MaxAttempts
	sup.Config.MaxApproaches = opts.MaxApproaches
	sup.Config.SelfTestMode = true
	sup.Config.AutoCleanup = opts.AutoCleanup

	role := opts.Role
	if role == "" {
		role = "collector"
	}
	perm := opts.Permission
	if perm == "" {
		perm = "read_only"
	}
	model := opts.Model
	if model == "" {
		model = "gemini-3.7-flash-high"
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	addDirs := opts.AddDirs
	if len(addDirs) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return orchestrateResultJSON{}, fmt.Errorf("getwd: %w", err)
		}
		addDirs = []string{cwd}
	}

	req := supervisor.RunRequest{
		TaskDescription: intent,
		Role:            role,
		Permission:      perm,
		Model:           model,
		AddDirs:         addDirs,
		SelfTestMode:    true,
		Timeout:         timeout,
		ParentTaskID:    opts.ParentTaskID,
	}

	res, runErr := sup.Run(ctx, req)
	if runErr != nil {
		return orchestrateResultJSON{}, fmt.Errorf("supervisor run: %w", runErr)
	}

	plan := make([]orchestrator.TaskSpec, len(subtasks))
	for i, taskPrompt := range subtasks {
		taskID := fmt.Sprintf("%s-sub-%d", res.SupervisorTaskID, i+1)
		plan[i] = orchestrator.TaskSpec{
			TaskID:         taskID,
			OrchestratorID: res.SupervisorTaskID,
			WorkerName:     worker.Name(),
			Iter:           0,
			Task: orchestrator.Task{
				ID:         taskID,
				ParentID:   res.SupervisorTaskID,
				Prompt:     taskPrompt,
				Role:       "collector",
				Permission: "read_only",
				Model:      model,
				Timeout:    timeout,
			},
		}
	}

	registry := opts.Registry
	if registry == nil && opts.Pool != nil {
		reg := orchestrator.NewRegistry()
		_ = reg.Register(worker.Name(), func() orchestrator.Worker { return worker })
		registry = reg
	}

	var receipts []orchestrator.Receipt
	if registry != nil && opts.Pool != nil {
		fanReceipts, fanErr := orchestrator.FanOut(ctx, plan, orchestrator.FanOutOptions{
			Registry:    registry,
			Pool:        opts.Pool,
			MaxParallel: len(plan),
		})
		if fanErr == nil {
			receipts = fanReceipts
		}
	}

	// Fallback to direct worker dispatch when Pool is not available (e.g. self-test / stub / non-git)
	if len(receipts) == 0 {
		receipts = make([]orchestrator.Receipt, len(plan))
		for i, spec := range plan {
			handle, spawnErr := worker.Spawn(ctx, spec.Task)
			if spawnErr != nil {
				receipts[i] = orchestrator.Receipt{
					TaskID:         spec.TaskID,
					OrchestratorID: spec.OrchestratorID,
					WorkerName:     worker.Name(),
					OK:             false,
					Stderr:         spawnErr.Error(),
				}
				continue
			}
			r, waitErr := handle.Wait(ctx)
			if waitErr != nil && r.TaskID == "" {
				r.TaskID = spec.TaskID
				r.OK = false
				r.Stderr = waitErr.Error()
			}
			if r.OrchestratorID == "" {
				r.OrchestratorID = spec.OrchestratorID
			}
			if r.TaskID == "" {
				r.TaskID = spec.TaskID
			}
			receipts[i] = r
		}
	}

	subTaskResults := make([]subTaskEnvelope, len(plan))
	summary := receiptSummary{
		TotalRuns: len(plan),
	}
	for i, spec := range plan {
		subTaskResults[i] = subTaskEnvelope{
			TaskID: spec.TaskID,
			Task:   spec.Task.Prompt,
			Status: "succeeded",
		}
		if i < len(receipts) {
			r := receipts[i]
			subTaskResults[i].CommitSHA = r.CommitSHA
			subTaskResults[i].FilesModified = r.FilesModified
			subTaskResults[i].DurationSeconds = r.DurationSeconds
			summary.TotalDurationSeconds += r.DurationSeconds
			if len(r.FilesModified) > 0 {
				summary.FilesModified = append(summary.FilesModified, r.FilesModified...)
			}
			if r.OK {
				subTaskResults[i].Status = "succeeded"
				summary.Succeeded++
			} else {
				subTaskResults[i].Status = "failed"
				summary.Failed++
			}
		} else {
			subTaskResults[i].Status = "failed"
			summary.Failed++
		}
	}

	out := orchestrateResultJSON{
		SupervisorTaskID: res.SupervisorTaskID,
		Outcome:          res.Outcome.String(),
		Verdict:          res.Verdict,
		ApproachesTried:  res.ApproachCount,
		TotalAttempts:    res.AttemptCount,
		Escalated:        res.Escalated,
		Escalation:       res.Escalation,
		SubTasks:         subTaskResults,
		ReceiptSummary:   &summary,
	}

	return out, nil
}

// runOrchestrate is the entry point invoked by the top-level dispatcher.
// When --self-test is set it drives an in-process supervisor loop with the
// real AgyWorker so the operator can dogfood the end-to-end fix cycle
// without a separate worker process. When --from-intent or --from-file is set,
// it parses the natural language intent into collector sub-tasks and runs
// them via FanOut.
func runOrchestrate(args []string) {
	fs := flag.NewFlagSet("orchestrate", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	selfTest := fs.Bool("self-test", false, "run a self-contained supervisor loop against the real agy worker")
	briefFile := fs.String("brief-file", "", "path to brief markdown file to issue and dispatch")
	dispatchID := fs.String("dispatch", "", "stored brief ID to re-issue and dispatch")
	issuedBy := fs.String("issued-by", "", "issuer identity for brief (defaults to --actor)")
	ttlStr := fs.String("ttl", "2h", "time-to-live duration for brief (e.g. 2h, 30m)")
	title := fs.String("title", "", "optional title for brief (overrides title in file)")
	dod := fs.String("dod", "", "optional DoD for brief (overrides DoD in file)")
	fromIntent := fs.String("from-intent", "", "free-text natural language intent")
	fromFile := fs.String("from-file", "", "path to file containing natural language intent")
	model := fs.String("model", "gemini-3.7-flash-high", "target worker model")
	role := fs.String("role", "collector", "worker role contract")
	permission := fs.String("permission", "read_only", "permission profile")
	taskDesc := fs.String("task", "scan ./src for MCP server candidate implementations", "task description handed to the supervisor")
	parentID := fs.String("parent-task-id", "", "optional parent supervisor task id")
	maxAttempts := fs.Int("max-attempts", 3, "attempts per approach")
	maxApproaches := fs.Int("max-approaches", 3, "approach budget before escalation")
	timeout := fs.Duration("timeout", 5*time.Minute, "per-attempt execution window")
	autoCleanup := fs.Bool("auto-cleanup", true, "automatically clean up orphan worktrees and dirs after run")
	var addDirs pathFlags
	fs.Var(&addDirs, "add-dir", "additional allowed directory (repeatable, defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		exitUsage("orchestrate", "", *traceID, err.Error(), "", *jsonl)
	}

	var jsonExplicit bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "json" || f.Name == "jsonl" {
			jsonExplicit = true
		}
	})

	if !*selfTest && *fromIntent == "" && *fromFile == "" && *briefFile == "" && *dispatchID == "" {
		exitUsage("orchestrate", "", *traceID, "usage: g8s orchestrate [--brief-file <path> | --dispatch <id> | --from-intent <text> | --from-file <path> | --self-test] [--task <text>] [--json] [--add-dir <path> ...]", "", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("orchestrate", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("orchestrate", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	effectiveIssuer := *issuedBy
	if effectiveIssuer == "" {
		effectiveIssuer = *actor
	}
	if effectiveIssuer == "" {
		effectiveIssuer = "sisyphus"
	}

	if *briefFile != "" {
		_, err := executeOrchestrateBriefFile(os.Stdout, store, *briefFile, effectiveIssuer, *ttlStr, *title, *dod, jsonExplicit && (*jsonMode || *jsonl), *traceID, *jsonl)
		if err != nil {
			exitRuntime("orchestrate", "brief-file", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		return
	}

	if *dispatchID != "" {
		_, err := executeOrchestrateDispatch(os.Stdout, store, *dispatchID, effectiveIssuer, *ttlStr, jsonExplicit && (*jsonMode || *jsonl), *traceID, *jsonl)
		if err != nil {
			exitRuntime("orchestrate", "dispatch", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		return
	}

	var intentText string
	if *fromFile != "" {
		raw, err := os.ReadFile(*fromFile)
		if err != nil {
			exitRuntime("orchestrate", "from-file", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		intentText = strings.TrimSpace(string(raw))
		if intentText == "" {
			exitUsage("orchestrate", "from-file", *traceID, "usage: g8s orchestrate: empty intent from file", "", *jsonl)
		}
	} else if *fromIntent != "" {
		intentText = strings.TrimSpace(*fromIntent)
		if intentText == "" {
			exitUsage("orchestrate", "from-intent", *traceID, "usage: g8s orchestrate: empty intent", "", *jsonl)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		exitRuntime("orchestrate", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	dirs := []string(addDirs)
	if len(dirs) == 0 {
		dirs = []string{cwd}
	}

	var parentIDPtr *string
	if *parentID != "" {
		parentIDPtr = parentID
	}

	var out orchestrateResultJSON
	if intentText != "" {
		var pool *orchestrator.Pool
		p, poolErr := orchestrator.NewPool(orchestrator.PoolOptions{Repo: cwd})
		if poolErr == nil {
			pool = p
		}
		worker := orchestratorWorkerCtor()
		reg := orchestrator.NewRegistry()
		_ = reg.Register(worker.Name(), func() orchestrator.Worker { return worker })

		opts := orchestrateOptions{
			Store:         store,
			Worker:        worker,
			Reviewer:      supervisor.NewStubReviewer(),
			Pool:          pool,
			Registry:      reg,
			MaxAttempts:   *maxAttempts,
			MaxApproaches: *maxApproaches,
			Timeout:       *timeout,
			Model:         *model,
			Role:          *role,
			Permission:    *permission,
			AddDirs:       dirs,
			ParentTaskID:  parentIDPtr,
			SelfTest:      *selfTest,
			AutoCleanup:   *autoCleanup,
		}
		res, err := executeOrchestration(context.Background(), intentText, opts)
		if err != nil {
			exitRuntime("orchestrate", "intent", *traceID, cli.CodeRuntime, err, "", *jsonl)
		}
		out = res
	} else {
		sup := supervisor.NewSelfTestSupervisor(store, orchestratorWorkerCtor(), supervisor.NewStubReviewer())
		sup.Config.MaxAttemptsPerApproach = *maxAttempts
		sup.Config.MaxApproaches = *maxApproaches
		sup.Config.SelfTestMode = true
		sup.Config.AutoCleanup = *autoCleanup
		sup.Config.RepoDir = cwd
		sup.Config.DBPath = dbPath

		req := supervisor.RunRequest{
			TaskDescription: *taskDesc,
			Role:            *role,
			Permission:      *permission,
			Model:           *model,
			AddDirs:         dirs,
			SelfTestMode:    true,
			Timeout:         *timeout,
			ParentTaskID:    parentIDPtr,
		}

		res, runErr := sup.Run(context.Background(), req)
		if runErr != nil {
			exitRuntime("orchestrate", "self-test", *traceID, cli.CodeRuntime, runErr, "", *jsonl)
		}

		out = orchestrateResultJSON{
			SupervisorTaskID: res.SupervisorTaskID,
			Outcome:          res.Outcome.String(),
			Verdict:          res.Verdict,
			ApproachesTried:  res.ApproachCount,
			TotalAttempts:    res.AttemptCount,
			Escalated:        res.Escalated,
			Escalation:       res.Escalation,
		}
	}

	if *jsonMode || *jsonl {
		env := cli.NewEnvelope("orchestrate_result", "orchestrate", "", out)
		env.TraceID = *traceID
		if err := cli.WriteResponse(os.Stdout, env, *jsonl); err != nil {
			exitRuntime("orchestrate", "", *traceID, cli.CodeIO, err, "", *jsonl)
		}
		return
	}

	fmt.Fprintf(os.Stdout, "supervisor task: %s\noutcome: %s\napproaches_tried: %d\ntotal_attempts: %d\nescalated: %t\n",
		out.SupervisorTaskID, out.Outcome, out.ApproachesTried, out.TotalAttempts, out.Escalated)
	if len(out.SubTasks) > 0 {
		fmt.Fprintf(os.Stdout, "sub-tasks: %d\n", len(out.SubTasks))
		for _, st := range out.SubTasks {
			fmt.Fprintf(os.Stdout, "  - [%s] %s\n", st.Status, st.Task)
		}
	}
}
