package supervisor

import (
	"reflect"
	"testing"
)

func TestSelectEnvelopeNil(t *testing.T) {
	env := SelectEnvelope(nil)

	mustTrue := map[string]bool{
		"DoR":        env.DoR,
		"DoD":        env.DoD,
		"Validateds": env.Validateds,
	}
	for name, val := range mustTrue {
		if !val {
			t.Errorf("expected %s to be true for nil hints, got false", name)
		}
	}
	if env.SRS || env.PRD || env.FSM || env.DnD {
		t.Errorf("expected SRS/PRD/FSM/DnD all false for nil hints, got %+v", env)
	}
}

func TestSelectEnvelopeHonorsHints(t *testing.T) {
	hints := EnvelopeHints{
		"SRS": true,
		"PRD": true,
		"FSM": true,
		"DnD": true,
	}
	env := SelectEnvelope(hints)

	mustTrue := map[string]bool{
		"DoR":        env.DoR,
		"DoD":        env.DoD,
		"DnD":        env.DnD,
		"Validateds": env.Validateds,
		"SRS":        env.SRS,
		"PRD":        env.PRD,
		"FSM":        env.FSM,
	}
	for name, val := range mustTrue {
		if !val {
			t.Errorf("expected %s true, got false", name)
		}
	}

	wantFields := []string{"DoR", "DoD", "DnD", "Validateds", "SRS", "PRD", "FSM"}
	if !reflect.DeepEqual(env.SelectedFields, wantFields) {
		t.Errorf("SelectedFields = %v, want %v", env.SelectedFields, wantFields)
	}
}

func TestSelectEnvelopeScoreBounds(t *testing.T) {
	minimal := SelectEnvelope(nil)
	if minimal.Score < 0 || minimal.Score > 1 {
		t.Errorf("minimal envelope score out of [0,1]: %f", minimal.Score)
	}

	full := SelectEnvelope(EnvelopeHints{"SRS": true, "PRD": true, "FSM": true, "DnD": true})
	if full.Score <= minimal.Score {
		t.Errorf("full envelope should score higher than minimal: full=%f minimal=%f", full.Score, minimal.Score)
	}
}
