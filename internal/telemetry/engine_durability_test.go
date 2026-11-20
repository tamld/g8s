package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// waitFor polls cond until it holds or the deadline expires; fails the test
// on timeout with the last observed state via failMsg.
func waitFor(t *testing.T, timeout time.Duration, failMsg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s (timeout after %v)", failMsg, timeout)
}

func countEventsByTask(t *testing.T, e *TelemetryEngine, taskID string) int {
	t.Helper()
	events, err := e.QueryEvents(context.Background(), TraceFilter{TaskID: &taskID, Limit: 200})
	require.NoError(t, err)
	return len(events)
}

func mustExec(t *testing.T, e *TelemetryEngine, query string, args ...any) sql.Result {
	t.Helper()
	res, err := e.db.Exec(query, args...)
	require.NoError(t, err)
	return res
}

func mustCount(t *testing.T, e *TelemetryEngine, query string, args ...any) int {
	t.Helper()
	var n int
	err := e.db.QueryRow(query, args...).Scan(&n)
	require.NoError(t, err)
	return n
}

// -----------------------------------------------------------------------------
// C-3: flush must retain the batch on persist failure (no silent event loss)
// -----------------------------------------------------------------------------

// TestTelemetryFlushRetainsEventsOnPersistFailure pins issue #984 C-3: when
// persistBatch fails, the flush path must NOT clear the in-memory batch.
// Callers drop flush errors by design (`_ = flush()`), so a cleared batch on
// failure silently drops acknowledged ingestions. The batch must be retained,
// retried on later ticks, and every event must eventually reach the store
// once persistence recovers. Failures must also surface via engine metrics.
func TestTelemetryFlushRetainsEventsOnPersistFailure(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "telemetry_c3_retain.db")
	config.BatchSize = 2
	config.FlushInterval = 10 * time.Millisecond

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer func() { _ = engine.Close() }()

	ctx := context.Background()

	// Inject persistence failure before any event is ingested (batch is empty
	// at construction, so no flush can race the injection).
	engine.setPersistFn(func(ctx context.Context, events []TraceEvent) error {
		return fmt.Errorf("injected persist outage")
	})

	const total = 5
	for i := 0; i < total; i++ {
		event := TraceEvent{
			TaskID:    "c3-task",
			EventType: TraceEventTaskCompleted,
			Timestamp: time.Now(),
			Payload:   map[string]any{"i": i},
		}
		require.NoError(t, engine.IngestEvent(ctx, event))
	}

	// Let several ticks attempt (and fail) to flush.
	waitFor(t, 2*time.Second, "engine never recorded a persist failure", func() bool {
		return engine.Metrics().PersistFailures > 0
	})

	// While persistence is down, nothing may have been dropped: the batch is
	// retained in memory, so the store stays empty but the events are alive.
	assert.Equal(t, 0, countEventsByTask(t, engine, "c3-task"), "store should be empty during outage")

	// Recover persistence and let the retained batch drain on a later tick.
	engine.setPersistFn(engine.persistBatch)
	waitFor(t, 5*time.Second, "retained batch never drained after persistence recovered",
		func() bool { return countEventsByTask(t, engine, "c3-task") == total })

	assert.Len(t, engine.queryAllTaskEvents(t, "c3-task"), total)
}

// TestTelemetryFlushSpillsOverflowToOutboxAndReplays pins the bounded-retention
// half of issue #984 C-3: a prolonged outage must not grow memory without
// limit. Once the retained batch exceeds 2×BatchSize, the oldest events spill
// to a JSONL outbox file next to the DB; after recovery the outbox is replayed
// (idempotent upsert) and removed, so no acknowledged event is lost.
func TestTelemetryFlushSpillsOverflowToOutboxAndReplays(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "telemetry_c3_outbox.db")
	config.BatchSize = 2
	config.FlushInterval = 10 * time.Millisecond

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)

	ctx := context.Background()

	engine.setPersistFn(func(ctx context.Context, events []TraceEvent) error {
		return fmt.Errorf("injected persist outage")
	})

	// BatchSize=2 → retained cap = 4; ingest 7 events so exactly 3 spill.
	const total = 7
	const wantSpilled = 3
	for i := 0; i < total; i++ {
		event := TraceEvent{
			TaskID:    "c3-outbox-task",
			EventType: TraceEventTaskCompleted,
			Timestamp: time.Now(),
			Payload:   map[string]any{"i": i},
		}
		require.NoError(t, engine.IngestEvent(ctx, event))
	}

	waitFor(t, 2*time.Second, "oldest overflow never spilled to the JSONL outbox", func() bool {
		return engine.Metrics().OutboxSpilledEvents == wantSpilled
	})

	outboxPath := config.DBPath + outboxSuffix
	data, err := os.ReadFile(outboxPath)
	require.NoError(t, err, "outbox file must exist next to the DB during the outage")
	lines := nonEmptyLines(string(data))
	require.Len(t, lines, wantSpilled, "outbox must hold exactly the oldest spilled events")

	// Recover: outbox events replay first, then the retained batch, and the
	// outbox file is removed once its contents are durably persisted.
	engine.setPersistFn(engine.persistBatch)
	waitFor(t, 5*time.Second, "spilled + retained events never persisted after recovery",
		func() bool { return countEventsByTask(t, engine, "c3-outbox-task") == total })

	assert.Equal(t, total, countEventsByTask(t, engine, "c3-outbox-task"))
	_, statErr := os.Stat(outboxPath)
	assert.True(t, os.IsNotExist(statErr), "outbox file must be removed after successful replay")

	// Close cleanly (deferred close already satisfied); explicit check that
	// Close after a successful drain reports no final-flush error.
	require.NoError(t, engine.Close())
}

