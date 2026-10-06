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

	// 5a. Test ladder advance without prompt on Rung 1 (must fail with E_USAGE)
	stdout.Reset()
	stderr.Reset()
	code, env, _ = executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2 for Rung 1 advance without prompt, got %d", code)
	}
	if env == nil || env.Error == nil || env.Error.Code != "E_USAGE" {
		t.Fatalf("expected E_USAGE envelope, got: %+v", env)
	}
	if !strings.Contains(env.Error.Message, "--prompt") {
		t.Errorf("error message missing --prompt flag mention: %s", env.Error.Message)
	}

	// 5b. Test ladder advance with --prompt on Rung 1 (succeeds)
	stdout.Reset()
	stderr.Reset()
	code, env, err = executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--prompt", "fix issue with docs test redo",
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
	store, cpPath, telemPath := setupLadderTestDB(t)
	// Windows: the TempDir cleanup unlinks open sqlite files with
	// "file in use" — every handle must be closed before the test ends.
	defer store.Close()
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

func TestGenerateDiagnosisPrompt_Table(t *testing.T) {
	tests := []struct {
		name          string
		taskID        string
		class         string
		lastError     string
		resultSummary string
		wantSub       []string
	}{
		{
			name:          "all fields present",
			taskID:        "task-1234",
			class:         "core",
			lastError:     "test failed in core/fsm: expected 1 got 2",
			resultSummary: `{"status":"FAILED","exit_code":1}`,
			wantSub: []string{
				"Quality ladder diagnosis dispatch for failed task task-1234",
				"- Task ID: task-1234",
				"- Class: core",
				"- Last Error: test failed in core/fsm: expected 1 got 2",
				`- Result Summary: {"status":"FAILED","exit_code":1}`,
				"read-only mode with low effort",
				"Classify the failure shape",
			},
		},
		{
			name:          "empty error and summary fallback to none",
			taskID:        "task-5678",
			class:         "docs",
			lastError:     "",
			resultSummary: "",
			wantSub: []string{
				"- Task ID: task-5678",
				"- Class: docs",
				"- Last Error: none",
				"- Result Summary: none",
			},
		},
		{
			name:          "empty task and class fallback to unknown and unregistered",
			taskID:        "",
			class:         "",
			lastError:     "crash",
			resultSummary: "failed",
			wantSub: []string{
				"- Task ID: unknown",
				"- Class: unregistered",
				"- Last Error: crash",
				"- Result Summary: failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1 := GenerateDiagnosisPrompt(tt.taskID, tt.class, tt.lastError, tt.resultSummary)
			got2 := GenerateDiagnosisPrompt(tt.taskID, tt.class, tt.lastError, tt.resultSummary)
			if got1 != got2 {
				t.Fatalf("GenerateDiagnosisPrompt is not deterministic: %q != %q", got1, got2)
			}
			for _, sub := range tt.wantSub {
				if !strings.Contains(got1, sub) {
					t.Errorf("prompt missing substring %q; got:\n%s", sub, got1)
				}
			}
		})
	}
}

func TestLadderAdvance_Rung0DiagnosisPromptAndTimeout(t *testing.T) {
	store, cpPath, telemPath := setupLadderTestDB(t)
	defer store.Close()
	ctx := context.Background()

	// Create a root task where prompt was redacted upon failure (ground truth scenario)
	customTimeout := "45s"
	lastErr := "class checks failed: tests exited with code 1"
	reqBytes, _ := json.Marshal(map[string]any{
		"prompt":          "initial prompt to be redacted",
		"role":            "scout",
		"permission":      "read_only",
		"model":           "gemini-3.8-flash-high",
		"effort":          "low",
		"class":           "docs",
		"timeout":         customTimeout,
		"prompt_redacted": true,
		"prompt_hash":     "abc123hash",
	})

	rootReq := controlplane.SubmitTaskRequest{
		IdempotencyKey: "root-redacted-test",
		Payload:        reqBytes,
		Role:           "scout",
		Permission:     "read_only",
		Model:          "gemini-3.8-flash-high",
		Timeout:        customTimeout,
		AddDirs:        []string{"."},
	}
	rootTask, err := store.SubmitTask(ctx, rootReq)
	if err != nil {
		t.Fatalf("submit root task: %v", err)
	}

	// Now redact the stored request JSON in SQLite as happens on task completion
	db, err := sql.Open("sqlite", cpPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	redactedPayload, _ := json.Marshal(map[string]any{
		"role":            "scout",
		"permission":      "read_only",
		"model":           "gemini-3.8-flash-high",
		"effort":          "low",
		"class":           "docs",
		"timeout":         customTimeout,
		"prompt_redacted": true,
		"prompt_hash":     "abc123hash",
	})
	_, err = db.Exec("UPDATE tasks SET state = ?, request_json = ?, result_json = ?, last_error = ? WHERE task_id = ?",
		controlplane.StateFailed, string(redactedPayload), `{"ok":false,"summary":"checks exited 1"}`, lastErr, rootTask.TaskID)
	if err != nil {
		t.Fatalf("update task state: %v", err)
	}

	// Advance root task to Rung 0 (diagnosis dispatch)
	var stdout, stderr bytes.Buffer
	code, env, err := executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)

	if code != 0 || err != nil {
		t.Fatalf("advance to Rung 0 failed (code %d): %v, stderr: %s", code, err, stderr.String())
	}
	if env == nil || env.Error != nil {
		t.Fatalf("advance envelope error: %+v", env)
	}

	advMap, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("advance data is not map: %T", env.Data)
	}
	newID := fmt.Sprint(advMap["new_task_id"])

	// Verify the newly created Rung 0 task
	newTask, err := store.GetTask(ctx, newID)
	if err != nil || newTask == nil {
		t.Fatalf("failed to retrieve new task %s: %v", newID, err)
	}

	var newPayload map[string]any
	if err := json.Unmarshal(newTask.Request, &newPayload); err != nil {
		t.Fatalf("unmarshal new payload: %v", err)
	}

	// 1. Prompt must NOT be redacted and must contain diagnosis template with evidence embedded
	promptStr, ok := newPayload["prompt"].(string)
	if !ok || strings.TrimSpace(promptStr) == "" {
		t.Fatalf("new task prompt missing or empty: %v", newPayload["prompt"])
	}
	if !strings.Contains(promptStr, "Quality ladder diagnosis dispatch") {
		t.Errorf("prompt missing diagnosis header: %s", promptStr)
	}
	if !strings.Contains(promptStr, rootTask.TaskID) {
		t.Errorf("prompt missing task ID %s: %s", rootTask.TaskID, promptStr)
	}
	if !strings.Contains(promptStr, lastErr) {
		t.Errorf("prompt missing last error %q: %s", lastErr, promptStr)
	}
	if !strings.Contains(promptStr, "checks exited 1") {
		t.Errorf("prompt missing result summary: %s", promptStr)
	}

	// 2. Timeout must carry the original task's timeout ("45s"), NOT RequestHash!
	var newReqDoc map[string]any
	_ = json.Unmarshal(newTask.Request, &newReqDoc)
	if newPayload["timeout"] != customTimeout {
		t.Errorf("payload timeout = %v, want %s", newPayload["timeout"], customTimeout)
	}
	// Verify RequestHash was NOT used as timeout
	if newTask.RequestHash == customTimeout {
		t.Fatalf("RequestHash unexpectedly equal to timeout")
	}
}

