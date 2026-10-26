// Package premise implements the DELTA-42 Premise Challenge & Sycophancy
// Gate: it decomposes an operator request or supervisor brief into atomic
// claims and classifies each claim into an evidence tier.
//
// Worker output is already challenged by `g8s verify` (blind packets, seeded
// mutant canaries, holdout suites). Nothing challenged the premises of the
// human operator or the F0 supervisor: a brief stating "the CLI lacks init
// and doctor" would be executed by a compliant worker that re-implements two
// commands that already exist. This package closes that gap.
//
// Claims about the repository itself (command exists, file exists, symbol
// exists, test exists/passes, feature or delta id exists, counted entities)
// are probed deterministically and offline — no LLM is consulted to decide a
// verdict. Every probed claim carries the probe description, a reproducible
// cross-check command, the bounded probe output, and the SHA-256 of that
// output. An optional LLM decomposer may ADD candidate claims; it can never
// remove deterministic claims nor assign a status (Sensor ≠ Decider).
package premise

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status is the evidence tier assigned to a claim.
type Status string

const (
	// StatusMechanicalVerified marks a claim confirmed by a deterministic
	// local probe whose output hash is attached.
	StatusMechanicalVerified Status = "MECHANICAL_VERIFIED"
	// StatusObservational marks a claim that cites runtime telemetry (exit
	// codes, logs, hashes) which the gate did not re-execute.
	StatusObservational Status = "OBSERVATIONAL"
	// StatusUnverified marks prose or a repo claim the gate could not probe.
	StatusUnverified Status = "UNVERIFIED"
	// StatusContradicted marks a claim refuted by a deterministic probe.
	StatusContradicted Status = "CONTRADICTED"
)

// Kind is the claim family, which selects the deterministic probe.
type Kind string

const (
	KindCommandExists Kind = "command_exists"
	KindFileExists    Kind = "file_exists"
	KindSymbolExists  Kind = "symbol_exists"
	KindTestPasses    Kind = "test_passes"
	KindFeatureExists Kind = "feature_exists"
	KindDeltaExists   Kind = "delta_exists"
	KindCommandCount  Kind = "command_count"
	KindDeltaCount    Kind = "delta_count"
	KindProse         Kind = "prose"
)

// Assertion is what the claim asserts about its subject.
type Assertion string

const (
	AssertPresent Assertion = "present"
	AssertAbsent  Assertion = "absent"
	AssertPasses  Assertion = "passes"
	AssertFails   Assertion = "fails"
	AssertEquals  Assertion = "equals"
	AssertStated  Assertion = "stated"
)

// Verdict values for a whole report.
const (
	VerdictContradicted = "CONTRADICTED"
	VerdictClear        = "CLEAR"
)

// ErrorCodePremiseContradicted is the stable machine code emitted when a
// premise check finds at least one CONTRADICTED claim.
const ErrorCodePremiseContradicted = "E_PREMISE_CONTRADICTED"

// ReportSchema identifies the JSON contract of Report.
const ReportSchema = "g8s.premise-report/v1"

// maxEvidenceOutput bounds the probe output stored in a claim.
const maxEvidenceOutput = 2048

// Evidence is the mechanical artifact attached to a probed claim.
type Evidence struct {
	// Probe names the deterministic probe that ran (e.g. "cli_registry_ast").
	Probe string `json:"probe"`
	// Command describes exactly what the probe executed.
	Command string `json:"command"`
	// Reproduce is an operator-runnable cross-check command.
	Reproduce string `json:"reproduce,omitempty"`
	// Output is the bounded probe output the verdict was derived from.
	Output string `json:"output"`
	// OutputSHA256 is the hex SHA-256 of Output.
	OutputSHA256 string `json:"output_sha256"`
	// ExitCode is the probe exit code (0 unless a subprocess failed).
	ExitCode int `json:"exit_code"`
}

// Claim is one atomic assertion extracted from the input text.
type Claim struct {
	ID       string    `json:"id"`
	Text     string    `json:"text"`
	Sentence string    `json:"sentence"`
	Kind     Kind      `json:"kind"`
	Subject  string    `json:"subject,omitempty"`
	Asserted Assertion `json:"asserted"`
	Value    *int      `json:"value,omitempty"`
	Observed string    `json:"observed,omitempty"`
	Status   Status    `json:"status"`
	Evidence *Evidence `json:"evidence,omitempty"`
	Note     string    `json:"note,omitempty"`
	Source   string    `json:"source"`
}

// Summary counts claims per evidence tier.
type Summary struct {
	Total              int `json:"total"`
	MechanicalVerified int `json:"mechanical_verified"`
	Observational      int `json:"observational"`
	Unverified         int `json:"unverified"`
	Contradicted       int `json:"contradicted"`
}

