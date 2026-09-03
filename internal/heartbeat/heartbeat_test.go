package heartbeat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRecordRoundtrip(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	store := NewStore(tempDir, clock)

	meta := map[string]any{
		"model":  "gemini-3.8-flash-high",
		"branch": "feat/debt29-worker-heartbeat",
	}

	hb, err := store.Record("agy-1788-6-sub-1", StatusRunning, meta,
		WithPID(12915),
		WithBinary("agy"),
		WithCommandLine("agy -p test"),
		WithCurrentStep("reviewing changes in internal/supervisor/..."),
	)
	if err != nil {
		t.Fatalf("unexpected Record error: %v", err)
	}

	if hb.SessionID != "agy-1788-6-sub-1" {
		t.Errorf("expected session ID agy-1788-6-sub-1, got %s", hb.SessionID)
	}
	if hb.PID != 12915 {
		t.Errorf("expected PID 12915, got %d", hb.PID)
	}
	if hb.Binary != "agy" {
		t.Errorf("expected Binary agy, got %s", hb.Binary)
	}
	if hb.Status != StatusRunning {
		t.Errorf("expected Status running, got %s", hb.Status)
	}
	if hb.CurrentStep != "reviewing changes in internal/supervisor/..." {
		t.Errorf("expected CurrentStep reviewing..., got %s", hb.CurrentStep)
	}

	// Verify read via Status
	readHB, err := store.Status("agy-1788-6-sub-1")
	if err != nil {
		t.Fatalf("unexpected Status error: %v", err)
	}

	if readHB.SessionID != hb.SessionID {
		t.Errorf("expected read session ID %s, got %s", hb.SessionID, readHB.SessionID)
	}
	if readHB.PID != hb.PID {
		t.Errorf("expected read PID %d, got %d", hb.PID, readHB.PID)
	}
	if readHB.Metadata["model"] != "gemini-3.8-flash-high" {
		t.Errorf("expected model metadata gemini-3.8-flash-high, got %v", readHB.Metadata["model"])
	}
}

func TestIsStale(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	currentTime := now
	clock := func() time.Time { return currentTime }

	store := NewStore(tempDir, clock)

	_, err := store.Record("session-stale-test", StatusRunning, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Immediate check: not stale for 60s
	stale, err := store.IsStale("session-stale-test", 60*time.Second)
	if err != nil {
		t.Fatalf("unexpected IsStale error: %v", err)
	}
	if stale {
		t.Errorf("expected fresh session not to be stale")
	}

	// Advance clock by 90s
	currentTime = now.Add(90 * time.Second)

	stale, err = store.IsStale("session-stale-test", 60*time.Second)
	if err != nil {
		t.Fatalf("unexpected IsStale error: %v", err)
	}
	if !stale {
		t.Errorf("expected session to be stale after 90s (threshold 60s)")
	}
}

func TestAtomicWrite(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStore(tempDir, time.Now)

	// Simulate concurrent writes to same session
	var wg sync.WaitGroup
	workers := 10
	iterations := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, err := store.Record("concurrent-session", StatusRunning, map[string]any{
					"worker": workerID,
					"iter":   j,
				})
				if err != nil {
					t.Errorf("concurrent record failed: %v", err)
				}
				// Verify file is always valid JSON at any moment
				data, readErr := os.ReadFile(filepath.Join(tempDir, "concurrent-session.json"))
				if readErr == nil {
					var hb Heartbeat
					if jsonErr := json.Unmarshal(data, &hb); jsonErr != nil {
						t.Errorf("detected corrupt JSON during concurrent write: %v (raw: %s)", jsonErr, string(data))
					}
				}
			}
		}(i)
	}

	wg.Wait()

	// Final verification
	finalHB, err := store.Status("concurrent-session")
	if err != nil {
		t.Fatalf("failed to read final status: %v", err)
	}
	if finalHB.SessionID != "concurrent-session" {
		t.Errorf("expected session ID concurrent-session, got %s", finalHB.SessionID)
	}
}

