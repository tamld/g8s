package sandbox

import (
	"errors"
	"fmt"
	"strings"
)

// EpistemicClass tags risk and assurance numbers with their epistemic status (ADR-0042 TRUST-G5).
type EpistemicClass string

const (
	// EpistemicMeasuredMetric identifies empirically measured metrics bound to executable checks.
	EpistemicMeasuredMetric EpistemicClass = "measured_metric"
	// EpistemicEstimatedHeuristic identifies uncalibrated triage priors (never measured exploit odds).
	EpistemicEstimatedHeuristic EpistemicClass = "estimated_heuristic"
)

// NetworkEnforcement distinguishes mediated userspace proxies from OS-enforced socket blocking (TRUST-G1).
type NetworkEnforcement string

const (
	NetworkEnforced     NetworkEnforcement = "enforced"
	NetworkMediatedOnly NetworkEnforcement = "mediated_only"
	NetworkUnrestricted NetworkEnforcement = "unrestricted"
)

// ErrNetworkEnforcementRequired is returned when enforced OS socket blocking is requested but unavailable.
var ErrNetworkEnforcementRequired = errors.New("E_NETWORK_ENFORCEMENT_REQUIRED")

// ErrDegradedSandboxNotAllowed is returned (#971 WP1) when a workspace_write
// attempt cannot achieve kernel-enforced containment on this host and the
// operator has not explicitly opted in via --allow-degraded-sandbox /
// G8S_ALLOW_DEGRADED_SANDBOX=1. Fail-closed, mirroring the #972-era posture:
// degraded execution is never silent — it is either refused with this typed
// error or proceeds with sandbox_degraded recorded in the attempt envelope.
var ErrDegradedSandboxNotAllowed = errors.New("E_SANDBOX_DEGRADED_NOT_ALLOWED")

// RequestedPolicy declares pre-spawn intent from permission, receipt, and sandbox flags.
type RequestedPolicy struct {
	Permission             string   `json:"permission,omitempty"`
	Role                   string   `json:"role,omitempty"`
	ReceiptID              string   `json:"receipt_id,omitempty"`
	AllowedPaths           []string `json:"allowed_paths,omitempty"`
	HoldoutDenyDirs        []string `json:"holdout_deny_dirs,omitempty"`
	RequireKernelEnforced  bool     `json:"require_kernel_enforced"`
	NoSandbox              bool     `json:"no_sandbox,omitempty"`
	RequireNetworkEnforced bool     `json:"require_network_enforced,omitempty"`
	// AllowDegradedSandbox is the explicit operator opt-in (#971 WP1) that lets
	// a workspace_write attempt proceed below kernel_enforced containment with
	// sandbox_degraded recorded in the envelope. Without it the attempt is
	// refused (E_SANDBOX_DEGRADED_NOT_ALLOWED).
	AllowDegradedSandbox bool   `json:"allow_degraded_sandbox,omitempty"`
	TransportKind        string `json:"transport_kind,omitempty"`
}

// EvaluatedPolicy captures deterministic pre-spawn admission decisions.
type EvaluatedPolicy struct {
	Allowed             bool     `json:"allowed"`
	RequiredMode        string   `json:"required_mode"`
	RejectedBeforeSpawn bool     `json:"rejected_before_spawn"`
	RejectionCode       string   `json:"rejection_code,omitempty"`
	RejectionReason     string   `json:"rejection_reason,omitempty"`
	EnforcedDenyDirs    []string `json:"enforced_deny_dirs,omitempty"`
}

// EffectiveEnforcement records observed OS containment and backstop enforcement without overclaiming.
type EffectiveEnforcement struct {
	ContainmentMode    ContainmentMode    `json:"containment_mode"`
	SandboxBackend     string             `json:"sandbox_backend"`
	NetworkEnforcement NetworkEnforcement `json:"network_enforcement"`
	NetworkDetail      string             `json:"network_detail,omitempty"`
	ProofSource        string             `json:"proof_source,omitempty"`
	HoldoutDenied      bool               `json:"holdout_denied"`
	DiffFirewallActive bool               `json:"diff_firewall_active"`
	EpistemicClass     EpistemicClass     `json:"epistemic_class"`
	// SandboxDegraded (#971 WP1) is true whenever the achieved containment is
	// below ModeKernelEnforced while a sandbox was requested — including the
	// Windows job_object_env_scrubbed baseline (recorded, never a lockout) and
	// explicit --no-sandbox operator opt-outs. Machine-readable, never silent.
	SandboxDegraded       bool   `json:"sandbox_degraded"`
	SandboxDegradedReason string `json:"sandbox_degraded_reason,omitempty"`
}

// EnforcementProof is an unforgeable capability value minted by enforcement probes.
// All fields are unexported so external callers cannot forge OS-enforced socket isolation.
type EnforcementProof struct {
	sealed             bool
	containmentMode    ContainmentMode
	backend            string
	networkEnforcement NetworkEnforcement
	networkDetail      string
	proofSource        string
}

