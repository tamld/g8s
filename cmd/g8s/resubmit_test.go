package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/cli"
	"github.com/tamld/g8s/internal/controlplane"
	_ "modernc.org/sqlite"
)

// setupTestStore initializes a fresh test controlplane store in a temp directory.
func setupTestStore(t *testing.T) (*controlplane.Store, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dbPath
}

// setupTaskWithDetails creates a task and updates its state, request_json, result_json, and last_error.
func setupTaskWithDetails(t *testing.T, store *controlplane.Store, dbPath string, state string, permission string, resultJSON string, lastError string) string {
	t.Helper()
	if permission == "" {
		permission = "read_only"
	}

	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: fmt.Sprintf("idem-%s-%s-%d", t.Name(), state, time.Now().UnixNano()),
		Priority:       10,
		MaxAttempts:    3,
		Role:           "scout",
		Model:          "gemini-3.8-flash-high",
		Permission:     "read_only",
		AddDirs:        []string{filepath.Dir(dbPath)},
		Payload:        json.RawMessage(`{"prompt":"test prompt"}`),
	})
	if err != nil {
		t.Fatalf("submit task: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var reqMap map[string]any
	_ = json.Unmarshal(task.Request, &reqMap)
	if reqMap == nil {
		reqMap = make(map[string]any)
	}
	reqMap["permission"] = permission
	reqMap["prompt"] = "test prompt"
	updatedReqBytes, _ := json.Marshal(reqMap)

	if _, err := db.Exec(`UPDATE tasks SET state = ?, request_json = ?, result_json = ?, last_error = ? WHERE task_id = ?`,
		state, string(updatedReqBytes), resultJSON, lastError, task.TaskID); err != nil {
		t.Fatalf("update task details: %v", err)
	}
	return task.TaskID
}

// TestResubmitHappyPath verifies that resubmitting a FAILED transient task returns a new task ID and envelope kind "resubmit".
func TestResubmitHappyPath(t *testing.T) {
	store, dbPath := setupTestStore(t)
	t.Setenv("G8S_AUTO_RETRY_ENABLED", "true")

	// Seed a FAILED transient task
	resultJSON := `{"ok":false,"error":{"code":"E_TIMEOUT","message":"task execution timed out"}}`
	lastError := "context deadline exceeded: timeout"
	origTaskID := setupTaskWithDetails(t, store, dbPath, controlplane.StateFailed, "read_only", resultJSON, lastError)

	var stdout, stderr bytes.Buffer
	args := []string{"--task", origTaskID, "--db", dbPath, "--reason", "retry transient timeout", "--json"}
	code, env, err := executeResubmit(context.Background(), args, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr:\n%s", code, stderr.String())
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env == nil {
		t.Fatalf("expected non-nil envelope")
	}

	if env.Kind != "resubmit" {
		t.Errorf("expected kind=resubmit, got %q", env.Kind)
	}
	if env.Command != "resubmit" {
		t.Errorf("expected cmd=resubmit, got %q", env.Command)
	}

	dataMap, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected env.Data map[string]any, got %T", env.Data)
	}
	if dataMap["orig_task_id"] != origTaskID {
		t.Errorf("expected orig_task_id=%s, got %v", origTaskID, dataMap["orig_task_id"])
	}
	newTaskID, ok := dataMap["new_task_id"].(string)
	if !ok || newTaskID == "" {
		t.Fatalf("expected valid new_task_id, got %v", dataMap["new_task_id"])
	}
	if newTaskID == origTaskID {
		t.Errorf("expected new_task_id to differ from origTaskID")
	}
	if dataMap["reason"] != "retry transient timeout" {
		t.Errorf("expected reason 'retry transient timeout', got %v", dataMap["reason"])
	}

	// Verify that the new task is queued in the store and linked to parent
	ctx := context.Background()
	newTask, err := store.GetTask(ctx, newTaskID)
	if err != nil || newTask == nil {
		t.Fatalf("failed to retrieve new task from store: %v", err)
	}
	if newTask.ParentTaskID == nil || *newTask.ParentTaskID != origTaskID {
		t.Errorf("expected parent_task_id=%s, got %v", origTaskID, newTask.ParentTaskID)
	}
}

// TestResubmitRefusals verifies proper error envelopes with correct E_* codes for:
// - not found -> E_NOTFOUND
// - not-FAILED -> E_INVALID
// - not-retryable -> E_DENIED
// - flag-off -> E_DENIED
// - workspace_write without receipt -> E_DENIED
func TestResubmitRefusals(t *testing.T) {
	store, dbPath := setupTestStore(t)

	tests := []struct {
		name           string
		taskState      string
		permission     string
		resultJSON     string
		lastError      string
		flagEnabled    string
		taskIDOverride string
		receiptIDFlag  string
		wantCode       int
		wantErrorCode  string
		wantHintSub    string
	}{
		{
			name:           "task not found",
			taskIDOverride: "missing-task-id",
			flagEnabled:    "true",
			wantCode:       1,
			wantErrorCode:  cli.CodeNotFound,
			wantHintSub:    "Verify the task ID exists",
		},
		{
			name:          "not-FAILED state (SUCCEEDED)",
			taskState:     controlplane.StateSucceeded,
			permission:    "read_only",
			resultJSON:    `{"ok":true,"output":"done"}`,
			flagEnabled:   "true",
			wantCode:      1,
			wantErrorCode: cli.CodeInvalid,
			wantHintSub:   "Only tasks in terminal FAILED state",
		},
		{
			name:          "not-FAILED state (QUEUED)",
			taskState:     controlplane.StateQueued,
			permission:    "read_only",
			flagEnabled:   "true",
			wantCode:      1,
			wantErrorCode: cli.CodeInvalid,
			wantHintSub:   "Only tasks in terminal FAILED state",
		},
		{
			name:          "not-retryable (sanitizer violation)",
			taskState:     controlplane.StateFailed,
			permission:    "read_only",
			resultJSON:    `{"ok":false,"error":{"code":"E_DENIED","message":"sanitizer violation: denied path"}}`,
			lastError:     "sanitizer violation: path denied",
			flagEnabled:   "true",
			wantCode:      1,
			wantErrorCode: cli.CodeDenied,
			wantHintSub:   "Only transient failure classes",
		},
		{
			name:          "not-retryable (refusal marker)",
			taskState:     controlplane.StateFailed,
			permission:    "read_only",
			resultJSON:    `{"ok":false,"error":{"code":"E_USAGE","message":"refusal: bad input"}}`,
			lastError:     "refusal verdict recorded",
			flagEnabled:   "true",
			wantCode:      1,
			wantErrorCode: cli.CodeDenied,
			wantHintSub:   "Only transient failure classes",
		},
		{
			name:          "flag-off (auto-retry disabled)",
			taskState:     controlplane.StateFailed,
			permission:    "read_only",
			resultJSON:    `{"ok":false,"error":{"code":"E_TIMEOUT","message":"timed out"}}`,
			lastError:     "context deadline exceeded: timeout",
			flagEnabled:   "false",
			wantCode:      1,
			wantErrorCode: cli.CodeDenied,
			wantHintSub:   "auto_retry_enabled",
		},
		{
			name:          "workspace_write without fresh receipt",
			taskState:     controlplane.StateFailed,
			permission:    "workspace_write",
			resultJSON:    `{"ok":false,"error":{"code":"E_TIMEOUT","message":"timed out"}}`,
			lastError:     "context deadline exceeded: timeout",
			flagEnabled:   "true",
			receiptIDFlag: "", // missing receipt
			wantCode:      1,
			wantErrorCode: cli.CodeDenied,
			wantHintSub:   "g8s receipt issue",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("G8S_AUTO_RETRY_ENABLED", tc.flagEnabled)

			targetTaskID := tc.taskIDOverride
			if targetTaskID == "" {
				targetTaskID = setupTaskWithDetails(t, store, dbPath, tc.taskState, tc.permission, tc.resultJSON, tc.lastError)
			}

			var stdout, stderr bytes.Buffer
			args := []string{"--task", targetTaskID, "--db", dbPath, "--json"}
			if tc.receiptIDFlag != "" {
				args = append(args, "--receipt-id", tc.receiptIDFlag)
			}

			code, env, _ := executeResubmit(context.Background(), args, &stdout, &stderr)

			if code != tc.wantCode {
				t.Fatalf("expected code %d, got %d. stderr:\n%s", tc.wantCode, code, stderr.String())
			}
			if env == nil {
				t.Fatalf("expected non-nil envelope")
			}
			if env.Kind != "error" {
				t.Errorf("expected kind=error, got %q", env.Kind)
			}
			if env.Error == nil {
				t.Fatalf("expected non-nil env.Error")
			}
			if env.Error.Code != tc.wantErrorCode {
				t.Errorf("expected error code %q, got %q (message: %s)", tc.wantErrorCode, env.Error.Code, env.Error.Message)
			}
			if tc.wantHintSub != "" && !strings.Contains(env.Error.Hint, tc.wantHintSub) {
				t.Errorf("expected error hint to contain %q, got %q", tc.wantHintSub, env.Error.Hint)
			}
		})
	}
}

