// Package supervisor — rca.go computes the root-cause-attribution record that
// decides whether to escalate, pause for info, or roll into a new approach.
package supervisor

import (
	"context"
	"fmt"
	"strings"
)

// RCARecord is the supervisor's post-attempt attribution. Confidence in
// [0, 1]: high means "roll into a new approach"; low (<0.6) means "pause for
// human input" because we are guessing.
type RCARecord struct {
	FailedAttemptIDs []string
	Symptom          string
	RootCause        string
	Evidence         string
	Confidence       float64
}

// RCA is the function-typed seam the supervisor uses to attribute failures.
// Default is StubRCA; T023 will replace it with a parser that reads attempt
// JSON and decides approach shifts.
type RCA func(ctx context.Context, attempts []AttemptRecord) (RCARecord, error)

// StubRCA is the deterministic default. Confidence is high when there are
// zero or one failed attempts, degrades by 0.1 per attempt, and is clamped to
// [0, 1]. Below 0.6 the caller must pause (NEEDS_INFO) instead of rolling
// forward; above 0.6 it is safe to switch approach.
//
// Symptom/RootCause/Evidence are derived from the latest attempt's verdict
// so the escalation JSON contains real signal, not placeholders.
func StubRCA(ctx context.Context, attempts []AttemptRecord) (RCARecord, error) {
	if err := ctx.Err(); err != nil {
		return RCARecord{}, err
	}
	if len(attempts) == 0 {
		return RCARecord{
			Symptom:    "no attempts recorded",
			RootCause:  "n/a",
			Evidence:   "empty attempt history",
			Confidence: 1.0,
		}, nil
	}

	failed := make([]string, 0, len(attempts))
	for _, a := range attempts {
		if a.Receipt.TaskID != "" && !a.Receipt.OK {
			failed = append(failed, a.Receipt.TaskID)
		}
	}

	latest := attempts[len(attempts)-1]
	symptom, rootCause, evidence := describeLatest(latest)

	confidence := 1.0 - 0.1*float64(len(failed))
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	if confidence < 0.6 {
		symptom = "low confidence RCA, escalation needed"
		rootCause = "heuristic"
	}

	return RCARecord{
		FailedAttemptIDs: failed,
		Symptom:          symptom,
		RootCause:        rootCause,
		Evidence:         evidence,
		Confidence:       confidence,
	}, nil
}

// describeLatest reduces the most recent AttemptRecord into a one-line
// (symptom, rootCause, evidence) tuple. Honest about being heuristic.
func describeLatest(a AttemptRecord) (symptom, rootCause, evidence string) {
	if len(a.Receipt.ScopeViolations) > 0 {
		return "scope violation", "worker mutated outside allowed_paths",
			strings.Join(a.Receipt.ScopeViolations, ",")
	}
	if a.ReviewVerdict == VerdictFail {
		return "policy violation", "reviewer flagged non-retryable failure", a.ReviewReason
	}
	if a.Receipt.ReturnCode != 0 {
		return fmt.Sprintf("exit code %d", a.Receipt.ReturnCode), "worker subprocess failed",
			a.Receipt.Stderr
	}
	if !a.Receipt.OK {
		return "receipt reported failure", "worker self-reported OK=false", a.ReviewReason
	}
	return "unknown", "insufficient signal", a.ReviewReason
}
