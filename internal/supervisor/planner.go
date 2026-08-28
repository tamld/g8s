// Package supervisor implements Concern A: a supervisor-driven fix loop that
// drives an orchestrator-bound worker through multiple attempts and approaches
// until the reviewed receipt passes, the loop is paused for human input, or the
// failure is escalated.
//
// This package depends only on stdlib + internal/orchestrator (types only) +
// internal/harness. It never imports worker backends (agy, codex, gemini,
// claude) — that isolation is asserted by the build tag-free grep test in
// supervisor_test.go (TestSupervisorNeverImportsWorkerBinaries).
package supervisor

import (
	"strings"
)

// EnvelopeHints flags the user-supplied evidence attached to a fix request.
// Keys map to the canonical envelope fields defined in the spec (DoR, DoD, SRS,
// PRD, FSM, DnD, Validateds). Unknown keys are ignored.
type EnvelopeHints map[string]bool

// TaskEnvelope is the bounded evidence contract attached to every attempt. The
// supervisor passes one envelope per approach so reviewers can detect drift
// across approaches. The minimal envelope (always present) is DoR + DoD +
// Validateds; the optional fields (SRS, PRD, FSM, DnD) activate only when the
// caller sets the matching EnvelopeHints flag.
type TaskEnvelope struct {
	DoR            bool     // Definition of Ready
	DoD            bool     // Definition of Done
	DnD            bool     // Definition of Done-decision rationale
	Validateds     bool     // Per-task validation checklist
	SRS            bool     // Software Requirements Specification
	PRD            bool     // Product Requirements Document
	FSM            bool     // Finite State Machine / lifecycle spec
	Score          float64  // Computed envelope quality score (0..1, higher better)
	SelectedFields []string // Names of fields actually included (for diagnostics)
}

// SelectEnvelope computes the minimal envelope required by Concern A plus any
// caller-requested extras. The minimal set (DoR + DoD + Validateds) is always
// present so reviewers have something to grade against; SRS / PRD / FSM / DnD
// are opt-in via EnvelopeHints.
//
// Score formula: fields_present * 1.0 - absent_field_cost. We treat every
// required field as worth 1.0; each absent optional field subtracts 0.25 so a
// minimal envelope scores 0.5 and the full envelope scores 1.0. Negative
// scores are clamped to zero.
func SelectEnvelope(hints EnvelopeHints) TaskEnvelope {
	env := TaskEnvelope{
		DoR:        true,
		DoD:        true,
		Validateds: true,
	}
	if hints["SRS"] {
		env.SRS = true
	}
	if hints["PRD"] {
		env.PRD = true
	}
	if hints["FSM"] {
		env.FSM = true
	}
	if hints["DnD"] {
		env.DnD = true
	}
	env.SelectedFields = selectedFields(env)
	env.Score = envelopeScore(env)
	return env
}

// selectedFields returns the canonical names of every envelope field set to
// true, in declaration order so tests can diff the list deterministically.
func selectedFields(env TaskEnvelope) []string {
	out := make([]string, 0, 7)
	if env.DoR {
		out = append(out, "DoR")
	}
	if env.DoD {
		out = append(out, "DoD")
	}
	if env.DnD {
		out = append(out, "DnD")
	}
	if env.Validateds {
		out = append(out, "Validateds")
	}
	if env.SRS {
		out = append(out, "SRS")
	}
	if env.PRD {
		out = append(out, "PRD")
	}
	if env.FSM {
		out = append(out, "FSM")
	}
	return out
}

// envelopeScore scores the envelope: every present field adds 1.0; every
// absent optional field subtracts 0.25. Result is clamped to [0, 1].
func envelopeScore(env TaskEnvelope) float64 {
	present := 0
	for _, on := range []bool{env.DoR, env.DoD, env.DnD, env.Validateds, env.SRS, env.PRD, env.FSM} {
		if on {
			present++
		}
	}
	const maxFields = 7
	absent := maxFields - present
	score := float64(present) - 0.25*float64(absent)
	// Normalize: best case = maxFields = 7, worst case = 0 fields = 0.
	if score < 0 {
		score = 0
	}
	if score > float64(maxFields) {
		score = float64(maxFields)
	}
	return score / float64(maxFields)
}

// String renders the envelope in human-readable form for logs.
func (e TaskEnvelope) String() string {
	return "envelope{" + strings.Join(e.SelectedFields, ",") + "}"
}
