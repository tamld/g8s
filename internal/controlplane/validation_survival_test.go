package controlplane

// #383 review finding: FinishAttempt previously reset result_validation and
// error_call_history to '{}'/'[]' on every finish, destroying the supervisor-
// written validation. The columns must survive both retryable and terminal
// finishes.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFinishAttemptPreservesValidationAndErrorCalls(t *testing.T) {
	store, dbPath := newTestStore(t)
	ctx := context.Background()

	task, err := store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "survive-383",
		Payload:        []byte(`{"prompt":"p"}`),
		Model:          "m",
		Role:           "collector",
		Permission:     "read_only",
		Timeout:        "30s",
		MaxAttempts:    1,
		AddDirs:        []string{"."},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	claimed, err := store.ClaimTask(ctx, "w-383", 60)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v (%v)", claimed, err)
	}
	if !store.StartTask(task.TaskID, "w-383", deref(claimed.LeaseToken)) {
		t.Fatal("start task failed")
	}
	if err := store.ValidateResult(ctx, task.TaskID, ResultValidation{
		Valid:        false,
		SchemaErrors: []string{`missing required boolean field "ok"`},
		ValidatedBy:  "w-383",
	}); err != nil {
		t.Fatalf("validate result: %v", err)
	}
	if err := store.AddErrorCall(ctx, task.TaskID, ErrorCallRecord{
		WorkerID:  "w-383",
		Attempt:   1,
		ErrorType: "schema_error",
		ErrorMsg:  "missing ok",
	}); err != nil {
		t.Fatalf("add error call: %v", err)
	}
	finished, err := store.FinishAttempt(task.TaskID, "w-383", deref(claimed.LeaseToken), FinishAttemptParams{
		Result:    []byte(`{"ok":false,"status":"failed"}`),
		Success:   false,
		Retryable: false,
		Err:       "validation failed",
	})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if finished.ResultValidation == nil || finished.ResultValidation.Valid {
		t.Fatalf("result_validation must survive FinishAttempt, got %+v", finished.ResultValidation)
	}
	if len(finished.ResultValidation.SchemaErrors) == 0 {
		t.Fatalf("schema errors must survive FinishAttempt, got %+v", finished.ResultValidation.SchemaErrors)
	}
	if len(finished.ErrorCallHistory) == 0 {
		t.Fatalf("error_call_history must survive FinishAttempt, got %+v", finished.ErrorCallHistory)
	}

	// Raw-row cross-check: the persisted columns carry the full payloads.
	raw := openRawDB(t, dbPath)
	defer raw.Close()
	var rawValidation string
	if err := raw.QueryRow(`SELECT result_validation FROM tasks WHERE task_id = ?`, task.TaskID).Scan(&rawValidation); err != nil {
		t.Fatalf("read raw validation: %v", err)
	}
	var decoded ResultValidation
	if err := json.Unmarshal([]byte(rawValidation), &decoded); err != nil {
		t.Fatalf("decoded persisted validation: %v", err)
	}
	if decoded.Valid || len(decoded.SchemaErrors) == 0 {
		t.Fatalf("persisted validation lost its schema errors: %s", rawValidation)
	}
}
