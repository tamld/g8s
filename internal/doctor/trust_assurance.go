package doctor

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/memory"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/sandbox"
	"github.com/tamld/g8s/internal/signing"
	"github.com/tamld/g8s/internal/verifier"
)

// TrustAssuranceSchema identifies the JSON contract for non-compensatory ADR-0042 telemetry.
const TrustAssuranceSchema = "g8s.trust-assurance/v1"

// TrustGateStatus classifies enforcement strength for a single TRUST gate or overall verdict.
type TrustGateStatus string

const (
	TrustGateEnforced     TrustGateStatus = "ENFORCED"
	TrustGateMediatedOnly TrustGateStatus = "MEDIATED_ONLY"
	TrustGateNotEnforced  TrustGateStatus = "NOT_ENFORCED"
	TrustGateUnverified   TrustGateStatus = "UNVERIFIED"
)

var canonicalTrustGateOrder = []string{
	"TRUST-G1",
	"TRUST-G2",
	"TRUST-G3",
	"TRUST-G4",
	"TRUST-G5",
	"TRUST-G6",
}

// TrustGateAssessment records the measured posture of one ADR-0042 TRUST gate.
type TrustGateAssessment struct {
	Gate           string                 `json:"gate"`
	Name           string                 `json:"name"`
	Status         TrustGateStatus        `json:"status"`
	EpistemicClass sandbox.EpistemicClass `json:"epistemic_class"`
	EvidenceSource string                 `json:"evidence_source"`
	Detail         string                 `json:"detail"`
}

// BinaryProvenance captures build metadata injected by the g8s CLI binary or VCS buildinfo.
type BinaryProvenance struct {
	Version     string `json:"version,omitempty"`
	Commit      string `json:"commit,omitempty"`
	BuildSource string `json:"build_source,omitempty"`
	VCSModified bool   `json:"vcs_modified"`
	BinaryPath  string `json:"binary_path,omitempty"`
}

// AssuranceProbeSummary supplies measured probe results when running alongside tools/assurance_benchmark.sh.
type AssuranceProbeSummary struct {
	EscapePassed bool   `json:"escape_passed"`
	EscapeDetail string `json:"escape_detail,omitempty"`
	MutantPassed bool   `json:"mutant_passed"`
	MutantDetail string `json:"mutant_detail,omitempty"`
	CrashPassed  bool   `json:"crash_passed"`
	CrashDetail  string `json:"crash_detail,omitempty"`
	AuditPassed  bool   `json:"audit_passed"`
	AuditDetail  string `json:"audit_detail,omitempty"`
}

// TrustAssuranceStatus emits a per-OS, per-binary non-compensatory TRUST status object (ADR-0042).
type TrustAssuranceStatus struct {
	Schema              string                 `json:"schema"`
	EpistemicClass      sandbox.EpistemicClass `json:"epistemic_class"`
	OS                  string                 `json:"os"`
	Arch                string                 `json:"arch"`
	Platform            string                 `json:"platform"`
	BinaryVersion       string                 `json:"binary_version,omitempty"`
	BinaryCommit        string                 `json:"binary_commit,omitempty"`
	BinaryBuildSource   string                 `json:"binary_build_source,omitempty"`
	VCSModified         bool                   `json:"vcs_modified"`
	BinaryPath          string                 `json:"binary_path,omitempty"`
	BinarySHA256        string                 `json:"binary_sha256,omitempty"`
	SourceCommit        string                 `json:"source_commit,omitempty"`
	BinaryMatchesSource *bool                  `json:"binary_matches_source,omitempty"`
	SandboxMode         string                 `json:"sandbox_mode"`
	SandboxBackend      string                 `json:"sandbox_backend"`
	KernelEnforced      bool                   `json:"kernel_enforced"`
	NetworkEnforcement  string                 `json:"network_enforcement"`
	S0KeyProvisioned    bool                   `json:"s0_key_provisioned"`
	OverallVerdict      TrustGateStatus        `json:"overall_verdict"`
	NonCompensatoryVeto bool                   `json:"non_compensatory_veto"`
	VetoReasons         []string               `json:"veto_reasons,omitempty"`
	MediatedGates       []string               `json:"mediated_gates,omitempty"`
	Gates               []TrustGateAssessment  `json:"gates"`
}

