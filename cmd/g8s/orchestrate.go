// Package main — orchestrate.go runs the supervisor self-test (Concern A)
// against a real agy worker. The flag-parsing surface mirrors the
// runSubmit / runResume conventions so operators can drive a single
// deterministic end-to-end fix loop from the terminal.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/supervisor"
)

// orchestrateResultJSON is the public machine-readable contract produced by
// the self-test mode. The `approaches_tried` and `total_attempts` keys are
// load-bearing: the supervisor regression suite greps for them.
type orchestrateResultJSON struct {
	SupervisorTaskID string                 `json:"supervisor_task_id"`
	Outcome          string                 `json:"outcome"`
	Verdict          string                 `json:"verdict"`
	ApproachesTried  int                    `json:"approaches_tried"`
	TotalAttempts    int                    `json:"total_attempts"`
	Escalated        bool                   `json:"escalated"`
	Escalation       *supervisor.Escalation `json:"escalation,omitempty"`
}

// runOrchestrate is the entry point invoked by the top-level dispatcher.
// When --self-test is set it drives an in-process supervisor loop with the
// real AgyWorker so the operator can dogfood the end-to-end fix cycle
// without a separate worker process.
func runOrchestrate(args []string) {
	fs := flag.NewFlagSet("orchestrate", flag.ExitOnError)
	selfTest := fs.Bool("self-test", false, "run a self-contained supervisor loop against the real agy worker")
	model := fs.String("model", "gemini-3.7-flash-high", "target worker model")
	role := fs.String("role", "collector", "worker role contract")
	permission := fs.String("permission", "read_only", "permission profile")
	taskDesc := fs.String("task", "scan ./src for MCP server candidate implementations", "task description handed to the supervisor")
	parentID := fs.String("parent-task-id", "", "optional parent supervisor task id")
	maxAttempts := fs.Int("max-attempts", 3, "attempts per approach")
	maxApproaches := fs.Int("max-approaches", 3, "approach budget before escalation")
	timeout := fs.Duration("timeout", 5*time.Minute, "per-attempt execution window")
	jsonMode := fs.Bool("json", true, "emit machine-readable JSON (default true for this command)")
	failIf(fs.Parse(args))

	if !*selfTest {
		fmt.Fprintln(os.Stderr, "usage: g8s orchestrate --self-test [--task <text>] [--json] [--add-dir <path> ...]")
		os.Exit(2)
	}

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	sup := supervisor.NewSelfTestSupervisor(store, orchestrator.NewAgyWorker(), supervisor.NewStubReviewer())
	sup.Config.MaxAttemptsPerApproach = *maxAttempts
	sup.Config.MaxApproaches = *maxApproaches
	sup.Config.SelfTestMode = true

	req := supervisor.RunRequest{
		TaskDescription: *taskDesc,
		Role:            *role,
		Permission:      *permission,
		Model:           *model,
		SelfTestMode:    true,
		Timeout:         *timeout,
	}
	if *parentID != "" {
		pid := *parentID
		req.ParentTaskID = &pid
	}

	res, runErr := sup.Run(context.Background(), req)
	if runErr != nil {
		failIf(runErr)
	}

	out := orchestrateResultJSON{
		SupervisorTaskID: res.SupervisorTaskID,
		Outcome:          res.Outcome.String(),
		Verdict:          res.Verdict,
		ApproachesTried:  res.ApproachCount,
		TotalAttempts:    res.AttemptCount,
		Escalated:        res.Escalated,
	}
	if res.Escalation != nil {
		out.Escalation = res.Escalation
	}

	if *jsonMode {
		encoded, encErr := json.MarshalIndent(out, "", "  ")
		failIf(encErr)
		fmt.Println(string(encoded))
		return
	}

	fmt.Fprintf(os.Stdout, "supervisor task: %s\noutcome: %s\napproaches_tried: %d\ntotal_attempts: %d\nescalated: %t\n",
		out.SupervisorTaskID, out.Outcome, out.ApproachesTried, out.TotalAttempts, out.Escalated)
}
