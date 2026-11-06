package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/testenv"
)

func TestGatesRecordListRoundTrip(t *testing.T) {
	if err := testenv.ScrubAmbientDelegationEnv(); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	t.Setenv("G8S_STATE_DIR", tmp)

	in := `[{"workflow":"ci.yml","name":"Quality","conclusion":"success","sha":"abc","started_at":"2026-11-06T12:00:00Z"}]`
	var stdout1, stderr1 bytes.Buffer
	if code := runGatesWithIO([]string{"record"}, strings.NewReader(in), &stdout1, &stderr1); code != 0 {
		t.Fatalf("record exit %d stderr=%s", code, stderr1.String())
	}
	ledger := filepath.Join(tmp, "gate_ci_run_events.jsonl")
	// Windows: filepath.Join yields backslashes that the JSON envelope escapes as \\ —
	// compare against the JSON-escaped form so the assertion is platform-fair (caught by windows-latest CI).
	jsonEscapedLedger := strings.ReplaceAll(ledger, `\`, `\\`)
	if !strings.Contains(stdout1.String(), jsonEscapedLedger) {
		t.Fatalf("expected stdout to mention ledger path, got %q", stdout1.String())
	}

	var stdout2, stderr2 bytes.Buffer
	if code := runGatesWithIO([]string{"list", "--json"}, strings.NewReader(""), &stdout2, &stderr2); code != 0 {
		t.Fatalf("list exit %d stderr=%s", code, stderr2.String())
	}
	var env map[string]any
	if err := json.Unmarshal(stdout2.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	data, _ := env["data"].(map[string]any)
	rows, _ := data["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1", len(rows))
	}
}