// NewUnrestrictedProcessProof constructs an honest proof for external/direct subprocesses without OS socket blocking.
func NewUnrestrictedProcessProof(mode ContainmentMode, backend string) EnforcementProof {
	if strings.TrimSpace(backend) == "" {
		backend = "direct_process"
	}
	return EnforcementProof{
		sealed:             true,
		containmentMode:    mode,
		backend:            backend,
		networkEnforcement: NetworkUnrestricted,
		networkDetail:      "outbound OS sockets unrestricted",
		proofSource:        "external_unrestricted_process",
	}
}

// NewMediatedTransportProof constructs an honest proof for HTTP/API transports with userspace mediation only.
func NewMediatedTransportProof(backend string) EnforcementProof {
	if strings.TrimSpace(backend) == "" {
		backend = "http_request_transport"
	}
	return EnforcementProof{
		sealed:             true,
		containmentMode:    ModePolicyOnly,
		backend:            backend,
		networkEnforcement: NetworkMediatedOnly,
		networkDetail:      "application proxy/HTTP mediation only; no OS socket block",
		proofSource:        "http_request_mediation",
	}
}

func newSandboxDisabledProof() EnforcementProof {
	return EnforcementProof{
		sealed:             true,
		containmentMode:    ModePolicyOnly,
		backend:            "disabled",
		networkEnforcement: NetworkUnrestricted,
		networkDetail:      "sandbox disabled; outbound OS sockets unrestricted",
		proofSource:        "sandbox_disabled",
	}
}

func newSandboxKernelProof(mode ContainmentMode, backend string, osSocketBlocked bool, detail string) EnforcementProof {
	trimmedDetail := strings.TrimSpace(detail)
	if mode == ModeKernelEnforced && osSocketBlocked {
		if trimmedDetail == "" {
			trimmedDetail = "OS/container outbound socket isolation enforced"
		}
		return EnforcementProof{
			sealed:             true,
			containmentMode:    mode,
			backend:            backend,
			networkEnforcement: NetworkEnforced,
			networkDetail:      trimmedDetail,
			proofSource:        "os_kernel_probe",
		}
	}
	if trimmedDetail == "" {
		trimmedDetail = "outbound OS sockets unrestricted"
	}
	return EnforcementProof{
		sealed:             true,
		containmentMode:    mode,
		backend:            backend,
		networkEnforcement: NetworkUnrestricted,
		networkDetail:      trimmedDetail,
		proofSource:        "os_sandbox_prepare",
	}
}

// PolicyEnforcementReport binds RequestedPolicy -> EvaluatedPolicy -> EffectiveEnforcement across execution receipts.
type PolicyEnforcementReport struct {
	Requested           RequestedPolicy      `json:"requested_policy"`
	Evaluated           EvaluatedPolicy      `json:"evaluated_policy"`
	Effective           EffectiveEnforcement `json:"effective_enforcement"`
	NetworkEnforcement  NetworkEnforcement   `json:"network_enforcement"`
	RejectedBeforeSpawn bool                 `json:"rejected_before_spawn"`
	EpistemicClass      EpistemicClass       `json:"epistemic_class"`
}

// PolicyRejectionError wraps a sentinel rejection error alongside its structured PolicyEnforcementReport.
type PolicyRejectionError struct {
	Err    error
	Report PolicyEnforcementReport
}

func (e *PolicyRejectionError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Report.Evaluated.RejectionReason) != "" && e.Err != nil {
		return fmt.Sprintf("%v: %s", e.Err, e.Report.Evaluated.RejectionReason)
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Report.Evaluated.RejectionCode
}

