package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Guarantee 1: Retry budget cannot be exceeded.
// =============================================================================

// TestRed_Guarantee1_MaxPerTask_ThirdResubmitRefused verifies:
// (a) 3rd resubmit of the same original (cap 2) via direct ResubmitTask calls
// with fresh contexts must be refused and cannot exceed the configured per-task budget.
func TestRed_Guarantee1_MaxPerTask_ThirdResubmitRefused(t *testing.T) {
	// Guarantee 1(a): Retry budget cannot be exceeded on 3rd resubmit of same original task.
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ResetHourlyRetryTracker()

	origReq := SubmitTaskRequest{
		IdempotencyKey: "red-budget-orig-task",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"adversarial retry budget test task"}`),
	}
	orig, err := store.SubmitTask(context.Background(), origReq)
	if err != nil {
		t.Fatalf("submit original task: %v", err)
	}

	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	t0 := float64(clock.Now().UnixNano()) / 1e9
	failTaskInDB(t, store.db, orig.TaskID, "context deadline exceeded", t0)

	// Attempt 1: First resubmit using fresh Context 1
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()
	r1ID, err := store.ResubmitTask(ctx1, orig.TaskID, opts)
	if err != nil {
		t.Fatalf("resubmit attempt 1 failed: %v", err)
	}
	if r1ID == "" {
		t.Fatalf("expected non-empty task ID on attempt 1")
	}

	// Fail retry 1
	failTaskInDB(t, store.db, r1ID, "504 gateway timeout", t0+10)

	// Attempt 2: Second resubmit using fresh Context 2
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	r2ID, err := store.ResubmitTask(ctx2, orig.TaskID, opts)
	if err != nil {
		t.Fatalf("resubmit attempt 2 failed: %v", err)
	}
	if r2ID == "" {
		t.Fatalf("expected non-empty task ID on attempt 2")
	}
	if r2ID == r1ID {
		t.Fatalf("attempt 2 returned duplicate ID %s of attempt 1", r2ID)
	}

	// Fail retry 2
	failTaskInDB(t, store.db, r2ID, "connection reset by peer", t0+20)

	// Attempt 3: Adversarial 3rd resubmit of same original task using fresh Context 3
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	r3ID, err := store.ResubmitTask(ctx3, orig.TaskID, opts)
	if err == nil {
		t.Fatalf("adversarial 3rd resubmit succeeded with task ID %s; expected retry budget exceeded error", r3ID)
	}
	if !strings.Contains(err.Error(), "retry budget exceeded") {
		t.Errorf("expected 'retry budget exceeded' in error message, got: %v", err)
	}

	// Attempt 4: Direct subsequent call with fresh Context 4 must also be denied
	ctx4 := context.Background()
	_, err = store.ResubmitTask(ctx4, orig.TaskID, opts)
	if err == nil {
		t.Fatalf("subsequent 4th resubmit succeeded; expected retry budget exceeded error")
	}
	if !strings.Contains(err.Error(), "retry budget exceeded") {
		t.Errorf("expected 'retry budget exceeded' in error message, got: %v", err)
	}

	// Verify database invariant: exactly 2 retry children were created
	var childCount int
	err = store.db.QueryRowContext(context.Background(),
		`SELECT COUNT(1) FROM tasks WHERE parent_task_id = ?`, orig.TaskID).Scan(&childCount)
	if err != nil {
		t.Fatalf("count children in DB: %v", err)
	}
	if childCount != 2 {
		t.Errorf("childCount in DB = %d, want exactly 2 (cap 2 strictly enforced)", childCount)
	}
}

// TestRed_Guarantee1_MaxPerHour_EleventhResubmitRefused verifies:
// (b) 11 resubmits within an hour across 11 DIFFERENT original tasks (per-dir cap 10)
// — clock-injected. 10 must succeed, the 11th must fail, and after 1 hour clock advance,
// the 11th task can be resubmitted.
func TestRed_Guarantee1_MaxPerHour_EleventhResubmitRefused(t *testing.T) {
	// Guarantee 1(b): Hourly retry budget cannot exceed 10 resubmits per hour across different original tasks.
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ResetHourlyRetryTracker()

	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	t0 := float64(clock.Now().UnixNano()) / 1e9

	// Submit and fail 11 DIFFERENT original tasks
	var origTasks []*Task
	for i := 1; i <= 11; i++ {
		req := SubmitTaskRequest{
			IdempotencyKey: fmt.Sprintf("red-hourly-orig-%02d", i),
			Priority:       1,
			MaxAttempts:    1,
			Model:          "gemini-3.8-flash-high",
			AddDirs:        []string{testScopeDir},
			Payload:        json.RawMessage(fmt.Sprintf(`{"prompt":"adversarial hourly task %d"}`, i)),
		}
		task, err := store.SubmitTask(context.Background(), req)
		if err != nil {
			t.Fatalf("submit orig task %d: %v", i, err)
		}
		failTaskInDB(t, store.db, task.TaskID, "503 service unavailable", t0+float64(i))
		origTasks = append(origTasks, task)
	}

	// Resubmit tasks 1..10 within the hour: all 10 must succeed
	for i := 0; i < 10; i++ {
		rID, err := store.ResubmitTask(context.Background(), origTasks[i].TaskID, opts)
		if err != nil {
			t.Fatalf("resubmit task %d within hourly budget failed: %v", i+1, err)
		}
		if rID == "" {
			t.Fatalf("resubmit task %d returned empty task ID", i+1)
		}
	}

	// 11th resubmit of a DIFFERENT original task in the same hour window MUST be refused
	r11ID, err := store.ResubmitTask(context.Background(), origTasks[10].TaskID, opts)
	if err == nil {
		t.Fatalf("adversarial 11th resubmit succeeded with task ID %s; expected hourly retry budget exceeded error", r11ID)
	}
	if !strings.Contains(err.Error(), "hourly retry budget exceeded") {
		t.Errorf("expected 'hourly retry budget exceeded' in error message, got: %v", err)
	}

	// Injected clock: advance 1 hour and 1 second
	clock.Advance(1*time.Hour + 1*time.Second)

	// Now the 11th task MUST be accepted
	r11AfterAdvanceID, err := store.ResubmitTask(context.Background(), origTasks[10].TaskID, opts)
	if err != nil {
		t.Fatalf("resubmit 11th task after 1 hour clock advance failed: %v", err)
	}
	if r11AfterAdvanceID == "" {
		t.Fatalf("resubmit 11th task after clock advance returned empty ID")
	}
}

// TestRed_Guarantee1_LineageVsForgedMetadata verifies:
// (c) budget counting via parent_task_id lineage when the caller forges retry_of
// metadata with a WRONG original id. The count must strictly follow the real SQL
// parent_task_id lineage, resisting forged JSON metadata.
func TestRed_Guarantee1_LineageVsForgedMetadata(t *testing.T) {
	// Guarantee 1(c): Retry budget counting follows real parent_task_id lineage, resisting forged retry_of metadata.
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ResetHourlyRetryTracker()

	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	t0 := float64(clock.Now().UnixNano()) / 1e9

	// 1. Submit legitimate original task A
	origReq := SubmitTaskRequest{
		IdempotencyKey: "lineage-task-a",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"legitimate task A","actor":"operator"}`),
	}
	origA, err := store.SubmitTask(context.Background(), origReq)
	if err != nil {
		t.Fatalf("submit origA: %v", err)
	}
	failTaskInDB(t, store.db, origA.TaskID, "context deadline exceeded", t0)

	// Sub-case 1: Attacker injects a child task directly into SQL with parent_task_id = origA.TaskID
	// and idempotency_key = origA.TaskID + "#r1", but forges `retry_of` metadata pointing to a
	// bogus foreign task ID "forged-foreign-uuid-999".
	// The budget counter must count this child towards origA's budget based on real SQL lineage.
	forgedChildReq := SubmitTaskRequest{
		IdempotencyKey: fmt.Sprintf("%s#r1", origA.TaskID),
		Priority:       1,
		MaxAttempts:    1,
		ParentTaskID:   &origA.TaskID,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"child 1","retry_of":"forged-foreign-uuid-999","actor":"operator"}`),
	}
	child1, err := store.SubmitTask(context.Background(), forgedChildReq)
	if err != nil {
		t.Fatalf("submit forgedChildReq: %v", err)
	}
	failTaskInDB(t, store.db, child1.TaskID, "context deadline exceeded", t0+1)

	// Now call ResubmitTask(origA.TaskID). Since child1 is already #r1, this call must produce #r2.
	r2ID, err := store.ResubmitTask(context.Background(), origA.TaskID, opts)
	if err != nil {
		t.Fatalf("resubmit origA (attempt 2) failed: %v", err)
	}
	r2Task, err := store.GetTask(context.Background(), r2ID)
	if err != nil {
		t.Fatalf("get r2Task: %v", err)
	}
	expectedKey := fmt.Sprintf("%s#r2", origA.TaskID)
	if r2Task.IdempotencyKey != expectedKey {
		t.Errorf("r2 idempotency key = %s, want %s (forged metadata did not evade lineage counter)",
			r2Task.IdempotencyKey, expectedKey)
	}

	// Fail child 2
	failTaskInDB(t, store.db, r2ID, "context deadline exceeded", t0+2)

	// Sub-case 2: Now origA has 2 children in SQL. 3rd resubmit of origA MUST be refused,
	// proving that the forged `retry_of` metadata could not conceal child 1 from the budget cap.
	_, err = store.ResubmitTask(context.Background(), origA.TaskID, opts)
	if err == nil {
		t.Fatalf("3rd resubmit of origA succeeded; expected budget exceeded despite forged metadata in child")
	}
	if !strings.Contains(err.Error(), "retry budget exceeded") {
		t.Errorf("expected 'retry budget exceeded', got: %v", err)
	}

	// Sub-case 3: Rogue task submits with `retry_of = origA.TaskID` in payload, but
	// ParentTaskID is NOT origA (attacker attempts to deplete origA's budget from outside).
	// Let's create a new task B with cap 1.
	origReqB := SubmitTaskRequest{
		IdempotencyKey: "lineage-task-b",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"victim task B","actor":"operator"}`),
	}
	origB, err := store.SubmitTask(context.Background(), origReqB)
	if err != nil {
		t.Fatalf("submit origB: %v", err)
	}
	failTaskInDB(t, store.db, origB.TaskID, "500 internal server error", t0+10)

	// Rogue task submitted by attacker pointing to origB.TaskID in payload, but parent_task_id = nil
	rogueReq := SubmitTaskRequest{
		IdempotencyKey: "attacker-rogue-task",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(fmt.Sprintf(`{"prompt":"rogue spoof","retry_of":"%s"}`, origB.TaskID)),
	}
	_, err = store.SubmitTask(context.Background(), rogueReq)
	if err != nil {
		t.Fatalf("submit rogue task: %v", err)
	}

	// Resubmit origB: must succeed because rogue task does NOT belong to origB's SQL lineage!
	optsB := ResubmitOpts{Enabled: true, MaxPerTask: 1, MaxPerHour: 10}
	newBID, err := store.ResubmitTask(context.Background(), origB.TaskID, optsB)
	if err != nil {
		t.Fatalf("resubmit origB failed: rogue metadata erroneously affected budget: %v", err)
	}
	if newBID == "" {
		t.Fatalf("resubmit origB returned empty task ID")
	}

	// Sub-case 4: Verify that ResubmitTask sanitizes and overwrites payload `retry_of` with real parent ID
	newBTask, err := store.GetTask(context.Background(), newBID)
	if err != nil {
		t.Fatalf("get newBTask: %v", err)
	}
	var newBPayload map[string]any
	if err := json.Unmarshal(newBTask.Request, &newBPayload); err != nil {
		t.Fatalf("unmarshal newB payload: %v", err)
	}
	if retryOf, _ := newBPayload["retry_of"].(string); retryOf != origB.TaskID {
		t.Errorf("new task retry_of = %v, want real parent ID %s", retryOf, origB.TaskID)
	}
}

