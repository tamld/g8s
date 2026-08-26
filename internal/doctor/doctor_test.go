package doctor

import (
	"context"
	"path/filepath"
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
