package doctor

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/sandbox"
)

func capabilityFor(mode sandbox.SandboxCapabilityLevel) sandbox.SandboxCapability {
	backend := "seatbelt"
	if mode == sandbox.CapabilityUnavailable {
		backend = "linux_policy_only"
	}
	if mode == sandbox.CapabilityMediated {
		backend = "windows_job_object"
	}
	return sandbox.SandboxCapability{Mode: mode, Backend: backend, Reason: "test probe"}
}

// TestEvaluateOperatingModes is the honest readiness matrix (#971 WP1):
// Mode 1 needs database + >=1 provider + sandbox capability; Mode 2 needs
// database + >=2 providers + git worktree isolation + sandbox capability.
func TestEvaluateOperatingModes(t *testing.T) {
	tests := []struct {
		name        string
		probe       OperatingModeProbe
		wantMode1   bool
		wantMode2   bool
		wantUnmetM1 string
		wantUnmetM2 string
	}{
		{
			name: "capable host single provider",
			probe: OperatingModeProbe{
				Sandbox:         capabilityFor(sandbox.CapabilityKernelEnforced),
				DatabaseOK:      true,
				UsableProviders: []string{"agy"},
				GitAvailable:    true,
			},
			wantMode1:   true,
			wantMode2:   false,
			wantUnmetM2: "multi_provider_configured",
		},
		{
			name: "capable host multi provider fleet",
			probe: OperatingModeProbe{
				Sandbox:         capabilityFor(sandbox.CapabilityKernelEnforced),
				DatabaseOK:      true,
				UsableProviders: []string{"agy", "claude"},
				GitAvailable:    true,
			},
			wantMode1: true,
			wantMode2: true,
		},
		{
			name: "windows mediated containment is not a false lockout",
			probe: OperatingModeProbe{
				Sandbox:         capabilityFor(sandbox.CapabilityMediated),
				DatabaseOK:      true,
				UsableProviders: []string{"agy"},
			},
			wantMode1:   true,
			wantMode2:   false,
			wantUnmetM2: "multi_provider_configured",
		},
		{
			name: "no sandbox backend and no opt-in keeps Mode 1 unready",
			probe: OperatingModeProbe{
				Sandbox:         capabilityFor(sandbox.CapabilityUnavailable),
				DatabaseOK:      true,
				UsableProviders: []string{"agy"},
				GitAvailable:    true,
			},
			wantMode1:   false,
			wantUnmetM1: "sandbox_capability",
			wantMode2:   false,
		},
		{
			name: "database down blocks both modes",
			probe: OperatingModeProbe{
				Sandbox:         capabilityFor(sandbox.CapabilityKernelEnforced),
				DatabaseOK:      false,
				UsableProviders: []string{"agy", "claude"},
				GitAvailable:    true,
			},
			wantMode1:   false,
			wantUnmetM1: "durable_queue_database",
			wantMode2:   false,
			wantUnmetM2: "durable_queue_database",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := EvaluateOperatingModes(tc.probe)
			if status == nil {
				t.Fatal("nil status")
			}
			if status.Mode1.Ready != tc.wantMode1 {
				t.Fatalf("mode1 ready=%v reason=%q, want %v", status.Mode1.Ready, status.Mode1.Reason, tc.wantMode1)
			}
			if status.Mode2.Ready != tc.wantMode2 {
				t.Fatalf("mode2 ready=%v reason=%q, want %v", status.Mode2.Ready, status.Mode2.Reason, tc.wantMode2)
			}
			if tc.wantUnmetM1 != "" && !strings.Contains(status.Mode1.Reason, tc.wantUnmetM1) {
				t.Fatalf("mode1 reason %q must name unmet requirement %q", status.Mode1.Reason, tc.wantUnmetM1)
			}
			if tc.wantUnmetM2 != "" && !strings.Contains(status.Mode2.Reason, tc.wantUnmetM2) {
				t.Fatalf("mode2 reason %q must name unmet requirement %q", status.Mode2.Reason, tc.wantUnmetM2)
			}
			// Honest requirement details on every requirement row.
			for _, m := range []OperatingModeReadiness{status.Mode1, status.Mode2} {
				for _, req := range m.Requirements {
					if strings.TrimSpace(req.Detail) == "" {
						t.Fatalf("mode %s requirement %s has empty detail (dishonest)", m.Mode, req.ID)
					}
				}
			}
			if tc.probe.Sandbox.Mode == sandbox.CapabilityUnavailable &&
				!strings.Contains(status.Mode1.Requirements[2].Detail, "allow-degraded-sandbox") {
				t.Fatal("unavailable sandbox capability must surface the explicit degraded opt-in")
			}
		})
	}
}

// TestDoctorReportOperatingModesSurfaced runs the full diagnostic suite on a
// temp database and asserts the additive #971 WP1 fields serialize into
// g8s doctor --json.
func TestDoctorReportOperatingModesSurfaced(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "g8s.db")
	report := RunDiagnostics(t.Context(), dbPath)
	if report.SandboxCapability == nil {
		t.Fatal("DoctorReport.SandboxCapability must be populated (capability-probed preflight)")
	}
	switch report.SandboxCapability.Mode {
	case sandbox.CapabilityKernelEnforced, sandbox.CapabilityMediated, sandbox.CapabilityUnavailable:
	default:
		t.Fatalf("sandbox capability mode %q outside vocabulary", report.SandboxCapability.Mode)
	}
	if report.OperatingModes == nil {
		t.Fatal("DoctorReport.OperatingModes must be populated")
	}
	if report.OperatingModes.Mode1.Mode != "mode1" || report.OperatingModes.Mode2.Mode != "mode2" {
		t.Fatalf("unexpected mode identifiers: %+v", report.OperatingModes)
	}
	if len(report.OperatingModes.Mode1.Requirements) == 0 || len(report.OperatingModes.Mode2.Requirements) == 0 {
		t.Fatal("operating modes must list machine-checkable requirements")
	}
	blob, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, key := range []string{`"sandbox_capability"`, `"operating_modes"`, `"mode1"`, `"mode2"`} {
		if !strings.Contains(string(blob), key) {
			t.Fatalf("doctor --json payload missing %s: %s", key, blob)
		}
	}
}
