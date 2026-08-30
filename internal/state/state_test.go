package state

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestStateApplyTransitions(t *testing.T) {
	now := time.Now()

	// 1. Task transitions
	next, err := Apply(SubjectTask, TaskStateQueued, TaskEventClaim, nil, now)
	if err != nil || next != TaskStateLeased {
		t.Fatalf("expected QUEUED -> LEASED on claim, got %v (err: %v)", next, err)
	}

	next, err = Apply(SubjectTask, TaskStateLeased, TaskEventStart, nil, now)
	if err != nil || next != TaskStateRunning {
		t.Fatalf("expected LEASED -> RUNNING on start, got %v (err: %v)", next, err)
	}

	next, err = Apply(SubjectTask, TaskStateRunning, TaskEventSucceed, nil, now)
	if err != nil || next != TaskStateSucceeded {
		t.Fatalf("expected RUNNING -> SUCCEEDED on succeed, got %v (err: %v)", next, err)
	}

	// Invalid transition
	_, err = Apply(SubjectTask, TaskStateSucceeded, TaskEventClaim, nil, now)
	if err == nil {
		t.Fatalf("expected error transitioning from terminal SUCCEEDED state")
	}

	// 2. Orchestrator transitions
	next, err = Apply(SubjectOrchestrator, OrchestratorStatePlan, OrchestratorEventSpawn, nil, now)
	if err != nil || next != OrchestratorStateSpawn {
		t.Fatalf("expected PLAN -> SPAWN on spawn, got %v (err: %v)", next, err)
	}

	next, err = Apply(SubjectOrchestrator, OrchestratorStateSpawn, OrchestratorEventMonitor, nil, now)
	if err != nil || next != OrchestratorStateMonitor {
		t.Fatalf("expected SPAWN -> MONITOR on monitor, got %v (err: %v)", next, err)
	}

	next, err = Apply(SubjectOrchestrator, OrchestratorStateMonitor, OrchestratorEventRetry, nil, now)
	if err != nil || next != OrchestratorStateSpawn {
		t.Fatalf("expected MONITOR -> SPAWN on retry, got %v (err: %v)", next, err)
	}

	// 3. Brief transitions
	next, err = Apply(SubjectBrief, BriefStateActive, BriefEventConsume, nil, now)
	if err != nil || next != BriefStateConsumed {
		t.Fatalf("expected active -> consumed on consume, got %v (err: %v)", next, err)
	}

	// 4. Heartbeat transitions
	next, err = Apply(SubjectHeartbeat, HeartbeatStateRunning, HeartbeatEventPause, nil, now)
	if err != nil || next != HeartbeatStateIdle {
		t.Fatalf("expected running -> idle on pause, got %v (err: %v)", next, err)
	}

	// 5. Unknown subject
	_, err = Apply("unknown-subject", "foo", "bar", nil, now)
	if !errors.Is(err, ErrSubjectNotFound) {
		t.Fatalf("expected ErrSubjectNotFound, got %v", err)
	}
}

func TestPredicateGuard(t *testing.T) {
	now := time.Now()
	subj := Subject("guarded-subject")

	Register(Transition{
		Subject: subj,
		From:    "INIT",
		To:      "DONE",
		Event:   "FINISH",
		Predicate: func(data any) error {
			if s, ok := data.(string); ok && s == "allow" {
				return nil
			}
			return errors.New("not allowed")
		},
	})

	// Predicate failure
	_, err := Apply(subj, "INIT", "FINISH", "disallow", now)
	if !errors.Is(err, ErrPredicateFailed) {
		t.Fatalf("expected ErrPredicateFailed, got %v", err)
	}

	// Predicate success
	next, err := Apply(subj, "INIT", "FINISH", "allow", now)
	if err != nil || next != "DONE" {
		t.Fatalf("expected DONE, got %s (err: %v)", next, err)
	}
}

func TestValidTransitions(t *testing.T) {
	trans := ValidTransitions(SubjectBrief, BriefStateActive)
	if len(trans) != 2 {
		t.Fatalf("expected 2 transitions from Brief active, got %d", len(trans))
	}
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite in-memory db: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS event_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		subject_id TEXT NOT NULL,
		subject TEXT NOT NULL DEFAULT '',
		from_state TEXT NOT NULL,
		to_state TEXT NOT NULL,
		event TEXT NOT NULL,
		actor TEXT NOT NULL,
		reason TEXT NOT NULL,
		ts REAL NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_event_log_subject ON event_log(subject_id, id ASC);`)
	if err != nil {
		t.Fatalf("failed to create event_log schema: %v", err)
	}
	return db
}

func TestEventLogAndReplay(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	subjectID := "task-100"
	t0 := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 30, 1, 1, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 30, 1, 2, 0, 0, time.UTC)

	err := Log(ctx, db, subjectID, SubjectTask, TaskStateQueued, TaskStateLeased, TaskEventClaim, "worker-1", "claimed task", t0)
	if err != nil {
		t.Fatalf("Log event 1 failed: %v", err)
	}

	err = Log(ctx, db, subjectID, SubjectTask, TaskStateLeased, TaskStateRunning, TaskEventStart, "worker-1", "started execution", t1)
	if err != nil {
		t.Fatalf("Log event 2 failed: %v", err)
	}

	err = Log(ctx, db, subjectID, SubjectTask, TaskStateRunning, TaskStateSucceeded, TaskEventSucceed, "worker-1", "execution completed", t2)
	if err != nil {
		t.Fatalf("Log event 3 failed: %v", err)
	}

	// Replay all
	records, err := Replay(ctx, db, subjectID)
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	if records[0].FromState != TaskStateQueued || records[0].ToState != TaskStateLeased {
		t.Errorf("record 0 state mismatch: %v -> %v", records[0].FromState, records[0].ToState)
	}
	if records[2].ToState != TaskStateSucceeded {
		t.Errorf("record 2 state mismatch: want SUCCEEDED, got %v", records[2].ToState)
	}

	// Show limited
	lastEvents, err := Show(ctx, db, subjectID, 2)
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if len(lastEvents) != 2 {
		t.Fatalf("expected 2 records, got %d", len(lastEvents))
	}
	// Chronological check
	if lastEvents[0].Event != TaskEventStart || lastEvents[1].Event != TaskEventSucceed {
		t.Errorf("Show chronological order mismatch: %v, %v", lastEvents[0].Event, lastEvents[1].Event)
	}
}
