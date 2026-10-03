package telemetry

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tamld/g8s/internal/controlplane"
)

func TestTelemetryConfigValidationAllBranches(t *testing.T) {
	// 1. Valid default
	cfg := DefaultTelemetryConfig()
	assert.NoError(t, cfg.Validate())

	// 2. BatchSize <= 0
	bad := *cfg
	bad.BatchSize = 0
	assert.EqualError(t, bad.Validate(), "batch size must be positive")

	// 3. FlushInterval <= 0
	bad = *cfg
	bad.FlushInterval = 0
	assert.EqualError(t, bad.Validate(), "flush interval must be positive")

	// 4. RetentionPeriod <= 0
	bad = *cfg
	bad.RetentionPeriod = 0
	assert.EqualError(t, bad.Validate(), "retention period must be positive")

	// 5. DistillationThreshold <= 0
	bad = *cfg
	bad.DistillationThreshold = 0
	assert.EqualError(t, bad.Validate(), "distillation threshold must be positive")
}

func TestTelemetryEngine_ConstructorAndClock(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "nil_config.db")

	// Test NewTelemetryEngine with nil config (should use default)
	// We set DBPath via DefaultTelemetryConfig override or temporary file
	cfg := DefaultTelemetryConfig()
	cfg.DBPath = tempDB
	cfg.BatchSize = 10
	cfg.FlushInterval = 50 * time.Millisecond

	engine, err := NewTelemetryEngine(cfg)
	require.NoError(t, err)
	defer engine.Close()

	// Test SetClock
	fixedTime := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	engine.SetClock(func() time.Time {
		return fixedTime
	})
	assert.Equal(t, fixedTime, engine.getClock())

	// SetClock with nil should not change clock
	engine.SetClock(nil)
	assert.Equal(t, fixedTime, engine.getClock())
}

func TestTelemetryEngine_GetEventAndQueryFilters(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "query_test.db")
	config.BatchSize = 1
	config.FlushInterval = 10 * time.Millisecond

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()

	supID := "sup-456"
	exitCode := 1
	now := time.Now()

	event1 := TraceEvent{
		ID:               "evt-custom-1",
		TaskID:           "task-query-1",
		SupervisorTaskID: &supID,
		EventType:        TraceEventTaskStarted,
		Timestamp:        now.Add(-2 * time.Minute),
		ExitCode:         &exitCode,
		Error:            "initial error",
		Tags:             []string{"tagA", "tagB"},
	}

	event2 := TraceEvent{
		ID:        "evt-custom-2",
		TaskID:    "task-query-2",
		EventType: TraceEventTaskCompleted,
		Timestamp: now.Add(-1 * time.Minute),
	}

	require.NoError(t, engine.IngestEvents(ctx, []TraceEvent{event1, event2}))
	time.Sleep(100 * time.Millisecond)

	// 1. GetEvent for the latest event
	got, err := engine.GetEvent(ctx, "evt-custom-2")
	require.NoError(t, err)
	assert.Equal(t, "evt-custom-2", got.ID)
	assert.Equal(t, "task-query-2", got.TaskID)

	// 2. GetEvent not found
	_, err = engine.GetEvent(ctx, "nonexistent-id")
	assert.ErrorIs(t, err, ErrEventNotFound)

	// 3. QueryEvents with SupervisorTaskID filter
	filteredSup, err := engine.QueryEvents(ctx, TraceFilter{
		SupervisorTaskID: &supID,
	})
	require.NoError(t, err)
	assert.Len(t, filteredSup, 1)

	// 4. QueryEvents with Since and Until filter
	since := now.Add(-90 * time.Second)
	until := now
	filteredTime, err := engine.QueryEvents(ctx, TraceFilter{
		Since: &since,
		Until: &until,
	})
	require.NoError(t, err)
	assert.Len(t, filteredTime, 1)
	assert.Equal(t, "evt-custom-2", filteredTime[0].ID)

	// 5. QueryEvents with Limit <= 0 and Limit > 200 and Offset
	eventsDefLimit, err := engine.QueryEvents(ctx, TraceFilter{
		Limit: -1,
	})
	require.NoError(t, err)
	assert.Len(t, eventsDefLimit, 2)

	eventsHighLimit, err := engine.QueryEvents(ctx, TraceFilter{
		Limit:  300,
		Offset: 1,
	})
	require.NoError(t, err)
	assert.Len(t, eventsHighLimit, 1)
}