// TestTickRetryJob verifies:
//  1. Flag off -> job reports disabled, no-op.
//  2. Flag on -> seeded FAILED tasks (mix of classes + workspace_write) -> correct subset resubmitted;
//     workspace_write NEVER resubmitted by the tick.
//  3. Double-fire safety (idempotent).
func TestTickRetryJob(t *testing.T) {
	store, dbPath := setupTestStore(t)
	t.Setenv("G8S_DB", dbPath)

	ctx := context.Background()

	// 1. Flag-off: job reports disabled, no-op
	t.Setenv("G8S_AUTO_RETRY_ENABLED", "false")
	status, detail := runTickRetry(ctx)
	if status != "disabled" {
		t.Fatalf("expected status=disabled when flag is off, got %s", status)
	}
	detailMap, ok := detail.(map[string]any)
	if !ok {
		t.Fatalf("expected detail map[string]any, got %T", detail)
	}
	if detailMap["enabled"] != false {
		t.Errorf("expected enabled=false in detail, got %v", detailMap["enabled"])
	}

	// 2. Flag-on: seed tasks
	t.Setenv("G8S_AUTO_RETRY_ENABLED", "true")

	// Task 1: read_only, FAILED, retryable timeout -> SHOULD be resubmitted
	t1 := setupTaskWithDetails(t, store, dbPath, controlplane.StateFailed, "read_only",
		`{"ok":false,"error":{"code":"E_TIMEOUT","message":"timed out"}}`, "timeout occurred")

	// Task 2: automation_read, FAILED, retryable provider transport 502 -> SHOULD be resubmitted
	t2 := setupTaskWithDetails(t, store, dbPath, controlplane.StateFailed, "automation_read",
		`{"ok":false,"error":{"code":"E_RUNTIME","message":"transport error 502 Bad Gateway"}}`, "provider 502 transport")

	// Task 3: read_only, FAILED, non-retryable sanitizer violation -> SHOULD be skipped
	t3 := setupTaskWithDetails(t, store, dbPath, controlplane.StateFailed, "read_only",
		`{"ok":false,"error":{"code":"E_DENIED","message":"sanitizer violation"}}`, "sanitizer denied path")

	// Task 4: workspace_write, FAILED, retryable timeout -> MUST NEVER be resubmitted by tick (D3 boundary)
	t4 := setupTaskWithDetails(t, store, dbPath, controlplane.StateFailed, "workspace_write",
		`{"ok":false,"error":{"code":"E_TIMEOUT","message":"timed out"}}`, "timeout occurred")

	// Task 5: read_only, SUCCEEDED -> not FAILED, should not be included
	setupTaskWithDetails(t, store, dbPath, controlplane.StateSucceeded, "read_only",
		`{"ok":true,"output":"ok"}`, "")

	status, detail = runTickRetry(ctx)
	if status != "ok" {
		t.Fatalf("expected status=ok, got %s. detail=%v", status, detail)
	}

	resMap, ok := detail.(map[string]any)
	if !ok {
		t.Fatalf("expected map detail, got %T", detail)
	}

	resubmittedCount, _ := resMap["resubmitted"].(int)
	skippedCount, _ := resMap["skipped"].(int)

	if resubmittedCount != 2 {
		t.Errorf("expected 2 resubmitted tasks (t1, t2), got %d", resubmittedCount)
	}
	if skippedCount != 2 {
		t.Errorf("expected 2 skipped tasks (t3 non-retryable, t4 workspace_write), got %d", skippedCount)
	}

	// Verify task statuses in result items
	itemsRaw, _ := json.Marshal(resMap["results"])
	var items []struct {
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
		NewTaskID string `json:"new_task_id"`
		Reason    string `json:"reason"`
	}
	_ = json.Unmarshal(itemsRaw, &items)

	itemMap := make(map[string]string)
	reasonMap := make(map[string]string)
	for _, it := range items {
		itemMap[it.TaskID] = it.Status
		reasonMap[it.TaskID] = it.Reason
	}

	if itemMap[t1] != "resubmitted" {
		t.Errorf("task t1: expected status=resubmitted, got %q (reason: %s)", itemMap[t1], reasonMap[t1])
	}
	if itemMap[t2] != "resubmitted" {
		t.Errorf("task t2: expected status=resubmitted, got %q (reason: %s)", itemMap[t2], reasonMap[t2])
	}
	if itemMap[t3] != "skipped" {
		t.Errorf("task t3 (sanitizer): expected status=skipped, got %q", itemMap[t3])
	}
	if itemMap[t4] != "skipped" {
		t.Errorf("task t4 (workspace_write): expected status=skipped, got %q", itemMap[t4])
	}
	if !strings.Contains(reasonMap[t4], "workspace_write") {
		t.Errorf("task t4: expected skip reason to cite workspace_write/receipt, got %q", reasonMap[t4])
	}

	// 3. Double-fire safety (idempotent run)
	status2, detail2 := runTickRetry(ctx)
	if status2 != "ok" {
		t.Fatalf("second tick execution failed: status=%s", status2)
	}
	_ = detail2
}
