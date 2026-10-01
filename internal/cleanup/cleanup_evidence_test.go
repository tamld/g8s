package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupEvidenceTarget(t *testing.T) {
	// Verify TargetEvidence is registered in AllCleanupTargets
	found := false
	for _, target := range AllCleanupTargets {
		if target == TargetEvidence {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected TargetEvidence %q to be in AllCleanupTargets", TargetEvidence)
	}

	baseDir := t.TempDir()
	evidenceDir := filepath.Join(baseDir, "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}

	// Non-evidence sibling directory
	nonEvidenceDir := filepath.Join(baseDir, "non_evidence")
	if err := os.MkdirAll(nonEvidenceDir, 0o700); err != nil {
		t.Fatalf("mkdir non_evidence: %v", err)
	}
	nonEvidenceFile := filepath.Join(nonEvidenceDir, "important.txt")
	if err := os.WriteFile(nonEvidenceFile, []byte("preserve me"), 0o600); err != nil {
		t.Fatalf("write non-evidence file: %v", err)
	}

	// Non-evidence file directly inside evidenceDir
	rootFile := filepath.Join(evidenceDir, "notes.txt")
	if err := os.WriteFile(rootFile, []byte("root file"), 0o600); err != nil {
		t.Fatalf("write root file: %v", err)
	}

	fixedNow := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	// Old evidence directory (10 days old)
	oldDir := filepath.Join(evidenceDir, "task-old-123")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	oldReceipt := filepath.Join(oldDir, "receipt.json")
	if err := os.WriteFile(oldReceipt, []byte(`{"task_id":"task-old-123"}`), 0o600); err != nil {
		t.Fatalf("write old receipt: %v", err)
	}
	oldTime := fixedNow.Add(-10 * 24 * time.Hour)
	_ = os.Chtimes(oldDir, oldTime, oldTime)
	_ = os.Chtimes(oldReceipt, oldTime, oldTime)

	// Fresh evidence directory (2 days old)
	freshDir := filepath.Join(evidenceDir, "task-fresh-456")
	if err := os.MkdirAll(freshDir, 0o700); err != nil {
		t.Fatalf("mkdir fresh: %v", err)
	}
	freshReceipt := filepath.Join(freshDir, "receipt.json")
	if err := os.WriteFile(freshReceipt, []byte(`{"task_id":"task-fresh-456"}`), 0o600); err != nil {
		t.Fatalf("write fresh receipt: %v", err)
	}
	freshTime := fixedNow.Add(-2 * 24 * time.Hour)
	_ = os.Chtimes(freshDir, freshTime, freshTime)
	_ = os.Chtimes(freshReceipt, freshTime, freshTime)

	// 1. Dry run: 7 days retention
	dryCfg := CleanupConfig{
		Targets:               []string{TargetEvidence},
		EvidenceDir:           evidenceDir,
		EvidenceRetentionDays: 7,
		DryRun:                true,
		Clock:                 clock,
	}

	report, err := RunCleanupSweep(context.Background(), dryCfg)
	if err != nil {
		t.Fatalf("dry run sweep: %v", err)
	}

	// Verify report
	if len(report.Items) != 1 {
		t.Fatalf("expected 1 item in dry run, got %d: %+v", len(report.Items), report.Items)
	}
	item := report.Items[0]
	if item.Target != TargetEvidence {
		t.Errorf("got target %s, want %s", item.Target, TargetEvidence)
	}
	if item.ID != oldDir {
		t.Errorf("got ID %s, want %s", item.ID, oldDir)
	}
	if item.Action != "would_delete" {
		t.Errorf("got action %s, want would_delete", item.Action)
	}

	// Verify files on disk untouched in dry run
	if _, err := os.Stat(oldDir); err != nil {
		t.Errorf("oldDir missing after dry-run: %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("freshDir missing after dry-run: %v", err)
	}
	if _, err := os.Stat(nonEvidenceFile); err != nil {
		t.Errorf("nonEvidenceFile missing after dry-run: %v", err)
	}
	if _, err := os.Stat(rootFile); err != nil {
		t.Errorf("rootFile missing after dry-run: %v", err)
	}

	// 2. Real run: 7 days retention
	realCfg := CleanupConfig{
		Targets:               []string{TargetEvidence},
		EvidenceDir:           evidenceDir,
		EvidenceRetentionDays: 7,
		DryRun:                false,
		Clock:                 clock,
	}

	realReport, err := RunCleanupSweep(context.Background(), realCfg)
	if err != nil {
		t.Fatalf("real run sweep: %v", err)
	}

	if len(realReport.Items) != 1 {
		t.Fatalf("expected 1 item in real run, got %d: %+v", len(realReport.Items), realReport.Items)
	}
	realItem := realReport.Items[0]
	if realItem.Action != "deleted" {
		t.Errorf("got action %s, want deleted", realItem.Action)
	}

	// Verify disk state
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("expected oldDir to be deleted, got err: %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("freshDir should remain: %v", err)
	}
	if _, err := os.Stat(nonEvidenceFile); err != nil {
		t.Errorf("nonEvidenceFile must remain untouched: %v", err)
	}
	if _, err := os.Stat(rootFile); err != nil {
		t.Errorf("rootFile must remain untouched: %v", err)
	}

	// 3. Unlimited retention (0 days): nothing should be deleted even if past any age
	unlimitedCfg := CleanupConfig{
		Targets:               []string{TargetEvidence},
		EvidenceDir:           evidenceDir,
		EvidenceRetentionDays: 0,
		DryRun:                false,
		Clock:                 clock,
	}

	unlimitedReport, err := RunCleanupSweep(context.Background(), unlimitedCfg)
	if err != nil {
		t.Fatalf("unlimited sweep: %v", err)
	}
	if len(unlimitedReport.Items) != 0 {
		t.Errorf("expected 0 items with unlimited retention, got %d", len(unlimitedReport.Items))
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("freshDir must remain: %v", err)
	}
}
