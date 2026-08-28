// Package main — supervisor_metrics.go surfaces the post-run telemetry
// bundle written by internal/supervisor.MetricsStore. The command reads
// from the shared SQLite control plane (supervisor_metrics table) so
// operators can inspect supervisor quality across runs without opening
// the database directly.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/tamld/g8s/internal/controlplane"
)

// supervisorMetricsOutput is the JSON envelope printed by the CLI. Either
// Metrics or Aggregate is populated based on the flag combination; the
// caller can tell which one via the top-level "mode" key.
type supervisorMetricsOutput struct {
	Mode      string                       `json:"mode"`
	Metrics   *controlplane.MetricsRow     `json:"metrics,omitempty"`
	Aggregate *supervisorMetricsAggregate  `json:"aggregate,omitempty"`
}

type supervisorMetricsAggregate struct {
	TotalRuns               int     `json:"total_runs"`
	FirstAttemptSuccessRate float64 `json:"first_attempt_success_rate"`
	AvgAttemptsToSuccess    float64 `json:"avg_attempts_to_success"`
	AvgApproachesToSuccess  float64 `json:"avg_approaches_to_success"`
	EscalationRate          float64 `json:"escalation_rate"`
	AvgCycleSeconds         float64 `json:"avg_cycle_duration_seconds"`
}

func runSupervisorMetrics(args []string) {
	fs := flag.NewFlagSet("supervisor-metrics", flag.ExitOnError)
	taskID := fs.String("task-id", "", "supervisor task id (returns single-run metrics)")
	aggregate := fs.Bool("aggregate", false, "aggregate metrics across every persisted run")
	jsonMode := fs.Bool("json", true, "emit machine-readable JSON (default true)")
	failIf(fs.Parse(args))

	if *taskID == "" && !*aggregate {
		fmt.Fprintln(os.Stderr, "usage: g8s supervisor-metrics --task-id <id> | --aggregate [--json]")
		os.Exit(2)
	}

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer func() { _ = store.Close() }()

	out := supervisorMetricsOutput{}

	if *taskID != "" {
		m, err := store.GetMetrics(context.Background(), *taskID)
		if err != nil {
			failIf(err)
		}
		out.Mode = "single"
		out.Metrics = &m
	} else {
		agg, err := aggregateSupervisorMetrics(context.Background(), store)
		if err != nil {
			failIf(err)
		}
		out.Mode = "aggregate"
		out.Aggregate = agg
	}

	if *jsonMode {
		encoded, encErr := json.MarshalIndent(out, "", "  ")
		failIf(encErr)
		fmt.Println(string(encoded))
		return
	}

	if out.Metrics != nil {
		m := out.Metrics
		fmt.Printf("task_id=%s attempts_to_success=%d approaches_to_success=%d escalation_count=%d cycle=%.3fs\n",
			m.SupervisorTaskID, m.AttemptsToSuccess, m.ApproachesToSuccess, m.EscalationCount, m.CycleDurationSeconds)
	}
	if out.Aggregate != nil {
		a := out.Aggregate
		fmt.Printf("total_runs=%d first_attempt_success_rate=%.2f avg_attempts=%.2f avg_approaches=%.2f escalation_rate=%.2f avg_cycle=%.3fs\n",
			a.TotalRuns, a.FirstAttemptSuccessRate, a.AvgAttemptsToSuccess, a.AvgApproachesToSuccess, a.EscalationRate, a.AvgCycleSeconds)
	}
}

// aggregateSupervisorMetrics scans supervisor_metrics rows via ListTasks +
// per-row GetMetrics. The controlplane package does not yet expose a
// "ListAllMetrics" surface so we use a one-shot scan keyed on the known
// supervisor_tasks table.
func aggregateSupervisorMetrics(ctx context.Context, store *controlplane.Store) (*supervisorMetricsAggregate, error) {
	tasks, err := store.ListSupervisorTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list supervisor tasks: %w", err)
	}
	agg := &supervisorMetricsAggregate{}
	for _, t := range tasks {
		m, err := store.GetMetrics(ctx, t.ID)
		if err != nil {
			continue
		}
		agg.TotalRuns++
		if m.FirstAttemptSuccess {
			agg.FirstAttemptSuccessRate++
		}
		agg.AvgAttemptsToSuccess += float64(m.AttemptsToSuccess)
		agg.AvgApproachesToSuccess += float64(m.ApproachesToSuccess)
		if m.EscalationCount > 0 {
			agg.EscalationRate++
		}
		agg.AvgCycleSeconds += m.CycleDurationSeconds
	}
	if agg.TotalRuns == 0 {
		return agg, nil
	}
	denom := float64(agg.TotalRuns)
	agg.FirstAttemptSuccessRate /= denom
	agg.AvgAttemptsToSuccess /= denom
	agg.AvgApproachesToSuccess /= denom
	agg.EscalationRate /= denom
	agg.AvgCycleSeconds /= denom
	return agg, nil
}
