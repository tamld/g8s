package doctor

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/sandbox"
	"github.com/tamld/g8s/internal/signing"
)

func allPassedProbeSummary() *AssuranceProbeSummary {
	return &AssuranceProbeSummary{
		EscapePassed: true,
		EscapeDetail: "as-001 blocked=5/5",
		MutantPassed: true,
		MutantDetail: "as-002 caught=4/4, false_passes=0",
		CrashPassed:  true,
		CrashDetail:  "as-003/as-004 passed",
		AuditPassed:  true,
		AuditDetail:  "as-005 checks=5/5",
	}
}

func mockPrepareSandbox(mode sandbox.ContainmentMode, backend string, supportsNetwork bool) func(sandbox.Config) (*sandbox.PreparedLaunch, error) {
	return func(cfg sandbox.Config) (*sandbox.PreparedLaunch, error) {
		if cfg.RequireKernelEnforced && mode != sandbox.ModeKernelEnforced {
			return nil, sandbox.ErrKernelSandboxRequired
		}
		if cfg.RequireNetworkEnforced && !supportsNetwork {
			return nil, sandbox.ErrNetworkEnforcementRequired
		}
		netEnf := sandbox.NetworkUnrestricted
		if cfg.RequireNetworkEnforced && supportsNetwork {
			netEnf = sandbox.NetworkEnforced
		}
		return &sandbox.PreparedLaunch{
			Mode:    mode,
			Backend: backend,
			PolicyReport: &sandbox.PolicyEnforcementReport{
				NetworkEnforcement: netEnf,
			},
			Cleanup: func() {},
		}, nil
	}
}

func findGateStatus(t *testing.T, st *TrustAssuranceStatus, gateID string) TrustGateAssessment {
	t.Helper()
	for _, g := range st.Gates {
		if g.Gate == gateID {
			return g
		}
	}
	t.Fatalf("missing gate %s in %+v", gateID, st.Gates)
	return TrustGateAssessment{}
}

