package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamld/g8s/internal/sandbox"
)

// EvolutionTier classifies an evolution change into Tier A (bounded autonomous) or Tier B (S0 Human TCB gated).
type EvolutionTier string

const (
	// TierABoundedAuto permits autonomous self-improvement strictly outside the TCB under TRUST-G3/G6 bounds.
	TierABoundedAuto EvolutionTier = "tier_a_bounded_auto"
	// TierBS0HumanTCB enforces mandatory deterministic checks plus distinct S0 Human Principal approval for any TCB change.
	TierBS0HumanTCB EvolutionTier = "tier_b_s0_human_tcb"
)

const (
	CodeTrustRootWorkerTCBDenied             = "E_TRUST_ROOT_WORKER_TCB_DENIED"
	CodeTrustRootS0ApprovalRequired          = "E_TRUST_ROOT_S0_APPROVAL_REQUIRED"
	CodeTrustRootSelfAttestationBlocked      = "E_TRUST_ROOT_SELF_ATTESTATION_BLOCKED"
	CodeTrustRootDeterministicChecksRequired = "E_TRUST_ROOT_DETERMINISTIC_CHECKS_REQUIRED"
)

// S0ApprovalAttestation carries non-LLM human principal sign-off bound to deterministic checks and artifact digests.
type S0ApprovalAttestation struct {
	ApprovedBy                string `json:"approved_by"`
	PrincipalStratum          string `json:"principal_stratum"`
	DeterministicChecksPassed bool   `json:"deterministic_checks_passed"`
	ChecksDigest              string `json:"checks_digest"`
	ArtifactDigest            string `json:"artifact_digest"`
	SignatureHex              string `json:"signature_hex,omitempty"`
}

// TrustRootRequest describes a proposed mutation or evolution target evaluated against ADR-0042 TRUST-G2.
type TrustRootRequest struct {
	ActorStratum              string                 `json:"actor_stratum"` // "F0" | "S2" | "S1" | "F1" | "F2" | "S3-M" | "S3-A" | "S0"
	ActorID                   string                 `json:"actor_id"`
	TargetPaths               []string               `json:"target_paths"`
	RequestedTier             EvolutionTier          `json:"requested_tier,omitempty"`
	DeterministicChecksPassed bool                   `json:"deterministic_checks_passed"`
	S0Approval                *S0ApprovalAttestation `json:"s0_approval,omitempty"`
}

// TrustRootDecision records the deterministic enforcement verdict outside LLM inference.
type TrustRootDecision struct {
	Allowed         bool                   `json:"allowed"`
	EffectiveTier   EvolutionTier          `json:"effective_tier"`
	TouchesTCB      bool                   `json:"touches_tcb"`
	MatchedTCBPaths []string               `json:"matched_tcb_paths,omitempty"`
	Code            string                 `json:"code,omitempty"`
	Reason          string                 `json:"reason"`
	EpistemicClass  sandbox.EpistemicClass `json:"epistemic_class"`
}

var staticTCBExactOrSuffix = []string{
	".g8s/trust-boundaries.yml",
	".g8s/verifier-classes.yml",
	".g8s/lane-bundles.yml",
	"f0-signing.key",
	"f0-signing.pub",
	"internal/worker/diff_firewall.go",
}

// trustRootBoundaryFloor mirrors .g8s/trust-boundaries.yml as an offline floor so
// internal/harness avoids an import cycle with internal/lane test helpers while staying SSoT-aligned.
var trustRootBoundaryFloor = []string{
	"internal/receipt/*",
	"internal/worker/proc_*",
	"internal/worker/diff_firewall*",
	"internal/server/*",
	"internal/harness/*",
	"internal/memory/*",
	"internal/dispatch/*",
	"internal/sandbox/*",
	"internal/signing/*",
	"internal/charter/*",
	"internal/fsops/*",
	"internal/verifier/*",
	"internal/premise/*",
	"internal/provider/conformance/*",
	"cmd/g8s/deliver.go",
	"scripts/install.sh",
}

