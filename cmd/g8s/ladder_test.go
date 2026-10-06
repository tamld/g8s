package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/ladder"
	"github.com/tamld/g8s/internal/telemetry"
	_ "modernc.org/sqlite"
)

func setupLadderTestDB(t *testing.T) (*controlplane.Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "controlplane.db")
	store, err := controlplane.NewControlPlane(cpPath, nil)
	if err != nil {
		t.Fatalf("create controlplane store: %v", err)
	}

	telemPath := filepath.Join(dir, "telemetry.db")
	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = telemPath
	eng, err := telemetry.NewTelemetryEngine(cfg)
	if err != nil {
		t.Fatalf("create telemetry engine: %v", err)
	}
	defer eng.Close()

	return store, cpPath, telemPath
}

func updateTaskState(t *testing.T, dbPath, taskID, state, resultJSON, lastErr string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("UPDATE tasks SET state = ?, result_json = ?, last_error = ? WHERE task_id = ?",
		state, resultJSON, lastErr, taskID)
	if err != nil {
		t.Fatalf("update task state: %v", err)
	}
}

func TestLadderCLIStatusAndAdvance(t *testing.T) {
	store, cpPath, telemPath := setupLadderTestDB(t)
	defer store.Close()
	ctx := context.Background()

	// 1. Submit a root task that failed checks
	lastErr := "tests failed: TestFoo expected 1 got 2"
	reqBytes, _ := json.Marshal(map[string]any{
		"prompt":           "fix issue with docs test",
		"role":             "scout",
		"permission":       "read_only",
		"model":            "gemini-3.8-flash-high",
		"effort":           "low",
		"effort_requested": "low",
		"class":            "docs",
	})

	rootReq := controlplane.SubmitTaskRequest{
		IdempotencyKey: "root-1",
		Payload:        reqBytes,
		Role:           "scout",
		Permission:     "read_only",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{"."},
	}
	rootTask, err := store.SubmitTask(ctx, rootReq)
	if err != nil {
		t.Fatalf("submit root task: %v", err)
	}

	// Update root task as failed with effort-shaped failure
	updateTaskState(t, cpPath, rootTask.TaskID, controlplane.StateFailed, `{"ok":false}`, lastErr)

	// 2. Test ladder status on root task (Rung 0 pending)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, env, err := executeLadder(ctx, []string{
		"status",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("ladder status failed (code %d): %v, stderr: %s", code, err, stderr.String())
	}
	if env == nil || env.Error != nil {
		t.Fatalf("ladder status envelope error: %+v", env)
	}

	dataMap, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("ladder status data is not map: %T", env.Data)
	}
	if val, ok := dataMap["current_rung"].(int); !ok || val != 0 {
		t.Errorf("current_rung = %v, want 0", dataMap["current_rung"])
	}

	// 3. Test ladder advance (Rung 0: diagnosis dispatch)
	stdout.Reset()
	stderr.Reset()
	code, env, err = executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("ladder advance Rung 0 failed (code %d): %v, stderr: %s", code, err, stderr.String())
	}

	advMap, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("advance data is not map: %T", env.Data)
	}
	if fmt.Sprint(advMap["action"]) != "diagnosis" {
		t.Errorf("action = %v, want diagnosis", advMap["action"])
	}
	if advMap["permission"] != "read_only" {
		t.Errorf("permission = %v, want read_only", advMap["permission"])
	}
	if advMap["effort"] != "low" {
		t.Errorf("effort = %v, want low", advMap["effort"])
	}

	rung0TaskID, ok := advMap["new_task_id"].(string)
	if !ok {
		t.Fatalf("new_task_id is not string: %T", advMap["new_task_id"])
	}

	// 4. Fail Rung 0 task with effort-shaped failure
	updateTaskState(t, cpPath, rung0TaskID, controlplane.StateFailed, `{"ok":false}`, lastErr)

	// 5. Test ladder advance (Rung 1: effort escalated to medium)
	stdout.Reset()
	stderr.Reset()
	code, env, err = executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("ladder advance Rung 1 failed (code %d): %v, stderr: %s", code, err, stderr.String())
	}

	advMap1, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("advance data is not map: %T", env.Data)
	}
	if fmt.Sprint(advMap1["action"]) != "escalate_effort" {
		t.Errorf("Rung 1 action = %v, want escalate_effort", advMap1["action"])
	}
	if advMap1["effort"] != "medium" {
		t.Errorf("Rung 1 effort = %v, want medium", advMap1["effort"])
	}

	rung1TaskID, ok := advMap1["new_task_id"].(string)
	if !ok {
		t.Fatalf("new_task_id is not string: %T", advMap1["new_task_id"])
	}

	// 6. Test Early Termination on Succeeded Task
	updateTaskState(t, cpPath, rung1TaskID, controlplane.StateSucceeded, `{"ok":true,"status":"SUCCESS"}`, "")

	stdout.Reset()
	stderr.Reset()
	code, _, _ = executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("ladder advance on succeeded task returned non-zero code %d", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("already succeeded")) {
		t.Errorf("expected 'already succeeded' message, got: %s", stdout.String())
	}
}

