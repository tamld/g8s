package controlplane

// Red tests for #986 part-2 batch 2 (blind-audit registries A-9/C-14, A-10).
//
// A-9/C-14 — retry budget not durable + non-transactional resubmit:
//   1. globalRetryTracker (retry.go) is a per-process in-memory map: every
//      coordinator restart resets the hourly retry budget and each concurrent
//      process gets a fresh budget. The budget must live in the store.
//   2. ResubmitTask computed not_before from the clock OUTSIDE any
//      transaction, so two concurrent resubmits embedded different not_before
//      values into otherwise-identical payloads; the loser's SubmitTask then
//      hit "idempotency_key already exists with a different request" (a raw
//      collision error) instead of the dedup path.
//
// A-10 — UpdateFalseEscalationRate read-modify-write without a transaction:
//   concurrent false-escalation reports lose increments.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestResubmitTask_ConcurrentResubmitsTakeDedupPath: with a live (non-frozen)
// clock, N concurrent resubmits of the same failed task must all succeed with
// the same retry task ID — the losers take the dedup path, never a raw
// idempotency-key collision error.
func TestResubmitTask_ConcurrentResubmitsTakeDedupPath(t *testing.T) {
	store, _ := newTestStore(t) // real clock — not_before differs per call
	ctx := context.Background()

	req := SubmitTaskRequest{
		IdempotencyKey: "conc-live-clock-orig",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"concurrent live-clock resubmit"}`),
	}
	orig, err := store.SubmitTask(ctx, req)
	if err != nil {
		t.Fatalf("submit orig: %v", err)
	}
	// completed_at = 0 forces baseTime = the per-call clock value — the exact
	// production interleaving that made concurrent payloads diverge.
	failTaskInDB(t, store.db, orig.TaskID, "context deadline exceeded", 0)

	opts := ResubmitOpts{Enabled: true, MaxPerTask: 2, MaxPerHour: 10}

	const goroutines = 10
	var wg sync.WaitGroup
	results := make([]string, goroutines)
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			id, rerr := store.ResubmitTask(ctx, orig.TaskID, opts)
			results[idx] = id
			errs[idx] = rerr
		}(i)
	}
	wg.Wait()

	for i, rerr := range errs {
		if rerr != nil {
			t.Fatalf("goroutine %d: concurrent resubmit fell off the dedup path: %v", i, rerr)
		}
	}
	winner := results[0]
	if winner == "" {
		t.Fatal("expected non-empty retry task id")
	}
	for i := 1; i < goroutines; i++ {
		if results[i] != winner {
			t.Errorf("goroutine %d returned task %s, want identical %s", i, results[i], winner)
		}
	}

	var childCount int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM tasks WHERE parent_task_id = ?`, orig.TaskID).Scan(&childCount); err != nil {
		t.Fatalf("count child tasks: %v", err)
	}
	if childCount != 1 {
		t.Errorf("childCount = %d, want exactly 1", childCount)
	}

	// Deduplicated resubmits must not consume the hourly budget.
	if got := hourlyRetryBudgetCount(t, store); got != 1 {
		t.Errorf("durable hourly retry count = %d, want exactly 1 (dedup calls must not consume budget)", got)
	}
}

func hourlyRetryBudgetCount(t *testing.T, s *Store) int {
	t.Helper()
	var count int
	err := s.db.QueryRow(`SELECT count FROM retry_budgets WHERE scope = ?`, retryBudgetScopeHourly).Scan(&count)
	if err != nil {
		t.Fatalf("read retry_budgets: %v", err)
	}
	return count
}

// TestResubmitTask_HourlyBudgetSurvivesRestart: the hourly retry budget is
// persisted in the store, so a simulated coordinator restart (close + reopen
// of the same DB) must NOT reset it.
func TestResubmitTask_HourlyBudgetSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "control-plane.sqlite3")
	clock := newFakeClock()
	ctx := context.Background()

	store1, err := NewControlPlane(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("NewControlPlane #1: %v", err)
	}
	opts := ResubmitOpts{Enabled: true, MaxPerTask: 5, MaxPerHour: 2}

	submitAndFail := func(s *Store, key string) string {
		t.Helper()
		req := SubmitTaskRequest{
			IdempotencyKey: key,
			Priority:       1,
			MaxAttempts:    1,
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        json.RawMessage(`{"prompt":"budget restart probe"}`),
		}
		task, serr := s.SubmitTask(ctx, req)
		if serr != nil {
			t.Fatalf("submit %s: %v", key, serr)
		}
		failTaskInDB(t, s.db, task.TaskID, "503 service unavailable", float64(clock.Now().UnixNano())/1e9)
		return task.TaskID
	}

	for i := 0; i < 2; i++ {
		id := submitAndFail(store1, "restart-budget-"+string(rune('a'+i)))
		if _, rerr := store1.ResubmitTask(ctx, id, opts); rerr != nil {
			t.Fatalf("resubmit %d: %v", i+1, rerr)
		}
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("close store1: %v", err)
	}

	// Simulated restart: a brand-new Store over the same database file.
	store2, err := NewControlPlane(dbPath, clock.Now)
	if err != nil {
		t.Fatalf("NewControlPlane #2: %v", err)
	}
	defer store2.Close()

	if got := hourlyRetryBudgetCount(t, store2); got != 2 {
		t.Fatalf("durable hourly retry count after restart = %d, want 2", got)
	}

	// The next resubmit must still be refused — the budget did not reset.
	id3 := submitAndFail(store2, "restart-budget-third")
	_, err = store2.ResubmitTask(ctx, id3, opts)
	if err == nil {
		t.Fatal("resubmit after simulated restart succeeded — the hourly retry budget was reset (in-memory tracker bug)")
	}
	if !strings.Contains(err.Error(), "hourly retry budget exceeded") {
		t.Errorf("error = %v, want containing 'hourly retry budget exceeded'", err)
	}

	// After the hour window elapses the budget opens again.
	clock.Advance(1*time.Hour + 1*time.Second)
	if _, rerr := store2.ResubmitTask(ctx, id3, opts); rerr != nil {
		t.Fatalf("resubmit after window expiry: %v", rerr)
	}
}

// TestUpdateFalseEscalationRate_ConcurrentUpdatesPreserveIncrements (A-10):
// concurrent false-escalation reports must each be counted — the final rate
// reflects every increment, not the last writer's stale read.
func TestUpdateFalseEscalationRate_ConcurrentUpdatesPreserveIncrements(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if err := store.CreateSupervisorTask(ctx, SupervisorTaskRow{
		ID:           "sup-esc-conc",
		State:        "running",
		EnvelopeJSON: `{"x":1}`,
		CreatedAt:    time.Unix(1_700_000_000, 0),
		UpdatedAt:    time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("create supervisor task: %v", err)
	}

	if err := store.SaveMetrics(ctx, "sup-esc-conc", MetricsRow{
		EscalationCount:     10,
		FalseEscalationRate: 0.0,
	}); err != nil {
		t.Fatalf("seed metrics: %v", err)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = store.UpdateFalseEscalationRate(ctx, "sup-esc-conc", true)
		}(i)
	}
	wg.Wait()
	for i, uerr := range errs {
		if uerr != nil {
			t.Fatalf("update %d: %v", i, uerr)
		}
	}

	m, err := store.GetMetrics(ctx, "sup-esc-conc")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	if m.FalseEscalationRate != 0.8 {
		t.Errorf("FalseEscalationRate = %v, want 0.8 (8 increments over 10 escalations)", m.FalseEscalationRate)
	}
}
