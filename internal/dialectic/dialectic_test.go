package dialectic

import (
	"errors"
	"strings"
	"testing"
)

// failEvidence builds a physical-evidence artifact for a critique.
func failEvidence(check string) *Evidence {
	return &Evidence{Check: check, Failed: true, Summary: "FAIL: assertion mismatch"}
}

// Adversarial simulation 1 (#258 mandate): a ping-pong debater keeps
// bouncing WITHOUT evidence. The engine must never bounce on bare opinion —
// the artifact stands and the debate converges when the reviewer approves.
func TestSimPingPongWithoutEvidenceTerminates(t *testing.T) {
	d := NewDebate("solution v1")
	versions := []string{"solution v1"}

	// Round 1-5: bare-opinion objections, no evidence.
	for i := 1; i <= 5; i++ {
		_, err := d.Critique(Critique{Reviewer: "opposer", Comment: "I just disagree"})
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		versions = append(versions, d.Artifact)
		if d.Bounces() != 0 {
			t.Fatalf("bare opinion must never bounce (round %d, bounces=%d)", i, d.Bounces())
		}
	}
	if len(versions) != 6 || versions[5] != "solution v1" {
		t.Errorf("artifact must be unchanged without evidence")
	}
	if d.State != StateCritiqued {
		t.Errorf("expected CRITIQUED, got %s", d.State)
	}
}

// Adversarial simulation 2: evidence-backed bounces are accepted but the
// hard ceiling N ≤ 3 forces escalation — a debate can never bounce a 4th
// time even with fresh evidence every round.
func TestSimHardCeilingForcesEscalation(t *testing.T) {
	d := NewDebate("artifact v0")
	version := "artifact v0"

	for round := 1; round <= 5; round++ {
		_, err := d.Critique(Critique{
			Reviewer: "auditor",
			Comment:  "failing check attached",
			Evidence: failEvidence("go test ./..."),
		})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if round <= HardCeiling {
			if d.State != StateBounced {
				t.Fatalf("round %d: expected BOUNCED, got %s", round, d.State)
			}
			version += "-r"
			if err := d.Revise(version); err != nil {
				t.Fatalf("round %d revise: %v", round, err)
			}
			continue
		}
		// Round 4 and 5: at/over the ceiling — escalation, no further bounce.
		if d.State != StateEscalated {
			t.Fatalf("round %d: expected ESCALATED at ceiling %d, got %s", round, HardCeiling, d.State)
		}
		if d.Bounces() != HardCeiling {
			t.Fatalf("ceiling violated: %d bounces", d.Bounces())
		}
	}
}

// A bounce without the artifact being revised is an invalid transition.
func TestReviseRequiresAcceptedBounce(t *testing.T) {
	d := NewDebate("v1")
	if err := d.Revise("v2"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("revise before any bounce must be invalid, got %v", err)
	}
}

// Evidence-backed dissent that gets fixed converges cleanly: propose →
// bounce(evidence) → revise → approve → CONVERGED.
func TestHappyPathConverges(t *testing.T) {
	d := NewDebate("design v1")
	out, err := d.Critique(Critique{Reviewer: "qa", Comment: "missing tests", Evidence: failEvidence("go test")})
	if err != nil || d.State != StateBounced {
		t.Fatalf("evidence bounce expected, got %s err %v out %s", d.State, err, out)
	}
	if err := d.Revise("design v2 with tests"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Critique(Critique{Reviewer: "qa", Comment: "LGTM"}); err != nil {
		t.Fatal(err)
	}
	if d.State != StateConverged {
		t.Errorf("expected CONVERGED, got %s", d.State)
	}
	if !strings.Contains(d.Artifact, "v2") {
		t.Errorf("artifact should be the revised one, got %q", d.Artifact)
	}
}
