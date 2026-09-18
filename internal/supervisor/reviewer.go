diffstat only, patch not produced. Full patch: git show -p <rev>
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
... more lines (truncated by snip)
