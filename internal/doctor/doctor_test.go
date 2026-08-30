package doctor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/heartbeat"
	"github.com/tamld/g8s/internal/pathutil"
	_ "modernc.org/sqlite"
)

func TestRunDiagnosticsHealthy(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "g8s.db")

	doc := New()
	report := doc.RunDiagnosticsWithFix(context.Background(), dbPath, false)
	if report == nil {
		t.Fatalf("expected non-nil report")
	}

	if report.OverallStatus != "HEALTHY" && report.OverallStatus != "DEGRADED" {
		t.Errorf("expected HEALTHY or DEGRADED, got %s", report.OverallStatus)
	}

	if report.ConfigDir == "" || report.DataDir == "" || report.CacheDir == "" || report.DatabasePath == "" {
		t.Errorf("expected populated path fields in report: %+v", report)
	}

	// Test default helper
	rep2 := RunDiagnostics(context.Background(), "")
	if rep2 == nil {
		t.Fatalf("expected non-nil report from RunDiagnostics")
	}
}

func TestRunDiagnosticsWithAutoFixPermissions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "g8s.db")

	// 1. autoFix creating parent directory and database
	report := RunDiagnosticsWithFix(context.Background(), dbPath, true)
	if report == nil {
		t.Fatalf("expected non-nil report")
	}

	if runtime.GOOS != "windows" {
		if err := os.WriteFile(dbPath, []byte(""), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		reportFix := RunDiagnosticsWithFix(context.Background(), dbPath, true)
		info, err := os.Stat(dbPath)
		if err != nil {
			t.Fatalf("stat file: %v", err)
		}

		if info.Mode().Perm() != 0o600 {
			t.Errorf("expected 0600 permissions after auto-fix, got %#o", info.Mode().Perm())
		}

		found := false
		for _, fix := range reportFix.AppliedFixes {
			if fix != "" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected applied fixes to contain chmod record")
		}
	}
}

func TestCheckDatabase_Scenarios(t *testing.T) {
	// 1. Database does not exist yet (OK)
	missingDB := filepath.Join(t.TempDir(), "missing.db")
	res := checkDatabase(missingDB)
	if res.Status != "OK" {
		t.Errorf("expected OK for missing db, got %s", res.Status)
	}

	// 2. Database empty path defaults to pathutil
	resDefault := checkDatabase("")
	if resDefault.Details == "" {
		t.Errorf("expected details to be populated for empty db path")
	}

	// 3. Real SQLite Database ping
	realDB := filepath.Join(t.TempDir(), "real.db")
	db, err := sql.Open("sqlite", realDB)
	if err != nil {
		t.Fatalf("open real sqlite: %v", err)
	}
	_, err = db.Exec("CREATE TABLE test (id INT);")
	_ = db.Close()
	if err != nil {
		t.Fatalf("create test table: %v", err)
	}
	_ = os.Chmod(realDB, 0o600)

	resReal := checkDatabase(realDB)
	if resReal.Status != "OK" {
		t.Errorf("expected OK for valid sqlite db, got %s: %s", resReal.Status, resReal.Message)
	}

	// 4. Loose permissions warning
	if runtime.GOOS != "windows" {
		looseDB := filepath.Join(t.TempDir(), "loose.db")
		_ = os.WriteFile(looseDB, []byte(""), 0o644)
		resLoose := checkDatabase(looseDB)
		if resLoose.Status != "WARN" {
			t.Errorf("expected WARN for loose permissions, got %s", resLoose.Status)
		}
	}
}

func TestCheckWorkspace_Scenarios(t *testing.T) {
	res := checkWorkspace()
	if res.Status != "OK" && res.Status != "WARN" {
		t.Errorf("checkWorkspace status = %s, want OK or WARN", res.Status)
	}
}

