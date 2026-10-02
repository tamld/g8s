package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/settings"
)

// Helper to set task into terminal FAILED state for testing.
func failTaskInDB(t *testing.T, db *sql.DB, taskID string, lastError string, completedAt float64) {
	t.Helper()
	_, err := db.Exec(`
		UPDATE tasks
		SET state = 'FAILED', last_error = ?, completed_at = ?, updated_at = ?,
		    lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL
		WHERE task_id = ?`,
		lastError, completedAt, completedAt, taskID)
	if err != nil {
		t.Fatalf("failTaskInDB for %s: %v", taskID, err)
	}
}

// 1. Classification table: each retryable class, each non-retryable class, unknown -> NOT_RETRYABLE.
func TestClassifyRetryableTable(t *testing.T) {
	tests := []struct {
		name       string
		resultJSON string
		lastError  string
		want       RetryClass
	}{
		// --- Retryable classes ---
		{
			name:       "timeout: E_TIMEOUT envelope code",
			resultJSON: `{"ok":false,"error":{"code":"E_TIMEOUT","message":"timed out executing step"}}`,
			lastError:  "",
			want:       RetryClassRetryable,
		},
		{
			name:       "timeout: context deadline exceeded in last_error",
			resultJSON: "",
			lastError:  "context deadline exceeded while awaiting child process",
			want:       RetryClassRetryable,
		},
		{
			name:       "timeout: lease expired in last_error",
			resultJSON: "",
			lastError:  "lease expired and retry budget exhausted",
			want:       RetryClassRetryable,
		},
		{
			name:       "interrupted: SIGINT in last_error",
			resultJSON: "",
			lastError:  "signal: interrupt (SIGINT)",
			want:       RetryClassRetryable,
		},
		{
			name:       "interrupted: SIGTERM in last_error",
			resultJSON: "",
			lastError:  "process interrupted by SIGTERM",
			want:       RetryClassRetryable,
		},
		{
			name:       "spawn-failure: executable not found",
			resultJSON: "",
			lastError:  "fork/exec /bin/agy: no such file or directory",
			want:       RetryClassRetryable,
		},
		{
			name:       "spawn-failure: failed to spawn worker process",
			resultJSON: `{"status":"failed","reason":"failed to spawn child process"}`,
			lastError:  "unable to spawn worker process",
			want:       RetryClassRetryable,
		},
		{
			name:       "provider-transport: malformed function call",
			resultJSON: "",
			lastError:  "provider transport error: malformed function call in API response",
			want:       RetryClassRetryable,
		},
		{
			name:       "provider-transport: 500 internal server error",
			resultJSON: `{"error":{"code":"E_TRANSPORT","message":"500 Internal Server Error"}}`,
			lastError:  "HTTP 500 internal server error",
			want:       RetryClassRetryable,
		},
		{
			name:       "provider-transport: 502 bad gateway",
			resultJSON: "",
			lastError:  "status 502 bad gateway from LLM upstream",
			want:       RetryClassRetryable,
		},
		{
			name:       "provider-transport: 503 service unavailable",
			resultJSON: "",
			lastError:  "503 Service Unavailable: upstream overloaded",
			want:       RetryClassRetryable,
		},
		{
			name:       "provider-transport: 504 gateway timeout",
			resultJSON: "",
			lastError:  "status 504 gateway timeout",
			want:       RetryClassRetryable,
		},
		{
			name:       "provider-transport: connection reset",
			resultJSON: "",
			lastError:  "connection reset by peer",
			want:       RetryClassRetryable,
		},
		{
			name:       "provider-transport: 5xx marker",
			resultJSON: "",
			lastError:  "upstream gateway returned 5xx status code",
			want:       RetryClassRetryable,
		},
		{
			name:       "provider-transport: E_IO transport error",
			resultJSON: `{"ok":false,"error":{"code":"E_IO","message":"network connection closed unexpectedly"}}`,
			lastError:  "",
			want:       RetryClassRetryable,
		},

		// --- Non-retryable classes ---
		{
			name:       "non-retryable: receipt violation in lastError",
			resultJSON: "",
			lastError:  "receipt violation: write outside receipt scope",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: receipt-bypass marker",
			resultJSON: "",
			lastError:  "receipt-bypass detected: target file unauthorized",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: receipt unknown or expired",
			resultJSON: "",
			lastError:  "receipt unknown or expired (rcpt-1234)",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: sanitizer violation",
			resultJSON: "",
			lastError:  "sanitizer violation: unescaped payload corruption",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: sanitizer-fidelity marker",
			resultJSON: "",
			lastError:  "sanitizer-fidelity error #434",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: provider refusal phrasing",
			resultJSON: "",
			lastError:  "this request was blocked by gemini's safety policies",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: provider content filter phrase",
			resultJSON: "",
			lastError:  "blocked by gemini's filters: content filter violation",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: provider refusal in JSON",
			resultJSON: `{"status":"blocked","reason":"worker response is a provider refusal, not a result"}`,
			lastError:  "",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: E_USAGE in envelope code",
			resultJSON: `{"ok":false,"error":{"code":"E_USAGE","message":"unknown flag --xyz"}}`,
			lastError:  "",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: E_USAGE in lastError",
			resultJSON: "",
			lastError:  "E_USAGE: invalid command syntax",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: E_INVALID envelope code",
			resultJSON: `{"ok":false,"error":{"code":"E_INVALID","message":"invalid prompt format"}}`,
			lastError:  "",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: E_DENIED envelope code",
			resultJSON: `{"ok":false,"error":{"code":"E_DENIED","message":"permission denied"}}`,
			lastError:  "",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: ok is true (success deliverable present)",
			resultJSON: `{"ok":true,"deliverable":{"mode":"worktree"}}`,
			lastError:  "timeout after write",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "non-retryable: status succeeded in JSON",
			resultJSON: `{"status":"succeeded","result":"done"}`,
			lastError:  "",
			want:       RetryClassNotRetryable,
		},

		// --- Unknown / empty (deny by default) ---
		{
			name:       "unknown: empty result and empty last_error",
			resultJSON: "",
			lastError:  "",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "unknown: whitespace only",
			resultJSON: "   ",
			lastError:  " \t\n ",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "unknown: generic unclassified error",
			resultJSON: `{"error":{"code":"E_UNKNOWN","message":"something completely unexpected happened"}}`,
			lastError:  "unhandled exception in worker runtime",
			want:       RetryClassNotRetryable,
		},
		{
			name:       "unknown: random string",
			resultJSON: "foo bar baz",
			lastError:  "corrupted memory segment 0x1234",
			want:       RetryClassNotRetryable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRetryable(tt.resultJSON, tt.lastError)
			if got != tt.want {
				t.Errorf("ClassifyRetryable(%q, %q) = %v, want %v",
					tt.resultJSON, tt.lastError, got, tt.want)
			}
		})
	}
}

