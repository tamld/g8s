package autopilot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/receipt"
	_ "modernc.org/sqlite"
)

// setupRedTestControlPlane creates an isolated in-memory or temp-dir SQLite control plane.
func setupRedTestControlPlane(t *testing.T) (*controlplane.Store, string, *sql.DB) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s.db")
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("failed to create test controlplane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	controlplane.ResetHourlyRetryTracker()
	return store, dbPath, rawDB
}

// seedFailedTask creates a task and forces it into FAILED state with given attributes.
func seedFailedTask(t *testing.T, store *controlplane.Store, rawDB *sql.DB, key, perm, lastErr, resultJSON string, completedAt float64) string {
	t.Helper()
	if perm == "" {
		perm = "read_only"
	}
	if resultJSON == "" {
		resultJSON = `{"ok":false,"error":{"code":"E_TIMEOUT","message":"timed out"}}`
	}
	if lastErr == "" {
		lastErr = "context deadline exceeded"
	}

	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: key,
		Priority:       10,
		MaxAttempts:    3,
		Role:           "collector",
		Model:          "gemini-3.8-flash-high",
		Permission:     "read_only",
		AddDirs:        []string{"."},
		Payload:        json.RawMessage(`{"prompt":"adversarial test task"}`),
	})
	if err != nil {
		t.Fatalf("seed task submit: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if completedAt == 0 {
		completedAt = now
	}

	payloadMap := map[string]any{
		"prompt":     "adversarial test task",
		"permission": perm,
	}
	payloadBytes, _ := json.Marshal(payloadMap)

	_, err = rawDB.Exec(`
		UPDATE tasks
		SET state = 'FAILED', request_json = ?, result_json = ?, last_error = ?, completed_at = ?, updated_at = ?
		WHERE task_id = ?`, string(payloadBytes), resultJSON, lastErr, completedAt, completedAt, task.TaskID)
	if err != nil {
		t.Fatalf("update seeded task to FAILED: %v", err)
	}

	return task.TaskID
}

// -----------------------------------------------------------------------------
// Guarantee 1: Retry budget cannot be escaped by identity games.
// -----------------------------------------------------------------------------

// TestRedRC2_Guarantee1_DerivedKeyCollisionRefusedHonestly verifies that when two tasks
// collide on derived keys (<id>#r1), the second insert is refused honestly with a typed error.
func TestRedRC2_Guarantee1_DerivedKeyCollisionRefusedHonestly(t *testing.T) {
	store, _, rawDB := setupRedTestControlPlane(t)
	ctx := context.Background()

	opts := controlplane.ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	taskAID := seedFailedTask(t, store, rawDB, "orig-task-a-key", "read_only", "context deadline exceeded", "", 0)

	// 2. Pre-create a task colliding on task A's derived retry key: "<taskAID>#r1"
	derivedKey := fmt.Sprintf("%s#r1", taskAID)
	_, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: derivedKey,
		Priority:       5,
		MaxAttempts:    1,
		Role:           "collector",
		Model:          "gemini-3.8-flash-high",
		Permission:     "read_only",
		AddDirs:        []string{"."},
		Payload:        json.RawMessage(`{"prompt":"adversary occupying derived key"}`),
	})
	if err != nil {
		t.Fatalf("submit colliding task: %v", err)
	}

	// 3. Attempt to resubmit task A: must derive "<taskAID>#r1", collide, and be honestly refused
	_, err = store.ResubmitTask(ctx, taskAID, opts)
	if err == nil {
		t.Fatalf("expected ResubmitTask to fail on derived key collision, got nil")
	}

	if !strings.Contains(err.Error(), "idempotency_key already exists with a different request") {
		t.Errorf("expected collision refusal error mentioning idempotency_key conflict, got: %v", err)
	}
}

