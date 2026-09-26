// Package dialectic implements the Dialectic Bounce lifecycle (#258 Phase A):
// a deterministic debate engine where a thesis is refined through bounded
// critique rounds. The two invariants from the issue mandate:
//
//  1. Hard debate ceiling N ≤ 3 — a debate can never bounce more than three
//     times; at the ceiling it converges on the last defensible artifact or
//     escalates to HITL.
//  2. Evidence-gated bounces — a bounce is only accepted when the critique
//     attaches a *physical evidence artifact* (e.g. a failing check). A
//     bare "I disagree" without evidence is rejected, which eliminates the
//     "debate ping-pong" token-burning loop by construction.
package dialectic

import (
	"errors"
	"fmt"
)

// State is the debate lifecycle state.
type State string

const (
	StateProposed  State = "PROPOSED"
	StateCritiqued State = "CRITIQUED"
	StateBounced   State = "BOUNCED"
	StateConverged State = "CONVERGED"
	StateEscalated State = "ESCALATED"
)

// HardCeiling is the maximum number of bounces (issue mandate N ≤ 3).
const HardCeiling = 3

// ErrInvalidTransition guards the FSM against illegal jumps.
var ErrInvalidTransition = errors.New("dialectic: invalid state transition")

// Evidence is the physical artifact a critique must attach to justify a
// bounce (a failing test, a rejected schema, a benchmark regression...).
type Evidence struct {
	Check   string // what was checked (e.g. "go test ./internal/x")
	Failed  bool   // the check failed (evidence exists only for failures)
	Summary string // bounded excerpt of the failure
}

// Critique is one round of opposition to the current artifact.
type Critique struct {
	Reviewer string
	Comment  string
	Evidence *Evidence // nil = opinion without proof (cannot bounce)
}

// Round records one full propose→critique cycle.
type Round struct {
	Number   int
	Critique Critique
	Accepted bool // bounce accepted (evidence attached)
}

// Debate is the deterministic lifecycle over one artifact.
type Debate struct {
	State    State
	Rounds   []Round
	Artifact string // current artifact under debate
}

// NewDebate starts a debate over the initial artifact.
func NewDebate(artifact string) *Debate {
	return &Debate{State: StateProposed, Artifact: artifact}
}

// Critique applies one critique and transitions the FSM.
//
// Semantics:
//   - critique without evidence → the artifact stands (state CONVERGED only
//     when the reviewer explicitly approves; otherwise the critique is
//     recorded and the artifact is returned unchanged — no bounce).
//   - critique with failing evidence and bounces < HardCeiling → BOUNCED.
//   - critique with failing evidence at the ceiling → ESCALATED (the engine
//     refuses round N+1; the operator decides).
func (d *Debate) Critique(c Critique) (string, error) {
	// CONVERGED is the only terminal state; critiques from CRITIQUED
	// (repeat opinions) and ESCALATED (post-ceiling notes) are recorded
	// without changing the artifact — the adversarial simulations rely on
	// this to prove debates terminate regardless of debater persistence.
	if d.State == StateConverged {
		return d.Artifact, fmt.Errorf("%w: debate already converged", ErrInvalidTransition)
	}

	bounces := countBounces(d.Rounds)
	if c.Evidence != nil && c.Evidence.Failed {
		if bounces >= HardCeiling {
			d.State = StateEscalated
			d.Rounds = append(d.Rounds, Round{Number: bounces + 1, Critique: c, Accepted: false})
			return d.Artifact, nil
		}
		d.State = StateBounced
		d.Rounds = append(d.Rounds, Round{Number: bounces + 1, Critique: c, Accepted: true})
		return d.Artifact, nil
	}

	d.Rounds = append(d.Rounds, Round{Number: bounces + 1, Critique: c, Accepted: false})
	if isApproval(c) {
		d.State = StateConverged
		return d.Artifact, nil
	}
	// Opinion without proof: the artifact stands — record, do not bounce.
	d.State = StateCritiqued
	return d.Artifact, nil
}

// Revise installs the improved artifact after an accepted bounce.
func (d *Debate) Revise(artifact string) error {
	if d.State != StateBounced {
		return fmt.Errorf("%w: revise requires an accepted bounce (state %s)", ErrInvalidTransition, d.State)
	}
	d.Artifact = artifact
	d.State = StateProposed
	return nil
}

// Bounces returns the number of accepted evidence-backed bounces.
func (d *Debate) Bounces() int { return countBounces(d.Rounds) }

func countBounces(rounds []Round) int {
	n := 0
	for _, r := range rounds {
		if r.Accepted {
			n++
		}
	}
	return n
}

func isApproval(c Critique) bool {
	return c.Evidence == nil && (c.Comment == "approve" || c.Comment == "approved" || c.Comment == "LGTM")
}
