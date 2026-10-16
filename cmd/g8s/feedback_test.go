package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeedbackCLIRecordOverrideAndReport(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "telemetry.db")

	// 1. Record an F0 override receipt
	var stdout, stderr bytes.Buffer
	code := runFeedbackWithIO([]string{
		"override",
		"--db", dbPath,
		"--task", "task-596",
		"--rule", "RULE-JIT-01",
		"--reason", "F0 allowed prototype helper",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("feedback override exited %d: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"task_id": "task-596"`) {
		t.Fatalf("unexpected override output: %s", stdout.String())
	}

	// 2. Record a Sovereign HITL veto
	stdout.Reset()
	stderr.Reset()
	code = runFeedbackWithIO([]string{
		"record",
		"--db", dbPath,
		"--rule", "RULE-JIT-01",
		"--task", "task-596",
		"--package", "internal/worker",
		"--verdict", "veto",
		"--note", "Human sovereign vetoed override",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("feedback record exited %d: stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"thermal_state": "hot"`) {
		t.Fatalf("expected thermal_state hot after HITL veto, got: %s", stdout.String())
	}

	// 3. Generate Hypothesis Report (H1, H2, H3)
	stdout.Reset()
	stderr.Reset()
	code = runFeedbackWithIO([]string{
		"report",
		"--db", dbPath,
		"--max-k", "3",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("feedback report exited %d: stderr=%s", code, stderr.String())
	}

	var env struct {
		Kind string `json:"kind"`
		Data struct {
			MaxActiveRulesPerPkg int `json:"max_active_rules_per_pkg"`
			Hypotheses           []struct {
				ID              string `json:"id"`
				EpistemicStatus string `json:"epistemic_status"`
			} `json:"hypotheses"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal report json: %v\nraw: %s", err, stdout.String())
	}
	if env.Kind != "hypothesis_report" || len(env.Data.Hypotheses) != 3 {
		t.Fatalf("expected kind=hypothesis_report and 3 hypotheses in report, got %+v", env)
	}
	for _, h := range env.Data.Hypotheses {
		if h.EpistemicStatus != "hypothesis" {
			t.Fatalf("expected hypothesis %s to have epistemic_status='hypothesis' with <5 samples, got %s", h.ID, h.EpistemicStatus)
		}
	}
}
