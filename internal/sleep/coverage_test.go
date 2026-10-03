package sleep

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileCollectorCoverage(t *testing.T) {
	tempDir := t.TempDir()
	evPath := filepath.Join(tempDir, "events.jsonl")
	collector := NewFileCollector(evPath)
	ctx := context.Background()

	// Default constructor path
	defCol := NewFileCollector("")
	if defCol.filePath == "" {
		t.Errorf("expected default filePath to be set")
	}

	// 1. Collect event with empty ID, zero timestamp, empty severity
	err := collector.Collect(ctx, Event{
		Type:    EventTaskDispatched,
		Message: "Dispatched without ID or severity",
	})
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// Verify it was written with auto-generated ID, UTC time, and info severity
	events, err := collector.ListEventsSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListEventsSince failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ID == "" {
		t.Errorf("expected generated ID")
	}
	if events[0].Severity != SeverityInfo {
		t.Errorf("expected default severity info, got %s", events[0].Severity)
	}

	// 2. Corrupted lines in event file should be skipped
	f, err := os.OpenFile(evPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	_, _ = f.WriteString("\nnot-valid-json\n\n")
	_ = f.Close()

	events, err = collector.ListEventsSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListEventsSince with corrupt line failed: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 valid event, got %d", len(events))
	}

	// 3. Clear event store
	err = collector.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// 4. ListEventsSince when file does not exist should return nil, nil
	events, err = collector.ListEventsSince(ctx, time.Time{})
	if err != nil || events != nil {
		t.Errorf("expected nil, nil when file missing, got %v, %v", events, err)
	}

	// 5. Collect error when path cannot be created
	badCol := NewFileCollector(filepath.Join(tempDir, "blocker_file", "sub", "events.jsonl"))
	_ = os.WriteFile(filepath.Join(tempDir, "blocker_file"), []byte("data"), 0o644)
	if err := badCol.Collect(ctx, Event{Type: EventTaskDispatched}); err == nil {
		t.Errorf("expected error when Collect cannot create directory")
	}
}

func TestFileStoreCoverage(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "state.json")
	store := NewFileStore(storePath)
	ctx := context.Background()

	// Default constructor path
	defStore := NewFileStore("")
	if defStore.filePath == "" {
		t.Errorf("expected default filePath to be set")
	}

	// 1. RecordSleep with nil state
	if err := store.RecordSleep(ctx, nil); err == nil {
		t.Errorf("expected error when state is nil")
	}

	// 2. RecordSleep with zero SleepStart
	state := &SleepState{ID: "s-1"}
	if err := store.RecordSleep(ctx, state); err != nil {
		t.Fatalf("RecordSleep failed: %v", err)
	}
	if state.SleepStart.IsZero() {
		t.Errorf("expected SleepStart to be populated")
	}

	// 3. RecordWake when state file does not exist
	missingStore := NewFileStore(filepath.Join(tempDir, "nonexistent.json"))
	wakeState, err := missingStore.RecordWake(ctx)
	if err != nil {
		t.Fatalf("RecordWake on missing file failed: %v", err)
	}
	if wakeState.Sleeping {
		t.Errorf("expected Sleeping=false for default wake state")
	}

	// 4. loadUnlocked error on invalid json
	corruptPath := filepath.Join(tempDir, "corrupt.json")
	_ = os.WriteFile(corruptPath, []byte("not json"), 0o644)
	corruptStore := NewFileStore(corruptPath)
	if _, err := corruptStore.GetSleepState(ctx); err == nil {
		t.Errorf("expected error when reading corrupt state")
	}
	if corruptStore.IsSleeping(ctx) {
		t.Errorf("expected IsSleeping=false on corrupt state")
	}

	// 5. RecordSleep failure when directory cannot be created
	badStore := NewFileStore(filepath.Join(tempDir, "blocker", "state.json"))
	_ = os.WriteFile(filepath.Join(tempDir, "blocker"), []byte("data"), 0o644)
	if err := badStore.RecordSleep(ctx, &SleepState{ID: "s-bad"}); err == nil {
		t.Errorf("expected error when RecordSleep cannot mkdir")
	}
}