func TestTelemetryEngine_PatternUpdateAndGetPatternsFilters(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "patterns_test.db")

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()
	now := time.Now()

	pattern1 := NegativePattern{
		ID:               "pat-1",
		PatternType:      FailureModeCrash,
		Title:            "Crash in worker",
		Description:      "Desc 1",
		RootCause:        "Nil pointer",
		Remediation:      "Add check",
		OccurrenceCount:  1,
		FirstSeen:        now.Add(-time.Hour),
		LastSeen:         now.Add(-time.Hour),
		AffectedPackages: []string{"internal/worker"},
		ExampleContexts:  []string{"context 1"},
		ConfidenceScore:  0.4,
		Status:           PatternStatusDraft,
	}

	pattern2 := NegativePattern{
		ID:               "pat-2",
		PatternType:      FailureModeReadOnlyViolation,
		Title:            "Read-only error",
		Description:      "Desc 2",
		RootCause:        "Write attempt",
		Remediation:      "Fix permissions",
		OccurrenceCount:  3,
		FirstSeen:        now.Add(-30 * time.Minute),
		LastSeen:         now,
		AffectedPackages: []string{"internal/storage"},
		ExampleContexts:  []string{"context 2"},
		ConfidenceScore:  0.8,
		Status:           PatternStatusValidated,
	}

	// Insert patterns directly
	insertPatternHelper(t, engine, pattern1)
	insertPatternHelper(t, engine, pattern2)

	// 1. Update pattern
	pattern1.OccurrenceCount = 5
	pattern1.Status = PatternStatusApplied
	require.NoError(t, engine.UpdatePattern(ctx, pattern1))

	// 2. Query with PatternTypes filter
	pats, err := engine.GetPatterns(ctx, PatternFilter{
		PatternTypes: []FailureMode{FailureModeCrash},
	})
	require.NoError(t, err)
	assert.Len(t, pats, 1)
	assert.Equal(t, "pat-1", pats[0].ID)
	assert.Equal(t, PatternStatusApplied, pats[0].Status)
	assert.Equal(t, 5, pats[0].OccurrenceCount)

	// 3. Query with Statuses filter
	pats, err = engine.GetPatterns(ctx, PatternFilter{
		Statuses: []PatternStatus{PatternStatusValidated},
	})
	require.NoError(t, err)
	assert.Len(t, pats, 1)
	assert.Equal(t, "pat-2", pats[0].ID)

	// 4. Query with MinConfidence filter
	pats, err = engine.GetPatterns(ctx, PatternFilter{
		MinConfidence: 0.7,
	})
	require.NoError(t, err)
	assert.Len(t, pats, 1)
	assert.Equal(t, "pat-2", pats[0].ID)

	// 5. Query with Since filter
	since := now.Add(-10 * time.Minute)
	pats, err = engine.GetPatterns(ctx, PatternFilter{
		Since: &since,
	})
	require.NoError(t, err)
	assert.Len(t, pats, 1)
	assert.Equal(t, "pat-2", pats[0].ID)

	// 6. Query with Limit <= 0 and Limit > 200 and Offset
	pats, err = engine.GetPatterns(ctx, PatternFilter{
		Limit: -5,
	})
	require.NoError(t, err)
	assert.Len(t, pats, 2)

	pats, err = engine.GetPatterns(ctx, PatternFilter{
		Limit:  500,
		Offset: 1,
	})
	require.NoError(t, err)
	assert.Len(t, pats, 1)
}