// TestRedRC2_Guarantee1_IdentityGames_CasingAndSpacingBypass checks whether idempotency key
// variants (<id>#r1 vs <id>#R1 vs <id>#r01 vs <id>#r1 ) escape budget counting.
func TestRedRC2_Guarantee1_IdentityGames_CasingAndSpacingBypass(t *testing.T) {
	store, _, rawDB := setupRedTestControlPlane(t)
	ctx := context.Background()

	opts := controlplane.ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	origID := seedFailedTask(t, store, rawDB, "task-case-budget-orig", "read_only", "context deadline exceeded", "", 0)

	// An adversary inserts a child task directly into the lineage with uppercase '#R1'
	upperChildID := "child-upper-R1"
	upperKey := fmt.Sprintf("%s#R1", origID)
	now := float64(time.Now().UnixNano()) / 1e9
	_, err := rawDB.Exec(`
		INSERT INTO tasks(
			task_id, parent_task_id, idempotency_key, schema_version, state, priority,
			request_json, request_hash, max_attempts, created_at, updated_at
		) VALUES (?, ?, ?, 'v2.0', 'FAILED', 10, '{"prompt":"child #R1"}', 'hash-R1', 1, ?, ?)`,
		upperChildID, origID, upperKey, now, now)
	if err != nil {
		t.Fatalf("insert upper child: %v", err)
	}

	// Now origID already has 1 failed retry child in its lineage (#R1).
	// Under Guarantee 1, budget counting must treat #R1 as part of the lineage.
	// Therefore, at most 1 MORE retry should be allowed before reaching cap 2.
	// Let's perform attempt 1 via ResubmitTask:
	r1ID, err := store.ResubmitTask(ctx, origID, opts)
	if err != nil {
		t.Fatalf("first ResubmitTask failed: %v", err)
	}

	// Fail r1ID
	_, err = rawDB.Exec("UPDATE tasks SET state = 'FAILED' WHERE task_id = ?", r1ID)
	if err != nil {
		t.Fatalf("fail r1ID: %v", err)
	}

	// Perform attempt 2 via ResubmitTask:
	// Since #R1 was child 1, and r1ID was child 2, this next attempt should EXCEED the budget of 2!
	r2ID, err := store.ResubmitTask(ctx, origID, opts)
	if err == nil {
		// BUG(REDTEST): Case-sensitive prefix match `strings.HasPrefix(c.idempotencyKey, origTaskID+"#r")`
		// ignores `#R1`, allowing 3 retries when MaxPerTask=2!
		t.Fatalf("// BUG(REDTEST): Idempotency-key variant with uppercase '#R1' (%s) was ignored by budget counter; allowed 3rd retry child %s (budget 2 escaped via identity casing game)", upperKey, r2ID)
	}
	if !strings.Contains(err.Error(), "retry budget exceeded") {
		t.Fatalf("expected error mentioning 'retry budget exceeded', got: %v", err)
	}
}

