package reflection

import (
	"reflect"
	"testing"
	"time"
)

func TestEvaluatePruneLaw(t *testing.T) {
	quarter := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cycle := func(add, prune []string) ProcessReflectionReceipt {
		return ProcessReflectionReceipt{Cycle: Cycle{Timestamp: quarter}, AddList: add, PruneList: prune}
	}
	empty := cycle(nil, nil)
	added := cycle([]string{"a", "b"}, nil)
	pruned := cycle(nil, []string{"a", "b"})
	old := added
	old.Cycle.Timestamp = quarter.Add(-time.Nanosecond)
	observed := empty
	observed.Verdicts = []Verdict{{ArtifactPath: "z.md"}, {ArtifactPath: "a.md"}, {ArtifactPath: "a.md"}, {ArtifactPath: "touched.md"}}
	touched := cycle([]string{"touched.md"}, []string{"removed.md"})
	for _, tc := range []struct {
		name      string
		cycles    []ProcessReflectionReceipt
		threshold int
		want      PruneEvaluation
	}{
		{name: "no history", want: PruneEvaluation{PruneRatio: 1}},
		{name: "fresh additions violate", cycles: []ProcessReflectionReceipt{added}, want: PruneEvaluation{QuarterAddCount: 2, PruneRatio: 0, EmptyPruneStreak: 1, Violations: []string{"E_REFLEX_PRUNE_RATIO_BELOW_ONE"}}},
		{name: "cumulative balance", cycles: []ProcessReflectionReceipt{pruned, added}, want: PruneEvaluation{QuarterAddCount: 2, QuarterPruneCount: 2, PruneRatio: 1}},
		{name: "three empty", cycles: []ProcessReflectionReceipt{empty, empty, empty}, want: PruneEvaluation{PruneRatio: 1, EmptyPruneStreak: 3, Tombstone: true}},
		{name: "two empty", cycles: []ProcessReflectionReceipt{empty, empty}, want: PruneEvaluation{PruneRatio: 1, EmptyPruneStreak: 2}},
		{name: "older prune stops streak", cycles: []ProcessReflectionReceipt{empty, empty, pruned, empty}, want: PruneEvaluation{QuarterPruneCount: 2, PruneRatio: 1, EmptyPruneStreak: 2}},
		{name: "newest prune resets", cycles: []ProcessReflectionReceipt{pruned, empty, empty, empty}, want: PruneEvaluation{QuarterPruneCount: 2, PruneRatio: 1}},
		{name: "outside quarter excluded", cycles: []ProcessReflectionReceipt{empty, old}, want: PruneEvaluation{PruneRatio: 1, EmptyPruneStreak: 2}},
		{name: "stale at two cycles sorted unique untouched", cycles: []ProcessReflectionReceipt{touched, observed}, threshold: 2, want: PruneEvaluation{QuarterAddCount: 1, QuarterPruneCount: 1, PruneRatio: 1, StaleArtifacts: []string{"a.md", "z.md"}}},
		{name: "too recent", cycles: []ProcessReflectionReceipt{observed}, threshold: 2, want: PruneEvaluation{PruneRatio: 1, EmptyPruneStreak: 1}},
		{name: "stale reporting disabled", cycles: []ProcessReflectionReceipt{empty, observed}, want: PruneEvaluation{PruneRatio: 1, EmptyPruneStreak: 2}},
		{name: "historical add excludes stale", cycles: []ProcessReflectionReceipt{observed, cycle([]string{"a.md", "z.md", "touched.md"}, nil)}, threshold: 1, want: PruneEvaluation{QuarterAddCount: 3, EmptyPruneStreak: 2, Violations: []string{"E_REFLEX_PRUNE_RATIO_BELOW_ONE"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluatePruneLaw(tc.cycles, quarter, tc.threshold)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v; want %+v", got, tc.want)
			}
		})
	}
}