// =============================================================================
// Guarantee 2: Non-retryable classes never resubmit.
// =============================================================================

// TestRed_Guarantee2_NonRetryableNeverResubmit verifies:
// Construct result_json payloads that a naive string-match classifier might misread:
// - success markers embedded in error text
// - E_USAGE nested in allowed fields
// - a receipt-violation reason that CONTAINS the word "timeout"
// - unicode/fullwidth look-alikes of class keywords
// Verify ClassifyRetryable denies all of them, and ResubmitTask refuses to resubmit.
func TestRed_Guarantee2_NonRetryableNeverResubmit(t *testing.T) {
	// Guarantee 2: Non-retryable classes never resubmit even when payloads embed naive match triggers.
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ResetHourlyRetryTracker()

	adversarialCases := []struct {
		name       string
		resultJSON string
		lastError  string
		category   string
	}{
		// 1. Success markers embedded in error text / JSON
		{
			name:       "success marker: ok=true with E_TIMEOUT error envelope",
			resultJSON: `{"ok":true,"error":{"code":"E_TIMEOUT","message":"timed out executing step"}}`,
			lastError:  "",
			category:   "success marker",
		},
		{
			name:       "success marker: status succeeded with 500 server error in message",
			resultJSON: `{"status":"succeeded","error":{"message":"500 internal server error during notification"}}`,
			lastError:  "",
			category:   "success marker",
		},
		{
			name:       "success marker: result.status SUCCESS with context deadline exceeded in error",
			resultJSON: `{"result":{"status":"SUCCESS","error":"context deadline exceeded while closing socket"}}`,
			lastError:  "",
			category:   "success marker",
		},
		{
			name:       "success marker: ok=true with summary stating execution timed out",
			resultJSON: `{"ok":true,"summary":"execution timed out during final commit check"}`,
			lastError:  "timed out",
			category:   "success marker",
		},
		{
			name:       "success marker: status SUCCEEDED with connection reset in result",
			resultJSON: `{"status":"SUCCEEDED","result":{"error":"connection reset by peer"}}`,
			lastError:  "",
			category:   "success marker",
		},

		// 2. E_USAGE nested in allowed/custom fields or combined with timeout
		{
			name:       "e_usage: envelope code E_USAGE with timeout message",
			resultJSON: `{"ok":false,"error":{"code":"E_USAGE","message":"unknown flag --timeout-seconds"}}`,
			lastError:  "",
			category:   "e_usage nested",
		},
		{
			name:       "e_usage: envelope code E_TIMEOUT with E_USAGE in error message",
			resultJSON: `{"ok":false,"error":{"code":"E_TIMEOUT","message":"command failed: E_USAGE invalid argument"}}`,
			lastError:  "",
			category:   "e_usage nested",
		},
		{
			name:       "e_usage: envelope code E_TIMEOUT with E_USAGE in cause field",
			resultJSON: `{"ok":false,"error":{"code":"E_TIMEOUT","message":"timed out","cause":"E_USAGE: bad flag"}}`,
			lastError:  "",
			category:   "e_usage nested",
		},
		{
			name:       "e_usage: reason field contains E_USAGE with timeout in error code",
			resultJSON: `{"ok":false,"reason":"validation failed with E_USAGE","error":{"code":"E_TIMEOUT","message":"deadline exceeded"}}`,
			lastError:  "",
			category:   "e_usage nested",
		},
		{
			name:       "e_usage: summary field contains E_USAGE with transport error in message",
			resultJSON: `{"ok":false,"summary":"command aborted: E_USAGE syntax error","error":{"code":"E_IO","message":"transport error"}}`,
			lastError:  "",
			category:   "e_usage nested",
		},
		{
			name:       "e_usage: envelope code E_INVALID with deadline exceeded in message",
			resultJSON: `{"ok":false,"error":{"code":"E_INVALID","message":"context deadline exceeded in validator"}}`,
			lastError:  "",
			category:   "e_usage nested",
		},
		{
			name:       "e_usage: envelope code E_DENIED with connection reset in message",
			resultJSON: `{"ok":false,"error":{"code":"E_DENIED","message":"connection reset by peer during auth"}}`,
			lastError:  "",
			category:   "e_usage nested",
		},

		// 3. Receipt-violation reason that CONTAINS the word "timeout"
		{
			name:       "receipt violation: reason contains timeout with E_TIMEOUT code",
			resultJSON: `{"ok":false,"reason":"receipt violation: lease timeout occurred during write verification","error":{"code":"E_TIMEOUT","message":"timed out"}}`,
			lastError:  "",
			category:   "receipt violation with timeout",
		},
		{
			name:       "receipt violation: error message contains receipt violation with timeout code",
			resultJSON: `{"ok":false,"error":{"code":"E_TIMEOUT","message":"receipt violation: unauthorized file write"}}`,
			lastError:  "",
			category:   "receipt violation with timeout",
		},
		{
			name:       "receipt violation: lastError has receipt violation with lease timeout",
			resultJSON: "",
			lastError:  "receipt violation: lease timeout while validating receipt boundaries",
			category:   "receipt violation with timeout",
		},
		{
			name:       "receipt violation: lastError has receipt-bypass with deadline exceeded",
			resultJSON: "",
			lastError:  "receipt-bypass detected: context deadline exceeded in validation filter",
			category:   "receipt violation with timeout",
		},
		{
			name:       "receipt violation: lastError has receipt unknown or expired with 504 gateway timeout",
			resultJSON: "",
			lastError:  "receipt unknown or expired (rcpt-99): status 504 gateway timeout",
			category:   "receipt violation with timeout",
		},
		{
			name:       "receipt violation: lastError has missing receipt with timed out",
			resultJSON: "",
			lastError:  "missing receipt for write operation: timed out awaiting supervisor signature",
			category:   "receipt violation with timeout",
		},

		// 4. Unicode / fullwidth look-alikes of class keywords
		{
			name:       "unicode lookalike: fullwidth ｔｉｍｅｏｕｔ in error message",
			resultJSON: `{"ok":false,"error":{"code":"E_UNKNOWN","message":"operation failed with ｔｉｍｅｏｕｔ"}}`,
			lastError:  "ｔｉｍｅｏｕｔ",
			category:   "unicode look-alike",
		},
		{
			name:       "unicode lookalike: fullwidth Ｅ＿ＴＩＭＥＯＵＴ in error code",
			resultJSON: `{"ok":false,"error":{"code":"Ｅ＿ＴＩＭＥＯＵＴ","message":"execution halted"}}`,
			lastError:  "",
			category:   "unicode look-alike",
		},
		{
			name:       "unicode lookalike: fullwidth ５００ server status",
			resultJSON: "",
			lastError:  "HTTP status ５００ from upstream host",
			category:   "unicode look-alike",
		},
		{
			name:       "unicode lookalike: fullwidth ｃｏｎｎｅｃｔｉｏｎ ｒｅｓｅｔ",
			resultJSON: "",
			lastError:  "ｃｏｎｎｅｃｔｉｏｎ ｒｅｓｅｔ by peer socket",
			category:   "unicode look-alike",
		},
		{
			name:       "unicode lookalike: Cyrillic homoglyph tіmeout (U+0456)",
			resultJSON: "",
			lastError:  "context t\u0456meout exceeded",
			category:   "unicode look-alike",
		},
		{
			name:       "unicode lookalike: Cyrillic homoglyph dеadline еxсееdеd (U+0435, U+0441)",
			resultJSON: "",
			lastError:  "d\u0435adlin\u0435 \u0435x\u0441\u0435\u0435d\u0435d",
			category:   "unicode look-alike",
		},
		{
			name:       "unicode lookalike: Cyrillic homoglyph іnterrupted (U+0456)",
			resultJSON: "",
			lastError:  "\u0456nterrupted by signal kill",
			category:   "unicode look-alike",
		},
		{
			name:       "unicode lookalike: Cyrillic homoglyph spawn-faіlure (U+0456)",
			resultJSON: "",
			lastError:  "spawn-fa\u0456lure detected in worker runtime",
			category:   "unicode look-alike",
		},
		{
			name:       "unicode lookalike: fullwidth ｓｉｇｎａｌ： ｉｎｔｅｒｒｕｐｔ",
			resultJSON: "",
			lastError:  "ｓｉｇｎａｌ： ｉｎｔｅｒｒｕｐｔ caught in worker",
			category:   "unicode look-alike",
		},
	}

	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	t0 := float64(clock.Now().UnixNano()) / 1e9

	for idx, tc := range adversarialCases {
		t.Run(tc.name, func(t *testing.T) {
			// Part A: Direct classification evaluation must DENY (return ClassNotRetryable)
			gotClass := ClassifyRetryable(tc.resultJSON, tc.lastError)
			if gotClass != RetryClassNotRetryable {
				t.Errorf("[%s] ClassifyRetryable(%q, %q) = %v, want NOT_RETRYABLE",
					tc.category, tc.resultJSON, tc.lastError, gotClass)
			}

			// Part B: Store.ResubmitTask integration test: resubmission must be REFUSED
			taskReq := SubmitTaskRequest{
				IdempotencyKey: fmt.Sprintf("adversarial-class-task-%d", idx),
				Priority:       1,
				MaxAttempts:    1,
				Model:          "gemini-3.8-flash-high",
				AddDirs:        []string{testScopeDir},
				Payload:        json.RawMessage(`{"prompt":"adversarial classification test task"}`),
			}
			task, err := store.SubmitTask(context.Background(), taskReq)
			if err != nil {
				t.Fatalf("submit task: %v", err)
			}

			// Transition task to FAILED in DB with the adversarial resultJSON and lastError
			_, err = store.db.Exec(`
				UPDATE tasks
				SET state = 'FAILED', result_json = ?, last_error = ?, completed_at = ?, updated_at = ?,
				    lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL
				WHERE task_id = ?`,
				tc.resultJSON, tc.lastError, t0, t0, task.TaskID)
			if err != nil {
				t.Fatalf("fail task in DB: %v", err)
			}

			// Resubmit MUST be refused with non-retryable error
			newID, err := store.ResubmitTask(context.Background(), task.TaskID, opts)
			if err == nil {
				t.Fatalf("[%s] ResubmitTask succeeded with ID %s; expected refusal due to NOT_RETRYABLE classification",
					tc.category, newID)
			}
			if !strings.Contains(err.Error(), "not retryable") {
				t.Errorf("[%s] expected error containing 'not retryable', got: %v", tc.category, err)
			}
		})
	}
}

