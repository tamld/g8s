package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tamld/g8s/internal/controlplane"
)

func TestTelemetryEngine_IngestAndQuery(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "telemetry_"+time.Now().Format("20060102150405")+".db")
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
	config.DBPath = filepath.Join(t.TempDir(), "telemetry.db") + time.Now().Format("20060102150405") + ".db"
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
	config.DBPath = filepath.Join(t.TempDir(), "telemetry.db") + time.Now().Format("20060102150405") + ".db"
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
	config.DBPath = filepath.Join(t.TempDir(), "telemetry.db") + time.Now().Format("20060102150405") + ".db"
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

	// Async flush + WAL visibility on slower runners: poll instead of a
	// single fixed sleep so the assertion stays timing-robust.
	deadline := time.Now().Add(5 * time.Second)
	var allEvents []TraceEvent
	for time.Now().Before(deadline) {
		allEvents, err = engine.QueryEvents(ctx, TraceFilter{Limit: 10})
		require.NoError(t, err)
		if len(allEvents) >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
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

func TestTelemetryEngine_RapidEventIDUniqueness(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "telemetry_rapid.db")
	config.BatchSize = 100
	config.FlushInterval = 50 * time.Millisecond

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()
	const count = 100

	for i := 0; i < count; i++ {
		ev := TraceEvent{
			TaskID:    fmt.Sprintf("task-%d", i),
			EventType: TraceEventTaskStarted,
		}
		err := engine.IngestEvent(ctx, ev)
		require.NoError(t, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var allEvents []TraceEvent
	for time.Now().Before(deadline) {
		allEvents, err = engine.QueryEvents(ctx, TraceFilter{Limit: count})
		require.NoError(t, err)
		if len(allEvents) == count {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, count, len(allEvents), "all events should be persisted")

	seen := make(map[string]bool)
	for _, ev := range allEvents {
		assert.NotEmpty(t, ev.ID)
		assert.False(t, seen[ev.ID], "duplicate event ID detected: %s", ev.ID)
		seen[ev.ID] = true
	}
	assert.Equal(t, count, len(seen))
}

func BenchmarkRandomString(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = randomString(8)
	}
}

func BenchmarkIngestEvent(b *testing.B) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(b.TempDir(), "bench.db")
	config.BatchSize = 10000
	config.FlushInterval = 1 * time.Hour // avoid flush during bench

	engine, err := NewTelemetryEngine(config)
	if err != nil {
		b.Fatal(err)
	}
	defer engine.Close()

	ctx := context.Background()
	ev := TraceEvent{
		TaskID:    "bench-task",
		EventType: TraceEventTaskStarted,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = engine.IngestEvent(ctx, ev)
	}
}

// #253 closed loop: distilled negative patterns surface as pre-flight
// context — QueryTopPatterns orders by occurrences/confidence.
func TestQueryTopPatterns(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "telemetry.db")
	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()
	now := time.Now().UnixNano()
	seed := []struct {
		id    string
		title string
		occ   int
		conf  float64
	}{
		{"pat-low", "Low confidence pattern", 1, 0.3},
		{"pat-top", "High frequency pattern", 9, 0.95},
		{"pat-mid", "Mid frequency pattern", 4, 0.6},
	}
	for _, s := range seed {
		pkg, _ := json.Marshal([]string{"internal/x"})
		_, err := engine.db.Exec(`INSERT INTO negative_patterns
			(id, pattern_type, title, description, root_cause, remediation, occurrence_count, first_seen, last_seen, affected_packages, example_contexts, confidence_score, status)
			VALUES (?, 'timeout', ?, 'desc', 'root cause', 'remediate', ?, ?, ?, ?, ?, ?, 'VALIDATED')`,
			s.id, s.title, s.occ, now, now, pkg, pkg, s.conf)
		require.NoError(t, err)
	}

	patterns, err := engine.QueryTopPatterns(ctx, 2)
	require.NoError(t, err)
	require.Len(t, patterns, 2)
	if patterns[0].ID != "pat-top" || patterns[1].ID != "pat-mid" {
		t.Errorf("expected top-2 ordered by occurrences, got %s, %s", patterns[0].ID, patterns[1].ID)
	}

	// The injected pre-flight section carries the pattern knowledge.
	section, err := engine.InjectPreflightContext(ctx, &controlplane.BriefRow{}, patterns)
	require.NoError(t, err)
	if !strings.Contains(section, "High frequency pattern") {
		t.Errorf("pre-flight section missing top pattern title")
	}
}
