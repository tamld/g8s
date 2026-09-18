// Package supervisor — reviewer.go classifies a worker receipt as pass /
// revise / fail. Pass returns success; revise triggers another attempt; fail
// short-circuits to escalation.
package supervisor

import (
	"encoding/json"

	"github.com/tamld/g8s/internal/orchestrator"
	"github.com/tamld/g8s/internal/shared"
)

// Verdict is the supervisor-level classification of a worker attempt.
type Verdict int

const (
	// VerdictPass means the receipt satisfies the envelope and the loop ends.
	VerdictPass Verdict = iota
	// VerdictRevise means the receipt is broken in a retryable way — try a
	// fresh attempt within the current approach (or the next approach if
	// attempts are exhausted).
	VerdictRevise
	// VerdictFail means the receipt violates policy in a non-retryable way
	// (typically scope violation) — escalate immediately.
	VerdictFail
)

// String renders a Verdict for logs.
func (v Verdict) String() string {
	switch v {
	case VerdictPass:
		return "PASS"
	case VerdictRevise:
		return "REVISE"
	case VerdictFail:
		return "FAIL"
	default:
		return "UNKNOWN"
	}
}

// ReviewOutcome bundles a verdict with the evidence that produced it.
type ReviewOutcome struct {
	Verdict   Verdict
	Reason    string
	Validated map[string]bool
}

// Reviewer is the dependency-injection seam for the receipt grader.
// RealReviewer implements structured receipt validation against TaskEnvelope.
type Reviewer interface {
	Validate(env TaskEnvelope, receipt orchestrator.Receipt) (map[string]bool, error)
}

// RealReviewer validates receipts against the TaskEnvelope contract.
// It checks each envelope field against the receipt's acceptance tracking,
// contract check, and error tracking.
type RealReviewer struct{}

// NewRealReviewer returns a ready-to-use real reviewer.
func NewRealReviewer() *RealReviewer { return &RealReviewer{} }

// Validate performs per-field validation of a receipt against the TaskEnvelope.
// It extracts acceptance tracking from the receipt and validates each
// envelope field (DoR, DoD, DnD, Validateds, SRS, PRD, FSM) against
// the receipt's ResultAcceptance, ContractCheck, and ErrorTracking.
func (r *RealReviewer) Validate(env TaskEnvelope, receipt orchestrator.Receipt) (map[string]bool, error) {
	out := make(map[string]bool, len(env.SelectedFields))

	// Extract acceptance tracking from receipt
	var acceptance shared.ResultAcceptance
	if len(receipt.Acceptance) > 0 {
		if err := json.Unmarshal(receipt.Acceptance, &acceptance); err != nil {
			// If we can't parse acceptance, fall back to stub behavior
			for _, f := range env.SelectedFields {
				out[f] = receipt.OK
			}
			return out, nil
		}
	}

	// If no acceptance data, fall back to OK-based validation
	if acceptance.Accepted && acceptance.ContractCheck.Passed && acceptance.ErrorTracking.ErrorCode == "" {
		// All checks passed - validate all envelope fields
		for _, f := range env.SelectedFields {
			out[f] = true
		}
		return out, nil
	}

	// Some checks failed - validate based on what failed
	for _, f := range env.SelectedFields {
		switch f {
		case "DoR", "DoD", "Validateds":
			// Core fields require acceptance
			out[f] = acceptance.Accepted
		case "DnD":
			// DnD requires acceptance + reason
			out[f] = acceptance.Accepted && acceptance.Reason != ""
		case "SRS", "PRD", "FSM":
			// Optional fields require contract check pass
			out[f] = acceptance.ContractCheck.Passed
		default:
			out[f] = false
		}
	}
	return out, nil
}

// mapToStruct converts a map to a struct using JSON marshaling
func mapToStruct(m map[string]any, v any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// StubReviewer is kept for backward compatibility and testing.
// It grades a receipt on three pass criteria (commit landed, clean scope, return-code zero).
// OK=false alone is treated as REVISE so the supervisor can retry within the approach budget.
type StubReviewer struct{}

// NewStubReviewer returns a ready-to-use reviewer.
func NewStubReviewer() *StubReviewer { return &StubReviewer{} }

// Validate returns the per-envelope-field validation map. Keys match the
// envelope SelectedFields list so callers can diff against the planner.
func (s *StubReviewer) Validate(env TaskEnvelope, receipt orchestrator.Receipt) (map[string]bool, error) {
	out := make(map[string]bool, len(env.SelectedFields))
	for _, f := range env.SelectedFields {
		// ponytail: stub treats every required field as "ok=true" when the
		// worker reported OK. T021 replaces this with a per-field parser.
		out[f] = receipt.OK
	}
	return out, nil
}

// ReviewReceipt is the canonical grading entry point. It composes a Reviewer
// with envelope-aware policy: scope violations always fail regardless of OK.
func ReviewReceipt(receipt orchestrator.Receipt, envelope TaskEnvelope, r Reviewer) ReviewOutcome {
	if len(receipt.ScopeViolations) > 0 {
		return ReviewOutcome{
			Verdict:   VerdictFail,
			Reason:    "scope violation: " + receipt.ScopeViolations[0],
			Validated: map[string]bool{},
		}
	}

	validated, err := r.Validate(envelope, receipt)
	if err != nil {
		return ReviewOutcome{
			Verdict:   VerdictRevise,
			Reason:    "reviewer error: " + err.Error(),
			Validated: map[string]bool{},
		}
	}

	if !receipt.OK || receipt.ReturnCode != 0 || receipt.CommitSHA == "" {
		reason := "receipt not OK"
		if receipt.ReturnCode != 0 {
			reason = "non-zero return code"
		} else if receipt.CommitSHA == "" {
			reason = "no commit recorded"
		}
		return ReviewOutcome{
			Verdict:   VerdictRevise,
			Reason:    reason,
			Validated: validated,
		}
	}

	return ReviewOutcome{
		Verdict:   VerdictPass,
		Reason:    "receipt OK + clean scope + commit recorded",
		Validated: validated,
	}
}