func loadTCBBoundaryPatterns() []string {
	wd, err := os.Getwd()
	if err != nil {
		return trustRootBoundaryFloor
	}
	dir := filepath.Clean(wd)
	for {
		cand := filepath.Join(dir, ".g8s", "trust-boundaries.yml")
		// #nosec G304 -- cand walks ancestors of current working directory for fixed .g8s/trust-boundaries.yml path.
		raw, rErr := os.ReadFile(cand)
		if rErr == nil {
			var parsed []string
			inSection := false
			for _, line := range strings.Split(string(raw), "\n") {
				s := strings.TrimSpace(line)
				if s == "" || strings.HasPrefix(s, "#") {
					continue
				}
				if s == "trust_boundaries:" {
					inSection = true
					continue
				}
				if inSection && strings.HasPrefix(s, "- ") {
					val := strings.TrimSpace(strings.TrimPrefix(s, "- "))
					val = strings.Trim(val, `"'`)
					if val != "" {
						parsed = append(parsed, val)
					}
				} else if inSection && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
					inSection = false
				}
			}
			if len(parsed) > 0 {
				return parsed
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return trustRootBoundaryFloor
}

func normalizeTCBPath(raw string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimLeft(cleaned, "/")
	return cleaned
}

func matchTCBPattern(cleanPath, pattern string) bool {
	pat := normalizeTCBPath(pattern)
	if pat == "" || cleanPath == "" {
		return false
	}
	if strings.HasSuffix(pat, "/*") {
		prefix := strings.TrimSuffix(pat, "/*")
		if cleanPath == prefix || strings.HasPrefix(cleanPath, prefix+"/") {
			return true
		}
	}
	if strings.HasSuffix(pat, "*") {
		prefix := strings.TrimSuffix(pat, "*")
		if strings.HasPrefix(cleanPath, prefix) {
			return true
		}
	}
	if matched, err := filepath.Match(pat, cleanPath); err == nil && matched {
		return true
	}
	return cleanPath == pat
}

// IsTCBPath reports whether rawPath touches g8s's Trusted Computing Base (ADR-0042 §4 Tier B + .g8s/trust-boundaries.yml).
func IsTCBPath(rawPath string) bool {
	norm := normalizeTCBPath(rawPath)
	if norm == "" || norm == "." {
		return false
	}
	lower := strings.ToLower(norm)
	for _, pat := range loadTCBBoundaryPatterns() {
		if matchTCBPattern(lower, strings.ToLower(pat)) {
			return true
		}
	}
	for _, pat := range trustRootBoundaryFloor {
		if matchTCBPattern(lower, strings.ToLower(pat)) {
			return true
		}
	}
	for _, fixed := range staticTCBExactOrSuffix {
		fLower := strings.ToLower(fixed)
		if lower == fLower || strings.HasSuffix(lower, "/"+fLower) || filepath.Base(lower) == "f0-signing.key" {
			return true
		}
	}
	return false
}

func isWorkerOrSubagentStratum(stratum string) bool {
	switch strings.ToUpper(strings.TrimSpace(stratum)) {
	case "", "F1", "F2", "S3", "S3-M", "S3-A", "WORKER", "SUBAGENT":
		return true
	default:
		return false
	}
}

func isSelfOrAgentAttestor(approvedBy, actorID string) bool {
	ab := strings.ToLower(strings.TrimSpace(approvedBy))
	aid := strings.ToLower(strings.TrimSpace(actorID))
	if ab == "" {
		return false
	}
	if aid != "" && ab == aid {
		return true
	}
	tokens := strings.FieldsFunc(ab, func(r rune) bool {
		return r == '-' || r == '_' || r == ':' || r == '.' || r == '/' || r == ' '
	})
	for _, tok := range tokens {
		switch tok {
		case "f0", "f1", "f2", "s1", "s2", "s3", "llm", "agent", "worker", "bot", "autonomous", "coordinator":
			return true
		}
	}
	return false
}

// EvaluateTrustRootMutation enforces ADR-0042 TRUST-G2: TCB changes ALWAYS resolve to
// Tier B (S0 Human Principal + deterministic checks), blocking worker mutations and F0 LLM self-attestation.
func EvaluateTrustRootMutation(req TrustRootRequest) TrustRootDecision {
	var matched []string
	for _, p := range req.TargetPaths {
		if IsTCBPath(p) {
			matched = append(matched, normalizeTCBPath(p))
		}
	}

	touchesTCB := len(matched) > 0
	if !touchesTCB {
		if !req.DeterministicChecksPassed {
			return TrustRootDecision{
				Allowed:        false,
				EffectiveTier:  TierABoundedAuto,
				TouchesTCB:     false,
				Code:           CodeTrustRootDeterministicChecksRequired,
				Reason:         "Tier A bounded evolution still requires passing deterministic checks",
				EpistemicClass: sandbox.EpistemicMeasuredMetric,
			}
		}
		return TrustRootDecision{
			Allowed:        true,
			EffectiveTier:  TierABoundedAuto,
			TouchesTCB:     false,
			Reason:         "non-TCB target permitted under Tier A bounded autonomous evolution",
			EpistemicClass: sandbox.EpistemicMeasuredMetric,
		}
	}

	// Any target touching the TCB unconditionally requires Tier B S0 Human custody.
	if isWorkerOrSubagentStratum(req.ActorStratum) {
		return TrustRootDecision{
			Allowed:         false,
			EffectiveTier:   TierBS0HumanTCB,
			TouchesTCB:      true,
			MatchedTCBPaths: matched,
			Code:            CodeTrustRootWorkerTCBDenied,
			Reason:          fmt.Sprintf("stratum %q is forbidden from modifying TCB paths (%v)", req.ActorStratum, matched),
			EpistemicClass:  sandbox.EpistemicMeasuredMetric,
		}
	}
	if !req.DeterministicChecksPassed {
		return TrustRootDecision{
			Allowed:         false,
			EffectiveTier:   TierBS0HumanTCB,
			TouchesTCB:      true,
			MatchedTCBPaths: matched,
			Code:            CodeTrustRootDeterministicChecksRequired,
			Reason:          "Tier B TCB mutation requires deterministic checks to pass before S0 approval",
			EpistemicClass:  sandbox.EpistemicMeasuredMetric,
		}
	}
	if req.S0Approval == nil {
		return TrustRootDecision{
			Allowed:         false,
			EffectiveTier:   TierBS0HumanTCB,
			TouchesTCB:      true,
			MatchedTCBPaths: matched,
			Code:            CodeTrustRootS0ApprovalRequired,
			Reason:          fmt.Sprintf("Tier B TCB paths (%v) require explicit S0 Human Principal approval", matched),
			EpistemicClass:  sandbox.EpistemicMeasuredMetric,
		}
	}
	if isSelfOrAgentAttestor(req.S0Approval.ApprovedBy, req.ActorID) {
		return TrustRootDecision{
			Allowed:         false,
			EffectiveTier:   TierBS0HumanTCB,
			TouchesTCB:      true,
			MatchedTCBPaths: matched,
			Code:            CodeTrustRootSelfAttestationBlocked,
			Reason:          fmt.Sprintf("blocked non-human/self-attestation in S0 approval (%q)", req.S0Approval.ApprovedBy),
			EpistemicClass:  sandbox.EpistemicMeasuredMetric,
		}
	}
	if !strings.EqualFold(strings.TrimSpace(req.S0Approval.PrincipalStratum), "S0") ||
		strings.TrimSpace(req.S0Approval.ApprovedBy) == "" ||
		strings.TrimSpace(req.S0Approval.ChecksDigest) == "" ||
		strings.TrimSpace(req.S0Approval.ArtifactDigest) == "" {
		return TrustRootDecision{
			Allowed:         false,
			EffectiveTier:   TierBS0HumanTCB,
			TouchesTCB:      true,
			MatchedTCBPaths: matched,
			Code:            CodeTrustRootS0ApprovalRequired,
			Reason:          "S0 approval must declare PrincipalStratum=S0, ApprovedBy, ChecksDigest, and ArtifactDigest",
			EpistemicClass:  sandbox.EpistemicMeasuredMetric,
		}
	}
	if !req.S0Approval.DeterministicChecksPassed {
		return TrustRootDecision{
			Allowed:         false,
			EffectiveTier:   TierBS0HumanTCB,
			TouchesTCB:      true,
			MatchedTCBPaths: matched,
			Code:            CodeTrustRootDeterministicChecksRequired,
			Reason:          "S0 approval attestation recorded deterministic_checks_passed=false",
			EpistemicClass:  sandbox.EpistemicMeasuredMetric,
		}
	}

	return TrustRootDecision{
		Allowed:         true,
		EffectiveTier:   TierBS0HumanTCB,
		TouchesTCB:      true,
		MatchedTCBPaths: matched,
		Reason:          fmt.Sprintf("Tier B TCB change authorized by S0 principal %q with deterministic checks %s", req.S0Approval.ApprovedBy, req.S0Approval.ChecksDigest),
		EpistemicClass:  sandbox.EpistemicMeasuredMetric,
	}
}