func TestRouterCoverage(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()

	// 1. StderrRouter with nil writer defaults to os.Stderr
	stderrRouter := NewStderrRouter(nil)
	if stderrRouter.writer != os.Stderr {
		t.Errorf("expected writer to be os.Stderr")
	}

	// 2. FileRouter
	filePath := filepath.Join(tempDir, "alerts.log")
	fileRouter := NewFileRouter(filePath)

	// Sleeping with non-critical event: should be ignored
	nonCrit := Event{Type: EventHeartbeatStale, Severity: SeverityWarning, Message: "test"}
	if err := fileRouter.Route(ctx, nonCrit, true); err != nil {
		t.Fatalf("Route ignored non-critical failed: %v", err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("file should not exist when non-critical is ignored")
	}

	// Sleeping with critical event: should write
	crit := Event{Type: EventWorkerDead, Severity: SeverityCritical, Message: "Worker died"}
	if err := fileRouter.Route(ctx, crit, true); err != nil {
		t.Fatalf("Route critical failed: %v", err)
	}
	if data, err := os.ReadFile(filePath); err != nil || len(data) == 0 {
		t.Errorf("expected written file router alert")
	}

	// Bad file router path
	badFileRouter := NewFileRouter(filepath.Join(tempDir, "blocker", "alerts.log"))
	_ = os.WriteFile(filepath.Join(tempDir, "blocker"), []byte("data"), 0o644)
	if err := badFileRouter.Route(ctx, crit, false); err == nil {
		t.Errorf("expected error on invalid file router path")
	}

	// 3. TelegramRouter branches
	// Unconfigured token or chatID is a no-op
	unconf := NewTelegramRouter("", "")
	if err := unconf.Route(ctx, crit, false); err != nil {
		t.Fatalf("unconfigured telegram should be no-op: %v", err)
	}

	// Sleeping with non-critical: no-op
	tg := NewTelegramRouter("tok", "chat")
	if err := tg.Route(ctx, nonCrit, true); err != nil {
		t.Fatalf("sleeping telegram non-critical should be no-op: %v", err)
	}

	// Server error response (status 500)
	tsErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer tsErr.Close()

	tgErr := NewTelegramRouter("tok", "chat")
	tgErr.BaseURL = tsErr.URL
	if err := tgErr.Route(ctx, crit, false); err == nil {
		t.Errorf("expected error when telegram server returns 500")
	}

	// Invalid URL (request fails)
	tgBadURL := NewTelegramRouter("tok", "chat")
	tgBadURL.BaseURL = "http://127.0.0.1:0" // closed port
	if err := tgBadURL.Route(ctx, crit, false); err == nil {
		t.Errorf("expected error when telegram request fails")
	}

	// 4. CompositeRouter
	var buf bytes.Buffer
	cr := NewCompositeRouter(NewStderrRouter(&buf), nil, badFileRouter)
	if err := cr.Route(ctx, crit, false); err != nil {
		t.Fatalf("CompositeRouter should not return error on child failure: %v", err)
	}
	if !strings.Contains(buf.String(), "CRITICAL ALERT") {
		t.Errorf("expected stderr router to have received event in composite")
	}
}

func TestVoiceSummaryBranches(t *testing.T) {
	// 1. formatDurationNatural edge cases
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{2 * time.Hour, "2h"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
		{45 * time.Minute, "45 minutes"},
		{10 * time.Second, "under a minute"},
	}
	for _, tt := range tests {
		got := formatDurationNatural(tt.d)
		if got != tt.expected {
			t.Errorf("formatDurationNatural(%v) = %q, want %q", tt.d, got, tt.expected)
		}
	}

	// 2. summarizeList edge cases
	if res := summarizeList(nil, 2); len(res) != 1 || res[0] != "unspecified errors" {
		t.Errorf("summarizeList(nil) unexpected: %v", res)
	}
	if res := summarizeList([]string{"a", "b", "c", "d"}, 2); len(res) != 3 || res[2] != "and 2 more" {
		t.Errorf("summarizeList(4 items, max 2) unexpected: %v", res)
	}

	// 3. truncateWords edge cases
	short := "one two three"
	if truncateWords(short, 5) != short {
		t.Errorf("truncateWords under limit failed")
	}
	long := "one two three four five six"
	if got := truncateWords(long, 3); got != "one two three..." {
		t.Errorf("truncateWords over limit: %q", got)
	}

	// 4. GenerateVoiceSummary with zero now, nil state, duration < 0
	futureStart := time.Now().UTC().Add(10 * time.Minute)
	summary := GenerateVoiceSummary(&SleepState{SleepStart: futureStart}, nil, time.Time{})
	if summary.SleepDuration != "under a minute" && summary.SleepDuration != "1 minutes" {
		t.Logf("futureStart sleep duration: %s", summary.SleepDuration)
	}
	if !strings.Contains(summary.VoiceText, "all systems remained idle") {
		t.Errorf("expected idle message in voice text, got: %s", summary.VoiceText)
	}
	if !strings.Contains(summary.VoiceText, "System is standing by for your next dispatch.") {
		t.Errorf("expected standing by message in voice text, got: %s", summary.VoiceText)
	}

	// 5. GenerateVoiceSummary with only successes (no failures, no criticals)
	successEvents := []Event{
		{Type: EventSessionComplete, Severity: SeverityInfo, Message: "Done"},
	}
	sumSucc := GenerateVoiceSummary(nil, successEvents, time.Now().UTC())
	if !strings.Contains(sumSucc.VoiceText, "All active workflows executed smoothly") {
		t.Errorf("expected smooth workflow paragraph, got: %s", sumSucc.VoiceText)
	}
	if !strings.Contains(sumSucc.VoiceText, "ready for your review") {
		t.Errorf("expected PR ready paragraph, got: %s", sumSucc.VoiceText)
	}

	// 6. GenerateVoiceSummary with critical failures
	critEvents := []Event{
		{Type: EventWorkerDead, Severity: SeverityCritical, Message: "Worker process died", SessionID: "sess-99"},
		{Type: EventReceiptFailure, Severity: SeverityWarning, Message: "Receipt missing"},
	}
	sumCrit := GenerateVoiceSummary(&SleepState{SleepStart: time.Now().UTC().Add(-time.Hour)}, critEvents, time.Now().UTC())
	if !strings.Contains(sumCrit.VoiceText, "Attention is required for 1 critical event(s)") {
		t.Errorf("expected critical alert paragraph, got: %s", sumCrit.VoiceText)
	}
	if sumCrit.CriticalCount != 1 {
		t.Errorf("expected 1 critical count, got %d", sumCrit.CriticalCount)
	}
	if len(sumCrit.Bullets) != 2 {
		t.Errorf("expected 2 bullets, got %d", len(sumCrit.Bullets))
	}
}
