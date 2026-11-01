package verifier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordGateOutcomesAndFlakeSummary(t *testing.T) {
	tmpDir := t.TempDir()
	ledgerPath := filepath.Join(tmpDir, "gate_outcome_events.jsonl")
	t.Setenv("G8S_GATE_OUTCOME_PATH", ledgerPath)

	class := "internal-worker"
	checkType := "go_test"
	checkTarget := "./internal/worker/..."

	recordVerdict := func(ok bool) {
		t.Helper()
		if err := RecordGateOutcomes(tmpDir, "task-flake", Verdict{
			Class: class,
			Checks: []CheckResult{{
				Type:   checkType,
				Target: checkTarget,
				OK:     ok,
			}},
			RecordedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("RecordGateOutcomes ok=%v: %v", ok, err)
		}
	}

	// FAIL -> PASS -> FAIL -> PASS
	recordVerdict(false)
	recordVerdict(true)
	recordVerdict(false)
	recordVerdict(true)

	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var transitions int
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, `"flake_transition":true`) {
			transitions++
		}
	}
	if transitions != 3 {
		t.Fatalf("flake_transition count = %d, want 3", transitions)
	}

	summaries, err := ComputeGateFlakeSummaries(tmpDir, 20)
	if err != nil {
		t.Fatalf("ComputeGateFlakeSummaries: %v", err)
	}
	var flaky *GateFlakeSummary
	for i := range summaries {
		if summaries[i].CheckType == checkType && summaries[i].CheckTarget == checkTarget {
			flaky = &summaries[i]
			break
		}
	}
	if flaky == nil {
		t.Fatalf("missing summary for %s/%s", checkType, checkTarget)
	}
	if flaky.WindowSamples != 4 || flaky.PassCount != 2 || flaky.FailCount != 2 {
		t.Fatalf("window counts: %+v", flaky)
	}
	if flaky.FlakeTransitions != 3 || flaky.FlakeRate != 1.0 || !flaky.QuarantineCandidate {
		t.Fatalf("flake metrics: %+v", flaky)
	}

	stablePath := filepath.Join(tmpDir, "gate_outcome_stable.jsonl")
	t.Setenv("G8S_GATE_OUTCOME_PATH", stablePath)
	for i := 0; i < 4; i++ {
		if err := RecordGateOutcomes(tmpDir, "task-stable", Verdict{
			Class: "stable-class",
			Checks: []CheckResult{{
				Type:   "lint",
				Target: "tools/ai_lint.sh",
				OK:     true,
			}},
			RecordedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("stable record %d: %v", i, err)
		}
	}
	stableSummaries, err := ComputeGateFlakeSummaries(tmpDir, 20)
	if err != nil {
		t.Fatalf("stable summaries: %v", err)
	}
	var stable *GateFlakeSummary
	for i := range stableSummaries {
		if stableSummaries[i].Class == "stable-class" {
			stable = &stableSummaries[i]
			break
		}
	}
	if stable == nil {
		t.Fatal("missing stable summary")
	}
	if stable.FlakeTransitions != 0 || stable.FlakeRate != 0 || stable.QuarantineCandidate {
		t.Fatalf("stable check should not quarantine: %+v", stable)
	}
}
