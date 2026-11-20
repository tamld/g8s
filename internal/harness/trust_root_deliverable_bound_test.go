package harness

import (
	"errors"
	"testing"
)

// TestCheckDeliverableSizeBoundaries pins the #981 deliverable read cap at the
// boundary without materializing a 512 MiB file: exactly-at-limit deliverables pass
// (trusted-size), one byte past the budget is typed-rejected.
func TestCheckDeliverableSizeBoundaries(t *testing.T) {
	if err := checkDeliverableSize("deliverables/report.txt", maxDeliverableFileBytes); err != nil {
		t.Fatalf("exactly-at-limit deliverable rejected: %v", err)
	}
	err := checkDeliverableSize("deliverables/pathological.bin", maxDeliverableFileBytes+1)
	if err == nil {
		t.Fatal("deliverable one byte past 512 MiB budget accepted, want typed reject (#981)")
	}
	if !errors.Is(err, ErrDeliverableArtifactTooLarge) {
		t.Fatalf("error = %v, want errors.Is(err, ErrDeliverableArtifactTooLarge)", err)
	}
}