// =============================================================================
// Guarantee 3: Idempotency keys resist collision.
// =============================================================================

// TestRed_Guarantee3_ConcurrentResubmitIdempotency verifies:
// Resubmit task A twice concurrently (goroutines) — exactly one new task.
func TestRed_Guarantee3_ConcurrentResubmitIdempotency(t *testing.T) {
	// Guarantee 3(a): Concurrent resubmits of task A produce exactly one new task without collisions.
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ResetHourlyRetryTracker()

	origReq := SubmitTaskRequest{
		IdempotencyKey: "concurrent-resubmit-orig",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"concurrent idempotency check"}`),
	}
	orig, err := store.SubmitTask(context.Background(), origReq)
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

	const goroutines = 10
	var wg sync.WaitGroup
	results := make([]string, goroutines)
	errs := make([]error, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			id, err := store.ResubmitTask(context.Background(), orig.TaskID, opts)
			results[idx] = id
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// Verify all goroutines succeeded without collision errors
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
	}

	// Verify all goroutines returned the exact same new task ID
	winnerID := results[0]
	if winnerID == "" {
		t.Fatalf("expected valid non-empty task ID from winner")
	}
	for i := 1; i < goroutines; i++ {
		if results[i] != winnerID {
			t.Errorf("goroutine %d returned task ID %s, want identical %s", i, results[i], winnerID)
		}
	}

	// Verify exactly 1 child task exists in DB
	var childCount int
	err = store.db.QueryRowContext(context.Background(),
		`SELECT COUNT(1) FROM tasks WHERE parent_task_id = ?`, orig.TaskID).Scan(&childCount)
	if err != nil {
		t.Fatalf("count child tasks in DB: %v", err)
	}
	if childCount != 1 {
		t.Errorf("childCount in DB = %d, want exactly 1 (double-fire deduplication failed)", childCount)
	}

	// Verify hourly tracker recorded exactly 1 retry (deduplicated calls do not consume budget)
	globalRetryTracker.mu.Lock()
	entryCount := len(globalRetryTracker.entries[store.dbPath])
	globalRetryTracker.mu.Unlock()
	if entryCount != 1 {
		t.Errorf("hourly retry entries recorded = %d, want exactly 1", entryCount)
	}
}

// TestRed_Guarantee3_DerivedKeyResistsHashR1Collision verifies:
// Craft an original task whose id CONTAINS "#r1" already (a task submitted with
// idempotency-key "x#r1" — verify the derived-key scheme stays collision-free for
// the second generation: can task <orig>#r1's own retry collide with <orig>'s retry #1?).
func TestRed_Guarantee3_DerivedKeyResistsHashR1Collision(t *testing.T) {
	// Guarantee 3(b): Derived idempotency keys resist collision even when task IDs or keys contain '#r1'.
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ResetHourlyRetryTracker()

	opts := ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	t0 := float64(clock.Now().UnixNano()) / 1e9

	// -------------------------------------------------------------------------
	// Part A: Original task submitted with idempotency-key "x#r1"
	// -------------------------------------------------------------------------
	taskWithHashR1KeyReq := SubmitTaskRequest{
		IdempotencyKey: "x#r1",
		Priority:       1,
		MaxAttempts:    1,
		Model:          "gemini-3.8-flash-high",
		AddDirs:        []string{testScopeDir},
		Payload:        json.RawMessage(`{"prompt":"task submitted with x#r1 key"}`),
	}
	taskA, err := store.SubmitTask(context.Background(), taskWithHashR1KeyReq)
	if err != nil {
		t.Fatalf("submit taskA: %v", err)
	}
	failTaskInDB(t, store.db, taskA.TaskID, "context deadline exceeded", t0)

	// Resubmit taskA: its retry #1 key is <taskA.TaskID>#r1 (derived from UUID, not idempotency key)
	rA1ID, err := store.ResubmitTask(context.Background(), taskA.TaskID, opts)
	if err != nil {
		t.Fatalf("resubmit taskA: %v", err)
	}
	rA1Task, err := store.GetTask(context.Background(), rA1ID)
	if err != nil {
		t.Fatalf("get rA1Task: %v", err)
	}
	expectedRA1Key := fmt.Sprintf("%s#r1", taskA.TaskID)
	if rA1Task.IdempotencyKey != expectedRA1Key {
		t.Errorf("rA1Task.IdempotencyKey = %s, want %s", rA1Task.IdempotencyKey, expectedRA1Key)
	}
	// The derived key must never collide with the submitted key "x#r1"
	if rA1Task.IdempotencyKey == "x#r1" {
		t.Errorf("rA1Task.IdempotencyKey collided with original idempotency key 'x#r1'")
	}

	// -------------------------------------------------------------------------
	// Part B: Craft a task whose actual task_id CONTAINS "#r1"
	// Can task <orig>#r1's own retry collide with <orig>'s retry #1?
	// -------------------------------------------------------------------------
	craftedTaskID := "orig-task-alpha#r1"
	_, err = store.db.ExecContext(context.Background(), `
		INSERT INTO tasks(
			task_id, idempotency_key, schema_version, state, priority,
			request_json, request_hash, max_attempts, created_at, updated_at,
			allowed_paths, allowed_tools, max_output_size, output_schema, contract_validation,
			last_error, completed_at
		) VALUES (
			?, 'key-crafted-alpha', ?, 'FAILED', 1,
			'{"prompt":"crafted #r1 task"}', 'hash-crafted', 1, ?, ?,
			'[]', '[]', 0, '{}', '{}',
			'504 gateway timeout', ?
		)`, craftedTaskID, TaskSchemaVersion, t0, t0, t0)
	if err != nil {
		t.Fatalf("insert crafted task with #r1 in task_id: %v", err)
	}

	// Resubmit craftedTaskID (<orig>#r1): its 1st retry key is "<orig-task-alpha#r1>#r1"
	rCrafted1ID, err := store.ResubmitTask(context.Background(), craftedTaskID, opts)
	if err != nil {
		t.Fatalf("resubmit crafted task (%s): %v", craftedTaskID, err)
	}
	rCrafted1Task, err := store.GetTask(context.Background(), rCrafted1ID)
	if err != nil {
		t.Fatalf("get rCrafted1Task: %v", err)
	}

	// Expected key for <orig>#r1's retry #1 is "orig-task-alpha#r1#r1"
	expectedCraftedR1Key := fmt.Sprintf("%s#r1", craftedTaskID)
	if rCrafted1Task.IdempotencyKey != expectedCraftedR1Key {
		t.Errorf("rCrafted1Task.IdempotencyKey = %s, want %s",
			rCrafted1Task.IdempotencyKey, expectedCraftedR1Key)
	}

	// Verify collision freedom:
	// "orig-task-alpha#r1#r1" does NOT collide with "orig-task-alpha#r1"
	if rCrafted1Task.IdempotencyKey == craftedTaskID {
		t.Errorf("collision detected: derived key %s matches original task ID %s",
			rCrafted1Task.IdempotencyKey, craftedTaskID)
	}

	// -------------------------------------------------------------------------
	// Part C: Second generation retry collision resistance
	// Fail rCrafted1Task and resubmit: produces attempt 2 ("orig-task-alpha#r1#r2")
	// -------------------------------------------------------------------------
	failTaskInDB(t, store.db, rCrafted1ID, "503 service unavailable", t0+5)
	rCrafted2ID, err := store.ResubmitTask(context.Background(), craftedTaskID, opts)
	if err != nil {
		t.Fatalf("resubmit crafted task attempt 2: %v", err)
	}
	rCrafted2Task, err := store.GetTask(context.Background(), rCrafted2ID)
	if err != nil {
		t.Fatalf("get rCrafted2Task: %v", err)
	}
	expectedCraftedR2Key := fmt.Sprintf("%s#r2", craftedTaskID)
	if rCrafted2Task.IdempotencyKey != expectedCraftedR2Key {
		t.Errorf("rCrafted2Task.IdempotencyKey = %s, want %s",
			rCrafted2Task.IdempotencyKey, expectedCraftedR2Key)
	}

	// Verify all keys in the generation hierarchy are strictly distinct
	keys := []string{
		"x#r1",
		rA1Task.IdempotencyKey,
		craftedTaskID,
		rCrafted1Task.IdempotencyKey,
		rCrafted2Task.IdempotencyKey,
	}
	seen := make(map[string]bool)
	for _, k := range keys {
		if seen[k] {
			t.Errorf("duplicate key found across generations: %s", k)
		}
		seen[k] = true
	}
}
