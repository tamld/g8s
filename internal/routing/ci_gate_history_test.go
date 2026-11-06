package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/testenv"
)

func TestMain(m *testing.M) {
	if err := testenv.ScrubAmbientDelegationEnv(); err != nil {
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestRecordCIGateRunsRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("G8S_CI_GATE_HISTORY_PATH", "")
	ledger := filepath.Join(tmpDir, "gate_ci_run_events.jsonl")

	want := []CIGateRunRecord{
		{
			Workflow:   "CI.yml",
			Name:       "Quality Gate",
			Conclusion: "success",
			SHA:        "abc123def456",
			StartedAt:  "2026-11-06T12:00:00Z",
		},
		{
			Workflow:   "Nightly.yml",
			Name:       "full-matrix",
			Conclusion: "failure",
			SHA:        "fedcba098765",
			StartedAt:  "2026-11-06T18:30:00Z",
		},
	}
	if err := RecordCIGateRuns(tmpDir, want); err != nil {
		t.Fatalf("RecordCIGateRuns: %v", err)
	}
	if CIGateHistoryPath(tmpDir) != ledger {
		t.Fatalf("CIGateHistoryPath = %q, want %q", CIGateHistoryPath(tmpDir), ledger)
	}
	got, err := ReadCIGateRuns(tmpDir)
	if err != nil {
		t.Fatalf("ReadCIGateRuns: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2: %+v", len(got), got)
	}
	for i := range want {
		if got[i].Workflow != want[i].Workflow || got[i].Name != want[i].Name ||
			got[i].Conclusion != want[i].Conclusion || got[i].SHA != want[i].SHA ||
			got[i].StartedAt != want[i].StartedAt {
			t.Fatalf("row %d mismatch: got %+v want %+v", i, got[i], want[i])
		}
		if got[i].RecordedAt == "" {
			t.Fatalf("row %d missing recorded_at", i)
		}
	}

	// Append path: second write preserves prior rows
	more := []CIGateRunRecord{{
		Workflow:   "CI.yml",
		Name:       "quick-check",
		Conclusion: "cancelled",
		SHA:        "111111",
		StartedAt:  "2026-11-07T01:00:00Z",
	}}
	if err := RecordCIGateRuns(tmpDir, more); err != nil {
		t.Fatalf("RecordCIGateRuns append: %v", err)
	}
	got2, err := ReadCIGateRuns(tmpDir)
	if err != nil {
		t.Fatalf("ReadCIGateRuns 2: %v", err)
	}
	if len(got2) != 3 {
		t.Fatalf("after append len = %d, want 3", len(got2))
	}
}

func TestRecordCIGateRunsSkipsIncompleteRows(t *testing.T) {
	tmpDir := t.TempDir()
	in := []CIGateRunRecord{
		{Workflow: "", Name: "n", Conclusion: "success", SHA: "a", StartedAt: "t"},
		{Workflow: "w", Name: "n", Conclusion: "success", SHA: "a", StartedAt: "2026-11-07T00:00:00Z"},
	}
	if err := RecordCIGateRuns(tmpDir, in); err != nil {
		t.Fatalf("RecordCIGateRuns: %v", err)
	}
	got, err := ReadCIGateRuns(tmpDir)
	if err != nil {
		t.Fatalf("ReadCIGateRuns: %v", err)
	}
	if len(got) != 1 || got[0].Workflow != "w" {
		t.Fatalf("got %+v, want single workflow w", got)
	}
}

func TestCIGateRunRecordJSON(t *testing.T) {
	const payload = `{"workflow":"w","name":"n","conclusion":"success","sha":"sh","started_at":"2026-01-02T00:00:00Z","recorded_at":"2026-01-02T01:00:00Z"}`
	var rec CIGateRunRecord
	if err := json.Unmarshal([]byte(payload), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Workflow != "w" || rec.Conclusion != "success" {
		t.Fatalf("unexpected decode: %+v", rec)
	}
}