func TestEvaluateTrustAssurance_PerOSContainmentMatrix(t *testing.T) {
	sha := strings.Repeat("a", 64)
	commit := "0123456789abcdef0123456789abcdef01234567"

	t.Run("macos_seatbelt_network_enforced_all_enforced", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "darwin",
			Arch:           "arm64",
			BinaryVersion:  "0.25.0",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
			ProbeMetrics:   allPassedProbeSummary(),
		})
		if st.Schema != TrustAssuranceSchema {
			t.Fatalf("schema = %q, want %q", st.Schema, TrustAssuranceSchema)
		}
		if st.OverallVerdict != TrustGateEnforced || st.NonCompensatoryVeto {
			t.Fatalf("expected ENFORCED without veto, got verdict=%s veto=%t reasons=%v", st.OverallVerdict, st.NonCompensatoryVeto, st.VetoReasons)
		}
		if !st.KernelEnforced || st.NetworkEnforcement != string(sandbox.NetworkEnforced) {
			t.Fatalf("unexpected sandbox/network posture: kernel=%t net=%s", st.KernelEnforced, st.NetworkEnforcement)
		}
		if st.BinaryMatchesSource == nil || !*st.BinaryMatchesSource {
			t.Fatalf("expected BinaryMatchesSource=true, got %v", st.BinaryMatchesSource)
		}
		for _, g := range st.Gates {
			if g.Status != TrustGateEnforced {
				t.Fatalf("gate %s = %s (%s), want ENFORCED", g.Gate, g.Status, g.Detail)
			}
		}
	})

	t.Run("linux_landlock_without_socket_block_is_mediated_only", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "linux",
			Arch:           "amd64",
			BinaryVersion:  "0.25.0",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "landlock", false),
			ProbeMetrics:   allPassedProbeSummary(),
		})
		if st.OverallVerdict != TrustGateMediatedOnly || st.NonCompensatoryVeto {
			t.Fatalf("expected MEDIATED_ONLY without veto, got verdict=%s veto=%t reasons=%v", st.OverallVerdict, st.NonCompensatoryVeto, st.VetoReasons)
		}
		if !st.KernelEnforced || st.NetworkEnforcement != string(sandbox.NetworkUnrestricted) {
			t.Fatalf("unexpected landlock posture: kernel=%t net=%s", st.KernelEnforced, st.NetworkEnforcement)
		}
		g1 := findGateStatus(t, st, "TRUST-G1")
		if g1.Status != TrustGateMediatedOnly {
			t.Fatalf("TRUST-G1 = %s, want MEDIATED_ONLY", g1.Status)
		}
		if len(st.MediatedGates) != 1 || st.MediatedGates[0] != "TRUST-G1" {
			t.Fatalf("MediatedGates = %v, want [TRUST-G1]", st.MediatedGates)
		}
	})

	t.Run("windows_job_object_reports_mediated_only_and_kernel_false", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "windows",
			Arch:           "amd64",
			BinaryVersion:  "0.25.0",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeJobObjectEnvScrubbed, "windows_job_object", false),
			ProbeMetrics:   allPassedProbeSummary(),
		})
		if st.KernelEnforced {
			t.Fatalf("Windows JobObject must report KernelEnforced=false")
		}
		if st.OverallVerdict != TrustGateMediatedOnly || st.NonCompensatoryVeto {
			t.Fatalf("expected MEDIATED_ONLY without veto on Windows JobObject, got verdict=%s veto=%t", st.OverallVerdict, st.NonCompensatoryVeto)
		}
		g1 := findGateStatus(t, st, "TRUST-G1")
		if g1.Status != TrustGateMediatedOnly {
			t.Fatalf("Windows TRUST-G1 = %s, want MEDIATED_ONLY", g1.Status)
		}
	})

	t.Run("policy_only_fallback_triggers_non_compensatory_veto", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "linux",
			Arch:           "amd64",
			BinaryVersion:  "0.25.0",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModePolicyOnly, "linux_policy_only", false),
			ProbeMetrics:   allPassedProbeSummary(),
		})
		if st.OverallVerdict != TrustGateNotEnforced || !st.NonCompensatoryVeto {
			t.Fatalf("expected NOT_ENFORCED + veto despite 5 other passing gates, got verdict=%s veto=%t", st.OverallVerdict, st.NonCompensatoryVeto)
		}
		g1 := findGateStatus(t, st, "TRUST-G1")
		if g1.Status != TrustGateNotEnforced {
			t.Fatalf("TRUST-G1 = %s, want NOT_ENFORCED", g1.Status)
		}
	})

	t.Run("sandbox_probe_error_marks_unverified_with_veto", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:            "linux",
			Arch:          "amd64",
			BinaryVersion: "0.25.0",
			BinaryCommit:  commit,
			SourceCommit:  commit,
			BinarySHA256:  sha,
			StateDir:      t.TempDir(),
			DBHealthy:     true,
			PrepareSandbox: func(sandbox.Config) (*sandbox.PreparedLaunch, error) {
				return nil, errors.New("probe unavailable")
			},
			ProbeMetrics: allPassedProbeSummary(),
		})
		if st.OverallVerdict != TrustGateUnverified || !st.NonCompensatoryVeto {
			t.Fatalf("expected UNVERIFIED + veto on probe failure, got verdict=%s veto=%t", st.OverallVerdict, st.NonCompensatoryVeto)
		}
	})
}

