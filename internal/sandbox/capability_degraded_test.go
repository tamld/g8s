package sandbox

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// TestProbeCapability_LiveShape validates the host probe's contract: a
// machine-readable mode from the fixed vocabulary, a backend identifier for
// non-unavailable results, and an honest non-empty reason (#971 WP1).
func TestProbeCapability_LiveShape(t *testing.T) {
	cap := ProbeCapability()
	switch cap.Mode {
	case CapabilityKernelEnforced, CapabilityMediated, CapabilityUnavailable:
	default:
		t.Fatalf("ProbeCapability mode %q outside the {kernel_enforced, mediated, unavailable} vocabulary", cap.Mode)
	}
	if strings.TrimSpace(cap.Reason) == "" {
		t.Fatal("ProbeCapability returned an empty reason (dishonest probe)")
	}
	if cap.Mode != CapabilityUnavailable && strings.TrimSpace(cap.Backend) == "" {
		t.Fatalf("mode %q must name a backend, got empty", cap.Mode)
	}
	if cap.Mode == CapabilityUnavailable && cap.Backend != "" && !strings.Contains(cap.Backend, "policy_only") {
		t.Fatalf("unavailable probe should not claim an enforcement backend, got %q", cap.Backend)
	}
	if runtime.GOOS == "darwin" && cap.Mode != CapabilityKernelEnforced {
		t.Fatalf("darwin host expected seatbelt kernel_enforced capability, got mode=%q backend=%q reason=%q", cap.Mode, cap.Backend, cap.Reason)
	}
	if runtime.GOOS == "windows" && (cap.Mode != CapabilityMediated || cap.Backend != "windows_job_object") {
		t.Fatalf("windows host expected mediated windows_job_object capability, got mode=%q backend=%q", cap.Mode, cap.Backend)
	}
}

// TestProbeCapability_MockablePerOS exercises the injectable seam (red-first
// mockable per-OS probe) without touching host state.
func TestProbeCapability_MockablePerOS(t *testing.T) {
	orig := capabilityProbe
	t.Cleanup(func() { capabilityProbe = orig })

	capabilityProbe = func() SandboxCapability {
		return SandboxCapability{
			Mode:    CapabilityKernelEnforced,
			Backend: "bwrap",
			Reason:  "mocked linux probe",
		}
	}
	if got := ProbeCapability(); got.Backend != "bwrap" || got.Mode != CapabilityKernelEnforced {
		t.Fatalf("seam not honored: %+v", got)
	}

	capabilityProbe = func() SandboxCapability {
		return SandboxCapability{
			Mode:    CapabilityUnavailable,
			Backend: "linux_policy_only",
			Reason:  "restricted Docker CI host",
		}
	}
	got := ProbeCapability()
	if got.Mode != CapabilityUnavailable {
		t.Fatalf("expected unavailable mock, got %+v", got)
	}
}

// TestEvaluatePolicyContract_DegradedWorkspaceWriteGate covers the #971 WP1
// capability-probed preflight decision matrix: workspace_write over a process
// transport must never silently degrade to policy_only.
func TestEvaluatePolicyContract_DegradedWorkspaceWriteGate(t *testing.T) {
	tests := []struct {
		name        string
		req         RequestedPolicy
		proof       EnforcementProof
		wantAllowed bool
		wantCode    string
		wantDegrade bool
	}{
		{
			name: "workspace_write policy_only without opt-in is refused",
			req:  RequestedPolicy{Permission: "workspace_write", TransportKind: "process"},
			proof: newSandboxKernelProof(ModePolicyOnly, "linux_policy_only", false,
				"Linux policy_only fallback"),
			wantAllowed: false,
			wantCode:    ErrDegradedSandboxNotAllowed.Error(),
		},
		{
			name: "workspace_write policy_only with opt-in proceeds recorded-degraded",
			req:  RequestedPolicy{Permission: "workspace_write", TransportKind: "process", AllowDegradedSandbox: true},
			proof: newSandboxKernelProof(ModePolicyOnly, "linux_policy_only", false,
				"Linux policy_only fallback"),
			wantAllowed: true,
			wantDegrade: true,
		},
		{
			name: "windows job_object baseline is never locked out",
			req:  RequestedPolicy{Permission: "workspace_write", TransportKind: "process"},
			proof: newSandboxKernelProof(ModeJobObjectEnvScrubbed, "windows_job_object", false,
				"Windows JobObject"),
			wantAllowed: true,
			wantDegrade: true,
		},
		{
			name:        "kernel_enforced workspace_write is not degraded",
			req:         RequestedPolicy{Permission: "workspace_write", TransportKind: "process"},
			proof:       newSandboxKernelProof(ModeKernelEnforced, "seatbelt", false, "Seatbelt"),
			wantAllowed: true,
		},
		{
			name:        "read_only policy_only is recorded but not gated",
			req:         RequestedPolicy{Permission: "read_only", TransportKind: "process"},
			proof:       newSandboxKernelProof(ModePolicyOnly, "linux_policy_only", false, "policy fallback"),
			wantAllowed: true,
			wantDegrade: true,
		},
		{
			name:        "explicit --no-sandbox workspace_write proceeds with recorded degradation",
			req:         RequestedPolicy{Permission: "workspace_write", TransportKind: "process", NoSandbox: true},
			proof:       newSandboxDisabledProof(),
			wantAllowed: true,
			wantDegrade: true,
		},
		{
			name:        "mediated http transport is exempt from the workspace_write gate",
			req:         RequestedPolicy{Permission: "workspace_write", TransportKind: "http_request_transport"},
			proof:       NewMediatedTransportProof("http_request_transport"),
			wantAllowed: true,
			wantDegrade: true,
		},
		{
			name:        "require-kernel-sandbox precedence preserved",
			req:         RequestedPolicy{Permission: "read_only", TransportKind: "process", RequireKernelEnforced: true},
			proof:       newSandboxKernelProof(ModePolicyOnly, "linux_policy_only", false, "policy fallback"),
			wantAllowed: false,
			wantCode:    ErrKernelSandboxRequired.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := EvaluatePolicyContract(tc.req, tc.proof)
			if report.Evaluated.Allowed != tc.wantAllowed {
				t.Fatalf("allowed=%v (code=%s reason=%s), want %v", report.Evaluated.Allowed, report.Evaluated.RejectionCode, report.Evaluated.RejectionReason, tc.wantAllowed)
			}
			if !tc.wantAllowed {
				if report.Evaluated.RejectionCode != tc.wantCode {
					t.Fatalf("rejection code=%s, want %s", report.Evaluated.RejectionCode, tc.wantCode)
				}
				if report.Effective.SandboxDegraded {
					t.Fatal("refused attempt must not report sandbox_degraded=true (it did not proceed)")
				}
				return
			}
			if report.Effective.SandboxDegraded != tc.wantDegrade {
				t.Fatalf("sandbox_degraded=%v, want %v", report.Effective.SandboxDegraded, tc.wantDegrade)
			}
			if tc.wantDegrade && strings.TrimSpace(report.Effective.SandboxDegradedReason) == "" {
				t.Fatal("degraded enforcement must carry a machine-readable reason")
			}
		})
	}
}

