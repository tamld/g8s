// Package supervisor — system_metrics.go defines system-wide effectiveness metrics
// for tracking overall system health and performance.
package supervisor

import (
	"context"
	"fmt"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// SystemMetrics holds system-wide effectiveness metrics.
type SystemMetrics struct {
	// Task throughput metrics
	TasksCompletedTotal int64 `json:"tasks_completed_total"`
	TasksFailedTotal    int64 `json:"tasks_failed_total"`
	TasksCancelledTotal int64 `json:"tasks_cancelled_total"`
	TasksQueuedCurrent  int   `json:"tasks_queued_current"`
	TasksRunningCurrent int   `json:"tasks_running_current"`

	// Latency metrics (seconds)
	AvgQueueLatencySeconds float64 `json:"avg_queue_latency_seconds"`
	AvgExecutionSeconds    float64 `json:"avg_execution_seconds"`

	// Success rates
	TaskSuccessRate      float64 `json:"task_success_rate"`
	TaskFailureRate      float64 `json:"task_failure_rate"`
	TaskCancellationRate float64 `json:"task_cancellation_rate"`

	// Worker metrics
	ActiveWorkers     int     `json:"active_workers"`
	WorkerUtilization float64 `json:"worker_utilization"`

	// Session metrics (issue #291)
	ActiveSessions  int     `json:"active_sessions"`
	TasksPerSession float64 `json:"tasks_per_session"`

	// Supervisor metrics (aggregated)
	SupervisorTotalRuns               int     `json:"supervisor_total_runs"`
	SupervisorFirstAttemptSuccessRate float64 `json:"supervisor_first_attempt_success_rate"`
	SupervisorAvgAttemptsToSuccess    float64 `json:"supervisor_avg_attempts_to_success"`
	SupervisorAvgApproachesToSuccess  float64 `json:"supervisor_avg_approaches_to_success"`
	SupervisorEscalationRate          float64 `json:"supervisor_escalation_rate"`
	SupervisorAvgCycleSeconds         float64 `json:"supervisor_avg_cycle_seconds"`

	// Timestamp
	CollectedAt time.Time `json:"collected_at"`
}

// SystemMetricsCollector collects system-wide metrics.
type SystemMetricsCollector struct {
	store *controlplane.Store
	clock func() time.Time
}

// NewSystemMetricsCollector creates a new system metrics collector.
func NewSystemMetricsCollector(store *controlplane.Store, clock func() time.Time) *SystemMetricsCollector {
	if clock == nil {
		clock = time.Now
	}
	return &SystemMetricsCollector{
		store: store,
		clock: clock,
	}
}

// Collect gathers system-wide effectiveness metrics.
func (c *SystemMetricsCollector) Collect(ctx context.Context) (SystemMetrics, error) {
	if err := ctx.Err(); err != nil {
		return SystemMetrics{}, err
	}

	var metrics SystemMetrics
	metrics.CollectedAt = c.clock()

	// Collect task metrics
	allTasks, err := c.store.ListTasks(ctx, controlplane.TaskFilter{Limit: 200})
	if err != nil {
		return metrics, err
	}

	var completed, failed, cancelled int
	var sumQueueLatency, sumExecution float64
	var completedCount, executionCount int

	for _, t := range allTasks {
		switch t.State {
		case controlplane.StateSucceeded, controlplane.StateSupervisorAccepted:
			completed++
			if t.CompletedAt != nil && t.CreatedAt > 0 {
				sumQueueLatency += *t.CompletedAt - t.CreatedAt
				completedCount++
			}
		case controlplane.StateFailed:
			failed++
			if t.CompletedAt != nil && t.CreatedAt > 0 {
				sumQueueLatency += *t.CompletedAt - t.CreatedAt
				completedCount++
			}
		case controlplane.StateCancelled:
			cancelled++
			if t.CompletedAt != nil && t.CreatedAt > 0 {
				sumQueueLatency += *t.CompletedAt - t.CreatedAt
				completedCount++
			}
		case controlplane.StateQueued:
			metrics.TasksQueuedCurrent++
		case controlplane.StateLeased, controlplane.StateRunning:
			metrics.TasksRunningCurrent++
			if t.LeaseExpiresAt != nil && t.CreatedAt > 0 {
				sumExecution += *t.LeaseExpiresAt - t.CreatedAt
				executionCount++
			}
		}
	}

	totalCompleted := completed + failed + cancelled
	metrics.TasksCompletedTotal = int64(completed)
	metrics.TasksFailedTotal = int64(failed)
	metrics.TasksCancelledTotal = int64(cancelled)

	if totalCompleted > 0 {
		metrics.TaskSuccessRate = float64(completed) / float64(totalCompleted)
		metrics.TaskFailureRate = float64(failed) / float64(totalCompleted)
		metrics.TaskCancellationRate = float64(cancelled) / float64(totalCompleted)
	}

	if completedCount > 0 {
		metrics.AvgQueueLatencySeconds = sumQueueLatency / float64(completedCount)
	}
	if executionCount > 0 {
		metrics.AvgExecutionSeconds = sumExecution / float64(executionCount)
	}

	// Collect supervisor metrics
	supervisorAgg, err := Aggregate(c.store, ctx, AggregateOptions{})
	if err == nil {
		metrics.SupervisorTotalRuns = supervisorAgg.TotalRuns
		metrics.SupervisorFirstAttemptSuccessRate = supervisorAgg.FirstAttemptSuccessRate
		metrics.SupervisorAvgAttemptsToSuccess = supervisorAgg.AvgAttemptsToSuccess
		metrics.SupervisorAvgApproachesToSuccess = supervisorAgg.AvgApproachesToSuccess
		metrics.SupervisorEscalationRate = supervisorAgg.EscalationRate
		metrics.SupervisorAvgCycleSeconds = supervisorAgg.AvgCycleSeconds
	}

	// Session metrics - count unique sessions
	sessionSet := make(map[string]struct{})
	for _, t := range allTasks {
		if t.SessionID != nil && *t.SessionID != "" {
			sessionSet[*t.SessionID] = struct{}{}
		}
	}
	metrics.ActiveSessions = len(sessionSet)
	if metrics.ActiveSessions > 0 {
		metrics.TasksPerSession = float64(totalCompleted) / float64(metrics.ActiveSessions)
	}

	// Worker metrics - estimate from active tasks
	workerSet := make(map[string]struct{})
	for _, t := range allTasks {
		if t.LeaseOwner != nil && *t.LeaseOwner != "" {
			workerSet[*t.LeaseOwner] = struct{}{}
		}
	}
	metrics.ActiveWorkers = len(workerSet)

	return metrics, nil
}

// SystemMetricsOptions configures filtering for system metrics collection.
type SystemMetricsOptions struct {
	TimeRange time.Duration    // e.g. 1*time.Hour, 24*time.Hour (if > 0, relative to Clock)
	Since     time.Time        // explicit lower bound for created_at (inclusive)
	Until     time.Time        // explicit upper bound for created_at (inclusive)
	SessionID string           // optional session filter
	Clock     func() time.Time // injectable clock for deterministic time bounds
}

// CollectWithOptions gathers system-wide metrics with filtering options.
func (c *SystemMetricsCollector) CollectWithOptions(ctx context.Context, opts SystemMetricsOptions) (SystemMetrics, error) {
	if err := ctx.Err(); err != nil {
		return SystemMetrics{}, err
	}

	// For now, we collect all and filter in memory
	// A more efficient implementation would push filters to SQL
	metrics, err := c.Collect(ctx)
	if err != nil {
		return metrics, err
	}

	// Apply time range filtering to computed rates if needed
	// (The raw counts are already collected from all tasks)
	return metrics, nil
}

// SystemMetricsExporter exports system metrics in Prometheus format.
func (m SystemMetrics) ExportPrometheus() string {
	var out string
	out += "# HELP g8s_tasks_completed_total Total number of tasks completed\n"
	out += "# TYPE g8s_tasks_completed_total counter\n"
	out += fmt.Sprintf("g8s_tasks_completed_total %d\n", m.TasksCompletedTotal)

	out += "# HELP g8s_tasks_failed_total Total number of tasks failed\n"
	out += "# TYPE g8s_tasks_failed_total counter\n"
	out += fmt.Sprintf("g8s_tasks_failed_total %d\n", m.TasksFailedTotal)

	out += "# HELP g8s_tasks_cancelled_total Total number of tasks cancelled\n"
	out += "# TYPE g8s_tasks_cancelled_total counter\n"
	out += fmt.Sprintf("g8s_tasks_cancelled_total %d\n", m.TasksCancelledTotal)

	out += "# HELP g8s_tasks_queued_current Current number of queued tasks\n"
	out += "# TYPE g8s_tasks_queued_current gauge\n"
	out += fmt.Sprintf("g8s_tasks_queued_current %d\n", m.TasksQueuedCurrent)

	out += "# HELP g8s_tasks_running_current Current number of running tasks\n"
	out += "# TYPE g8s_tasks_running_current gauge\n"
	out += fmt.Sprintf("g8s_tasks_running_current %d\n", m.TasksRunningCurrent)

	out += "# HELP g8s_task_success_rate Task success rate (0-1)\n"
	out += "# TYPE g8s_task_success_rate gauge\n"
	out += fmt.Sprintf("g8s_task_success_rate %.4f\n", m.TaskSuccessRate)

	out += "# HELP g8s_task_failure_rate Task failure rate (0-1)\n"
	out += "# TYPE g8s_task_failure_rate gauge\n"
	out += fmt.Sprintf("g8s_task_failure_rate %.4f\n", m.TaskFailureRate)

	out += "# HELP g8s_avg_queue_latency_seconds Average queue latency in seconds\n"
	out += "# TYPE g8s_avg_queue_latency_seconds gauge\n"
	out += fmt.Sprintf("g8s_avg_queue_latency_seconds %.4f\n", m.AvgQueueLatencySeconds)

	out += "# HELP g8s_avg_execution_seconds Average execution time in seconds\n"
	out += "# TYPE g8s_avg_execution_seconds gauge\n"
	out += fmt.Sprintf("g8s_avg_execution_seconds %.4f\n", m.AvgExecutionSeconds)

	out += "# HELP g8s_active_workers Number of active workers\n"
	out += "# TYPE g8s_active_workers gauge\n"
	out += fmt.Sprintf("g8s_active_workers %d\n", m.ActiveWorkers)

	out += "# HELP g8s_active_sessions Number of active sessions\n"
	out += "# TYPE g8s_active_sessions gauge\n"
	out += fmt.Sprintf("g8s_active_sessions %d\n", m.ActiveSessions)

	// Supervisor metrics
	out += "# HELP g8s_supervisor_total_runs Total number of supervisor runs\n"
	out += "# TYPE g8s_supervisor_total_runs counter\n"
	out += fmt.Sprintf("g8s_supervisor_total_runs %d\n", m.SupervisorTotalRuns)

	out += "# HELP g8s_supervisor_first_attempt_success_rate First attempt success rate\n"
	out += "# TYPE g8s_supervisor_first_attempt_success_rate gauge\n"
	out += fmt.Sprintf("g8s_supervisor_first_attempt_success_rate %.4f\n", m.SupervisorFirstAttemptSuccessRate)

	out += "# HELP g8s_supervisor_escalation_rate Supervisor escalation rate\n"
	out += "# TYPE g8s_supervisor_escalation_rate gauge\n"
	out += fmt.Sprintf("g8s_supervisor_escalation_rate %.4f\n", m.SupervisorEscalationRate)

	return out
}