func TestLadderRefusalNonEffortShape(t *testing.T) {
	store, cpPath, telemPath := setupLadderTestDB(t)
	defer store.Close()
	ctx := context.Background()

	// Task that failed with a brief-shaped contract violation
	briefErr := "contract violation: tool 'unauthorized_cmd' forbidden"
	reqBytes, _ := json.Marshal(map[string]any{
		"prompt":     "execute operation",
		"role":       "scout",
		"permission": "read_only",
		"class":      "docs",
	})
	rootTask, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "brief-fail-1",
		Payload:        reqBytes,
		Role:           "scout",
		Permission:     "read_only",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	updateTaskState(t, cpPath, rootTask.TaskID, controlplane.StateFailed, `{"ok":false}`, briefErr)

	outFile := filepath.Join(t.TempDir(), "hitl.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, env, _ := executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--out", outFile,
		"--json",
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("expected non-zero exit code for non-effort shape refusal, got 0")
	}
	if env == nil || env.Error == nil {
		t.Fatalf("expected error envelope for non-effort shape, got success: %+v", env)
	}

	// Verify evidence packet written to outFile
	fileData, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read out file: %v", err)
	}
	var packet ladder.EvidencePacket
	if err := json.Unmarshal(fileData, &packet); err != nil {
		t.Fatalf("failed to parse evidence packet from file: %v", err)
	}
	if packet.RootTaskID != rootTask.TaskID {
		t.Errorf("packet RootTaskID = %s, want %s", packet.RootTaskID, rootTask.TaskID)
	}
	if !bytes.Contains(fileData, []byte("brief-shaped")) {
		t.Errorf("packet missing 'brief-shaped' verdict: %s", string(fileData))
	}
}

func TestLadderGaugesCLI(t *testing.T) {
	_, cpPath, telemPath := setupLadderTestDB(t)
	ctx := context.Background()

	// Ingest sample telemetry
	cfg := telemetry.DefaultTelemetryConfig()
	cfg.DBPath = telemPath
	eng, err := telemetry.NewTelemetryEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	exit0 := 0
	_ = eng.IngestEvent(ctx, telemetry.TraceEvent{
		ID:            "ev-cli-1",
		TaskID:        "t-1",
		EventType:     telemetry.TraceEventTaskCompleted,
		Class:         "docs",
		EffortApplied: "low",
		ExitCode:      &exit0,
		Timestamp:     time.Now(),
	})
	_ = eng.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, env, err := executeLadder(ctx, []string{
		"gauges",
		"docs",
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("ladder gauges failed (code %d): %v, stderr: %s", code, err, stderr.String())
	}
	if env == nil || env.Error != nil {
		t.Fatalf("ladder gauges envelope error: %+v", env)
	}

	dataMap, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not map: %T", env.Data)
	}
	if dataMap["filter_class"] != "docs" {
		t.Errorf("filter_class = %v, want docs", dataMap["filter_class"])
	}
}

