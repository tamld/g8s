package reflection

import (
	"sort"
	"time"
)

// PruneEvaluation reports quarter accounting and cycle-based retirement signals.
type PruneEvaluation struct {
	QuarterAddCount   float64
	QuarterPruneCount float64
	PruneRatio        float64
	EmptyPruneStreak  int
	StaleArtifacts    []string
	Tombstone         bool
	Violations        []string
}

// EvaluatePruneLaw expects cycles newest first. Only quarter accounting is
// timestamp-filtered; streak and staleness use the complete supplied history.
// Verdict paths provide the observable artifact inventory (no filesystem I/O).
// An artifact's observation cycle counts toward its age; verdicts are judgments,
// not touches. A path in any supplied add/prune list is excluded from staleness.
// Nonpositive staleAfterCycles disables stale reporting. Callers must supply
// complete history to detect artifacts absent from recent verdicts reliably.
func EvaluatePruneLaw(cycles []ProcessReflectionReceipt, quarterStart time.Time, staleAfterCycles int) PruneEvaluation {
	evaluation := PruneEvaluation{PruneRatio: 1}
	touched := make(map[string]bool)
	age := make(map[string]int)
	streakOpen := true
	for i, cycle := range cycles {
		if !cycle.Cycle.Timestamp.Before(quarterStart) {
			evaluation.QuarterAddCount += float64(len(cycle.AddList))
			evaluation.QuarterPruneCount += float64(len(cycle.PruneList))
		}
		if streakOpen && len(cycle.PruneList) == 0 {
			evaluation.EmptyPruneStreak++
		} else {
			streakOpen = false
		}
		for _, path := range cycle.AddList {
			touched[path] = true
		}
		for _, path := range cycle.PruneList {
			touched[path] = true
		}
		for _, verdict := range cycle.Verdicts {
			age[verdict.ArtifactPath] = i + 1
		}
	}
	if evaluation.QuarterAddCount > 0 {
		evaluation.PruneRatio = evaluation.QuarterPruneCount / evaluation.QuarterAddCount
		if evaluation.PruneRatio < 1 {
			evaluation.Violations = append(evaluation.Violations, "E_REFLEX_PRUNE_RATIO_BELOW_ONE")
		}
	}
	if staleAfterCycles > 0 {
		for path, cyclesObserved := range age {
			if path != "" && !touched[path] && cyclesObserved >= staleAfterCycles {
				evaluation.StaleArtifacts = append(evaluation.StaleArtifacts, path)
			}
		}
		sort.Strings(evaluation.StaleArtifacts)
	}
	evaluation.Tombstone = evaluation.EmptyPruneStreak >= 3
	return evaluation
}