func TestLadderAdvance_EscalationRedoRequiresPrompt(t *testing.T) {
	store, cpPath, telemPath := setupLadderTestDB(t)
	defer store.Close()
	ctx := context.Background()

	// Create root task
	reqBytes, _ := json.Marshal(map[string]any{
		"prompt":     "original prompt",
		"role":       "scout",
		"permission": "read_only",
		"model":      "gemini-3.8-flash-high",
		"effort":     "low",
		"class":      "docs",
		"timeout":    "20s",
	})
	rootTask, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: "root-prompt-req-test",
		Payload:        reqBytes,
		Role:           "scout",
		Permission:     "read_only",
		Model:          "gemini-3.8-flash-high",
		Timeout:        "20s",
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	updateTaskState(t, cpPath, rootTask.TaskID, controlplane.StateFailed, `{"ok":false}`, "test failure")

	// Advance to Rung 0 (diagnosis - doesn't need prompt)
	var stdout, stderr bytes.Buffer
	code, env, err := executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)
	if code != 0 || err != nil {
		t.Fatalf("advance to Rung 0 failed: code=%d err=%v stderr=%s", code, err, stderr.String())
	}
	advMap0, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("advance data is not map: %T", env.Data)
	}
	r0ID, ok := advMap0["new_task_id"].(string)
	if !ok {
		t.Fatalf("new_task_id is not string: %T", advMap0["new_task_id"])
	}
	updateTaskState(t, cpPath, r0ID, controlplane.StateFailed, `{"ok":false}`, "test failure")

	// Attempt to advance to Rung 1 WITHOUT --prompt or --prompt-file -> must fail with E_USAGE (code 2)
	stdout.Reset()
	stderr.Reset()
	code, env, _ = executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2 when advancing to Rung 1 without prompt, got %d", code)
	}
	if env == nil || env.Error == nil || env.Error.Code != "E_USAGE" {
		t.Fatalf("expected E_USAGE envelope, got: %+v", env)
	}
	if !strings.Contains(env.Error.Message, "--prompt") || !strings.Contains(env.Error.Message, "--prompt-file") {
		t.Errorf("error message does not name required flags: %s", env.Error.Message)
	}

	// Advance to Rung 1 with --prompt-file -> must succeed
	promptFile := filepath.Join(t.TempDir(), "redo_prompt.txt")
	promptContent := "escalated prompt for rung 1 re-do"
	if err := os.WriteFile(promptFile, []byte(promptContent), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code, env, err = executeLadder(ctx, []string{
		"advance",
		rootTask.TaskID,
		"--prompt-file", promptFile,
		"--db", cpPath,
		"--telemetry-db", telemPath,
		"--json",
	}, &stdout, &stderr)
	if code != 0 || err != nil {
		t.Fatalf("advance to Rung 1 with --prompt-file failed (code %d): %v, stderr: %s", code, err, stderr.String())
	}

	advMap1, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("advance data is not map: %T", env.Data)
	}
	r1ID, ok := advMap1["new_task_id"].(string)
	if !ok {
		t.Fatalf("new_task_id is not string: %T", advMap1["new_task_id"])
	}
	r1Task, err := store.GetTask(ctx, r1ID)
	if err != nil {
		t.Fatalf("get r1 task: %v", err)
	}
	var r1Payload map[string]any
	_ = json.Unmarshal(r1Task.Request, &r1Payload)
	if r1Payload["prompt"] != promptContent {
		t.Errorf("r1 prompt = %v, want %q", r1Payload["prompt"], promptContent)
	}
}
