package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type tickResultEnvelope struct {
	V          int    `json:"v"`
	Kind       string `json:"kind"`
	Command    string `json:"cmd"`
	Subcommand string `json:"sub"`
	Data       struct {
		Job    string          `json:"job"`
		Status string          `json:"status"`
		Detail json.RawMessage `json:"detail"`
	} `json:"data"`
}

func TestAutopilotTick_DefaultJobs(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	stateDir := filepath.Join(tempDir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	evidenceDir := filepath.Join(stateDir, "evidence")
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}

	// 1. Fixture: over-retention evidence directory (30 days old)
	oldDir := filepath.Join(evidenceDir, "task-old-fixture")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("mkdir old fixture: %v", err)
	}
	oldReceipt := filepath.Join(oldDir, "receipt.json")
	if err := os.WriteFile(oldReceipt, []byte(`{"task_id":"task-old-fixture"}`), 0o600); err != nil {
		t.Fatalf("write old receipt: %v", err)
	}
	oldTime := time.Now().Add(-30 * 24 * time.Hour)
	_ = os.Chtimes(oldDir, oldTime, oldTime)
	_ = os.Chtimes(oldReceipt, oldTime, oldTime)

	// 2. Fixture: fresh evidence directory (1 day old)
	freshDir := filepath.Join(evidenceDir, "task-fresh-fixture")
	if err := os.MkdirAll(freshDir, 0o700); err != nil {
		t.Fatalf("mkdir fresh fixture: %v", err)
	}
	freshReceipt := filepath.Join(freshDir, "receipt.json")
	if err := os.WriteFile(freshReceipt, []byte(`{"task_id":"task-fresh-fixture"}`), 0o600); err != nil {
		t.Fatalf("write fresh receipt: %v", err)
	}
	freshTime := time.Now().Add(-24 * time.Hour)
	_ = os.Chtimes(freshDir, freshTime, freshTime)
	_ = os.Chtimes(freshReceipt, freshTime, freshTime)

	// 3. Fixture: database with empty sessions registry
	dbPath := filepath.Join(stateDir, "g8s.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, status TEXT, heartbeat_at REAL)`)
	db.Close()
	if err != nil {
		t.Fatalf("create sessions table: %v", err)
	}

	// Run g8s autopilot tick
	cmd := exec.Command(binPath, "autopilot", "tick")
	cmd.Env = append(cmd.Environ(),
		"G8S_STATE_DIR="+stateDir,
		"G8S_EVIDENCE_DIR="+evidenceDir,
		"G8S_EVIDENCE_RETENTION_DAYS=7",
		"G8S_DB="+dbPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("autopilot tick failed: %v\nOutput: %s", err, string(out))
	}

	// Parse JSON envelopes
	var envelopes []tickResultEnvelope
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var env tickResultEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("failed to unmarshal tick output line %q: %v\nFull output:\n%s", line, err, string(out))
		}
		envelopes = append(envelopes, env)
	}

	if len(envelopes) != 3 {
		t.Fatalf("expected 3 envelopes (one per default job), got %d:\n%s", len(envelopes), string(out))
	}

	jobsSeen := make(map[string]tickResultEnvelope)
	for _, env := range envelopes {
		if env.Kind != "autopilot_tick" {
			t.Errorf("expected kind=autopilot_tick, got %q", env.Kind)
		}
		if env.Command != "autopilot" || env.Subcommand != "tick" {
			t.Errorf("expected cmd=autopilot sub=tick, got cmd=%s sub=%s", env.Command, env.Subcommand)
		}
		jobsSeen[env.Data.Job] = env
	}

	// Verify doctor ran
	docEnv, ok := jobsSeen["doctor"]
	if !ok {
		t.Errorf("doctor job not found in tick output")
	} else {
		if docEnv.Data.Status != "ok" && docEnv.Data.Status != "unhealthy" {
			t.Errorf("unexpected doctor status: %s", docEnv.Data.Status)
		}
		var docDetail map[string]any
		if err := json.Unmarshal(docEnv.Data.Detail, &docDetail); err != nil {
			t.Errorf("unmarshal doctor detail: %v", err)
		} else {
			if _, exists := docDetail["overall_status"]; !exists {
				t.Errorf("expected overall_status in doctor detail")
			}
		}
	}

	// Verify retention ran and deleted over-retention directory
	retEnv, ok := jobsSeen["retention"]
	if !ok {
		t.Errorf("retention job not found in tick output")
	} else {
		if retEnv.Data.Status != "ok" {
			t.Errorf("expected retention status=ok, got %s", retEnv.Data.Status)
		}
	}

	// Disk verification: oldDir must be removed, freshDir must remain
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("expected over-retention dir %s to be deleted, but still exists", oldDir)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("expected freshDir %s to remain, got err: %v", freshDir, err)
	}

	// Verify hygiene ran and tolerated empty sessions registry
	hygEnv, ok := jobsSeen["hygiene"]
	if !ok {
		t.Errorf("hygiene job not found in tick output")
	} else {
		if hygEnv.Data.Status != "ok" {
			t.Errorf("expected hygiene status=ok with empty registry, got %s", hygEnv.Data.Status)
		}
	}
}

func TestAutopilotTick_JobsSubset(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	stateDir := filepath.Join(tempDir, "state")
	evidenceDir := filepath.Join(stateDir, "evidence")
	_ = os.MkdirAll(evidenceDir, 0o700)

	// Run with --jobs retention only
	cmd := exec.Command(binPath, "autopilot", "tick", "--jobs", "retention")
	cmd.Env = append(cmd.Environ(),
		"G8S_STATE_DIR="+stateDir,
		"G8S_EVIDENCE_DIR="+evidenceDir,
		"G8S_EVIDENCE_RETENTION_DAYS=7",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("autopilot tick --jobs retention failed: %v\nOutput: %s", err, string(out))
	}

	var envelopes []tickResultEnvelope
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var env tickResultEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("failed to unmarshal JSON envelope: %v\nLine: %s", err, line)
		}
		envelopes = append(envelopes, env)
	}

	if len(envelopes) != 1 {
		t.Fatalf("expected exactly 1 envelope for --jobs retention, got %d:\n%s", len(envelopes), string(out))
	}

	if envelopes[0].Data.Job != "retention" {
		t.Errorf("expected job=retention, got %s", envelopes[0].Data.Job)
	}
	if envelopes[0].Data.Status != "ok" {
		t.Errorf("expected status=ok, got %s", envelopes[0].Data.Status)
	}
}

func TestAutopilotTick_InvalidJob(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	cmd := exec.Command(binPath, "autopilot", "tick", "--jobs", "unknown_job")
	cmd.Env = append(cmd.Environ(), "G8S_STATE_DIR="+tempDir)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error on invalid job name, but command succeeded:\n%s", string(out))
	}

	if !strings.Contains(string(out), "unknown_job") && !strings.Contains(string(out), "invalid job") {
		t.Errorf("expected error message to mention invalid job, got:\n%s", string(out))
	}
}
