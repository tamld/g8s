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
	TransportKind          string   `json:"transport_kind,omitempty"`
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
	HoldoutDenied      bool               `json:"holdout_denied"`
	DiffFirewallActive bool               `json:"diff_firewall_active"`
	EpistemicClass     EpistemicClass     `json:"epistemic_class"`
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

// EvaluatePolicyContract binds requested policy intent to evaluated admission and observed OS/network enforcement.
func EvaluatePolicyContract(
	req RequestedPolicy,
	mode ContainmentMode,
	backend string,
	networkMediatedProxy bool,
	osSocketBlocked bool,
) PolicyEnforcementReport {
	netEnf := NetworkUnrestricted
	netDetail := "outbound OS sockets unrestricted"
	switch {
	case osSocketBlocked:
		netEnf = NetworkEnforced
		netDetail = "OS/container outbound socket isolation enforced"
	case networkMediatedProxy:
		netEnf = NetworkMediatedOnly
		netDetail = "application proxy/HTTP mediation only; no OS socket block"
	}

	reqMode := "best_effort"
	if req.RequireKernelEnforced {
		reqMode = "kernel_enforced"
	} else if req.NoSandbox {
		reqMode = "policy_only"
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
	}

	denyDirs := MergeDenyDirs(nil, req.HoldoutDenyDirs)
	holdoutDenied := len(denyDirs) > 0 && mode == ModeKernelEnforced
	var enforcedDenyDirs []string
	if holdoutDenied {
		enforcedDenyDirs = denyDirs
	}

	perm := strings.ToLower(strings.TrimSpace(req.Permission))
	diffFirewallActive := perm == "workspace_write" || perm == "read_only"

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
			ContainmentMode:    mode,
			SandboxBackend:     backend,
			NetworkEnforcement: netEnf,
			NetworkDetail:      netDetail,
			HoldoutDenied:      holdoutDenied,
			DiffFirewallActive: diffFirewallActive,
			EpistemicClass:     EpistemicMeasuredMetric,
		},
		NetworkEnforcement:  netEnf,
		RejectedBeforeSpawn: rejected,
		EpistemicClass:      EpistemicMeasuredMetric,
	}
}