// -----------------------------------------------------------------------------
// C-4: retention must actually delete expired rows
// -----------------------------------------------------------------------------

// TestTelemetryRetentionDeletesExpiredRows pins issue #984 C-4: RetentionPeriod
// is validated at construction but nothing ever deleted rows. The batch
// processor tick must DELETE rows older than now-RetentionPeriod from
// telemetry_events and from the timestamped side tables (negative_patterns via
// last_seen, hitl_feedback and k_yield_samples via created_at), while fresh
// rows survive.
func TestTelemetryRetentionDeletesExpiredRows(t *testing.T) {
	config := DefaultTelemetryConfig()
	config.DBPath = filepath.Join(t.TempDir(), "telemetry_c4_retention.db")
	config.BatchSize = 1
	config.FlushInterval = 10 * time.Millisecond
	config.RetentionPeriod = time.Minute // fresh rows (now) survive; ancient rows do not

	engine, err := NewTelemetryEngine(config)
	require.NoError(t, err)
	defer func() { _ = engine.Close() }()

	now := time.Now()
	ancient := now.Add(-2 * time.Hour).UnixNano()

	// Ancients: one row per timestamped table, all well past retention.
	mustExec(t, engine, `INSERT INTO telemetry_events (id, task_id, event_type, timestamp, payload)
		VALUES ('ret-old', 'ret-old-task', 'task_failed', ?, '{}')`, ancient)
	mustExec(t, engine, `INSERT INTO negative_patterns (id, pattern_type, title, description, root_cause,
		remediation, occurrence_count, first_seen, last_seen, affected_packages, example_contexts,
		confidence_score, status)
		VALUES ('neg-old', 'crash', 't', 'd', 'rc', 'rem', 1, ?, ?, '[]', '[]', 0.5, 'DRAFT')`, ancient, ancient)
	mustExec(t, engine, `INSERT INTO hitl_feedback (id, rule_id, verdict, actor, created_at)
		VALUES ('hitl-old', 'rule-a', 'TP', 'operator', ?)`, ancient)
	mustExec(t, engine, `INSERT INTO k_yield_samples (id, task_id, package_name, active_k, first_pass_ok, created_at)
		VALUES ('ky-old', 'task-1', 'pkg', 2, 1, ?)`, ancient)

	// Fresh rows: ingested event plus one fresh row per side table.
	require.NoError(t, engine.IngestEvent(context.Background(), TraceEvent{
		ID:        "ret-fresh-evt",
		TaskID:    "ret-fresh-task",
		EventType: TraceEventTaskCompleted,
		Timestamp: now,
		Payload:   map[string]any{"fresh": true},
	}))
	mustExec(t, engine, `INSERT INTO negative_patterns (id, pattern_type, title, description, root_cause,
		remediation, occurrence_count, first_seen, last_seen, affected_packages, example_contexts,
		confidence_score, status)
		VALUES ('neg-fresh', 'crash', 't', 'd', 'rc', 'rem', 1, ?, ?, '[]', '[]', 0.5, 'DRAFT')`,
		now.UnixNano(), now.UnixNano())
	mustExec(t, engine, `INSERT INTO hitl_feedback (id, rule_id, verdict, actor, created_at)
		VALUES ('hitl-fresh', 'rule-a', 'TP', 'operator', ?)`, now.UnixNano())
	mustExec(t, engine, `INSERT INTO k_yield_samples (id, task_id, package_name, active_k, first_pass_ok, created_at)
		VALUES ('ky-fresh', 'task-2', 'pkg', 2, 1, ?)`, now.UnixNano())

	// The very first tick must sweep the ancients away.
	waitFor(t, 3*time.Second, "expired rows were never deleted by the retention sweep", func() bool {
		return mustCount(t, engine, `SELECT COUNT(*) FROM telemetry_events WHERE id = 'ret-old'`) == 0 &&
			mustCount(t, engine, `SELECT COUNT(*) FROM negative_patterns WHERE id = 'neg-old'`) == 0 &&
			mustCount(t, engine, `SELECT COUNT(*) FROM hitl_feedback WHERE id = 'hitl-old'`) == 0 &&
			mustCount(t, engine, `SELECT COUNT(*) FROM k_yield_samples WHERE id = 'ky-old'`) == 0
	})

	// Fresh rows must survive the same sweep that removed the ancients.
	assert.Equal(t, 1, mustCount(t, engine, `SELECT COUNT(*) FROM telemetry_events WHERE id = 'ret-fresh-evt'`))
	assert.Equal(t, 1, mustCount(t, engine, `SELECT COUNT(*) FROM negative_patterns WHERE id = 'neg-fresh'`))
	assert.Equal(t, 1, mustCount(t, engine, `SELECT COUNT(*) FROM hitl_feedback WHERE id = 'hitl-fresh'`))
	assert.Equal(t, 1, mustCount(t, engine, `SELECT COUNT(*) FROM k_yield_samples WHERE id = 'ky-fresh'`))

	// And the ingested fresh event remains queryable through the public API.
	assert.Equal(t, 1, countEventsByTask(t, engine, "ret-fresh-task"))
}

// -----------------------------------------------------------------------------
// Small helpers shared by the durability tests
// -----------------------------------------------------------------------------

func (e *TelemetryEngine) queryAllTaskEvents(t *testing.T, taskID string) []TraceEvent {
	t.Helper()
	events, err := e.EventsByTask(context.Background(), taskID)
	require.NoError(t, err)
	return events
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
