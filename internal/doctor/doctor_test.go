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
	if report == nil {
		t.Fatalf("expected non-nil report")
	}

	if report.OverallStatus != "HEALTHY" && report.OverallStatus != "DEGRADED" {
		t.Errorf("expected HEALTHY or DEGRADED, got %s", report.OverallStatus)
	}

	if report.ConfigDir == "" || report.DataDir == "" || report.CacheDir == "" || report.DatabasePath == "" {
		t.Errorf("expected populated path fields in report: %+v", report)
	}
}

func TestRunDiagnosticsWithAutoFixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping POSIX permission test on windows")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "g8s.db")

	// Create with wrong permissions
	if err := os.WriteFile(dbPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	report := RunDiagnosticsWithFix(context.Background(), dbPath, true)
	if report == nil {
		t.Fatalf("expected non-nil report")
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600 permissions after auto-fix, got %#o", info.Mode().Perm())
	}

	found := false
	for _, fix := range report.AppliedFixes {
		if fix != "" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected applied fixes to contain chmod record")
	}
}

func TestDoctorWindowsDetection(t *testing.T) {
	d := New()
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