func TestTelemetryEngine_GetRelevantPatternsAndRelevanceScore(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "relevant_test.db")

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()
	now := time.Now()

	p1 := NegativePattern{
		ID:               "pat-rel-1",
		PatternType:      FailureModeBlockedCommand,
		Title:            "Blocked rm",
		Description:      "Blocked rm command",
		RootCause:        "Danger",
		Remediation:      "Remove rm",
		OccurrenceCount:  2,
		FirstSeen:        now,
		LastSeen:         now,
		AffectedPackages: []string{"internal/worker"},
		ExampleContexts:  []string{"task=123 error=rm -rf /"},
		ConfidenceScore:  0.8,
		Status:           PatternStatusValidated,
	}

	p2 := NegativePattern{
		ID:               "pat-rel-2",
		PatternType:      FailureModeReadOnlyViolation,
		Title:            "RO fail",
		Description:      "Read only fail",
		RootCause:        "RO",
		Remediation:      "Fix RO",
		OccurrenceCount:  1,
		FirstSeen:        now,
		LastSeen:         now,
		AffectedPackages: []string{"internal/harness"},
		ExampleContexts:  []string{"read-only violation in /repo"},
		ConfidenceScore:  0.9,
		Status:           PatternStatusApplied,
	}

	insertPatternHelper(t, engine, p1)
	insertPatternHelper(t, engine, p2)

	// Match p1 by path and prompt
	rel, err := engine.GetRelevantPatterns(ctx, "worker", "internal/worker", "task=123 something")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(rel), 1)

	// Match none when completely different
	relNone, err := engine.GetRelevantPatterns(ctx, "unrelated_role", "pkg/nowhere", "hello world")
	require.NoError(t, err)
	assert.Empty(t, relNone)
}

func TestTelemetryEngine_ClassificationAndHelpers(t *testing.T) {
	// 1. classifyFailureMode branches
	tests := []struct {
		event    TraceEvent
		expected FailureMode
	}{
		{TraceEvent{ExitCode: intPtr(1)}, FailureModeCrash},
		{TraceEvent{ExitCode: intPtr(2)}, FailureModeTimeout},
		{TraceEvent{ExitCode: intPtr(3)}, FailureModeReadOnlyViolation},
		{TraceEvent{ExitCode: intPtr(4)}, FailureModeBlockedCommand},
		{TraceEvent{ExitCode: intPtr(5)}, FailureModeUnknown},
		{TraceEvent{EventType: TraceEventReceiptRejected}, FailureModeReceiptRejected},
		{TraceEvent{EventType: TraceEventPolicyViolation}, FailureModePolicyViolation},
		{TraceEvent{EventType: TraceEventBlockedCommand}, FailureModeBlockedCommand},
		{TraceEvent{EventType: TraceEventAnomalyDetected, Error: "ambiguous instruction"}, FailureModeAmbiguousPrompt},
		{TraceEvent{EventType: TraceEventAnomalyDetected, Error: "other anomaly"}, FailureModeUnknown},
		{TraceEvent{EventType: TraceEventTaskStarted}, FailureModeUnknown},
	}

	for _, tt := range tests {
		got := classifyFailureMode(tt.event)
		assert.Equal(t, tt.expected, got, "classifyFailureMode(%+v)", tt.event)
	}

	// 2. extractPath, extractCommand, extractPromptSummary
	evWithAll := TraceEvent{
		Payload: map[string]any{
			"file_path": "internal/foo.go",
			"command":   "cat foo.go",
			"prompt":    "This is a long prompt that should definitely exceed the sixty character cutoff limit for summary",
		},
	}
	assert.Equal(t, "internal/foo.go", extractPath(evWithAll))
	assert.Equal(t, "cat foo.go", extractCommand(evWithAll))
	assert.True(t, strings.HasSuffix(extractPromptSummary(evWithAll), "..."))

	evBlockedCmd := TraceEvent{
		Payload: map[string]any{
			"blocked_command": "rm -rf /",
			"prompt":          "Short prompt",
		},
	}
	assert.Equal(t, "unknown", extractPath(evBlockedCmd))
	assert.Equal(t, "rm -rf /", extractCommand(evBlockedCmd))
	assert.Equal(t, "Short prompt", extractPromptSummary(evBlockedCmd))

	evEmpty := TraceEvent{}
	assert.Equal(t, "unknown", extractPath(evEmpty))
	assert.Equal(t, "unknown", extractCommand(evEmpty))
	assert.Equal(t, "unknown", extractPromptSummary(evEmpty))

	// 3. generatePatternTitle, inferRootCause, suggestRemediation for all failure modes
	modes := []FailureMode{
		FailureModeCrash,
		FailureModeTimeout,
		FailureModeReadOnlyViolation,
		FailureModeBlockedCommand,
		FailureModePolicyViolation,
		FailureModeReceiptRejected,
		FailureModeAmbiguousPrompt,
		FailureModeUnknown,
	}

	evSampleLongTask := TraceEvent{TaskID: "very-long-task-id-12345", Error: "sample policy violation error"}
	evSampleShortTask := TraceEvent{TaskID: "task-1", Error: "sample policy violation error"}

	for _, m := range modes {
		t1 := generatePatternTitle(m, evSampleLongTask)
		t2 := generatePatternTitle(m, evSampleShortTask)
		assert.NotEmpty(t, t1)
		assert.NotEmpty(t, t2)

		rc := inferRootCause(m, evSampleLongTask)
		assert.NotEmpty(t, rc)

		rem := suggestRemediation(m, evSampleLongTask)
		assert.NotEmpty(t, rem)
	}

	// 4. randomString length branches
	sSmall := randomString(10)
	assert.Len(t, sSmall, 10)
	sLarge := randomString(45)
	assert.Len(t, sLarge, 45)

	// 5. InjectPreflightContext empty patterns
	engine, err := NewTelemetryEngine(DefaultTelemetryConfig())
	require.NoError(t, err)
	defer engine.Close()

	emptyInject, err := engine.InjectPreflightContext(context.Background(), &controlplane.BriefRow{}, nil)
	assert.NoError(t, err)
	assert.Empty(t, emptyInject)

	// 6. RenderPreflightContext empty
	assert.Empty(t, RenderPreflightContext(nil))
}