// TrustProbeInput configures EvaluateTrustAssurance for live diagnostics or deterministic per-OS testing.
type TrustProbeInput struct {
	OS                string
	Arch              string
	BinaryVersion     string
	BinaryCommit      string
	BinaryBuildSource string
	VCSModified       bool
	BinaryPath        string
	BinarySHA256      string
	SourceCommit      string
	StateDir          string
	S0PubKeyPath      string
	DBHealthy         bool
	PrepareSandbox    func(sandbox.Config) (*sandbox.PreparedLaunch, error)
	ProbeMetrics      *AssuranceProbeSummary
}

func gateStatusRank(s TrustGateStatus) int {
	switch s {
	case TrustGateEnforced:
		return 3
	case TrustGateMediatedOnly:
		return 2
	case TrustGateUnverified:
		return 1
	case TrustGateNotEnforced:
		return 0
	default:
		return 0
	}
}

// ComputeNonCompensatoryVerdict applies the ADR-0042 non-compensatory rule across all 6 TRUST gates.
// No strong gate can compensate for any weak, unverified, missing, or mediated-only gate.
func ComputeNonCompensatoryVerdict(gates []TrustGateAssessment) (verdict TrustGateStatus, veto bool, vetoReasons []string, mediated []string) {
	byGate := make(map[string]TrustGateAssessment, len(gates))
	for _, g := range gates {
		if _, exists := byGate[g.Gate]; !exists {
			byGate[g.Gate] = g
		}
	}

	verdict = TrustGateEnforced
	minRank := gateStatusRank(TrustGateEnforced)

	for _, id := range canonicalTrustGateOrder {
		g, ok := byGate[id]
		if !ok {
			veto = true
			vetoReasons = append(vetoReasons, fmt.Sprintf("%s: gate assessment missing (fail-closed)", id))
			if gateStatusRank(TrustGateUnverified) < minRank {
				minRank = gateStatusRank(TrustGateUnverified)
				verdict = TrustGateUnverified
			}
			continue
		}
		rank := gateStatusRank(g.Status)
		if rank < minRank {
			minRank = rank
			verdict = g.Status
		}
		switch g.Status {
		case TrustGateEnforced:
		case TrustGateMediatedOnly:
			mediated = append(mediated, g.Gate)
		case TrustGateUnverified:
			veto = true
			vetoReasons = append(vetoReasons, fmt.Sprintf("%s: %s", g.Gate, g.Detail))
		default:
			if g.Status != TrustGateNotEnforced {
				verdict = TrustGateNotEnforced
				minRank = 0
			}
			veto = true
			vetoReasons = append(vetoReasons, fmt.Sprintf("%s: %s", g.Gate, g.Detail))
		}
	}

	for _, g := range gates {
		isCanonical := false
		for _, id := range canonicalTrustGateOrder {
			if g.Gate == id {
				isCanonical = true
				break
			}
		}
		if isCanonical {
			continue
		}
		rank := gateStatusRank(g.Status)
		if rank < minRank {
			minRank = rank
			verdict = g.Status
		}
		switch g.Status {
		case TrustGateNotEnforced, TrustGateUnverified:
			veto = true
			vetoReasons = append(vetoReasons, fmt.Sprintf("%s: %s", g.Gate, g.Detail))
		case TrustGateMediatedOnly:
			mediated = append(mediated, g.Gate)
		}
	}

	return verdict, veto, vetoReasons, mediated
}

