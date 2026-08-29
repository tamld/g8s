// Package main — supervisor_metrics.go surfaces the post-run telemetry
// bundle written by internal/supervisor.MetricsStore. The command reads
// from the shared SQLite control plane (supervisor_metrics table) so
// operators can inspect supervisor quality across runs without opening
// the database directly.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/tamld/g8s/internal/cli"
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
	fs := flag.NewFlagSet("supervisor-metrics", flag.ContinueOnError)
	actor, traceID, jsonl, jsonMode := cli.AddCommonFlags(fs)
	_ = actor
	taskID := fs.String("task-id", "", "supervisor task id (returns single-run metrics)")
	aggregate := fs.Bool("aggregate", false, "aggregate metrics across every persisted run")
	jsonStream := fs.Bool("json-stream", false, "emit one JSON object per supervisor task as it queries")
	timeRange := fs.Duration("time-range", 0, "filter metrics by time window (e.g. 1h, 24h)")
	workerName := fs.String("worker-name", "", "filter metrics by worker name")
	if err := fs.Parse(args); err != nil {
		exitUsage("supervisor-metrics", "", *traceID, err.Error(), "", *jsonl)
	}

	if *taskID == "" && !*aggregate && !*jsonStream {
		exitUsage("supervisor-metrics", "", *traceID, "usage: g8s supervisor-metrics --task-id <id> | --aggregate | --json-stream [--time-range <dur>] [--worker-name <name>] [--json]", "Specify --task-id, --aggregate, or --json-stream", *jsonl)
	}

	dbPath, err := databasePath()
	if err != nil {
		exitRuntime("supervisor-metrics", "", *traceID, cli.CodeIO, err, "", *jsonl)
	}
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		exitRuntime("supervisor-metrics", "", *traceID, cli.CodeRuntime, err, "", *jsonl)
	}
	defer store.Close()

	opts := supervisor.AggregateOptions{
		TimeRange:  *timeRange,
		WorkerName: *workerName,
	}

	executeSupervisorMetrics(*taskID, *aggregate, *jsonStream, *jsonMode, opts, store, *traceID, *jsonl)
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
	extra ...any,
) {
	traceID := cli.GenerateTraceID()
	jsonl := false
	if len(extra) > 0 {
		if t, ok := extra[0].(string); ok && t != "" {
			traceID = t
		}
	}
	if len(extra) > 1 {
		if j, ok := extra[1].(bool); ok {
			jsonl = j
		}
	}

	ctx := context.Background()

	if jsonStream {
		err := supervisor.StreamMetrics(store, ctx, opts, func(item supervisor.TaskMetricsItem) error {
			env := cli.NewEnvelope("supervisor_metrics_item", "supervisor-metrics", "stream", item)
			env.TraceID = traceID
			return cli.WriteJSONL(os.Stdout, env)
		})
		if err != nil {
			exitRuntime("supervisor-metrics", "stream", traceID, cli.CodeRuntime, err, "", jsonl)
		}
		return
	}

	out := supervisorMetricsOutput{}

	if taskID != "" {
		m, err := store.GetMetrics(ctx, taskID)
		if err != nil {
			exitRuntime("supervisor-metrics", "", traceID, cli.CodeNotFound, err, "", jsonl)
		}
		out.Mode = "single"
		out.Metrics = &m
	} else {
		agg, err := supervisor.Aggregate(store, ctx, opts)
		if err != nil {
			exitRuntime("supervisor-metrics", "", traceID, cli.CodeRuntime, err, "", jsonl)
		}
		out.Mode = "aggregate"
		out.Aggregate = &agg
	}

	if jsonMode || jsonl {
		env := cli.NewEnvelope("supervisor_metrics", "supervisor-metrics", "", out)
		env.TraceID = traceID
		if err := cli.WriteResponse(os.Stdout, env, jsonl); err != nil {
			exitRuntime("supervisor-metrics", "", traceID, cli.CodeIO, err, "", jsonl)
		}
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
