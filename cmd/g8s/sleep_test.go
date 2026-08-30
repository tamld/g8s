package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/sleep"
)

func TestCLISleepAndWake(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()

	// 1. Run g8s sleep --until 09:00 --json
	cmd := exec.Command(binPath, "sleep", "--until", "09:00", "--json")
	cmd.Env = append(cmd.Environ(), "G8S_STATE_DIR="+tempDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sleep --json failed: %v\nOutput: %s", err, string(out))
	}

	var env testEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal sleep envelope: %v\nRaw: %s", err, string(out))
	}
	if env.Command != "sleep" || env.Kind != "sleep_state" {
		t.Errorf("unexpected sleep envelope: %+v", env)
	}

	// 2. Add an event to the collector in tempDir
	collector := sleep.NewFileCollector(filepath.Join(tempDir, "sleep_events.jsonl"))
	_ = collector.Collect(context.Background(), sleep.Event{
		Type:      sleep.EventReceiptSuccess,
		Severity:  sleep.SeverityInfo,
		SessionID: "sess-100",
		Message:   "DEBT-48 dual-blind design merged",
		Timestamp: time.Now().UTC(),
	})

	// 3. Run g8s wake --json
	wCmd := exec.Command(binPath, "wake", "--json")
	wCmd.Env = append(wCmd.Environ(), "G8S_STATE_DIR="+tempDir)
	wOut, err := wCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wake --json failed: %v\nOutput: %s", err, string(wOut))
	}

	var wEnv testEnvelope
	if err := json.Unmarshal(wOut, &wEnv); err != nil {
		t.Fatalf("unmarshal wake envelope: %v\nRaw: %s", err, string(wOut))
	}
	if wEnv.Command != "wake" || wEnv.Kind != "wake_summary" {
		t.Errorf("unexpected wake envelope: %+v", wEnv)
	}

	var summary sleep.WakeSummary
	if err := json.Unmarshal(wEnv.Data, &summary); err != nil {
		t.Fatalf("unmarshal wake summary: %v", err)
	}

	if summary.TotalSessions < 1 {
		t.Errorf("expected at least 1 session, got %d", summary.TotalSessions)
	}
	if summary.VoiceText == "" {
		t.Errorf("expected non-empty voice text")
	}

	// 4. Run g8s wake in voice format (plain text output)
	vCmd := exec.Command(binPath, "wake", "--format=voice")
	vCmd.Env = append(vCmd.Environ(), "G8S_STATE_DIR="+tempDir)
	vOut, err := vCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wake --format=voice failed: %v\nOutput: %s", err, string(vOut))
	}
	if !strings.Contains(string(vOut), "Wake Summary") {
		t.Errorf("expected header in plain text output, got: %s", string(vOut))
	}
}