func TestEvaluateTrustAssurance_BinaryCommitMismatchAndFastDoctorMode(t *testing.T) {
	sha := strings.Repeat("b", 64)
	binCommit := "1111111111111111111111111111111111111111"
	srcCommit := "2222222222222222222222222222222222222222"

	stMismatch := EvaluateTrustAssurance(TrustProbeInput{
		OS:             "darwin",
		Arch:           "arm64",
		BinaryCommit:   binCommit,
		SourceCommit:   srcCommit,
		BinarySHA256:   sha,
		StateDir:       t.TempDir(),
		DBHealthy:      true,
		PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
		ProbeMetrics:   allPassedProbeSummary(),
	})
	if stMismatch.BinaryMatchesSource == nil || *stMismatch.BinaryMatchesSource {
		t.Fatalf("expected BinaryMatchesSource=false, got %v", stMismatch.BinaryMatchesSource)
	}
	if stMismatch.OverallVerdict != TrustGateNotEnforced || !stMismatch.NonCompensatoryVeto {
		t.Fatalf("expected NOT_ENFORCED + veto on stale binary mismatch, got verdict=%s veto=%t", stMismatch.OverallVerdict, stMismatch.NonCompensatoryVeto)
	}
	g5 := findGateStatus(t, stMismatch, "TRUST-G5")
	if g5.Status != TrustGateNotEnforced {
		t.Fatalf("TRUST-G5 = %s, want NOT_ENFORCED", g5.Status)
	}

	stFast := EvaluateTrustAssurance(TrustProbeInput{
		OS:             "darwin",
		Arch:           "arm64",
		BinaryCommit:   srcCommit,
		SourceCommit:   srcCommit,
		BinarySHA256:   sha,
		StateDir:       t.TempDir(),
		DBHealthy:      true,
		PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
		ProbeMetrics:   nil, // fast doctor mode
	})
	if stFast.OverallVerdict != TrustGateUnverified || !stFast.NonCompensatoryVeto {
		t.Fatalf("expected UNVERIFIED + veto in fast doctor mode, got verdict=%s veto=%t", stFast.OverallVerdict, stFast.NonCompensatoryVeto)
	}
	for _, id := range []string{"TRUST-G3", "TRUST-G4", "TRUST-G5"} {
		if g := findGateStatus(t, stFast, id); g.Status != TrustGateUnverified {
			t.Fatalf("%s = %s in fast doctor mode, want UNVERIFIED", id, g.Status)
		}
	}
}

func TestEvaluateTrustAssurance_S0KeyCustody(t *testing.T) {
	sha := strings.Repeat("c", 64)
	commit := "abcdef0123456789abcdef0123456789abcdef01"

	f0Pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s0Pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("disjoint_s0_key_provisioned", func(t *testing.T) {
		dir := t.TempDir()
		if wErr := os.WriteFile(filepath.Join(dir, signing.F0PublicKeyFile), []byte(hex.EncodeToString(f0Pub)+"\n"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		if wErr := os.WriteFile(filepath.Join(dir, signing.S0PublicKeyFile), []byte(hex.EncodeToString(s0Pub)+"\n"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}

		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "darwin",
			Arch:           "arm64",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       dir,
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
			ProbeMetrics:   allPassedProbeSummary(),
		})
		if !st.S0KeyProvisioned {
			t.Fatalf("expected S0KeyProvisioned=true")
		}
		g2 := findGateStatus(t, st, "TRUST-G2")
		if g2.Status != TrustGateEnforced {
			t.Fatalf("TRUST-G2 = %s, want ENFORCED", g2.Status)
		}
	})

	t.Run("s0_key_colliding_with_f0_key_triggers_veto", func(t *testing.T) {
		dir := t.TempDir()
		if wErr := os.WriteFile(filepath.Join(dir, signing.F0PublicKeyFile), []byte(hex.EncodeToString(f0Pub)+"\n"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		if wErr := os.WriteFile(filepath.Join(dir, signing.S0PublicKeyFile), []byte(hex.EncodeToString(f0Pub)+"\n"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}

		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "darwin",
			Arch:           "arm64",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       dir,
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
			ProbeMetrics:   allPassedProbeSummary(),
		})
		if st.S0KeyProvisioned {
			t.Fatalf("expected S0KeyProvisioned=false on colliding F0/S0 keys")
		}
		g2 := findGateStatus(t, st, "TRUST-G2")
		if g2.Status != TrustGateNotEnforced || !st.NonCompensatoryVeto || st.OverallVerdict != TrustGateNotEnforced {
			t.Fatalf("expected NOT_ENFORCED + veto on colliding keys, got g2=%+v overall=%s veto=%t", g2, st.OverallVerdict, st.NonCompensatoryVeto)
		}
	})

	t.Run("invalid_explicit_s0_path_triggers_veto", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "darwin",
			Arch:           "arm64",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			S0PubKeyPath:   filepath.Join(t.TempDir(), "missing-s0.pub"),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
			ProbeMetrics:   allPassedProbeSummary(),
		})
		g2 := findGateStatus(t, st, "TRUST-G2")
		if g2.Status != TrustGateNotEnforced || !st.NonCompensatoryVeto {
			t.Fatalf("expected NOT_ENFORCED + veto for missing explicit S0 path, got g2=%+v veto=%t", g2, st.NonCompensatoryVeto)
		}
	})
}