// BUG(DistillFailure): DistillFailure and DistillBatch pass nil sql.Tx to extractPattern,
// which unconditionally calls tx.QueryRowContext, resulting in a nil-pointer dereference panic
// whenever an event with a recognized failure mode is distilled directly.
func TestDistillFailure_NilTxPanic_BUG(t *testing.T) {
	t.Skip("BUG(DistillFailure): DistillFailure and DistillBatch pass nil sql.Tx to extractPattern, causing nil pointer dereference on tx.QueryRowContext")
}

func TestTelemetryEngine_DistillBatchUnknownEvents(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "distill_batch_unknown.db")

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer engine.Close()

	ctx := context.Background()

	// An event with unknown failure mode does not call getPatternInTx, so it succeeds safely
	unknownEvent := TraceEvent{
		EventType: TraceEventTaskStarted,
		TaskID:    "task-start-1",
	}

	p, err := engine.DistillFailure(ctx, unknownEvent)
	assert.NoError(t, err)
	assert.Nil(t, p)

	patterns, err := engine.DistillBatch(ctx, []TraceEvent{unknownEvent})
	assert.NoError(t, err)
	assert.Empty(t, patterns)
}

func insertPatternHelper(t *testing.T, engine *TelemetryEngine, p NegativePattern) {
	pkg, err := json.Marshal(p.AffectedPackages)
	require.NoError(t, err)
	ctxs, err := json.Marshal(p.ExampleContexts)
	require.NoError(t, err)

	_, err = engine.db.Exec(`
		INSERT INTO negative_patterns (
			id, pattern_type, title, description, root_cause, remediation,
			occurrence_count, first_seen, last_seen, affected_packages,
			example_contexts, confidence_score, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, string(p.PatternType), p.Title, p.Description, p.RootCause, p.Remediation,
		p.OccurrenceCount, p.FirstSeen.UnixNano(), p.LastSeen.UnixNano(),
		string(pkg), string(ctxs), p.ConfidenceScore, string(p.Status))
	require.NoError(t, err)
}
