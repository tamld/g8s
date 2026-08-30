package sleep

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSleepStore(t *testing.T) {
	tempDir := t.TempDir()
	store := NewFileStore(filepath.Join(tempDir, "sleep_state.json"))
	ctx := context.Background()

	if store.IsSleeping(ctx) {
		t.Errorf("expected not sleeping initially")
	}

	sleepStart := time.Now().UTC().Add(-2 * time.Hour)
	state := &SleepState{
		ID:           "sleep-123",
		SleepStart:   sleepStart,
		Until:        "09:00",
		Operator:     "tamld",
		CriticalOnly: true,
		ReportFormat: "voice",
	}

	if err := store.RecordSleep(ctx, state); err != nil {
		t.Fatalf("RecordSleep failed: %v", err)
	}

	if !store.IsSleeping(ctx) {
		t.Errorf("expected IsSleeping = true")
	}

	got, err := store.GetSleepState(ctx)
	if err != nil {
		t.Fatalf("GetSleepState failed: %v", err)
	}
	if got.Operator != "tamld" || got.Until != "09:00" || !got.CriticalOnly {
		t.Errorf("unexpected state: %+v", got)
	}

	wake, err := store.RecordWake(ctx)
	if err != nil {
		t.Fatalf("RecordWake failed: %v", err)
	}
	if wake.Sleeping {
		t.Errorf("expected wake.Sleeping = false")
	}
	if store.IsSleeping(ctx) {
		t.Errorf("expected IsSleeping = false after wake")
	}
}

func TestEventCollector(t *testing.T) {
	tempDir := t.TempDir()
	collector := NewFileCollector(filepath.Join(tempDir, "events.jsonl"))
	ctx := context.Background()

	t1 := time.Now().UTC().Add(-10 * time.Minute)
	t2 := time.Now().UTC().Add(-5 * time.Minute)

	_ = collector.Collect(ctx, Event{
		Type:      EventReceiptSuccess,
		Severity:  SeverityInfo,
		SessionID: "sess-1",
		Message:   "Task finished cleanly",
		Timestamp: t1,
	})

	_ = collector.Collect(ctx, Event{
		Type:      EventWorkerDead,
		Severity:  SeverityCritical,
		SessionID: "sess-2",
		Message:   "Process terminated unexpectedly",
		Timestamp: t2,
	})

	allEvents, err := collector.ListEventsSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListEventsSince failed: %v", err)
	}
	if len(allEvents) != 2 {
		t.Fatalf("expected 2 events, got %d", len(allEvents))
	}

	recentEvents, err := collector.ListEventsSince(ctx, t2)
	if err != nil {
		t.Fatalf("ListEventsSince recent failed: %v", err)
	}
	if len(recentEvents) != 1 {
		t.Fatalf("expected 1 recent event, got %d", len(recentEvents))
	}
	if recentEvents[0].SessionID != "sess-2" {
		t.Errorf("expected sess-2, got %s", recentEvents[0].SessionID)
	}
}

func TestEventRouterSleepPolicy(t *testing.T) {
	var buf bytes.Buffer
	router := NewStderrRouter(&buf)
	ctx := context.Background()

	// 1. When sleeping: non-critical is ignored
	nonCrit := Event{Type: EventHeartbeatStale, Severity: SeverityWarning, Message: "Stale heartbeat"}
	if err := router.Route(ctx, nonCrit, true); err != nil {
		t.Fatalf("Route failed: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for non-critical when sleeping, got %s", buf.String())
	}

	// 2. When sleeping: critical is routed immediately
	crit := Event{Type: EventWorkerDead, Severity: SeverityCritical, Message: "Worker died"}
	if err := router.Route(ctx, crit, true); err != nil {
		t.Fatalf("Route critical failed: %v", err)
	}
	if !strings.Contains(buf.String(), "CRITICAL ALERT") {
		t.Errorf("expected critical alert in buffer, got: %s", buf.String())
	}

	// 3. When awake: all are routed
	buf.Reset()
	if err := router.Route(ctx, nonCrit, false); err != nil {
		t.Fatalf("Route awake failed: %v", err)
	}
	if !strings.Contains(buf.String(), "WARNING") {
		t.Errorf("expected warning in buffer when awake, got: %s", buf.String())
	}
}

func TestTelegramRouter(t *testing.T) {
	var receivedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		receivedBody = buf.String()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	router := NewTelegramRouter("token123", "chat999")
	router.BaseURL = ts.URL

	crit := Event{
		Type:      EventBranchConflict,
		Severity:  SeverityCritical,
		SessionID: "sess-conflict",
		Message:   "Merge conflict on main",
	}

	if err := router.Route(context.Background(), crit, true); err != nil {
		t.Fatalf("Telegram route failed: %v", err)
	}

	if !strings.Contains(receivedBody, "chat999") || !strings.Contains(receivedBody, "Merge conflict on main") {
		t.Errorf("unexpected Telegram request payload: %s", receivedBody)
	}
}

func TestVoiceSummaryGeneration(t *testing.T) {
	now := time.Now().UTC()
	start := now.Add(-4*time.Hour - 32*time.Minute)

	state := &SleepState{
		SleepStart: start,
		Operator:   "tamld",
	}

	events := []Event{
		{
			Type:      EventReceiptSuccess,
			Severity:  SeverityInfo,
			SessionID: "sess-1",
			Message:   "Issue #165 merged",
			Timestamp: start.Add(1 * time.Hour),
		},
		{
			Type:      EventReceiptSuccess,
			Severity:  SeverityInfo,
			SessionID: "sess-2",
			Message:   "Issue #166 merged",
			Timestamp: start.Add(2 * time.Hour),
		},
		{
			Type:      EventReceiptSuccess,
			Severity:  SeverityInfo,
			SessionID: "sess-3",
			Message:   "Issue #167 completed",
			Timestamp: start.Add(3 * time.Hour),
		},
		{
			Type:      EventReceiptFailure,
			Severity:  SeverityWarning,
			SessionID: "sess-4",
			Message:   "gofmt error on commit 3; automatically rebased and pushed",
			Timestamp: start.Add(3*time.Hour + 30*time.Minute),
		},
	}

	summary := GenerateVoiceSummary(state, events, now)

	if summary.TotalSessions != 4 {
		t.Errorf("expected 4 sessions, got %d", summary.TotalSessions)
	}
	if summary.Succeeded != 3 {
		t.Errorf("expected 3 succeeded, got %d", summary.Succeeded)
	}
	if summary.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", summary.Failed)
	}

	if len(summary.Paragraphs) > 4 {
		t.Errorf("expected <= 4 paragraphs, got %d", len(summary.Paragraphs))
	}

	for i, p := range summary.Paragraphs {
		words := len(strings.Fields(p))
		if words > 200 {
			t.Errorf("paragraph %d exceeds 200 words (%d words)", i+1, words)
		}
	}

	if !strings.Contains(summary.VoiceText, "While you were away") {
		t.Errorf("expected natural voice intro, got: %s", summary.VoiceText)
	}
	if !strings.Contains(summary.VoiceText, "4h 32m") {
		t.Errorf("expected 4h 32m in voice text, got: %s", summary.VoiceText)
	}
}
