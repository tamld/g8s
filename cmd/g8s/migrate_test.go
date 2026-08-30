package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateData_DryRunAndExecute(t *testing.T) {
	srcDir := t.TempDir()
	destDir := t.TempDir()

	// Create legacy files in srcDir
	dbFile := filepath.Join(srcDir, "g8s.db")
	if err := os.WriteFile(dbFile, []byte("sqlite-dummy-data"), 0o600); err != nil {
		t.Fatalf("write dbFile: %v", err)
	}

	hbDir := filepath.Join(srcDir, ".heartbeat")
	if err := os.MkdirAll(hbDir, 0o700); err != nil {
		t.Fatalf("mkdir hbDir: %v", err)
	}
	hbFile := filepath.Join(hbDir, "session1.json")
	if err := os.WriteFile(hbFile, []byte(`{"session_id":"session1"}`), 0o600); err != nil {
		t.Fatalf("write hbFile: %v", err)
	}

	// 1. Test Dry Run
	dryReport, err := MigrateData(srcDir, destDir, true, false)
	if err != nil {
		t.Fatalf("MigrateData dry-run failed: %v", err)
	}
	if dryReport.TotalFiles != 2 {
		t.Fatalf("expected 2 files in dry-run, got %d", dryReport.TotalFiles)
	}
	for _, it := range dryReport.Items {
		if it.Status != "would_migrate" {
			t.Errorf("expected would_migrate status, got %s for %s", it.Status, it.Source)
		}
	}
	// Verify target files do not exist after dry run
	if _, err := os.Stat(filepath.Join(destDir, "g8s.db")); err == nil {
		t.Errorf("target g8s.db should not exist after dry-run")
	}

	// 2. Test Real Migration
	realReport, err := MigrateData(srcDir, destDir, false, false)
	if err != nil {
		t.Fatalf("MigrateData real run failed: %v", err)
	}
	if realReport.TotalFiles != 2 {
		t.Fatalf("expected 2 files migrated, got %d", realReport.TotalFiles)
	}
	for _, it := range realReport.Items {
		if it.Status != "migrated" {
			t.Errorf("expected migrated status, got %s for %s", it.Status, it.Source)
		}
	}

	// Verify target files exist and content matches
	data, err := os.ReadFile(filepath.Join(destDir, "g8s.db"))
	if err != nil || string(data) != "sqlite-dummy-data" {
		t.Errorf("target g8s.db content mismatch: %v", err)
	}

	dataHB, err := os.ReadFile(filepath.Join(destDir, ".heartbeat", "session1.json"))
	if err != nil || string(dataHB) != `{"session_id":"session1"}` {
		t.Errorf("target heartbeat content mismatch: %v", err)
	}

	// 3. Test Skip existing without force
	skipReport, err := MigrateData(srcDir, destDir, false, false)
	if err != nil {
		t.Fatalf("MigrateData skip check failed: %v", err)
	}
	for _, it := range skipReport.Items {
		if it.Status != "skipped" {
			t.Errorf("expected skipped status when file exists without force, got %s for %s", it.Status, it.Source)
		}
	}

	// 4. Test Force Overwrite
	forceReport, err := MigrateData(srcDir, destDir, false, true)
	if err != nil {
		t.Fatalf("MigrateData force check failed: %v", err)
	}
	if forceReport.TotalFiles != 2 {
		t.Fatalf("expected 2 files migrated with force, got %d", forceReport.TotalFiles)
	}
}