// 2. ResubmitTask happy path: FAILED transient task -> new QUEUED task with derived key,
// parent linkage, retry metadata, no receipt.
func TestResubmitTask_HappyPath(t *testing.T) {
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()

	// Submit an initial task
	req := SubmitTaskRequest{
		IdempotencyKey: "happy-orig-task",
		Priority:       5,
		MaxAttempts:    3,
		Role:           "collector",
		Permission:     "read_only",
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"initial research query","actor":"operator","receipt_id":"rcpt-should-be-dropped"}`),
	}
	orig, err := store.SubmitTask(ctx, req)
	if err != nil {
		t.Fatalf("submit initial task: %v", err)
	}

	// Transition original to FAILED with transient error
	nowSec := float64(clock.Now().UnixNano()) / 1e9
	failTaskInDB(t, store.db, orig.TaskID, "execution timed out: deadline exceeded", nowSec)

	// Resubmit original
	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}
	newID, err := store.ResubmitTask(ctx, orig.TaskID, opts)
	if err != nil {
		t.Fatalf("ResubmitTask failed: %v", err)
	}
	if newID == "" {
		t.Fatalf("expected non-empty new task ID")
	}

	// Verify new task properties
	newTask, err := store.GetTask(ctx, newID)
	if err != nil {
		t.Fatalf("GetTask(newID): %v", err)
	}
	if newTask == nil {
		t.Fatalf("resubmitted task not found in store")
	}

	// 1. State must be QUEUED
	if newTask.State != StateQueued {
		t.Errorf("newTask.State = %s, want QUEUED", newTask.State)
	}
	// 2. Derived idempotency key <orig>#r1
	expectedKey := fmt.Sprintf("%s#r1", orig.TaskID)
	if newTask.IdempotencyKey != expectedKey {
		t.Errorf("newTask.IdempotencyKey = %s, want %s", newTask.IdempotencyKey, expectedKey)
	}
	// 3. Parent linkage
	if newTask.ParentTaskID == nil || *newTask.ParentTaskID != orig.TaskID {
		t.Errorf("newTask.ParentTaskID = %v, want %s", newTask.ParentTaskID, orig.TaskID)
	}

	// 4. Request metadata: retry_of, retry_attempt, no receipt_id
	var payload map[string]any
	if err := json.Unmarshal(newTask.Request, &payload); err != nil {
		t.Fatalf("unmarshal newTask.Request: %v", err)
	}
	if retryOf, _ := payload["retry_of"].(string); retryOf != orig.TaskID {
		t.Errorf("payload[retry_of] = %v, want %s", retryOf, orig.TaskID)
	}
	if retryAttempt, _ := payload["retry_attempt"].(float64); retryAttempt != 1 {
		t.Errorf("payload[retry_attempt] = %v, want 1", retryAttempt)
	}
	if receiptID, exists := payload["receipt_id"]; exists && receiptID != nil && receiptID != "" {
		t.Errorf("receipt_id was NOT dropped; payload[receipt_id] = %v", receiptID)
	}
	if perm, _ := payload["permission"].(string); perm != "read_only" {
		t.Errorf("permission preserved: got %v, want read_only", perm)
	}
}

// 3. Budget: 3rd resubmit of same original refused; 11th in an hour refused (inject clock).
func TestResubmitTask_Budget(t *testing.T) {
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()
	ResetHourlyRetryTracker()

	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	// Part A: 3rd resubmit of same original refused
	t.Run("max_per_task_refused_on_3rd", func(t *testing.T) {
		req := SubmitTaskRequest{
			IdempotencyKey: "budget-task-a",
			Priority:       1,
			MaxAttempts:    1,
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        json.RawMessage(`{"prompt":"budget test task"}`),
		}
		orig, err := store.SubmitTask(ctx, req)
		if err != nil {
			t.Fatalf("submit orig: %v", err)
		}

		nowSec := float64(clock.Now().UnixNano()) / 1e9
		failTaskInDB(t, store.db, orig.TaskID, "context deadline exceeded", nowSec)

		// 1st resubmit
		r1ID, err := store.ResubmitTask(ctx, orig.TaskID, opts)
		if err != nil {
			t.Fatalf("1st resubmit: %v", err)
		}
		// Fail 1st retry
		failTaskInDB(t, store.db, r1ID, "connection reset by peer", nowSec)

		// 2nd resubmit
		r2ID, err := store.ResubmitTask(ctx, orig.TaskID, opts)
		if err != nil {
			t.Fatalf("2nd resubmit: %v", err)
		}
		// Fail 2nd retry
		failTaskInDB(t, store.db, r2ID, "502 Bad Gateway", nowSec)

		// 3rd resubmit must be refused!
		_, err = store.ResubmitTask(ctx, orig.TaskID, opts)
		if err == nil {
			t.Fatalf("3rd resubmit succeeded, want budget exceeded error")
		}
		if !strings.Contains(err.Error(), "budget exceeded") {
			t.Errorf("error = %v, want containing 'budget exceeded'", err)
		}
	})

	// Part B: 11th in an hour refused (inject clock)
	t.Run("max_per_hour_refused_on_11th", func(t *testing.T) {
		ResetHourlyRetryTracker()

		// Resubmit 10 distinct tasks
		for i := 1; i <= 10; i++ {
			req := SubmitTaskRequest{
				IdempotencyKey: fmt.Sprintf("hourly-task-%d", i),
				Priority:       1,
				MaxAttempts:    1,
				Model:          "gemini-3.8-flash-high",
				AddDirs:        []string{testScopeDir},
				Payload:        json.RawMessage(`{"prompt":"hourly load task"}`),
			}
			task, err := store.SubmitTask(ctx, req)
			if err != nil {
				t.Fatalf("submit task %d: %v", i, err)
			}
			nowSec := float64(clock.Now().UnixNano()) / 1e9
			failTaskInDB(t, store.db, task.TaskID, "503 service unavailable", nowSec)

			_, err = store.ResubmitTask(ctx, task.TaskID, opts)
			if err != nil {
				t.Fatalf("resubmit task %d: %v", i, err)
			}
		}

		// 11th distinct task in the same hour
		req11 := SubmitTaskRequest{
			IdempotencyKey: "hourly-task-11",
			Priority:       1,
			MaxAttempts:    1,
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        json.RawMessage(`{"prompt":"hourly load task 11"}`),
		}
		task11, err := store.SubmitTask(ctx, req11)
		if err != nil {
			t.Fatalf("submit task 11: %v", err)
		}
		nowSec := float64(clock.Now().UnixNano()) / 1e9
		failTaskInDB(t, store.db, task11.TaskID, "504 gateway timeout", nowSec)

		// Resubmission of 11th task MUST be refused
		_, err = store.ResubmitTask(ctx, task11.TaskID, opts)
		if err == nil {
			t.Fatalf("11th resubmit succeeded, want hourly budget limit error")
		}
		if !strings.Contains(err.Error(), "hourly retry budget exceeded") {
			t.Errorf("error = %v, want containing 'hourly retry budget exceeded'", err)
		}

		// Advance clock past 1 hour window
		clock.Advance(1*time.Hour + 1*time.Second)

		// Resubmission of 11th task must NOW succeed!
		newID, err := store.ResubmitTask(ctx, task11.TaskID, opts)
		if err != nil {
			t.Fatalf("resubmit after 1 hour clock advance failed: %v", err)
		}
		if newID == "" {
			t.Fatalf("expected valid new task id after clock advance")
		}
	})
}

// 4. Backoff: resubmitted task not claimable before backoff elapses (fake clock via newTestStoreWithClock).
func TestResubmitTask_Backoff(t *testing.T) {
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()

	req := SubmitTaskRequest{
		IdempotencyKey: "backoff-task-1",
		Priority:       10,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"backoff check"}`),
	}
	orig, err := store.SubmitTask(ctx, req)
	if err != nil {
		t.Fatalf("submit orig: %v", err)
	}

	t0 := float64(clock.Now().UnixNano()) / 1e9
	failTaskInDB(t, store.db, orig.TaskID, "context deadline exceeded", t0)

	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}
	rID, err := store.ResubmitTask(ctx, orig.TaskID, opts)
	if err != nil {
		t.Fatalf("ResubmitTask: %v", err)
	}

	// 1st retry backoff is 5 minutes (300 seconds).
	// Immediately at t0: claim must return nil!
	claimed, err := store.ClaimTask(ctx, "worker-test", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed != nil {
		t.Fatalf("claimed task = %v before backoff elapsed, want nil", claimed.TaskID)
	}

	// Advance clock by 4 minutes (still within 5m backoff)
	clock.Advance(4 * time.Minute)
	claimed, err = store.ClaimTask(ctx, "worker-test", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed != nil {
		t.Fatalf("claimed task = %v at 4m before backoff elapsed, want nil", claimed.TaskID)
	}

	// Advance clock by 1 minute and 1 second (now past 5m backoff)
	clock.Advance(1*time.Minute + 1*time.Second)
	claimed, err = store.ClaimTask(ctx, "worker-test", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected task to be claimed after backoff elapsed, got nil")
	}
	if claimed.TaskID != rID {
		t.Errorf("claimed task ID = %s, want %s", claimed.TaskID, rID)
	}
}

// 5. Flag off -> refused with a clear error.
func TestResubmitTask_FlagOff(t *testing.T) {
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()

	req := SubmitTaskRequest{
		IdempotencyKey: "flag-off-task",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"flag test"}`),
	}
	orig, err := store.SubmitTask(ctx, req)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	nowSec := float64(clock.Now().UnixNano()) / 1e9
	failTaskInDB(t, store.db, orig.TaskID, "timeout occurred", nowSec)

	// Enabled = false
	opts := ResubmitOpts{
		Enabled:    false,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}
	_, err = store.ResubmitTask(ctx, orig.TaskID, opts)
	if err == nil {
		t.Fatalf("expected error when flag is off, got nil")
	}
	if !strings.Contains(err.Error(), "auto-retry is disabled") {
		t.Errorf("error = %v, want containing 'auto-retry is disabled'", err)
	}
}

// 6. Non-FAILED state, NOT_RETRYABLE class, workspace_write WITH receipt_id requested -> refused.
func TestResubmitTask_Refusals(t *testing.T) {
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()
	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	// Refusal 1: Non-FAILED state (e.g. QUEUED)
	t.Run("non_failed_state_refused", func(t *testing.T) {
		req := SubmitTaskRequest{
			IdempotencyKey: "queued-task",
			Priority:       1,
			MaxAttempts:    1,
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        json.RawMessage(`{"prompt":"queued task"}`),
		}
		task, err := store.SubmitTask(ctx, req)
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		// Task is in QUEUED state
		_, err = store.ResubmitTask(ctx, task.TaskID, opts)
		if err == nil {
			t.Fatalf("expected error resubmitting non-FAILED task, got nil")
		}
		if !strings.Contains(err.Error(), "only FAILED tasks can be resubmitted") {
			t.Errorf("error = %v, want containing 'only FAILED tasks can be resubmitted'", err)
		}
	})

	// Refusal 2: NOT_RETRYABLE class
	t.Run("not_retryable_class_refused", func(t *testing.T) {
		req := SubmitTaskRequest{
			IdempotencyKey: "refusal-class-task",
			Priority:       1,
			MaxAttempts:    1,
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        json.RawMessage(`{"prompt":"refusal test"}`),
		}
		task, err := store.SubmitTask(ctx, req)
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		nowSec := float64(clock.Now().UnixNano()) / 1e9
		failTaskInDB(t, store.db, task.TaskID, "receipt violation: write outside receipt scope", nowSec)

		_, err = store.ResubmitTask(ctx, task.TaskID, opts)
		if err == nil {
			t.Fatalf("expected error resubmitting non-retryable task, got nil")
		}
		if !strings.Contains(err.Error(), "not retryable") {
			t.Errorf("error = %v, want containing 'not retryable'", err)
		}
	})

	// Refusal 3: workspace_write WITH receipt_id requested -> refused without fresh receipt
	t.Run("workspace_write_with_receipt_requested_refused", func(t *testing.T) {
		t.Setenv("AGY_MCP_ALLOW_WORKSPACE_WRITE", "1")
		req := SubmitTaskRequest{
			IdempotencyKey: "write-with-receipt-task",
			Priority:       1,
			MaxAttempts:    1,
			Role:           "test-runner",
			Permission:     "workspace_write",
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        json.RawMessage(`{"prompt":"write code file","receipt_id":"rcpt-test-123","permission":"workspace_write","role":"test-runner"}`),
		}
		task, err := store.SubmitTask(ctx, req)
		if err != nil {
			t.Fatalf("submit write task: %v", err)
		}
		nowSec := float64(clock.Now().UnixNano()) / 1e9
		failTaskInDB(t, store.db, task.TaskID, "context deadline exceeded", nowSec)

		// Resubmit WITHOUT fresh receipt (ReceiptID is empty)
		_, err = store.ResubmitTask(ctx, task.TaskID, opts)
		if err == nil {
			t.Fatalf("expected error resubmitting workspace_write task without fresh receipt, got nil")
		}
		if !strings.Contains(err.Error(), "fresh receipt_id") {
			t.Errorf("error = %v, want containing 'fresh receipt_id'", err)
		}

		// When fresh receipt is provided, resubmit should succeed
		optsWithReceipt := opts
		optsWithReceipt.ReceiptID = "rcpt-fresh-456"
		newID, err := store.ResubmitTask(ctx, task.TaskID, optsWithReceipt)
		if err != nil {
			t.Fatalf("resubmit with fresh receipt failed: %v", err)
		}
		if newID == "" {
			t.Fatalf("expected valid new task id with fresh receipt")
		}
	})
}

// 7. Double-fire idempotency: same resubmit twice -> same new task id (UNIQUE key), not an error.
func TestResubmitTask_DoubleFireIdempotency(t *testing.T) {
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()

	req := SubmitTaskRequest{
		IdempotencyKey: "double-fire-task",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"idempotency check"}`),
	}
	orig, err := store.SubmitTask(ctx, req)
	if err != nil {
		t.Fatalf("submit orig: %v", err)
	}

	nowSec := float64(clock.Now().UnixNano()) / 1e9
	failTaskInDB(t, store.db, orig.TaskID, "fork/exec /bin/agy: spawn-failure", nowSec)

	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	// First resubmission
	newID1, err := store.ResubmitTask(ctx, orig.TaskID, opts)
	if err != nil {
		t.Fatalf("first resubmit failed: %v", err)
	}

	// Second resubmission of same original while first is still QUEUED
	newID2, err := store.ResubmitTask(ctx, orig.TaskID, opts)
	if err != nil {
		t.Fatalf("second resubmit failed: %v", err)
	}

	// Both calls must return the identical task ID
	if newID1 != newID2 {
		t.Errorf("newID1 (%s) != newID2 (%s), want identical task ID on double-fire", newID1, newID2)
	}

	// Verify only 1 child task was inserted into the database
	var childCount int
	err = store.db.QueryRow(`SELECT COUNT(1) FROM tasks WHERE parent_task_id = ?`, orig.TaskID).Scan(&childCount)
	if err != nil {
		t.Fatalf("count children: %v", err)
	}
	if childCount != 1 {
		t.Errorf("childCount = %d, want 1", childCount)
	}
}

