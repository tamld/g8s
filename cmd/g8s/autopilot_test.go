package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/autopilot"
	"github.com/tamld/g8s/internal/controlplane"
)

func TestSubmitAutopilotItems_DryRunAndLivePersistence(t *testing.T) {
	ctx := context.Background()
	items := []*autopilot.WorkItem{
		{
			ID:           "todo-item-1",
			Source:       autopilot.SourceStaleTodo,
			Title:        "Investigate stale TODO 1",
			Description:  "Check stale TODO in cmd/g8s",
			Severity:     0.6,
			Confidence:   0.8,
			Cost:         1.0,
			DiscoveredAt: time.Now(),
			Score:        0.82,
		},
		{
			ID:           "todo-item-2",
			Source:       autopilot.SourceStaleTodo,
			Title:        "Investigate stale TODO 2",
			Description:  "Check stale TODO in internal/cli",
			Severity:     0.5,
			Confidence:   0.7,
			Cost:         1.0,
			DiscoveredAt: time.Now(),
			Score:        0.71,
		},
	}

	// 1. Dry-run invokes production helper without opening SQLite
	dryResults, drySubmitted, drySkipped, dryErr := submitAutopilotItems(ctx, nil, items, 5, true, "9router", "fast-agent", ".")
	if dryErr != nil {
		t.Fatalf("unexpected dry-run error: %v", dryErr)
	}
	if drySubmitted != 0 || drySkipped != 2 || len(dryResults) != 2 {
		t.Fatalf("dry-run mismatch: submitted=%d skipped=%d results=%+v", drySubmitted, drySkipped, dryResults)
	}
	if dryResults[0].Status != "would_submit" {
		t.Fatalf("expected would_submit status, got %+v", dryResults[0])
	}

	// 2. Live persistence into isolated temp SQLite database with maxSubmissions=1 cap
	dbPath := filepath.Join(t.TempDir(), "cp.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}

	liveResults, submitted, skipped, liveErr := submitAutopilotItems(ctx, store, items, 1, false, "9router", "fast-agent", ".")
	if liveErr != nil {
		t.Fatalf("unexpected live submit error: %v", liveErr)
	}
	if submitted != 1 || skipped != 0 || len(liveResults) != 1 {
		t.Fatalf("live submit mismatch: submitted=%d skipped=%d results=%+v", submitted, skipped, liveResults)
	}
	if liveResults[0].Status != "submitted" || liveResults[0].TaskID == "" {
		t.Fatalf("expected submitted task with TaskID, got %+v", liveResults[0])
	}

	persisted, err := store.GetTask(ctx, liveResults[0].TaskID)
	if err != nil {
		t.Fatalf("GetTask(%s): %v", liveResults[0].TaskID, err)
	}
	if persisted.State != controlplane.StateQueued {
		t.Fatalf("expected state QUEUED, got %s", persisted.State)
	}

	// 3. Fail-closed error return when SubmitTask fails on a closed store
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	failResults, failSubmitted, _, failErr := submitAutopilotItems(ctx, store, items[:1], 1, false, "9router", "fast-agent", ".")
	if failErr == nil {
		t.Fatalf("expected fail-closed error when SubmitTask fails on closed store, got results=%+v", failResults)
	}
	if failSubmitted != 0 || failResults[0].Status != "failed" {
		t.Fatalf("expected failed status and 0 submitted, got submitted=%d results=%+v", failSubmitted, failResults)
	}
}

func TestSubmitSingleAutopilotItem_SurfacesStoreErrorsAndEnforcesCap(t *testing.T) {
	ctx := context.Background()
	item := &autopilot.WorkItem{
		ID:           "vet-item-1",
		Source:       autopilot.SourceStaticAnalysis,
		Title:        "Static analysis finding",
		Description:  "Verify go vet finding",
		DiscoveredAt: time.Now(),
		Score:        0.90,
	}

	// 1. Fail-closed error propagation when dbPath is a directory (cannot open SQLite DB)
	submittedSoFar := 0
	badDBPath := t.TempDir()
	taskID, err := submitSingleAutopilotItem(ctx, badDBPath, item, &submittedSoFar, 5, "9router", "fast-agent", ".")
	if err == nil {
		t.Fatalf("expected error opening directory as SQLite DB, got taskID=%q", taskID)
	}
	if submittedSoFar != 0 {
		t.Fatalf("submittedSoFar incremented on error: %d", submittedSoFar)
	}

	// 2. Successful submission increments counter and enforces maxSubmissions cap
	goodDBPath := filepath.Join(t.TempDir(), "autopilot-start.db")
	taskID, err = submitSingleAutopilotItem(ctx, goodDBPath, item, &submittedSoFar, 1, "9router", "fast-agent", ".")
	if err != nil || taskID == "" {
		t.Fatalf("expected valid task submission, got taskID=%q err=%v", taskID, err)
	}
	if submittedSoFar != 1 {
		t.Fatalf("expected submittedSoFar=1, got %d", submittedSoFar)
	}

	cappedID, err := submitSingleAutopilotItem(ctx, goodDBPath, item, &submittedSoFar, 1, "9router", "fast-agent", ".")
	if err != nil || cappedID != "" {
		t.Fatalf("expected cap to skip second submission cleanly, got id=%q err=%v", cappedID, err)
	}
}

func TestAutopilotSubmitResultItemJSON(t *testing.T) {
	item := autopilotSubmitResultItem{
		ItemID: "test-1",
		Status: "submitted",
		TaskID: "task-123",
		Score:  0.85,
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var parsed autopilotSubmitResultItem
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if parsed != item {
		t.Errorf("JSON round-trip mismatch: got %+v, want %+v", parsed, item)
	}
}
