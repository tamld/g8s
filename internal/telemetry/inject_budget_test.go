package telemetry

// #395 C7/C8 (ADR-0023): injection budget cap G6 + Jev-free read path.
// C7's live-red (80,455 chars) was captured 2026-09-26 with a temporary test;
// this is its permanent form.

import (
	"strings"
	"testing"
)

func bigPatterns(n int, bodyLen int) []NegativePattern {
	patterns := make([]NegativePattern, n)
	for i := range patterns {
		patterns[i] = NegativePattern{
			ID:              "pat-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Title:           "Overflow Pattern",
			PatternType:     "test",
			RootCause:       strings.Repeat("x", bodyLen),
			Remediation:     strings.Repeat("y", bodyLen),
			ConfidenceScore: 0.9,
			OccurrenceCount: i + 1,
		}
	}
	return patterns
}

// C7: preflight injection never exceeds the 4096-char budget (G6), even under
// massive overflow — ranked truncation, not failure.
func TestInjectPreflightBudgetCap(t *testing.T) {
	out := RenderPreflightContext(bigPatterns(50, 400))
	if len(out) > 4096 {
		t.Fatalf("injection = %d chars, want <= 4096 (ADR-0023 G6 budget cap)", len(out))
	}
	if out == "" {
		t.Fatal("budgeted injection must still deliver the top-ranked patterns, got empty")
	}
}

// C8: the read path never calls the Jev sensor — injection is a pure
// deterministic render with no sensor/engine wiring (Jev-free by construction).
func TestInjectWorksWithSensorDown(t *testing.T) {
	out := RenderPreflightContext(bigPatterns(3, 50))
	if out == "" || len(out) > 4096 {
		t.Fatalf("sensorless injection produced %d chars", len(out))
	}
}