// 8. Settings validators: negative/out-of-range rejected.
func TestSettingsValidators(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	mgr, err := settings.NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Check canonical defaults
	if val, ok := mgr.Get("auto_retry_enabled"); !ok || val != false {
		t.Errorf("Get(auto_retry_enabled) = %v, want false", val)
	}
	if val, ok := mgr.Get("auto_retry_max_per_task"); !ok || val != 2 {
		t.Errorf("Get(auto_retry_max_per_task) = %v, want 2", val)
	}
	if val, ok := mgr.Get("auto_retry_max_per_hour"); !ok || val != 10 {
		t.Errorf("Get(auto_retry_max_per_hour) = %v, want 10", val)
	}

	// Valid values
	validCases := []struct {
		key   string
		value any
	}{
		{"auto_retry_enabled", true},
		{"auto_retry_enabled", false},
		{"auto_retry_enabled", "true"},
		{"auto_retry_enabled", "false"},
		{"auto_retry_max_per_task", 0},
		{"auto_retry_max_per_task", 2},
		{"auto_retry_max_per_task", 10},
		{"auto_retry_max_per_task", "5"},
		{"auto_retry_max_per_hour", 0},
		{"auto_retry_max_per_hour", 10},
		{"auto_retry_max_per_hour", 1000},
		{"auto_retry_max_per_hour", "500"},
	}
	for _, tc := range validCases {
		if err := mgr.Set(tc.key, tc.value); err != nil {
			t.Errorf("Set(%q, %v) unexpected error: %v", tc.key, tc.value, err)
		}
	}

	// Invalid / out-of-range values
	invalidCases := []struct {
		name  string
		key   string
		value any
	}{
		{"enabled: not a bool", "auto_retry_enabled", "not_a_bool"},
		{"enabled: int type", "auto_retry_enabled", 123},
		{"max_per_task: negative", "auto_retry_max_per_task", -1},
		{"max_per_task: string negative", "auto_retry_max_per_task", "-5"},
		{"max_per_task: exceeds 10", "auto_retry_max_per_task", 11},
		{"max_per_task: string exceeds 10", "auto_retry_max_per_task", "15"},
		{"max_per_task: non-integer float", "auto_retry_max_per_task", 2.5},
		{"max_per_task: invalid string", "auto_retry_max_per_task", "abc"},
		{"max_per_hour: negative", "auto_retry_max_per_hour", -1},
		{"max_per_hour: string negative", "auto_retry_max_per_hour", "-10"},
		{"max_per_hour: exceeds 1000", "auto_retry_max_per_hour", 1001},
		{"max_per_hour: string exceeds 1000", "auto_retry_max_per_hour", "1500"},
		{"max_per_hour: non-integer float", "auto_retry_max_per_hour", 10.5},
		{"max_per_hour: invalid string", "auto_retry_max_per_hour", "xyz"},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			err := mgr.Set(tc.key, tc.value)
			if err == nil {
				t.Errorf("Set(%q, %v) expected validation error, got nil", tc.key, tc.value)
			}
			if !errors.Is(err, settings.ErrInvalidVal) {
				t.Errorf("Set(%q, %v) error = %v, want ErrInvalidVal", tc.key, tc.value, err)
			}
		})
	}
}