func TestList(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	store := NewStore(tempDir, clock)

	_, _ = store.Record("session-1", StatusRunning, nil, WithLastUpdate(now.Add(-10*time.Second)))
	_, _ = store.Record("session-2", StatusIdle, nil, WithLastUpdate(now))
	_, _ = store.Record("session-3", StatusFailed, nil, WithLastUpdate(now.Add(-60*time.Second)))

	list, err := store.List()
	if err != nil {
		t.Fatalf("unexpected List error: %v", err)
	}

	if len(list) != 3 {
		t.Fatalf("expected 3 heartbeats, got %d", len(list))
	}

	// Should be sorted by last update descending: session-2, session-1, session-3
	if list[0].SessionID != "session-2" {
		t.Errorf("expected first element session-2, got %s", list[0].SessionID)
	}
	if list[1].SessionID != "session-1" {
		t.Errorf("expected second element session-1, got %s", list[1].SessionID)
	}
	if list[2].SessionID != "session-3" {
		t.Errorf("expected third element session-3, got %s", list[2].SessionID)
	}
}

func TestExpiredNoUpdate(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	currentTime := now
	clock := func() time.Time { return currentTime }

	store := NewStore(tempDir, clock)

	_, _ = store.Record("session-exp", StatusRunning, nil)

	// Check freshness progression
	f1, _ := store.Freshness("session-exp")
	if f1 != FreshnessActive {
		t.Errorf("expected active (<60s), got %s", f1)
	}

	// Advance 70s -> stale (60-300s)
	currentTime = now.Add(70 * time.Second)
	f2, _ := store.Freshness("session-exp")
	if f2 != FreshnessStale {
		t.Errorf("expected stale (60-300s), got %s", f2)
	}

	// Advance 400s -> dead (>300s)
	currentTime = now.Add(400 * time.Second)
	f3, _ := store.Freshness("session-exp")
	if f3 != FreshnessDead {
		t.Errorf("expected dead (>300s), got %s", f3)
	}
}

func TestRecordEventAndEmit(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := NewStore(tempDir, clock)

	// 1. store.RecordEvent
	evt := Event{
		Kind:   "self_review_required",
		Prompt: "What test failed?",
	}
	hb, err := store.RecordEvent("sess-event-1", evt)
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}
	if hb.Metadata["event_kind"] != "self_review_required" {
		t.Errorf("expected event_kind self_review_required, got %v", hb.Metadata["event_kind"])
	}
	if hb.CurrentStep != "What test failed?" {
		t.Errorf("expected current_step 'What test failed?', got %q", hb.CurrentStep)
	}

	// 2. store.Record with *Event and extra RecordOption
	hb2, err := store.Record("sess-event-2", &evt, WithPID(9999))
	if err != nil {
		t.Fatalf("Record with *Event failed: %v", err)
	}
	if hb2.PID != 9999 {
		t.Errorf("expected PID 9999, got %d", hb2.PID)
	}

	// 3. store.Record with nil *Event
	var nilEvt *Event
	hb3, err := store.Record("sess-event-3", nilEvt)
	if err != nil {
		t.Fatalf("Record with nil *Event failed: %v", err)
	}
	if hb3.Status != StatusRunning {
		t.Errorf("expected status running, got %s", hb3.Status)
	}

	// 4. store.Record with empty session error
	_, err = store.Record("", evt)
	if err == nil {
		t.Errorf("expected error on empty session_id, got nil")
	}

	// 5. Default store helpers (RecordEvent, Emit, Record with Event)
	defaultStore = store
	hb4, err := RecordEvent("sess-pkg-1", evt)
	if err != nil {
		t.Fatalf("pkg RecordEvent failed: %v", err)
	}
	if hb4.SessionID != "sess-pkg-1" {
		t.Errorf("expected sess-pkg-1, got %s", hb4.SessionID)
	}

	if err := Emit(context.Background(), "sess-pkg-2", evt); err != nil {
		t.Fatalf("Emit failed: %v", err)
	}

	hb5, err := Record("sess-pkg-3", evt)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if hb5.SessionID != "sess-pkg-3" {
		t.Errorf("expected sess-pkg-3, got %s", hb5.SessionID)
	}
}
