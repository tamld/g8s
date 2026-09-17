// Package autopilot implements the cron-based supervisor trigger that scans
// GitHub issues, failing CI runs, static analysis findings, and stale
// @agy-fix-me TODOs. It uses a priority queue weighted by severity,
// confidence, and cost-inverse.
package autopilot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScheduler_StartStop(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Cron = "" // Disable cron for testing

	handler := func(ctx context.Context, item *WorkItem) error {
		return nil
	}

	s, err := NewScheduler(cfg, handler)
	if err != nil {
		t.Fatalf("NewScheduler failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !s.IsRunning() {
		t.Error("scheduler should be running")
	}

	// Wait a bit for initial scan
	time.Sleep(100 * time.Millisecond)

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if s.IsRunning() {
		t.Error("scheduler should not be running after stop")
	}
}

func TestScheduler_TriggerScan(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Cron = ""
	cfg.ScanSources = ScanSources{
		GitHubIssues:   false,
		FailingCI:      false,
		StaticAnalysis: false,
		StaleTodos:     true,
	}
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "sample.go")
	if err := os.WriteFile(testFile, []byte("// TODO: @agy-fix-me write unit tests for scheduler\npackage sample\n"), 0o600); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	cfg.CodebasePath = tmpDir

	handlerCalled := false
	handler := func(ctx context.Context, item *WorkItem) error {
		handlerCalled = true
		return nil
	}

	s, err := NewScheduler(cfg, handler)
	if err != nil {
		t.Fatalf("NewScheduler failed: %v", err)
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for initial scan
	time.Sleep(200 * time.Millisecond)

	// Manually trigger another scan
	ctx := context.Background()
	if err := s.TriggerScan(ctx); err != nil {
		t.Fatalf("TriggerScan failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Handler should have been called at least once
	if !handlerCalled {
		t.Error("handler should have been called")
	}
}

func TestScheduler_UpdateConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Cron = ""

	s, err := NewScheduler(cfg, nil)
	if err != nil {
		t.Fatalf("NewScheduler failed: %v", err)
	}

	newCfg := DefaultConfig()
	newCfg.Cron = ""
	newCfg.MaxItemsPerTick = 100
	newCfg.MinScoreThreshold = 0.5

	if err := s.UpdateConfig(newCfg); err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}

	updated := s.GetConfig()
	if updated.MaxItemsPerTick != 100 {
		t.Errorf("config not updated: MaxItemsPerTick = %d", updated.MaxItemsPerTick)
	}
	if updated.MinScoreThreshold != 0.5 {
		t.Errorf("config not updated: MinScoreThreshold = %f", updated.MinScoreThreshold)
	}
}

func TestScanner_StaleTodos(t *testing.T) {
	// Create a temp directory with test files
	// This is a simplified test - in reality we'd use a temp dir

	cfg := DefaultConfig()
	cfg.Cron = ""
	cfg.ScanSources = ScanSources{
		GitHubIssues:   false,
		FailingCI:      false,
		StaticAnalysis: false,
		StaleTodos:     true,
	}
	cfg.CodebasePath = "."
	cfg.LookbackWindow = 24 * time.Hour

	queue := NewPriorityQueue()
	scanner, err := NewScanner(cfg, queue)
	if err != nil {
		t.Fatalf("NewScanner failed: %v", err)
	}

	ctx := context.Background()
	if err := scanner.Scan(ctx); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Should find some TODOs in the codebase
	t.Logf("Found %d todo items", queue.Len())
}
