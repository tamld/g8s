// Package supervisor — reviewer.go classifies a worker receipt as pass /
// revise / fail. Pass returns success; revise triggers another attempt; fail
// short-circuits to escalation.
package supervisor

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

// RealReviewer validates receipts by running tests on modified packages.
// It replaces StubReviewer for production use.
type RealReviewer struct {
	Timeout       time.Duration
	MaxOutputSize int64
}

// NewRealReviewer creates a reviewer with bounded execution limits.
// Default: 30s timeout, 1MB output cap.
func NewRealReviewer() *RealReviewer {
	return &RealReviewer{
		Timeout:       30 * time.Second,
		MaxOutputSize: 1024 * 1024,
	}
}

// Validate runs the scope gate and test gate.
// Scope gate: already enforced by orchestrator via diffScope (receipt.ScopeViolations).
// Test gate: runs `go test` on packages containing modified .go files.
func (r *RealReviewer) Validate(env TaskEnvelope, receipt orchestrator.Receipt) (map[string]bool, error) {
	out := make(map[string]bool, len(env.SelectedFields))
	for _, f := range env.SelectedFields {
		out[f] = false
	}

	// Test gate: run tests on modified Go packages
	testPassed, err := r.runTestGate(receipt)
	if err != nil {
		return out, err
	}

	if !testPassed {
		return out, nil
	}

	// All gates passed
	for _, f := range env.SelectedFields {
		out[f] = true
	}
	return out, nil
}

// runTestGate runs `go test` on packages containing modified .go files.
// Returns true if all tests pass (or no Go files to test).
func (r *RealReviewer) runTestGate(receipt orchestrator.Receipt) (bool, error) {
	// Collect unique packages from modified .go files
	packages := r.collectPackages(receipt.FilesModified)
	if len(packages) == 0 {
		return true, nil // No Go files to test
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()

	for _, pkg := range packages {
		cmd := exec.CommandContext(ctx, "go", "test", pkg)
		cmd.Dir = filepath.Dir(pkg) // Run from package directory
		output, err := cmd.CombinedOutput()
		if err != nil {
			// Test failed
			return false, nil
		}
		// Check output size
		if int64(len(output)) > r.MaxOutputSize {
			return false, nil
		}
	}
	return true, nil
}

// collectPackages extracts unique package paths from modified .go files.
// A package path is the directory containing the .go file.
func (r *RealReviewer) collectPackages(files []string) []string {
	pkgSet := make(map[string]struct{})
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			dir := filepath.Dir(f)
			if dir != "." {
				pkgSet[dir] = struct{}{}
			}
		}
	}
	pkgs := make([]string, 0, len(pkgSet))
	for p := range pkgSet {
		pkgs = append(pkgs, p)
	}
	return pkgs
}