func (e *PolicyRejectionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// MergeDenyDirs canonicalizes and deduplicates base and extra deny directory slices in order.
func MergeDenyDirs(base, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(base)+len(extra))
	var out []string
	for _, list := range [][]string{base, extra} {
		for _, raw := range list {
			c := CanonicalPath(raw)
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// EvaluatePolicyContract binds requested policy intent to evaluated admission and an unforgeable EnforcementProof.
func EvaluatePolicyContract(req RequestedPolicy, proof EnforcementProof) PolicyEnforcementReport {
	mode := proof.containmentMode
	backend := proof.backend
	netEnf := proof.networkEnforcement
	netDetail := proof.networkDetail
	proofSource := proof.proofSource
	if !proof.sealed {
		mode = ModePolicyOnly
		backend = "unprobed"
		netEnf = NetworkUnrestricted
		netDetail = "unsealed policy proof; outbound OS sockets unrestricted"
		proofSource = "unsealed_default"
	} else {
		if netEnf == "" {
			netEnf = NetworkUnrestricted
		}
		if netDetail == "" {
			netDetail = "outbound OS sockets unrestricted"
		}
	}

	reqMode := "best_effort"
	if req.RequireKernelEnforced {
		reqMode = "kernel_enforced"
	} else if req.NoSandbox {
		reqMode = "policy_only"
	}

	// #971 WP1: honest degraded-containment recording. Whenever the achieved
	// containment is below kernel_enforced while a sandbox was requested, the
	// report says so explicitly — including the Windows job_object_env_scrubbed
	// platform baseline (recorded, never a false lockout) and explicit
	// --no-sandbox operator opt-outs.
	degraded := mode != ModeKernelEnforced
	degradedReason := ""
	if degraded {
		if req.NoSandbox {
			degradedReason = "sandbox disabled by operator (--no-sandbox); no OS containment applied"
		} else {
			degradedReason = sandboxDegradedReason(mode, backend)
		}
	}

	allowed := true
	rejected := false
	rejCode := ""
	rejReason := ""
	switch {
	case req.RequireKernelEnforced && mode != ModeKernelEnforced:
		allowed = false
		rejected = true
		rejCode = ErrKernelSandboxRequired.Error()
		rejReason = fmt.Sprintf("containment degraded to mode=%s backend=%s (kernel_enforced required)", mode, backend)
	case req.RequireNetworkEnforced && netEnf != NetworkEnforced:
		allowed = false
		rejected = true
		rejCode = ErrNetworkEnforcementRequired.Error()
		rejReason = fmt.Sprintf("network enforcement is %s backend=%s (enforced OS socket isolation required)", netEnf, backend)
	// #971 WP1 capability-probed preflight: workspace_write over a local process
	// transport must not silently degrade to policy_only on hosts without a
	// kernel sandbox backend (restricted Linux/Docker CI). Fail closed unless the
	// operator explicitly allowed degradation; Windows job_object_env_scrubbed
	// and kernel_enforced hosts are never gated.
	case isProcessTransport(req.TransportKind) && !req.NoSandbox &&
		strings.EqualFold(strings.TrimSpace(req.Permission), "workspace_write") &&
		mode == ModePolicyOnly && !req.AllowDegradedSandbox:
		allowed = false
		rejected = true
		rejCode = ErrDegradedSandboxNotAllowed.Error()
		rejReason = fmt.Sprintf("workspace_write cannot achieve kernel-enforced containment on this host (mode=%s backend=%s); re-run with --allow-degraded-sandbox (submit) or G8S_ALLOW_DEGRADED_SANDBOX=1 (worker) to proceed with sandbox_degraded recorded in the envelope", mode, backend)
	}

	denyDirs := MergeDenyDirs(nil, req.HoldoutDenyDirs)
	holdoutDenied := len(denyDirs) > 0 && mode == ModeKernelEnforced
	var enforcedDenyDirs []string
	if holdoutDenied {
		enforcedDenyDirs = denyDirs
	}

	perm := strings.ToLower(strings.TrimSpace(req.Permission))
	diffFirewallActive := perm == "workspace_write" || perm == "read_only"

	// A refused attempt did not proceed degraded; the rejection reason carries
	// the explanation instead (SandboxDegraded stays false).
	if rejected {
		degraded = false
		degradedReason = ""
	}

	return PolicyEnforcementReport{
		Requested: req,
		Evaluated: EvaluatedPolicy{
			Allowed:             allowed,
			RequiredMode:        reqMode,
			RejectedBeforeSpawn: rejected,
			RejectionCode:       rejCode,
			RejectionReason:     rejReason,
			EnforcedDenyDirs:    enforcedDenyDirs,
		},
		Effective: EffectiveEnforcement{
			ContainmentMode:       mode,
			SandboxBackend:        backend,
			NetworkEnforcement:    netEnf,
			NetworkDetail:         netDetail,
			ProofSource:           proofSource,
			HoldoutDenied:         holdoutDenied,
			DiffFirewallActive:    diffFirewallActive,
			EpistemicClass:        EpistemicMeasuredMetric,
			SandboxDegraded:       degraded,
			SandboxDegradedReason: degradedReason,
		},
		NetworkEnforcement:  netEnf,
		RejectedBeforeSpawn: rejected,
		EpistemicClass:      EpistemicMeasuredMetric,
	}
}

// isProcessTransport reports whether the policy request targets a locally
// spawned process. Mediated HTTP/API transports have no local process to
// sandbox, so the workspace_write degraded gate does not apply to them.
func isProcessTransport(transport string) bool {
	t := strings.ToLower(strings.TrimSpace(transport))
	return t == "" || t == "process"
}

// sandboxDegradedReason explains, per containment mode, why the achieved
// containment sits below kernel_enforced (#971 WP1).
func sandboxDegradedReason(mode ContainmentMode, backend string) string {
	switch mode {
	case ModeJobObjectEnvScrubbed:
		return fmt.Sprintf("windows job_object_env_scrubbed baseline (backend=%s): process-tree termination and environment scrubbing only; no kernel filesystem ACLs or outbound socket isolation", backend)
	case ModePolicyOnly:
		return fmt.Sprintf("host lacks kernel sandbox enforcement (backend=%s): policy-only fallback has no OS filesystem or socket isolation", backend)
	default:
		return fmt.Sprintf("containment mode=%s backend=%s below kernel_enforced", mode, backend)
	}
}
