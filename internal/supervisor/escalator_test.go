package supervisor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/orchestrator"
)

func TestBuildEscalationJSONKeys(t *testing.T) {
	attempts := make([]AttemptRecord, 0, 9)
	for i := 0; i < 9; i++ {
		approachIdx := i / 3
		attemptIdx := i % 3
		attempts = append(attempts, AttemptRecord{
			ApproachIdx:   approachIdx,
			AttemptIdx:    attemptIdx,
			StartedAt:     time.Now(),
			FinishedAt:    time.Now(),
			Receipt:       orchestrator.Receipt{OK: false, TaskID: "t-" + intStr(i)},
			ReviewVerdict: VerdictRevise,
		})
	}
	receipt := &orchestrator.Receipt{OK: false, CommitSHA: "deadbeef", FilesModified: []string{"x.go"}}
	rca := RCARecord{
		Symptom:    "scope violation",
		RootCause:  "mutated outside allowed_paths",
		Confidence: 0.7,
	}

	esc := BuildEscalation("sup-1", "sup-1", "approach_budget_exhausted", attempts, receipt, rca)

	if esc.ApproachesTried != 3 {
		t.Errorf("expected approaches_tried=3, got %d", esc.ApproachesTried)
	}
	if esc.TotalAttempts != 9 {
		t.Errorf("expected total_attempts=9, got %d", esc.TotalAttempts)
	}

	raw, err := json.Marshal(esc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	required := []string{
		"task_id", "trigger", "envelope_summary",
		"approaches_tried", "total_attempts",
		"failed_receipt_ids", "rca_summary",
		"last_diff_summary", "recommended_human_action",
	}
	for _, key := range required {
		if _, ok := asMap[key]; !ok {
			t.Errorf("missing key %q in escalation JSON: %s", key, string(raw))
		}
	}
}
