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
	"github.com/tamld/g8s/internal/supervisor"
)

// supervisorMetricsOutput is the JSON envelope printed by the CLI. Either
// Metrics or Aggregate is populated based on the flag combination; the
// caller can tell which one via the top-level "mode" key.
type supervisorMetricsOutput struct {
	Mode      string                       `json:"mode"`
	Metrics   *controlplane.MetricsRow     `json:"metrics,omitempty"`
	Aggregate *supervisor.AggregateMetrics `json:"aggregate,omitempty"`
}

func runSupervisorMetrics(args []string) {
	fs := flag.NewFlagSet("supervisor-metrics", flag.ExitOnError)
	taskID := fs.String("task-id", "", "supervisor task id (returns single-run metrics)")
	aggregate := fs.Bool("aggregate", false, "aggregate metrics across every persisted run")
	jsonStream := fs.Bool("json-stream", false, "emit one JSON object per supervisor task as it queries")
	timeRange := fs.Duration("time-range", 0, "filter metrics by time window (e.g. 1h, 24h)")
	workerName := fs.String("worker-name", "", "filter metrics by worker name")
	jsonMode := fs.Bool("json", true, "emit machine-readable JSON (default true)")
	failIf(fs.Parse(args))

	if *taskID == "" && !*aggregate && !*jsonStream {
		fmt.Fprintln(os.Stderr, "usage: g8s supervisor-metrics --task-id <id> | --aggregate | --json-stream [--time-range <dur>] [--worker-name <name>] [--json]")
		os.Exit(2)
	}

	dbPath, err := databasePath()
	failIf(err)
	store, err := controlplane.NewControlPlane(dbPath, nil)
	failIf(err)
	defer store.Close()

	opts := supervisor.AggregateOptions{
		TimeRange:  *timeRange,
		WorkerName: *workerName,
	}

	executeSupervisorMetrics(*taskID, *aggregate, *jsonStream, *jsonMode, opts, store)
}

// executeSupervisorMetrics is the testable core of runSupervisorMetrics.
// Production callers (the CLI dispatcher) open + close the *controlplane.Store
// themselves; tests can inject an already-open store so a single TempDir can
// host both the seeding and the reporting without a second Open() racing for
// the SQLite WAL lock on Windows.
func executeSupervisorMetrics(
	taskID string,
	aggregate, jsonStream, jsonMode bool,
	opts supervisor.AggregateOptions,
	store *controlplane.Store,
) {
	ctx := context.Background()

	if jsonStream {
		enc := json.NewEncoder(os.Stdout)
		err := supervisor.StreamMetrics(store, ctx, opts, func(item supervisor.TaskMetricsItem) error {
			return enc.Encode(item)
		})
		failIf(err)
		return
	}

	out := supervisorMetricsOutput{}

	if taskID != "" {
		m, err := store.GetMetrics(ctx, taskID)
		if err != nil {
			failIf(err)
		}
		out.Mode = "single"
		out.Metrics = &m
	} else {
		agg, err := supervisor.Aggregate(store, ctx, opts)
		if err != nil {
			failIf(err)
		}
		out.Mode = "aggregate"
		out.Aggregate = &agg
	}

	if jsonMode {
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