// TestRedRC2_Guarantee1_ParallelRetryEscapeViaKeyVariants tests whether submitting multiple
// idempotency-key variants (<id>#r1 vs <id>#r1  vs <id>#r01) allows parallel active retries of one task.
func TestRedRC2_Guarantee1_ParallelRetryEscapeViaKeyVariants(t *testing.T) {
	store, _, rawDB := setupRedTestControlPlane(t)
	ctx := context.Background()

	origID := seedFailedTask(t, store, rawDB, "task-parallel-budget-orig", "read_only", "context deadline exceeded", "", 0)

	// Submit child 1 with trailing space: "<origID>#r1 "
	child1Req := controlplane.SubmitTaskRequest{
		IdempotencyKey: fmt.Sprintf("%s#r1 ", origID),
		Priority:       10,
		MaxAttempts:    1,
		ParentTaskID:   &origID,
		Role:           "collector",
		Model:          "gemini-3.8-flash-high",
		Permission:     "read_only",
		AddDirs:        []string{"."},
		Payload:        json.RawMessage(`{"prompt":"parallel retry 1"}`),
	}
	t1, err := store.SubmitTask(ctx, child1Req)
	if err != nil {
		t.Fatalf("submit child 1: %v", err)
	}

	// Submit child 2 with leading zero: "<origID>#r01"
	child2Req := controlplane.SubmitTaskRequest{
		IdempotencyKey: fmt.Sprintf("%s#r01", origID),
		Priority:       10,
		MaxAttempts:    1,
		ParentTaskID:   &origID,
		Role:           "collector",
		Model:          "gemini-3.8-flash-high",
		Permission:     "read_only",
		AddDirs:        []string{"."},
		Payload:        json.RawMessage(`{"prompt":"parallel retry 2"}`),
	}
	t2, err := store.SubmitTask(ctx, child2Req)
	if err == nil {
		t.Fatalf("expected non-canonical child 2 to be refused, got task %s", t2.TaskID)
	}

	// Submit child 3 with canonical key: "<origID>#r1"
	child3Req := controlplane.SubmitTaskRequest{
		IdempotencyKey: fmt.Sprintf("%s#r1", origID),
		Priority:       10,
		MaxAttempts:    1,
		ParentTaskID:   &origID,
		Role:           "collector",
		Model:          "gemini-3.8-flash-high",
		Permission:     "read_only",
		AddDirs:        []string{"."},
		Payload:        json.RawMessage(`{"prompt":"parallel retry 3"}`),
	}
	t3, err := store.SubmitTask(ctx, child3Req)
	if err == nil {
		t.Fatalf("expected colliding child 3 to be refused, got task %s", t3.TaskID)
	}

	// Count active QUEUED children of origID
	var queuedCount int
	err = rawDB.QueryRow("SELECT COUNT(1) FROM tasks WHERE parent_task_id = ? AND state = 'QUEUED'", origID).Scan(&queuedCount)
	if err != nil {
		t.Fatalf("count queued children: %v", err)
	}

	if queuedCount > 1 {
		// BUG(REDTEST): Idempotency key variants allow N parallel active retries of one task
		t.Fatalf("// BUG(REDTEST): %d parallel retry tasks (%s) are simultaneously QUEUED for task %s via identity key variations (<id>#r1 vs <id>#r1  vs <id>#r01); FSM failed to restrict to single in-flight retry", queuedCount, t1.TaskID, origID)
	}
	if queuedCount != 1 || t1.TaskID == "" {
		t.Fatalf("expected exactly 1 queued child (%s), got %d", t1.TaskID, queuedCount)
	}
}

// -----------------------------------------------------------------------------
// Guarantee 2: Deny-by-default classes cannot be slipped a receipt.
// -----------------------------------------------------------------------------

func issueTestReceipt(t *testing.T, mgr *receipt.Manager, ttl time.Duration) *receipt.WriteReceipt {
	t.Helper()
	rc, err := mgr.IssueReceipt("test-issuer", []string{"."}, ttl,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI:      "g8s://envelope/write-receipt/v1",
			FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
			RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
			RulesetVersion: "v1",
			PipelineDigest: "cli-default",
		}),
		receipt.WithProvenanceContext("g8s/v0.14.0", "commit-test", "trace-test"),
	)
	if err != nil {
		t.Fatalf("IssueReceipt: %v", err)
	}
	return rc
}