// Issue #974: trust-gate status must distinguish "capability detected" (policy
// contract only) from "adversarially verified" (live escape-probe evidence).
func TestEvaluateTrustAssurance_G1CapabilityDetectedWithoutLiveProbe(t *testing.T) {
	sha := strings.Repeat("d", 64)
	commit := "0123456789abcdef0123456789abcdef01234567"

	t.Run("kernel_capable_policy_without_probe_is_capability_detected", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "darwin",
			Arch:           "arm64",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
			ProbeMetrics:   nil, // fast doctor mode: no live as-001 escape probe
		})
		g1 := findGateStatus(t, st, "TRUST-G1")
		if g1.Status != TrustGateCapabilityDetected {
			t.Fatalf("TRUST-G1 = %s (%s), want CAPABILITY_DETECTED when only the sandbox policy contract was probed", g1.Status, g1.Detail)
		}
		if !strings.Contains(g1.Detail, "policy-contract capability; no live adversarial probe executed") {
			t.Fatalf("TRUST-G1 detail must state policy-contract capability, got %q", g1.Detail)
		}
		if g1.EpistemicClass != sandbox.EpistemicMeasuredMetric {
			t.Fatalf("epistemic_class = %s, want measured_metric (ADR-0042 G5 labeling intact)", g1.EpistemicClass)
		}
		if st.KernelEnforced != true {
			t.Fatalf("kernel_enforced posture telemetry must stay true, got %t", st.KernelEnforced)
		}
	})

	t.Run("live_escape_probe_pass_keeps_enforced", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "darwin",
			Arch:           "arm64",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
			ProbeMetrics:   &AssuranceProbeSummary{EscapePassed: true, EscapeDetail: "as-001 blocked=5/5"},
		})
		g1 := findGateStatus(t, st, "TRUST-G1")
		if g1.Status != TrustGateEnforced {
			t.Fatalf("TRUST-G1 = %s (%s), want ENFORCED when the live as-001 escape probe passed", g1.Status, g1.Detail)
		}
	})

	t.Run("live_escape_probe_fail_stays_not_enforced", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "darwin",
			Arch:           "arm64",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
			ProbeMetrics:   &AssuranceProbeSummary{EscapePassed: false, EscapeDetail: "as-001 vector 3 escaped"},
		})
		g1 := findGateStatus(t, st, "TRUST-G1")
		if g1.Status != TrustGateNotEnforced {
			t.Fatalf("TRUST-G1 = %s, want NOT_ENFORCED when the live as-001 escape probe failed", g1.Status)
		}
	})

	t.Run("capability_detected_degrades_overall_verdict_to_unverified_with_veto", func(t *testing.T) {
		st := EvaluateTrustAssurance(TrustProbeInput{
			OS:             "darwin",
			Arch:           "arm64",
			BinaryCommit:   commit,
			SourceCommit:   commit,
			BinarySHA256:   sha,
			StateDir:       t.TempDir(),
			DBHealthy:      true,
			PrepareSandbox: mockPrepareSandbox(sandbox.ModeKernelEnforced, "seatbelt", true),
			ProbeMetrics:   nil,
		})
		if st.OverallVerdict != TrustGateUnverified || !st.NonCompensatoryVeto {
			t.Fatalf("overall verdict = %s veto=%t, want UNVERIFIED + veto when TRUST-G1 is only capability-detected", st.OverallVerdict, st.NonCompensatoryVeto)
		}
		vetoNamedG1 := false
		for _, r := range st.VetoReasons {
			if strings.Contains(r, "TRUST-G1") {
				vetoNamedG1 = true
				break
			}
		}
		if !vetoNamedG1 {
			t.Fatalf("veto reasons must mention TRUST-G1, got %v", st.VetoReasons)
		}
	})
}