func TestCheckWorkerBinaries(t *testing.T) {
	t.Setenv("AGY_BIN", "")
	res := checkWorkerBinaries()
	if len(res) < 4 {
		t.Errorf("expected at least 4 worker CLI checks, got %d", len(res))
	}

	fakeAgy := filepath.Join(t.TempDir(), "fake_agy")
	_ = os.WriteFile(fakeAgy, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("AGY_BIN", fakeAgy)
	resWithAgy := checkWorkerBinaries()
	if len(resWithAgy) < 5 {
		t.Errorf("expected at least 5 checks with AGY_BIN, got %d", len(resWithAgy))
	}

	t.Setenv("AGY_BIN", filepath.Join(t.TempDir(), "nonexistent"))
	resInvalidAgy := checkWorkerBinaries()
	foundWarn := false
	for _, r := range resInvalidAgy {
		if r.Name == "Worker Binary (AGY_BIN)" && r.Status == "WARN" {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected WARN status for nonexistent AGY_BIN")
	}
}

func TestCheckProvidersAndHarness(t *testing.T) {
	provRes := checkProviders()
	if provRes.Status != "OK" {
		t.Errorf("checkProviders status = %s, want OK", provRes.Status)
	}

	harnRes := checkHarnessProfiles()
	if harnRes.Status != "OK" {
		t.Errorf("checkHarnessProfiles status = %s, want OK", harnRes.Status)
	}
}

func TestDiagnoseWindowsEnvironment(t *testing.T) {
	resultsOnPath := diagnoseWindowsEnvironment(
		pathutil.ScopeUser,
		`C:\Users\Alice`,
		"msi-or-nsis",
		`C:\Program Files\g8s`,
		true,
		true,
	)
	if len(resultsOnPath) < 8 {
		t.Errorf("expected at least 8 diagnostic results, got %d", len(resultsOnPath))
	}

	resultsNotOnPath := diagnoseWindowsEnvironment(
		pathutil.ScopeSystem,
		`C:\Users\Bob`,
		"zip-or-manual",
		`C:\Tools\g8s`,
		false,
		false,
	)
	foundWarn := false
	for _, r := range resultsNotOnPath {
		if r.Name == "Windows PATH State" && r.Status == "WARN" {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected WARN status when not on PATH")
	}
}

func TestDoctorWindowsDetection(t *testing.T) {
	d := &Doctor{
		Scope:       pathutil.ScopeUser,
		DetectPaths: true,
	}
	src := d.detectInstallSource()
	if runtime.GOOS != "windows" {
		if src != "" {
			t.Fatalf("expected empty install source on non-windows, got %s", src)
		}
		if path := d.detectInstallPath(); path != "" {
			t.Fatalf("expected empty install path on non-windows, got %s", path)
		}
		checks := d.checkWindowsEnvironment()
		if len(checks) != 0 {
			t.Fatalf("expected 0 windows checks on non-windows, got %d", len(checks))
		}
	} else {
		if src != "msi-or-nsis" && src != "zip-or-manual" {
			t.Fatalf("unexpected install source on windows: %s", src)
		}
		path := d.detectInstallPath()
		if path == "" {
			t.Fatalf("expected non-empty install path on windows")
		}
		checks := d.checkWindowsEnvironment()
		if len(checks) < 6 {
			t.Fatalf("expected at least 6 windows checks on windows, got %d", len(checks))
		}
	}
}

func TestRunAttentionCheck(t *testing.T) {
	tempDir := t.TempDir()
	hbDir := filepath.Join(tempDir, ".heartbeat")

	doc := New()
	ctx := context.Background()

	// 1. Run without active heartbeats or actor
	rep1 := doc.RunAttentionCheck(ctx, "", hbDir)
	if rep1 == nil || len(rep1.Questions) != 5 {
		t.Fatalf("expected 5 questions in report, got %+v", rep1)
	}

	expectedQuestions := []string{
		"What 2-3 edge cases might your last task have missed?",
		"Run 'g8s cleanup --target ghost-process --dry-run' — does it list your own session? (it should NOT)",
		"Are you running with --actor set to a unique identifier?",
		"When was your last heartbeat update?",
		"What contract from your brief would you violate by accident?",
	}

	for i, q := range expectedQuestions {
		if rep1.Questions[i].Question != q {
			t.Errorf("question %d mismatch: got %q, want %q", i+1, rep1.Questions[i].Question, q)
		}
		if rep1.Questions[i].Number != i+1 {
			t.Errorf("question number mismatch: got %d, want %d", rep1.Questions[i].Number, i+1)
		}
	}

	// 2. Run with active actor and heartbeat
	hbStore := heartbeat.NewStore(hbDir, time.Now)
	_, err := hbStore.Record("sess-attn-1", heartbeat.StatusRunning, nil,
		heartbeat.WithPID(1234),
		heartbeat.WithBinary("agy"),
		heartbeat.WithLastUpdate(time.Now()),
	)
	if err != nil {
		t.Fatalf("failed to record test heartbeat: %v", err)
	}

	rep2 := doc.RunAttentionCheck(ctx, "agent-007", hbDir)
	if rep2.Questions[2].Answer != "yes (actor=agent-007)" {
		t.Errorf("expected actor answer 'yes (actor=agent-007)', got %q", rep2.Questions[2].Answer)
	}
	if !strings.Contains(rep2.Questions[3].Answer, "sess-attn-1") {
		t.Errorf("expected heartbeat answer to mention sess-attn-1, got %q", rep2.Questions[3].Answer)
	}
}