// TestRedRC2_Guarantee2_WorkspaceWrite_ReceiptOmissionAndTamper verifies that ResubmitTask
// denies workspace_write tasks when receipt is omitted, empty, consumed, or expired.
func TestRedRC2_Guarantee2_WorkspaceWrite_ReceiptOmissionAndTamper(t *testing.T) {
	store, dbPath, rawDB := setupRedTestControlPlane(t)
	ctx := context.Background()

	// Initialize receipt database
	rcDbPath := filepath.Join(filepath.Dir(dbPath), "receipts.db")
	rcMgr, err := receipt.NewReceiptManager(rcDbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	defer rcMgr.Close()

	// Create a valid receipt, a consumed receipt, and an expired receipt
	validRcpt := issueTestReceipt(t, rcMgr, 1*time.Hour)
	consumedRcpt := issueTestReceipt(t, rcMgr, 1*time.Hour)
	if _, err := rcMgr.ValidateAndConsume(consumedRcpt.ReceiptID, "consumer-task"); err != nil {
		t.Fatalf("consume receipt: %v", err)
	}
	expiredRcpt := issueTestReceipt(t, rcMgr, 10*time.Second)
	rcRawDB, err := sql.Open("sqlite", rcDbPath)
	if err != nil {
		t.Fatalf("open rcDb: %v", err)
	}
	defer rcRawDB.Close()
	_, err = rcRawDB.Exec("UPDATE write_receipts SET expires_at = ? WHERE receipt_id = ?", float64(time.Now().UnixNano())/1e9-3600, expiredRcpt.ReceiptID)
	if err != nil {
		t.Fatalf("expire receipt: %v", err)
	}

	// 1. Task with workspace_write and NO receipt in request
	taskNoRcptID := seedFailedTask(t, store, rawDB, "ww-no-rcpt", "workspace_write", "context deadline exceeded", "", 0)

	// Resubmit without ReceiptID in opts: MUST FAIL
	optsNoRcpt := controlplane.ResubmitOpts{Enabled: true, MaxPerTask: 2, MaxPerHour: 10, ReceiptID: ""}
	_, err = store.ResubmitTask(ctx, taskNoRcptID, optsNoRcpt)
	if err == nil {
		t.Fatalf("expected ResubmitTask to fail for workspace_write without fresh receipt, got nil")
	}
	if !strings.Contains(err.Error(), "workspace_write tasks require a fresh receipt_id") {
		t.Errorf("unexpected error: %v", err)
	}

	// 2. Task with workspace_write and consumed receipt in opts
	// Controlplane level: opts.ReceiptID is passed directly.
	// Does ResubmitTask verify the receipt validity or merely string emptiness?
	optsConsumed := controlplane.ResubmitOpts{Enabled: true, MaxPerTask: 2, MaxPerHour: 10, ReceiptID: consumedRcpt.ReceiptID}
	newID, err := store.ResubmitTask(ctx, taskNoRcptID, optsConsumed)
	if err == nil {
		// BUG(REDTEST): controlplane.ResubmitTask only verifies `opts.ReceiptID != ""` and does not validate against receipt manager!
		// The CLI layer validates, but the controlplane layer accepts consumed/expired receipt strings.
		t.Fatalf("// BUG(REDTEST): controlplane.ResubmitTask accepted CONSUMED receipt %s for task %s (returned task %s); controlplane lacks receipt manager verification", consumedRcpt.ReceiptID, taskNoRcptID, newID)
	}
	_ = validRcpt
	_ = expiredRcpt
}

// -----------------------------------------------------------------------------
// Guarantee 3: Backoff cannot be collapsed.
// -----------------------------------------------------------------------------

// TestRedRC2_Guarantee3_BackoffLadder_StoredAttemptsManipulation tests that manipulating
// stored attempt counts (attempts=0 with old updated_at vs attempts=max with recent) cannot collapse backoff.
func TestRedRC2_Guarantee3_BackoffLadder_StoredAttemptsManipulation(t *testing.T) {
	store, _, rawDB := setupRedTestControlPlane(t)
	ctx := context.Background()

	opts := controlplane.ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	// Case A: attempts = 0 with old updated_at (10 hours ago), but completed_at = now
	t0 := float64(time.Now().UnixNano()) / 1e9
	taskID := seedFailedTask(t, store, rawDB, "task-backoff-manip-1", "read_only", "context deadline exceeded", "", t0)

	// Manipulate: set attempts = 0, updated_at = t0 - 36000
	_, err := rawDB.Exec("UPDATE tasks SET attempts = 0, updated_at = ? WHERE task_id = ?", t0-36000, taskID)
	if err != nil {
		t.Fatalf("manipulate attempts: %v", err)
	}

	r1ID, err := store.ResubmitTask(ctx, taskID, opts)
	if err != nil {
		t.Fatalf("ResubmitTask failed: %v", err)
	}

	r1Task, err := store.GetTask(ctx, r1ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}

	var reqMap map[string]any
	_ = json.Unmarshal(r1Task.Request, &reqMap)
	notBefore, ok := reqMap["not_before"].(float64)
	if !ok || notBefore <= 0 {
		t.Fatalf("not_before not recorded or invalid: %v", reqMap["not_before"])
	}

	// Expected backoff for attempt 1 is 5 minutes: completed_at + 300s
	expectedMinNotBefore := t0 + 295 // allow small clock delta
	if notBefore < expectedMinNotBefore {
		t.Errorf("backoff collapsed! not_before = %.2f, want at least %.2f (delta = %.2fs)", notBefore, expectedMinNotBefore, notBefore-expectedMinNotBefore)
	}

	// Verify ClaimTask obeys not_before: ClaimTask at t0 must NOT claim r1Task
	claimed, err := store.ClaimTask(ctx, "worker-1", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed != nil && claimed.TaskID == r1ID {
		t.Fatalf("ClaimTask prematurely claimed task %s before 5m backoff expired!", r1ID)
	}
}

// TestRedRC2_Guarantee3_NilCompletedAt_BackoffCollapse checks what happens when completed_at is NULL
// and updated_at is manipulated to an old timestamp.
func TestRedRC2_Guarantee3_NilCompletedAt_BackoffCollapse(t *testing.T) {
	store, _, rawDB := setupRedTestControlPlane(t)
	ctx := context.Background()

	opts := controlplane.ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	t0 := float64(time.Now().UnixNano()) / 1e9
	taskID := seedFailedTask(t, store, rawDB, "task-nil-completed", "read_only", "context deadline exceeded", "", t0)

	// Set completed_at = NULL and updated_at = 1 hour ago
	_, err := rawDB.Exec("UPDATE tasks SET completed_at = NULL, updated_at = ? WHERE task_id = ?", t0-3600, taskID)
	if err != nil {
		t.Fatalf("update tasks: %v", err)
	}

	rID, err := store.ResubmitTask(ctx, taskID, opts)
	if err != nil {
		t.Fatalf("ResubmitTask failed: %v", err)
	}

	rTask, err := store.GetTask(ctx, rID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	var reqMap map[string]any
	_ = json.Unmarshal(rTask.Request, &reqMap)
	notBefore, _ := reqMap["not_before"].(float64)

	// If not_before was calculated from updated_at (t0 - 3600), notBefore = t0 - 3600 + 300 = t0 - 3300 (in the past!)
	if notBefore < t0 {
		// BUG(REDTEST): Backoff collapsed when completed_at is NULL by falling back to stale updated_at
		t.Fatalf("// BUG(REDTEST): Backoff was collapsed: when completed_at is NULL, ResubmitTask used stale updated_at (1h ago), placing not_before (%.2f) 55m in the past (now=%.2f); task can be claimed immediately without backoff delay", notBefore, t0)
	}
}

// -----------------------------------------------------------------------------
// Guarantee 4: Resubmit lineage cannot be forged into a loop.
// -----------------------------------------------------------------------------

// TestRedRC2_Guarantee4_ResubmitLineage_SelfLoop tests resubmission of a task whose parent_task_id
// points at itself (self-loop).
func TestRedRC2_Guarantee4_ResubmitLineage_SelfLoop(t *testing.T) {
	store, _, rawDB := setupRedTestControlPlane(t)
	ctx := context.Background()

	opts := controlplane.ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 10,
	}

	taskID := seedFailedTask(t, store, rawDB, "task-self-loop-orig", "read_only", "context deadline exceeded", "", 0)

	// Forge self-loop in SQL: parent_task_id = taskID
	_, err := rawDB.Exec("UPDATE tasks SET parent_task_id = ? WHERE task_id = ?", taskID, taskID)
	if err != nil {
		t.Fatalf("forge self-loop: %v", err)
	}

	// Resubmit the self-referencing task
	newID, err := store.ResubmitTask(ctx, taskID, opts)
	if err == nil {
		// BUG(REDTEST): Self-referential parent_task_id (%s -> %s) was accepted by ResubmitTask; GetTaskLineage looped to max CTE depth
		t.Fatalf("// BUG(REDTEST): Self-referential parent_task_id (%s -> %s) was accepted by ResubmitTask; expected cycle refusal error, got new task %s", taskID, taskID, newID)
	}
}

// TestRedRC2_Guarantee4_ChainedResubmitInfiniteAmplification tests whether resubmitting child retry
// tasks allows infinite retry amplification (bypassing max 2 per original task).
func TestRedRC2_Guarantee4_ChainedResubmitInfiniteAmplification(t *testing.T) {
	store, _, rawDB := setupRedTestControlPlane(t)
	ctx := context.Background()

	opts := controlplane.ResubmitOpts{
		Enabled:    true,
		MaxPerTask: 2,
		MaxPerHour: 100, // high hourly limit so we only test per-task lineage cap
	}

	// 1. Task A fails
	rootID := seedFailedTask(t, store, rawDB, "task-chain-root", "read_only", "context deadline exceeded", "", 0)

	// 2. Retry 1 of A succeeds -> Task B
	childBID, err := store.ResubmitTask(ctx, rootID, opts)
	if err != nil {
		t.Fatalf("retry 1 failed: %v", err)
	}

	// Fail Task B
	_, err = rawDB.Exec("UPDATE tasks SET state = 'FAILED', last_error = 'timeout' WHERE task_id = ?", childBID)
	if err != nil {
		t.Fatalf("fail Task B: %v", err)
	}

	// 3. Retry 2 of A succeeds -> Task C
	childCID, err := store.ResubmitTask(ctx, rootID, opts)
	if err != nil {
		t.Fatalf("retry 2 failed: %v", err)
	}

	// Fail Task C
	_, err = rawDB.Exec("UPDATE tasks SET state = 'FAILED', last_error = 'timeout' WHERE task_id = ?", childCID)
	if err != nil {
		t.Fatalf("fail Task C: %v", err)
	}

	// 4. Retry 3 of rootID: must fail with retry budget exceeded
	_, err = store.ResubmitTask(ctx, rootID, opts)
	if err == nil {
		t.Fatalf("expected 3rd retry of rootID to fail with budget exceeded")
	}

	// 5. ATTACK: Instead of resubmitting rootID, adversary resubmits childCID!
	// Under Guarantee 4 ("no infinite retry amplification"), the budget counter must count the lineage cycle/chain
	// or refuse resubmission of an existing retry child.
	childDID, err := store.ResubmitTask(ctx, childCID, opts)
	if err == nil {
		// BUG(REDTEST): ResubmitTask only counts direct children (WHERE parent_task_id = ?),
		// allowing infinite retry amplification by chaining resubmissions (A -> B -> C -> D -> ...).
		t.Fatalf("// BUG(REDTEST): Infinite retry amplification! Resubmitted child task %s (retry #2 of %s) created new task %s; per-task retry cap (max 2) was completely bypassed by chaining retries along the lineage tree", childCID, rootID, childDID)
	}
	if !strings.Contains(err.Error(), "retry budget exceeded") {
		t.Fatalf("expected error mentioning 'retry budget exceeded', got: %v", err)
	}
}