func TestComputeNonCompensatoryVerdict_CapabilityDetectedIsNotEnforced(t *testing.T) {
	gates := []TrustGateAssessment{
		{Gate: "TRUST-G1", Status: TrustGateCapabilityDetected, Detail: "policy-contract capability; no live adversarial probe executed"},
		{Gate: "TRUST-G2", Status: TrustGateEnforced, Detail: "ok"},
		{Gate: "TRUST-G3", Status: TrustGateEnforced, Detail: "ok"},
		{Gate: "TRUST-G4", Status: TrustGateEnforced, Detail: "ok"},
		{Gate: "TRUST-G5", Status: TrustGateEnforced, Detail: "ok"},
		{Gate: "TRUST-G6", Status: TrustGateEnforced, Detail: "ok"},
	}
	verdict, veto, reasons, mediated := ComputeNonCompensatoryVerdict(gates)
	if verdict != TrustGateUnverified {
		t.Fatalf("verdict = %s, want UNVERIFIED (a capability-detected gate must not count as enforced)", verdict)
	}
	if !veto || len(reasons) == 0 {
		t.Fatalf("expected non-compensatory veto with reasons, got veto=%t reasons=%v", veto, reasons)
	}
	namedG1 := false
	for _, r := range reasons {
		if strings.Contains(r, "TRUST-G1") {
			namedG1 = true
			break
		}
	}
	if !namedG1 {
		t.Fatalf("veto reasons must mention TRUST-G1, got %v", reasons)
	}
	if len(mediated) != 0 {
		t.Fatalf("capability-detected gate must not be reported as mediated, got %v", mediated)
	}
}

func TestComputeNonCompensatoryVerdict_MissingGateFailsClosed(t *testing.T) {
	incomplete := []TrustGateAssessment{
		{Gate: "TRUST-G1", Status: TrustGateEnforced, Detail: "ok"},
		{Gate: "TRUST-G2", Status: TrustGateEnforced, Detail: "ok"},
		{Gate: "TRUST-G3", Status: TrustGateEnforced, Detail: "ok"},
		{Gate: "TRUST-G4", Status: TrustGateEnforced, Detail: "ok"},
		{Gate: "TRUST-G6", Status: TrustGateEnforced, Detail: "ok"},
	}
	verdict, veto, reasons, _ := ComputeNonCompensatoryVerdict(incomplete)
	if verdict != TrustGateUnverified || !veto || len(reasons) == 0 {
		t.Fatalf("expected UNVERIFIED + veto on missing TRUST-G5, got verdict=%s veto=%t reasons=%v", verdict, veto, reasons)
	}
}

// TestTrustAssuranceRemediations (#975): the status carries actionable
// remediation hints — a missing S0 key maps to the custody runbook, and a
// capability-detected gate maps to the live-probe upgrade path.
func TestTrustAssuranceRemediations(t *testing.T) {
	in := TrustProbeInput{
		BinaryVersion:     "0.27.1",
		BinaryCommit:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		BinaryBuildSource: "release",
		SourceCommit:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	}
	st := EvaluateTrustAssurance(in)
	found := false
	for _, r := range st.Remediations {
		if strings.Contains(r, "s0-custody-runbook") {
			found = true
		}
	}
	if !st.S0KeyProvisioned && !found {
		t.Fatalf("s0 key unprovisioned but no runbook remediation emitted: %+v", st.Remediations)
	}
	capDetected := false
	for _, g := range st.Gates {
		if g.Status == TrustGateCapabilityDetected {
			capDetected = true
		}
	}
	if capDetected {
		upgraded := false
		for _, r := range st.Remediations {
			if strings.Contains(r, "CAPABILITY_DETECTED") {
				upgraded = true
			}
		}
		if !upgraded {
			t.Fatalf("CAPABILITY_DETECTED gate present but no upgrade remediation: %+v", st.Remediations)
		}
	}
}
