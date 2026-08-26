package doctor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunDiagnosticsHealthy(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "g8s.db")

	report := RunDiagnostics(context.Background(), dbPath)
	if report.OverallStatus != "HEALTHY" {
		t.Fatalf("expected HEALTHY status, got %s", report.OverallStatus)
	}
	if !report.ZeroCGO {
		t.Fatalf("expected ZeroCGO=true")
	}
	if len(report.Checks) == 0 {
		t.Fatalf("expected diagnostic checks to be executed")
	}
}

func TestCheckHarnessProfiles(t *testing.T) {
	res := checkHarnessProfiles()
	if res.Status != "OK" {
		t.Fatalf("expected OK status for harness profiles, got %s (%s)", res.Status, res.Message)
	}
}

func TestCheckProviders(t *testing.T) {
	res := checkProviders()
	if res.Status != "OK" {
		t.Fatalf("expected OK status for provider registry, got %s (%s)", res.Status, res.Message)
	}
}

func TestRunDiagnosticsWithAutoFixPermissions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "g8s.db")

	// Pre-create database file with loose permissions 0644
	if err := os.WriteFile(dbPath, []byte("fake db content"), 0644); err != nil {
		t.Fatalf("write fake db: %v", err)
	}
	_ = os.Chmod(dbPath, 0644)

	// 1. Diagnostics without fix reports WARN for permissions
	report := RunDiagnosticsWithFix(context.Background(), dbPath, false)
	if runtime.GOOS != "windows" {
		var hasWarn bool
		for _, c := range report.Checks {
			if c.Name == "Database Permissions" && c.Status == "WARN" {
				hasWarn = true
				break
			}
		}
		if !hasWarn {
			t.Errorf("expected WARN on loose db permissions without autoFix")
		}
	}

	// 2. Diagnostics with fix repairs permissions
	reportFixed := RunDiagnosticsWithFix(context.Background(), dbPath, true)
	if len(reportFixed.AppliedFixes) == 0 && runtime.GOOS != "windows" {
		t.Fatalf("expected applied fixes to be recorded")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(dbPath)
		if err != nil {
			t.Fatalf("stat fixed db: %v", err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("expected 0600 permissions, got %#o", info.Mode().Perm())
		}
	}
}
