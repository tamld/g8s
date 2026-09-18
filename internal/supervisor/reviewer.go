// Package supervisor — reviewer.go classifies a worker receipt as pass /
// revise / fail. Pass returns success; revise triggers another attempt; fail
// short-circuits to escalation.
package supervisor

import (
	"github.com/tamld/g8s/internal/orchestrator"
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

// Reviewer is the dependency-injection seam for the receipt grader. The
// default implementation is StubReviewer; T021 (real reviewer) will replace
// it with one that parses receipt JSON.
type Reviewer interface {
	Validate(env TaskEnvelope, receipt orchestrator.Receipt) (map[string]bool, error)
}

// StubReviewer is the deterministic default. It grades a receipt on three
// pass criteria (commit landed, clean scope, return-code zero) and one fail
// criterion (any scope violation). OK=false alone is treated as REVISE so
// the supervisor can retry within the approach budget.
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