func isHexCommit(s string) bool {
	norm := normalizeRevision(s)
	if len(norm) < 7 || len(norm) > 40 {
		return false
	}
	for i := 0; i < len(norm); i++ {
		c := norm[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func hashExecutableSHA256(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	// #nosec G304 -- hashes the running g8s executable resolved via os.Executable for provenance reporting.
	f, err := os.Open(trimmed)
	if err != nil {
		return ""
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func resolveProbeExecutable(binPath string) string {
	if p, err := exec.LookPath("true"); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	if strings.TrimSpace(binPath) != "" {
		return strings.TrimSpace(binPath)
	}
	if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
		return exe
	}
	return "g8s"
}

func evaluateTrustG1(in TrustProbeInput) (gate TrustGateAssessment, mode, backend string, kernelEnforced bool, netEnf string) {
	prepareFn := in.PrepareSandbox
	if prepareFn == nil {
		prepareFn = sandbox.Prepare
	}
	probeExe := resolveProbeExecutable(in.BinaryPath)

	basePrep, baseErr := prepareFn(sandbox.Config{
		Argv:       []string{probeExe},
		Permission: "read_only",
	})
	if basePrep != nil && basePrep.Cleanup != nil {
		basePrep.Cleanup()
	}

	if baseErr != nil || basePrep == nil {
		reason := "OS sandbox baseline probe returned nil launch"
		if baseErr != nil {
			reason = fmt.Sprintf("OS sandbox baseline probe failed: %v", baseErr)
		}
		return TrustGateAssessment{
			Gate:           "TRUST-G1",
			Name:           "Hostile Worker Containment & Network Isolation",
			Status:         TrustGateUnverified,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "os_sandbox_probe",
			Detail:         reason,
		}, "unknown", "unknown", false, string(sandbox.NetworkUnrestricted)
	}

	mode = string(basePrep.Mode)
	backend = basePrep.Backend
	kernelEnforced = basePrep.Mode == sandbox.ModeKernelEnforced

	netObserved := sandbox.NetworkUnrestricted
	if basePrep.PolicyReport != nil && basePrep.PolicyReport.NetworkEnforcement != "" {
		netObserved = basePrep.PolicyReport.NetworkEnforcement
	}

	netPrep, netErr := prepareFn(sandbox.Config{
		Argv:                   []string{probeExe},
		Permission:             "read_only",
		RequireKernelEnforced:  true,
		RequireNetworkEnforced: true,
	})
	if netPrep != nil && netPrep.Cleanup != nil {
		netPrep.Cleanup()
	}
	if netErr == nil && netPrep != nil && netPrep.PolicyReport != nil &&
		netPrep.PolicyReport.NetworkEnforcement == sandbox.NetworkEnforced {
		netObserved = sandbox.NetworkEnforced
	}
	netEnf = string(netObserved)

	var st TrustGateStatus
	var detail string
	switch {
	case basePrep.Mode == sandbox.ModeKernelEnforced && netObserved == sandbox.NetworkEnforced:
		st = TrustGateEnforced
		detail = fmt.Sprintf("mode=%s backend=%s network_enforcement=%s (OS filesystem + outbound socket isolation active)", mode, backend, netEnf)
	case basePrep.Mode == sandbox.ModeKernelEnforced:
		st = TrustGateMediatedOnly
		detail = fmt.Sprintf("mode=%s backend=%s network_enforcement=%s (kernel FS containment active without OS socket isolation)", mode, backend, netEnf)
	case basePrep.Mode == sandbox.ModeJobObjectEnvScrubbed:
		st = TrustGateMediatedOnly
		detail = fmt.Sprintf("mode=%s backend=%s network_enforcement=%s (process-tree + env scrub active; no kernel FS ACLs or OS outbound socket block)", mode, backend, netEnf)
	case basePrep.Mode == sandbox.ModePolicyOnly:
		st = TrustGateNotEnforced
		detail = fmt.Sprintf("mode=%s backend=%s network_enforcement=%s (policy_only fallback without kernel isolation)", mode, backend, netEnf)
	default:
		st = TrustGateUnverified
		detail = fmt.Sprintf("unrecognized sandbox mode=%s backend=%s", mode, backend)
	}

	if in.ProbeMetrics != nil {
		if !in.ProbeMetrics.EscapePassed {
			st = TrustGateNotEnforced
			if strings.TrimSpace(in.ProbeMetrics.EscapeDetail) != "" {
				detail = fmt.Sprintf("as-001 escape containment failed: %s", in.ProbeMetrics.EscapeDetail)
			} else {
				detail = "as-001 escape containment failed"
			}
		} else if strings.TrimSpace(in.ProbeMetrics.EscapeDetail) != "" {
			detail = fmt.Sprintf("%s; %s", detail, in.ProbeMetrics.EscapeDetail)
		}
	}

	return TrustGateAssessment{
		Gate:           "TRUST-G1",
		Name:           "Hostile Worker Containment & Network Isolation",
		Status:         st,
		EpistemicClass: sandbox.EpistemicMeasuredMetric,
		EvidenceSource: "os_sandbox_probe",
		Detail:         detail,
	}, mode, backend, kernelEnforced, netEnf
}

func verifyTrustRootContract() error {
	f0Seed := make([]byte, ed25519.SeedSize)
	s0Seed := make([]byte, ed25519.SeedSize)
	f0Seed[0] = 1
	s0Seed[0] = 2
	f0Pub, okF0 := ed25519.NewKeyFromSeed(f0Seed).Public().(ed25519.PublicKey)
	s0Pub, okS0 := ed25519.NewKeyFromSeed(s0Seed).Public().(ed25519.PublicKey)
	if !okF0 || !okS0 {
		return errors.New("derive deterministic self-check Ed25519 public keys")
	}

	decWorker := harness.EvaluateTrustRootMutation(harness.TrustRootRequest{
		ActorStratum:              "F1",
		ActorID:                   "worker-selfcheck",
		TargetPaths:               []string{".g8s/trust-boundaries.yml"},
		DeterministicChecksPassed: true,
	})
	if decWorker.Allowed || decWorker.Code != harness.CodeTrustRootWorkerTCBDenied {
		return fmt.Errorf("worker TCB mutation must deny with %s (got allowed=%t code=%s)",
			harness.CodeTrustRootWorkerTCBDenied, decWorker.Allowed, decWorker.Code)
	}

	now := time.Now().UTC()
	decSelf := harness.EvaluateTrustRootMutation(harness.TrustRootRequest{
		ActorStratum:              "F0",
		ActorID:                   "f0-brain",
		TargetPaths:               []string{".g8s/trust-boundaries.yml"},
		DeterministicChecksPassed: true,
		S0PublicKey:               s0Pub,
		F0PublicKey:               f0Pub,
		Now:                       now,
		S0Approval: &harness.S0ApprovalAttestation{
			ApprovedBy:                "f0-brain",
			PrincipalStratum:          "S0",
			Nonce:                     "nonce-selfcheck-1234",
			IssuedAt:                  now.Add(-time.Minute).Format(time.RFC3339),
			ExpiresAt:                 now.Add(time.Hour).Format(time.RFC3339),
			TargetPaths:               []string{".g8s/trust-boundaries.yml"},
			DeterministicChecksPassed: true,
		},
	})
	if decSelf.Allowed || decSelf.Code != harness.CodeTrustRootSelfAttestationBlocked {
		return fmt.Errorf("F0 self-attestation must deny with %s (got allowed=%t code=%s)",
			harness.CodeTrustRootSelfAttestationBlocked, decSelf.Allowed, decSelf.Code)
	}

	decMissingKey := harness.EvaluateTrustRootMutation(harness.TrustRootRequest{
		ActorStratum:              "F0",
		ActorID:                   "f0-supervisor",
		TargetPaths:               []string{".g8s/trust-boundaries.yml"},
		DeterministicChecksPassed: true,
		F0PublicKey:               f0Pub,
		Now:                       now,
		S0Approval: &harness.S0ApprovalAttestation{
			ApprovedBy:                "human-operator",
			PrincipalStratum:          "S0",
			Nonce:                     "nonce-selfcheck-5678",
			IssuedAt:                  now.Add(-time.Minute).Format(time.RFC3339),
			ExpiresAt:                 now.Add(time.Hour).Format(time.RFC3339),
			TargetPaths:               []string{".g8s/trust-boundaries.yml"},
			DeterministicChecksPassed: true,
		},
	})
	if decMissingKey.Allowed || decMissingKey.Code != harness.CodeTrustRootS0KeyMissing {
		return fmt.Errorf("missing S0 key must fail closed with %s (got allowed=%t code=%s)",
			harness.CodeTrustRootS0KeyMissing, decMissingKey.Allowed, decMissingKey.Code)
	}

	return nil
}

func evaluateTrustG2(in TrustProbeInput) (gate TrustGateAssessment, s0Provisioned bool) {
	if err := verifyTrustRootContract(); err != nil {
		return TrustGateAssessment{
			Gate:           "TRUST-G2",
			Name:           "Trust Root Outside LLM Inference",
			Status:         TrustGateNotEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "trust_root_self_check",
			Detail:         fmt.Sprintf("trust root contract check failed: %v", err),
		}, false
	}

	stateDir := strings.TrimSpace(in.StateDir)
	var f0Pub ed25519.PublicKey
	if stateDir != "" {
		if loadedF0, err := signing.LoadPublicKeyFile(filepath.Join(stateDir, signing.F0PublicKeyFile)); err == nil {
			f0Pub = loadedF0
		}
	}

	explicitPath := strings.TrimSpace(in.S0PubKeyPath)
	switch {
	case explicitPath != "":
		s0Pub, err := signing.LoadPublicKeyFile(explicitPath)
		if err != nil {
			return TrustGateAssessment{
				Gate:           "TRUST-G2",
				Name:           "Trust Root Outside LLM Inference",
				Status:         TrustGateNotEnforced,
				EpistemicClass: sandbox.EpistemicMeasuredMetric,
				EvidenceSource: "s0_key_custody",
				Detail:         fmt.Sprintf("explicit S0 public key %q invalid/unreadable: %v", explicitPath, err),
			}, false
		}
		if err := signing.VerifyDisjointS0Key(s0Pub, f0Pub); err != nil {
			return TrustGateAssessment{
				Gate:           "TRUST-G2",
				Name:           "Trust Root Outside LLM Inference",
				Status:         TrustGateNotEnforced,
				EpistemicClass: sandbox.EpistemicMeasuredMetric,
				EvidenceSource: "s0_key_custody",
				Detail:         fmt.Sprintf("explicit S0 public key %q failed disjoint custody check: %v", explicitPath, err),
			}, false
		}
		s0Provisioned = true

	case stateDir != "":
		defaultPath := filepath.Join(stateDir, signing.S0PublicKeyFile)
		s0Pub, err := signing.LoadPublicKeyFile(defaultPath)
		switch {
		case err == nil:
			if dErr := signing.VerifyDisjointS0Key(s0Pub, f0Pub); dErr != nil {
				return TrustGateAssessment{
					Gate:           "TRUST-G2",
					Name:           "Trust Root Outside LLM Inference",
					Status:         TrustGateNotEnforced,
					EpistemicClass: sandbox.EpistemicMeasuredMetric,
					EvidenceSource: "s0_key_custody",
					Detail:         fmt.Sprintf("state S0 public key failed disjoint custody check: %v", dErr),
				}, false
			}
			s0Provisioned = true
		case !errors.Is(err, os.ErrNotExist):
			return TrustGateAssessment{
				Gate:           "TRUST-G2",
				Name:           "Trust Root Outside LLM Inference",
				Status:         TrustGateNotEnforced,
				EpistemicClass: sandbox.EpistemicMeasuredMetric,
				EvidenceSource: "s0_key_custody",
				Detail:         fmt.Sprintf("state S0 public key %q corrupt or unreadable: %v", defaultPath, err),
			}, false
		}
	}

	if in.ProbeMetrics != nil && (!in.ProbeMetrics.EscapePassed || !in.ProbeMetrics.AuditPassed) {
		return TrustGateAssessment{
			Gate:           "TRUST-G2",
			Name:           "Trust Root Outside LLM Inference",
			Status:         TrustGateNotEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "assurance_probe:as-001+as-005",
			Detail:         "fail-closed trust root or cryptographic scope probe failed",
		}, s0Provisioned
	}

	detail := "Tier B fail-closed TCB enforcement active (s0-human.pub not provisioned yet; Tier B deliveries blocked fail-closed)"
	if s0Provisioned {
		detail = "Tier B fail-closed TCB enforcement active with distinct Ed25519 S0 human public key"
	}
	return TrustGateAssessment{
		Gate:           "TRUST-G2",
		Name:           "Trust Root Outside LLM Inference",
		Status:         TrustGateEnforced,
		EpistemicClass: sandbox.EpistemicMeasuredMetric,
		EvidenceSource: "trust_root_self_check+s0_key_custody",
		Detail:         detail,
	}, s0Provisioned
}

func evaluateTrustG3(in TrustProbeInput) TrustGateAssessment {
	if in.ProbeMetrics == nil {
		return TrustGateAssessment{
			Gate:           "TRUST-G3",
			Name:           "Independent Evaluation & Mutant Canaries",
			Status:         TrustGateUnverified,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "doctor_fast_mode",
			Detail:         "live as-002 mutant canary probe not executed in fast doctor mode; run 'bash tools/assurance_benchmark.sh --json'",
		}
	}
	detail := strings.TrimSpace(in.ProbeMetrics.MutantDetail)
	if in.ProbeMetrics.MutantPassed {
		if detail == "" {
			detail = "as-002 seeded mutant canary + anti-false-PASS suite passed"
		}
		return TrustGateAssessment{
			Gate:           "TRUST-G3",
			Name:           "Independent Evaluation & Mutant Canaries",
			Status:         TrustGateEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "assurance_probe:as-002",
			Detail:         detail,
		}
	}
	if detail == "" {
		detail = "as-002 seeded mutant canary or anti-false-PASS check failed"
	}
	return TrustGateAssessment{
		Gate:           "TRUST-G3",
		Name:           "Independent Evaluation & Mutant Canaries",
		Status:         TrustGateNotEnforced,
		EpistemicClass: sandbox.EpistemicMeasuredMetric,
		EvidenceSource: "assurance_probe:as-002",
		Detail:         detail,
	}
}

func evaluateTrustG4(in TrustProbeInput) TrustGateAssessment {
	if !in.DBHealthy {
		return TrustGateAssessment{
			Gate:           "TRUST-G4",
			Name:           "Recoverability & WAL CAS Fencing",
			Status:         TrustGateNotEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "sqlite_health_check",
			Detail:         "SQLite WAL control-plane database health check failed",
		}
	}
	if in.ProbeMetrics == nil {
		return TrustGateAssessment{
			Gate:           "TRUST-G4",
			Name:           "Recoverability & WAL CAS Fencing",
			Status:         TrustGateUnverified,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "doctor_fast_mode",
			Detail:         "SQLite WAL healthy; live as-003/as-004 crash recovery probes not executed in fast doctor mode",
		}
	}
	detail := strings.TrimSpace(in.ProbeMetrics.CrashDetail)
	if in.ProbeMetrics.CrashPassed {
		if detail == "" {
			detail = "SQLite WAL CAS lease recovery and orphan cleanup verified"
		}
		return TrustGateAssessment{
			Gate:           "TRUST-G4",
			Name:           "Recoverability & WAL CAS Fencing",
			Status:         TrustGateEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "assurance_probe:as-003+as-004",
			Detail:         detail,
		}
	}
	if detail == "" {
		detail = "crash lease recovery or CAS overhead probe failed"
	}
	return TrustGateAssessment{
		Gate:           "TRUST-G4",
		Name:           "Recoverability & WAL CAS Fencing",
		Status:         TrustGateNotEnforced,
		EpistemicClass: sandbox.EpistemicMeasuredMetric,
		EvidenceSource: "assurance_probe:as-003+as-004",
		Detail:         detail,
	}
}

func evaluateTrustG5(in TrustProbeInput, normBin, normSrc, binSHA string) TrustGateAssessment {
	if normSrc != "" && normBin != "" && !binaryRevisionsMatch(normBin, normSrc) {
		return TrustGateAssessment{
			Gate:           "TRUST-G5",
			Name:           "Evidence Integrity & Binary Provenance",
			Status:         TrustGateNotEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "binary_provenance",
			Detail:         fmt.Sprintf("binary commit %s does not match source commit %s", normBin, normSrc),
		}
	}
	if normSrc != "" && normBin == "" {
		return TrustGateAssessment{
			Gate:           "TRUST-G5",
			Name:           "Evidence Integrity & Binary Provenance",
			Status:         TrustGateUnverified,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "binary_provenance",
			Detail:         fmt.Sprintf("running binary lacks VCS commit SHA to compare against source %s", normSrc),
		}
	}
	if normBin == "" {
		return TrustGateAssessment{
			Gate:           "TRUST-G5",
			Name:           "Evidence Integrity & Binary Provenance",
			Status:         TrustGateUnverified,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "binary_provenance",
			Detail:         "running binary carries no VCS commit SHA",
		}
	}
	if binSHA == "" {
		return TrustGateAssessment{
			Gate:           "TRUST-G5",
			Name:           "Evidence Integrity & Binary Provenance",
			Status:         TrustGateUnverified,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "binary_provenance",
			Detail:         "binary SHA-256 digest unavailable",
		}
	}
	if in.ProbeMetrics == nil {
		return TrustGateAssessment{
			Gate:           "TRUST-G5",
			Name:           "Evidence Integrity & Binary Provenance",
			Status:         TrustGateUnverified,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "doctor_fast_mode",
			Detail:         fmt.Sprintf("binary commit=%s sha256=%s... verified; run 'bash tools/assurance_benchmark.sh --json' for live as-005 audit proof", normBin, shortHexPrefix(binSHA)),
		}
	}
	detail := strings.TrimSpace(in.ProbeMetrics.AuditDetail)
	if in.ProbeMetrics.AuditPassed {
		if detail == "" {
			detail = fmt.Sprintf("binary commit=%s sha256=%s... and as-005 cryptographic audit trail verified", normBin, shortHexPrefix(binSHA))
		}
		return TrustGateAssessment{
			Gate:           "TRUST-G5",
			Name:           "Evidence Integrity & Binary Provenance",
			Status:         TrustGateEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "assurance_probe:as-005+binary_provenance",
			Detail:         detail,
		}
	}
	if detail == "" {
		detail = "as-005 cryptographic audit trail probe failed"
	}
	return TrustGateAssessment{
		Gate:           "TRUST-G5",
		Name:           "Evidence Integrity & Binary Provenance",
		Status:         TrustGateNotEnforced,
		EpistemicClass: sandbox.EpistemicMeasuredMetric,
		EvidenceSource: "assurance_probe:as-005+binary_provenance",
		Detail:         detail,
	}
}

func shortHexPrefix(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func evaluateTrustG6() TrustGateAssessment {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	fixedNow := func() time.Time { return now }

	tokenGuard := verifier.NewEvolutionGuard(verifier.EvolutionBudget{
		MaxTokensPerCycle:     100,
		MaxWallClockPerCycle:  time.Minute,
		MaxRetriesPerCycle:    5,
		MaxNonImprovingRounds: 3,
		MinImprovementDelta:   0.01,
	}, fixedNow)
	stToken, err := tokenGuard.RecordRound(verifier.EvolutionRoundRecord{
		Round:         1,
		TokensUsed:    150,
		HoldoutScore:  0.5,
		HoldoutPassed: true,
		ScoreClass:    sandbox.EpistemicMeasuredMetric,
		RecordedAt:    now,
	})
	if err == nil || !stToken.Halted || stToken.HaltCode != verifier.CodeEvolutionTokenCap {
		return TrustGateAssessment{
			Gate:           "TRUST-G6",
			Name:           "Bounded Evolution & PASS/FAIL Memory Gate",
			Status:         TrustGateNotEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "trust_g6_self_check",
			Detail:         fmt.Sprintf("EvolutionGuard failed token cap enforcement (halted=%t code=%s)", stToken.Halted, stToken.HaltCode),
		}
	}

	oscGuard := verifier.NewEvolutionGuard(verifier.EvolutionBudget{
		MaxTokensPerCycle:     10000,
		MaxWallClockPerCycle:  time.Minute,
		MaxRetriesPerCycle:    5,
		MaxNonImprovingRounds: 1,
		MinImprovementDelta:   0.01,
	}, fixedNow)
	if st1, err1 := oscGuard.RecordRound(verifier.EvolutionRoundRecord{
		Round:         1,
		TokensUsed:    10,
		HoldoutScore:  0.80,
		HoldoutPassed: true,
		ScoreClass:    sandbox.EpistemicMeasuredMetric,
		RecordedAt:    now,
	}); err1 != nil || st1.Halted {
		return TrustGateAssessment{
			Gate:           "TRUST-G6",
			Name:           "Bounded Evolution & PASS/FAIL Memory Gate",
			Status:         TrustGateNotEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "trust_g6_self_check",
			Detail:         fmt.Sprintf("EvolutionGuard halted prematurely on improving round 1 (err=%v)", err1),
		}
	}
	if st2, err2 := oscGuard.RecordRound(verifier.EvolutionRoundRecord{
		Round:         2,
		TokensUsed:    10,
		HoldoutScore:  0.80,
		HoldoutPassed: true,
		ScoreClass:    sandbox.EpistemicMeasuredMetric,
		RecordedAt:    now,
	}); err2 == nil || !st2.Halted || st2.HaltCode != verifier.CodeEvolutionOscillationStop {
		return TrustGateAssessment{
			Gate:           "TRUST-G6",
			Name:           "Bounded Evolution & PASS/FAIL Memory Gate",
			Status:         TrustGateNotEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "trust_g6_self_check",
			Detail:         fmt.Sprintf("EvolutionGuard failed anti-oscillation halt (halted=%t code=%s)", st2.Halted, st2.HaltCode),
		}
	}

	if err := memory.VerifyPromotionGateContract(); err != nil {
		return TrustGateAssessment{
			Gate:           "TRUST-G6",
			Name:           "Bounded Evolution & PASS/FAIL Memory Gate",
			Status:         TrustGateNotEnforced,
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
			EvidenceSource: "trust_g6_self_check",
			Detail:         fmt.Sprintf("Memory PromotionGate contract self-check failed: %v", err),
		}
	}

	return TrustGateAssessment{
		Gate:           "TRUST-G6",
		Name:           "Bounded Evolution & PASS/FAIL Memory Gate",
		Status:         TrustGateEnforced,
		EpistemicClass: sandbox.EpistemicMeasuredMetric,
		EvidenceSource: "trust_g6_self_check",
		Detail:         "EvolutionGuard budget/anti-oscillation halts and universal PASS/FAIL PromotionGate guards verified",
	}
}

// EvaluateTrustAssurance constructs a non-compensatory per-OS, per-binary TRUST status report.
func EvaluateTrustAssurance(in TrustProbeInput) *TrustAssuranceStatus {
	osName := strings.TrimSpace(in.OS)
	if osName == "" {
		osName = runtime.GOOS
	}
	archName := strings.TrimSpace(in.Arch)
	if archName == "" {
		archName = runtime.GOARCH
	}

	vcsModified := in.VCSModified || strings.HasSuffix(strings.ToLower(strings.TrimSpace(in.BinaryCommit)), "-dirty")
	var normBin string
	if isHexCommit(in.BinaryCommit) {
		normBin = normalizeRevision(in.BinaryCommit)
	}
	var normSrc string
	if isHexCommit(in.SourceCommit) {
		normSrc = normalizeRevision(in.SourceCommit)
	}
	var matchesSource *bool
	if normSrc != "" {
		matched := normBin != "" && binaryRevisionsMatch(normBin, normSrc)
		matchesSource = &matched
	}

	binSHA := strings.ToLower(strings.TrimSpace(in.BinarySHA256))
	if binSHA == "" && strings.TrimSpace(in.BinaryPath) != "" {
		binSHA = hashExecutableSHA256(in.BinaryPath)
	}

	g1, sbxMode, sbxBackend, kernelEnforced, netEnf := evaluateTrustG1(in)
	g2, s0Provisioned := evaluateTrustG2(in)
	g3 := evaluateTrustG3(in)
	g4 := evaluateTrustG4(in)
	g5 := evaluateTrustG5(in, normBin, normSrc, binSHA)
	g6 := evaluateTrustG6()

	gates := []TrustGateAssessment{g1, g2, g3, g4, g5, g6}
	verdict, veto, vetoReasons, mediated := ComputeNonCompensatoryVerdict(gates)

	return &TrustAssuranceStatus{
		Schema:              TrustAssuranceSchema,
		EpistemicClass:      sandbox.EpistemicMeasuredMetric,
		OS:                  osName,
		Arch:                archName,
		Platform:            fmt.Sprintf("%s/%s", osName, archName),
		BinaryVersion:       strings.TrimSpace(in.BinaryVersion),
		BinaryCommit:        normBin,
		BinaryBuildSource:   strings.TrimSpace(in.BinaryBuildSource),
		VCSModified:         vcsModified,
		BinaryPath:          strings.TrimSpace(in.BinaryPath),
		BinarySHA256:        binSHA,
		SourceCommit:        normSrc,
		BinaryMatchesSource: matchesSource,
		SandboxMode:         sbxMode,
		SandboxBackend:      sbxBackend,
		KernelEnforced:      kernelEnforced,
		NetworkEnforcement:  netEnf,
		S0KeyProvisioned:    s0Provisioned,
		OverallVerdict:      verdict,
		NonCompensatoryVeto: veto,
		VetoReasons:         vetoReasons,
		MediatedGates:       mediated,
		Gates:               gates,
	}
}

func (d *Doctor) evaluateTrustAssurance(dbPath string, checks []DiagnosticResult) *TrustAssuranceStatus {
	prov := d.BinaryProvenance
	if strings.TrimSpace(prov.BinaryPath) == "" {
		if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
			if resolved, rErr := filepath.EvalSymlinks(exe); rErr == nil {
				prov.BinaryPath = resolved
			} else {
				prov.BinaryPath = exe
			}
		}
	}
	if !isHexCommit(prov.Commit) {
		if info, ok := debug.ReadBuildInfo(); ok && info != nil {
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					if !isHexCommit(prov.Commit) {
						prov.Commit = setting.Value
					}
				case "vcs.modified":
					if setting.Value == "true" && prov.BuildSource != "release" {
						prov.VCSModified = true
					}
				}
			}
		}
	}

	var sourceCommit string
	if headOut, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		sourceCommit = strings.TrimSpace(string(headOut))
	}

	dbHealthy := true
	for _, chk := range checks {
		if (chk.Name == "Database State" || chk.Name == "Database Connectivity") && chk.Status == "FAIL" {
			dbHealthy = false
			break
		}
	}

	stateDir := strings.TrimSpace(pathutil.DefaultStateDir())
	if strings.TrimSpace(dbPath) != "" {
		stateDir = filepath.Dir(dbPath)
	}

	return EvaluateTrustAssurance(TrustProbeInput{
		OS:                runtime.GOOS,
		Arch:              runtime.GOARCH,
		BinaryVersion:     prov.Version,
		BinaryCommit:      prov.Commit,
		BinaryBuildSource: prov.BuildSource,
		VCSModified:       prov.VCSModified,
		BinaryPath:        prov.BinaryPath,
		SourceCommit:      sourceCommit,
		StateDir:          stateDir,
		S0PubKeyPath:      os.Getenv("G8S_S0_PUBKEY_PATH"),
		DBHealthy:         dbHealthy,
		PrepareSandbox:    sandbox.Prepare,
	})
}
