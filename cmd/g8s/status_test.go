package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/heartbeat"
)

func TestGenerateStatusReport(t *testing.T) {
	tempDir := t.TempDir()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	store := heartbeat.NewStore(tempDir, clock)

	// 1. Active worker (<60s)
	_, _ = store.Record("sess-active", heartbeat.StatusRunning, map[string]any{"model": "gemini-3.7-flash-high"},
		heartbeat.WithPID(1001),
		heartbeat.WithBinary("agy"),
		heartbeat.WithLastUpdate(now.Add(-20*time.Second)),
		heartbeat.WithCurrentStep("dispatching test-runner subagent"),
	)

	// 2. Stale worker (60-300s)
	_, _ = store.Record("sess-stale", heartbeat.StatusRunning, nil,
		heartbeat.WithPID(1002),
		heartbeat.WithBinary("agy"),
		heartbeat.WithLastUpdate(now.Add(-150*time.Second)),
		heartbeat.WithCurrentStep("waiting for lock"),
	)

	// 3. Dead worker (>300s)
	_, _ = store.Record("sess-dead", heartbeat.StatusFailed, nil,
		heartbeat.WithPID(1003),
		heartbeat.WithBinary("claude"),
		heartbeat.WithLastUpdate(now.Add(-400*time.Second)),
		heartbeat.WithCurrentStep("unexpected panic"),
	)

	t.Run("all workers report with freshness categories", func(t *testing.T) {
		opts := StatusOptions{
			HeartbeatDir: tempDir,
			Clock:        clock,
		}

		report, err := GenerateStatusReport(context.Background(), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Workers) != 3 {
			t.Fatalf("expected 3 workers, got %d", len(report.Workers))
		}
		if report.ActiveCount != 1 {
			t.Errorf("expected 1 active, got %d", report.ActiveCount)
		}
		if report.StaleCount != 1 {
			t.Errorf("expected 1 stale, got %d", report.StaleCount)
		}
		if report.DeadCount != 1 {
			t.Errorf("expected 1 dead, got %d", report.DeadCount)
		}
		if report.Recommendation != "g8s cleanup --target ghost-process" {
			t.Errorf("expected ghost-process recommendation, got %s", report.Recommendation)
		}
	})

	t.Run("filter by session ID", func(t *testing.T) {
		opts := StatusOptions{
			HeartbeatDir: tempDir,
			SessionID:    "sess-active",
			Clock:        clock,
		}

		report, err := GenerateStatusReport(context.Background(), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Workers) != 1 {
			t.Fatalf("expected 1 worker, got %d", len(report.Workers))
		}
		if report.Workers[0].SessionID != "sess-active" {
			t.Errorf("expected sess-active, got %s", report.Workers[0].SessionID)
		}
		if report.Workers[0].Freshness != heartbeat.FreshnessActive {
			t.Errorf("expected active freshness, got %s", report.Workers[0].Freshness)
		}
	})

	t.Run("render status report to writer", func(t *testing.T) {
		opts := StatusOptions{
			HeartbeatDir: tempDir,
			Clock:        clock,
		}

		report, err := GenerateStatusReport(context.Background(), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var buf bytes.Buffer
		renderStatusReport(report, &buf)
		output := buf.String()

		if len(output) == 0 {
			t.Errorf("expected non-empty rendered output")
		}
	})

	t.Run("render empty status report", func(t *testing.T) {
		emptyReport := &StatusReport{
			Workers: []WorkerStatusView{},
		}
		var buf bytes.Buffer
		renderStatusReport(emptyReport, &buf)
		output := buf.String()

		if len(output) == 0 {
			t.Errorf("expected non-empty rendered output")
		}
	})

	t.Run("json output formatting", func(t *testing.T) {
		opts := StatusOptions{
			HeartbeatDir: tempDir,
			Clock:        clock,
			JSONMode:     true,
		}

		report, err := GenerateStatusReport(context.Background(), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := json.Marshal(report)
		if err != nil {
			t.Fatalf("failed to marshal json: %v", err)
		}
		if len(data) == 0 {
			t.Errorf("expected valid json payload")
		}
	})
}