// TestPrepare_DegradedWorkspaceWriteFailClosed drives the full Prepare gate
// with a stubbed per-OS layer that reports a policy-only fallback (#971 WP1).
func TestPrepare_DegradedWorkspaceWriteFailClosed(t *testing.T) {
	origPrepare := prepareOSFn
	t.Cleanup(func() { prepareOSFn = origPrepare })

	stubPolicyOnly := func(cfg Config, env []string, tmpDir, gocacheDir, sandboxID string, ownTmp bool) (*PreparedLaunch, error) {
		return &PreparedLaunch{
			Argv:      append([]string{}, cfg.Argv...),
			Env:       env,
			Mode:      ModePolicyOnly,
			Backend:   "linux_policy_only",
			TempDir:   tmpDir,
			SandboxID: sandboxID,
			Cleanup: func() {
				if ownTmp {
					removeAllLogged(tmpDir)
				}
			},
			proof: newSandboxKernelProof(ModePolicyOnly, "linux_policy_only", false,
				"Linux policy_only fallback; no kernel filesystem or socket isolation"),
		}, nil
	}

	t.Run("refused without flag", func(t *testing.T) {
		prepareOSFn = stubPolicyOnly
		_, err := Prepare(Config{
			Argv:        []string{"true"},
			WorktreeDir: t.TempDir(),
			RunDir:      t.TempDir(),
			Permission:  "workspace_write",
		})
		if !errors.Is(err, ErrDegradedSandboxNotAllowed) {
			t.Fatalf("expected ErrDegradedSandboxNotAllowed, got %v", err)
		}
		var rej *PolicyRejectionError
		if !errors.As(err, &rej) {
			t.Fatalf("expected *PolicyRejectionError, got %T", err)
		}
		if !strings.Contains(rej.Report.Evaluated.RejectionReason, "allow-degraded-sandbox") {
			t.Fatalf("refusal must name the explicit opt-in flag, got %q", rej.Report.Evaluated.RejectionReason)
		}
	})

	t.Run("allowed with flag records degraded", func(t *testing.T) {
		prepareOSFn = stubPolicyOnly
		prep, err := Prepare(Config{
			Argv:                 []string{"true"},
			WorktreeDir:          t.TempDir(),
			RunDir:               t.TempDir(),
			Permission:           "workspace_write",
			AllowDegradedSandbox: true,
		})
		if err != nil {
			t.Fatalf("expected degraded proceed, got %v", err)
		}
		defer prep.Cleanup()
		if !prep.Degraded || strings.TrimSpace(prep.DegradedReason) == "" {
			t.Fatalf("expected Degraded=true with reason, got degraded=%v reason=%q", prep.Degraded, prep.DegradedReason)
		}
		if prep.PolicyReport == nil || !prep.PolicyReport.Effective.SandboxDegraded {
			t.Fatalf("policy report must record sandbox_degraded, got %+v", prep.PolicyReport)
		}
	})

	t.Run("capable host path unchanged (kernel_enforced)", func(t *testing.T) {
		prepareOSFn = func(cfg Config, env []string, tmpDir, gocacheDir, sandboxID string, ownTmp bool) (*PreparedLaunch, error) {
			return &PreparedLaunch{
				Argv:      append([]string{}, cfg.Argv...),
				Env:       env,
				Mode:      ModeKernelEnforced,
				Backend:   "seatbelt",
				TempDir:   tmpDir,
				SandboxID: sandboxID,
				Cleanup:   func() {},
				proof:     newSandboxKernelProof(ModeKernelEnforced, "seatbelt", false, "mocked capable host"),
			}, nil
		}
		prep, err := Prepare(Config{
			Argv:        []string{"true"},
			WorktreeDir: t.TempDir(),
			RunDir:      t.TempDir(),
			Permission:  "workspace_write",
		})
		if err != nil {
			t.Fatalf("capable host must auto-enforce without flags, got %v", err)
		}
		defer prep.Cleanup()
		if prep.Mode != ModeKernelEnforced || prep.Degraded {
			t.Fatalf("expected kernel_enforced non-degraded, got mode=%s degraded=%v", prep.Mode, prep.Degraded)
		}
	})
}
