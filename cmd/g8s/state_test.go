package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/state"
)

func TestCLIStateShowAndReplay(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("create test control plane: %v", err)
	}

	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: "idem-fsm-1",
		Priority:       10,
		MaxAttempts:    3,
		Payload:        json.RawMessage(`{"prompt":"hello"}`),
		Role:           "scout",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{tempDir},
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}
	taskID := task.TaskID

	// Log some events
	ctx := context.Background()
	t0 := time.Now().Add(-2 * time.Minute)
	t1 := time.Now().Add(-1 * time.Minute)
	if err := store.LogStateEvent(ctx, taskID, state.SubjectTask, state.TaskStateQueued, state.TaskStateLeased, state.TaskEventClaim, "worker-1", "claim"); err != nil {
		t.Fatalf("log event 1: %v", err)
	}
	_ = t0
	_ = t1
	if err := store.LogStateEvent(ctx, taskID, state.SubjectTask, state.TaskStateLeased, state.TaskStateRunning, state.TaskEventStart, "worker-1", "start"); err != nil {
		t.Fatalf("log event 2: %v", err)
	}
	store.Close()

	// Test state show with JSON
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runStateShow([]string{"--json", taskID})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	out := buf.String()

	var env cli.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal state show output: %v, raw: %s", err, out)
	}
	if env.Kind != "state" {
		t.Errorf("env.Kind = %s, want state", env.Kind)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected env.Data map, got %T", env.Data)
	}
	if data["id"] != taskID {
		t.Errorf("data.id = %v, want %s", data["id"], taskID)
	}
	events, ok := data["events"].([]any)
	if !ok || len(events) != 2 {
		t.Fatalf("expected 2 events, got %v", data["events"])
	}

	// Test state replay
	rReplay, wReplay, _ := os.Pipe()
	os.Stdout = wReplay

	runStateReplay([]string{taskID})

	wReplay.Close()
	os.Stdout = oldStdout

	var bufReplay bytes.Buffer
	_, _ = bufReplay.ReadFrom(rReplay)
	replayOut := strings.TrimSpace(bufReplay.String())
	lines := strings.Split(replayOut, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 jsonl lines from replay, got %d (output: %s)", len(lines), replayOut)
	}

	var rec1 state.EventRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec1); err != nil {
		t.Fatalf("unmarshal replay line 1: %v", err)
	}
	if rec1.Event != state.TaskEventClaim {
		t.Errorf("rec1.Event = %s, want claim", rec1.Event)
	}
}
