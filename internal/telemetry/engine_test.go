package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tamld/g8s/internal/controlplane"
)

func TestTelemetryEngine_IngestAndQuery(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = "/tmp/test_telemetry_" + time.Now().Format("20060102150405") + ".db"
	config.EnableDistillation = true
	config.FlushInterval = 10 * time.Millisecond
	config.BatchSize = 1

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()

	// Test IngestEvent
	event := TraceEvent{
		TaskID:    "task-123",
		EventType: TraceEventTaskFailed,
		Timestamp: time.Now(),
		ExitCode:  intPtr(1),
		Error:     "panic: nil pointer dereference",
		Payload: map[string]any{
			"package":   "internal/worker",
			"file_path": "worker.go",
		},
		Tags: []string{"crash", "nil-pointer"},
	}

	err = engine.IngestEvent(ctx, event)
	assert.NoError(t, err)

	// Allow time for batch processor to flush
	time.Sleep(100 * time.Millisecond)

	// Query events
	filter := TraceFilter{
		TaskID:     &event.TaskID,
		EventTypes: []TraceEventType{TraceEventTaskFailed},
		Limit:      10,
	}

	events, err := engine.QueryEvents(ctx, filter)
	assert.NoError(t, err)
	assert.Len(t, events, 1)

	queried := events[0]
	assert.Equal(t, event.TaskID, queried.TaskID)
	assert.Equal(t, event.EventType, queried.EventType)
	assert.Equal(t, *event.ExitCode, *queried.ExitCode)
	assert.Equal(t, event.Error, queried.Error)
}

func TestTelemetryEngine_DistillFailure(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = "/tmp/test_telemetry_distill_" + time.Now().Format("20060102150405") + ".db"
	config.EnableDistillation = true
	config.DistillationThreshold = 1
	config.FlushInterval = 10 * time.Millisecond
	config.BatchSize = 1

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()

	// Ingest multiple similar failures
	for i := 0; i < 3; i++ {
		event := TraceEvent{
			TaskID:    "task-456",
			EventType: TraceEventTaskFailed,
			Timestamp: time.Now(),
			ExitCode:  intPtr(3),
			Error:     "read-only violation: attempted write to /etc/passwd",
			Payload: map[string]any{
				"package":   "internal/harness",
				"file_path": "/etc/passwd",
			},
		}
		err = engine.IngestEvent(ctx, event)
		require.NoError(t, err)
	}

	time.Sleep(300 * time.Millisecond)

	// Check patterns were distilled
	patterns, err := engine.GetPatterns(ctx, PatternFilter{
		PatternTypes: []FailureMode{FailureModeReadOnlyViolation},
		Limit:        10,
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(patterns), 1)

	pattern := patterns[0]
	assert.Equal(t, FailureModeReadOnlyViolation, pattern.PatternType)
	assert.Greater(t, pattern.OccurrenceCount, 1)
	assert.Greater(t, pattern.ConfidenceScore, 0.3)
	assert.Equal(t, PatternStatusDraft, pattern.Status)
}

func TestTelemetryEngine_PreflightInjection(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = "/tmp/test_telemetry_preflight_" + time.Now().Format("20060102150405") + ".db"
	config.EnableDistillation = true
	config.DistillationThreshold = 1
	config.FlushInterval = 10 * time.Millisecond
	config.BatchSize = 1

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()

	// Test injection with manually created pattern
	pattern := NegativePattern{
		ID:               "neg-test-pattern",
		PatternType:      FailureModeBlockedCommand,
		Title:            "Blocked rm -rf command",
		RootCause:        "Harness blocked dangerous command",
		Remediation:      "Use safer file operations",
		OccurrenceCount:  5,
		FirstSeen:        time.Now().Add(-24 * time.Hour),
		LastSeen:         time.Now(),
		AffectedPackages: []string{"internal/harness"},
		ExampleContexts:  []string{"task=task-123, type=task_failed, error=blocked command: rm -rf /"},
		ConfidenceScore:  0.8,
		Status:           PatternStatusValidated,
	}

	brief := &controlplane.BriefRow{
		ID:        "brief-123",
		Title:     "Test Brief",
		PayloadMD: "Delete all files in /tmp",
		DodMD:     "- [x] Files deleted",
	}

	injected, err := engine.InjectPreflightContext(ctx, brief, []NegativePattern{pattern})
	require.NoError(t, err)
	assert.Contains(t, injected, "Blocked rm -rf command")
	assert.Contains(t, injected, "Root Cause")
	assert.Contains(t, injected, "Remediation")
}

func TestTelemetryEngine_IngestEventsBatch(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = "/tmp/test_telemetry_batch_" + time.Now().Format("20060102150405") + ".db"
	config.BatchSize = 5
	config.FlushInterval = 100 * time.Millisecond

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()

	events := []TraceEvent{
		{TaskID: "batch-1", EventType: TraceEventTaskStarted},
		{TaskID: "batch-2", EventType: TraceEventTaskCompleted},
		{TaskID: "batch-3", EventType: TraceEventTaskFailed, ExitCode: intPtr(1), Error: "error 1"},
		{TaskID: "batch-4", EventType: TraceEventWorkerFailed, ExitCode: intPtr(2), Error: "timeout"},
		{TaskID: "batch-5", EventType: TraceEventReceiptRejected, Error: "invalid receipt"},
	}

	err = engine.IngestEvents(ctx, events)
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	// Query all - at least verify some events persisted
	allEvents, err := engine.QueryEvents(ctx, TraceFilter{Limit: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(allEvents), 3)
}

func TestTelemetryEngine_ConfigValidation(t *testing.T) {
	// Invalid batch size
	_, err := NewTelemetryEngine(&TelemetryConfig{BatchSize: 0})
	assert.Error(t, err)

	// Invalid flush interval
	_, err = NewTelemetryEngine(&TelemetryConfig{FlushInterval: -1})
	assert.Error(t, err)

	// Invalid retention
	_, err = NewTelemetryEngine(&TelemetryConfig{RetentionPeriod: 0})
	assert.Error(t, err)

	// Invalid distillation threshold
	_, err = NewTelemetryEngine(&TelemetryConfig{DistillationThreshold: 0})
	assert.Error(t, err)

	// Valid config
	engine, err := NewTelemetryEngine(DefaultTelemetryConfig())
	require.NoError(t, err)
	engine.Close()
}

func intPtr(i int) *int {
	return &i
}