// Report is the full premise-check result.
type Report struct {
	Schema      string   `json:"schema"`
	InputSHA256 string   `json:"input_sha256"`
	Repo        string   `json:"repo"`
	Decomposer  string   `json:"decomposer"`
	Claims      []Claim  `json:"claims"`
	Summary     Summary  `json:"summary"`
	Verdict     string   `json:"verdict"`
	Warnings    []string `json:"warnings,omitempty"`
}

// Contradicted reports whether any claim was refuted.
func (r *Report) Contradicted() bool {
	return r != nil && r.Summary.Contradicted > 0
}

// ContradictedClaims returns the refuted claims in report order.
func (r *Report) ContradictedClaims() []Claim {
	if r == nil {
		return nil
	}
	var out []Claim
	for _, c := range r.Claims {
		if c.Status == StatusContradicted {
			out = append(out, c)
		}
	}
	return out
}

// Options configures a premise check.
type Options struct {
	// RepoRoot is the repository whose claims are probed (default ".").
	RepoRoot string
	// RunTests allows executing `go test -run ^Name$` for test claims.
	RunTests bool
	// TestTimeout bounds one test execution (default 120s).
	TestTimeout time.Duration
	// Complete, when non-nil, enables additive LLM decomposition.
	Complete CompleteFunc
	// DecomposerLabel names the LLM decomposer in the report.
	DecomposerLabel string
}

// Check decomposes text into atomic claims and classifies each one.
func Check(ctx context.Context, text string, opts Options) (*Report, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("premise: input text is empty")
	}
	root := opts.RepoRoot
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("premise: resolve repo root %q: %w", root, err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("premise: repo root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("premise: repo root %q is not a directory", absRoot)
	}

	report := &Report{
		Schema:      ReportSchema,
		InputSHA256: sha256Hex(text),
		Repo:        absRoot,
		Decomposer:  "deterministic",
	}

	claims := Decompose(text)
	if opts.Complete != nil {
		label := opts.DecomposerLabel
		if label == "" {
			label = "llm"
		}
		extra, llmErr := LLMDecompose(ctx, opts.Complete, text)
		if llmErr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("llm decomposition unavailable (%s): %v; deterministic claims only", label, llmErr))
		} else {
			report.Decomposer = "deterministic+" + label
			claims = mergeClaims(claims, extra)
		}
	}

	p := newProber(absRoot, opts)
	for i := range claims {
		claims[i].ID = fmt.Sprintf("C%d", i+1)
		p.classify(ctx, &claims[i])
	}
	report.Claims = claims
	if report.Claims == nil {
		report.Claims = []Claim{}
	}
	report.Warnings = append(report.Warnings, p.warnings...)
	report.Summary = summarize(report.Claims)
	report.Verdict = VerdictClear
	if report.Summary.Contradicted > 0 {
		report.Verdict = VerdictContradicted
	}
	return report, nil
}

func summarize(claims []Claim) Summary {
	s := Summary{Total: len(claims)}
	for _, c := range claims {
		switch c.Status {
		case StatusMechanicalVerified:
			s.MechanicalVerified++
		case StatusObservational:
			s.Observational++
		case StatusContradicted:
			s.Contradicted++
		default:
			s.Unverified++
		}
	}
	return s
}

// claimKey identifies semantically identical claims for de-duplication.
func claimKey(c Claim) string {
	v := ""
	if c.Value != nil {
		v = fmt.Sprintf("%d", *c.Value)
	}
	subject := strings.ToLower(c.Subject)
	if c.Kind == KindProse {
		subject = strings.ToLower(strings.TrimSpace(c.Text))
	}
	return strings.Join([]string{string(c.Kind), subject, string(c.Asserted), v}, "\x00")
}

// mergeClaims appends extra claims not already present in base.
func mergeClaims(base, extra []Claim) []Claim {
	seen := make(map[string]bool, len(base))
	for _, c := range base {
		seen[claimKey(c)] = true
	}
	for _, c := range extra {
		k := claimKey(c)
		if seen[k] {
			continue
		}
		seen[k] = true
		base = append(base, c)
	}
	return base
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newEvidence(probe, command, reproduce, output string, exitCode int) *Evidence {
	if len(output) > maxEvidenceOutput {
		output = output[:maxEvidenceOutput] + "\n...[truncated]"
	}
	return &Evidence{
		Probe:        probe,
		Command:      command,
		Reproduce:    reproduce,
		Output:       output,
		OutputSHA256: sha256Hex(output),
		ExitCode:     exitCode,
	}
}

func intPtr(v int) *int { return &v }