func TestLadderCeilingAndTokenBudgetRefusals(t *testing.T) {
	store, cpPath, telemPath := setupLadderTestDB(t)
	defer store.Close()
	ctx := context.Background()

	// 1. Submit root task
	reqBytes, _ := json.Marshal(map[string]any{
		"prompt":     "task for ceiling check",
		"role":       "scout",
		"permission": "read_only",
		"model":      "gemini-3.8-flash-high",
		"effort":     "low",
		"class":      "docs",
	})
	rootTask, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "ceiling-root",
		Payload:        reqBytes,
		Role:           "scout",
		Permission:     "read_only",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	updateTaskState(t, cpPath, rootTask.TaskID, controlplane.StateFailed, `{"ok":false}`, "checks failed")

	// Create 6 child rungs descending from root
	for i := 0; i < 6; i++ {
		chReq, _ := json.Marshal(map[string]any{
			"prompt":      fmt.Sprintf("rung %d", i),
			"role":        "scout",
			"permission":  "read_only",
			"ladder_rung": i,
		})
		chTask, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
			IdempotencyKey: fmt.Sprintf("ceiling-r%d", i),
			ParentTaskID:   &rootTask.TaskID,
			Payload:        chReq,
			Role:           "scout",
			Permission:     "read_only",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{"."},
		})
		if err != nil {
			t.Fatalf("submit child %d: %v", i, err)
		}
		updateTaskState(t, cpPath, chTask.TaskID, controlplane.StateFailed, `{"ok":false}`, "checks failed")
	}

	// Now try to advance: ceiling of 6 rungs must refuse!
	var stdout, stderr bytes.Buffer
	code, env, _ := executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("expected non-zero exit code when ceiling reached, got 0")
	}
	if env == nil || env.Error == nil {
		t.Fatalf("expected error envelope when ceiling reached, got: %+v", env)
	}
	if !strings.Contains(env.Error.Message, "ceiling") {
		t.Errorf("expected ceiling message in error, got: %s", env.Error.Message)
	}

	// Verify status displays ceiling reached
	stdout.Reset()
	stderr.Reset()
	code, _, err = executeLadder(ctx, []string{
		"status",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Rung Position: 6 / 6") {
		t.Errorf("status output missing Rung Position: 6 / 6: %s", stdout.String())
	}
}

func TestLadderHumanReadableTextOutput(t *testing.T) {
	store, cpPath, telemPath := setupLadderTestDB(t)
	defer store.Close()
	ctx := context.Background()

	reqBytes, _ := json.Marshal(map[string]any{
		"prompt":     "test prompt",
		"role":       "scout",
		"permission": "read_only",
		"model":      "gemini-3.8-flash-high",
		"effort":     "low",
		"class":      "docs",
	})
	rootTask, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "hr-root",
		Payload:        reqBytes,
		Role:           "scout",
		Permission:     "read_only",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	updateTaskState(t, cpPath, rootTask.TaskID, controlplane.StateFailed, `{"ok":false}`, "syntax check failed")

	// 1. Text status
	var stdout, stderr bytes.Buffer
	code, _, err := executeLadder(ctx, []string{
		"status",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("status error: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Ladder Status for Task") {
		t.Errorf("stdout missing header: %s", stdout.String())
	}

	// 2. Text advance
	stdout.Reset()
	stderr.Reset()
	code, _, err = executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("advance error: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Advanced ladder for root") {
		t.Errorf("stdout missing advance message: %s", stdout.String())
	}

	// 3. Text gauges
	stdout.Reset()
	stderr.Reset()
	code, _, err = executeLadder(ctx, []string{
		"gauges",
		"--db", cpPath,
		"--telemetry-db", telemPath,
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("gauges error: %v, stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Quality Ladder Gauges") {
		t.Errorf("stdout missing gauges header: %s", stdout.String())
	}
}
