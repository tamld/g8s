// Package routing — CI workflow run history for reflex scorecard (D9-S1, #714).
package routing

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/pathutil"
)

// CIGateRunRecord is one append-only observation of a GitHub Actions workflow run
// (or compatible) outcome ingested via `g8s gates record`.
type CIGateRunRecord struct {
	Workflow   string `json:"workflow"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	SHA        string `json:"sha"`
	StartedAt  string `json:"started_at"`
	RecordedAt string `json:"recorded_at"`
}

// CIGateHistoryPath resolves the JSONL ledger path for CI gate / workflow run history.
func CIGateHistoryPath(stateDir string) string {
	if override := strings.TrimSpace(os.Getenv("G8S_CI_GATE_HISTORY_PATH")); override != "" {
		return override
	}
	if strings.TrimSpace(stateDir) == "" {
		stateDir = pathutil.DefaultStateDir()
	}
	return filepath.Join(stateDir, "gate_ci_run_events.jsonl")
}

// RecordCIGateRuns appends one or more workflow run rows to the history ledger.
func RecordCIGateRuns(stateDir string, recs []CIGateRunRecord) error {
	if len(recs) == 0 {
		return nil
	}
	p := CIGateHistoryPath(stateDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for i := range recs {
		rec := recs[i]
		rec.Workflow = strings.TrimSpace(rec.Workflow)
		rec.Name = strings.TrimSpace(rec.Name)
		rec.Conclusion = strings.TrimSpace(rec.Conclusion)
		rec.SHA = strings.TrimSpace(rec.SHA)
		rec.StartedAt = strings.TrimSpace(rec.StartedAt)
		if rec.Workflow == "" || rec.Name == "" || rec.Conclusion == "" || rec.SHA == "" || rec.StartedAt == "" {
			continue
		}
		if rec.RecordedAt == "" {
			rec.RecordedAt = now
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// ReadCIGateRuns returns all decoded rows from the CI gate history ledger (oldest first).
func ReadCIGateRuns(stateDir string) ([]CIGateRunRecord, error) {
	p := CIGateHistoryPath(stateDir)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []CIGateRunRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec CIGateRunRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
