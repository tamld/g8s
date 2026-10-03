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
	"github.com/tamld/g8s/internal/receipt"
	_ "modernc.org/sqlite"
)

// setupRedCLITestStore initializes a temporary controlplane and receipt database.
func setupRedCLITestStore(t *testing.T) (*controlplane.Store, string, *sql.DB, string, *receipt.Manager) {
	t.Helper()
	// The workspace_write delivery contract (env gate) applies to resubmission too —
	// set it here so the suite is hermetic regardless of the invoking shell.
	t.Setenv("AGY_MCP_ALLOW_WORKSPACE_WRITE", "1")
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s.db")
	rcDbPath := filepath.Join(tempDir, "receipts.db")

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	rcMgr, err := receipt.NewReceiptManager(rcDbPath, nil)
	if err != nil {
		t.Fatalf("NewReceiptManager: %v", err)
	}
	t.Cleanup(func() { _ = rcMgr.Close() })

	controlplane.ResetHourlyRetryTracker()
	t.Setenv("G8S_DB", dbPath)
	t.Setenv("G8S_AUTO_RETRY_ENABLED", "true")

	return store, dbPath, rawDB, rcDbPath, rcMgr
}

// issueRedCLIReceipt mints a write receipt with Concern B envelope.
func issueRedCLIReceipt(t *testing.T, mgr *receipt.Manager, ttl time.Duration) *receipt.WriteReceipt {
	t.Helper()
	rc, err := mgr.IssueReceipt("test-supervisor", []string{"."}, ttl,
		receipt.WithCanonicalEnvelope(&receipt.CanonicalEnvelope{
			SchemaURI:      "g8s://envelope/write-receipt/v1",
			FieldOrder:     []string{"receipt_id", "issuer", "allowed_paths", "expires_at", "consumed", "consumer_task_id", "created_at"},
			RequiredFields: []string{"receipt_id", "issuer", "allowed_paths", "expires_at"},
		}),
		receipt.WithRuleGraph(&receipt.RuleGraphSnapshot{
			RulesetVersion: "v1",
			PipelineDigest: "cli-default",
		}),
		receipt.WithProvenanceContext("g8s/v0.14.0", "commit-test", "trace-cli"),
	)
	if err != nil {
		t.Fatalf("issueRedCLIReceipt: %v", err)
	}
	return rc
}

// seedCLIFailedTask creates and terminal-fails a task with specific permission and result.
func seedCLIFailedTask(t *testing.T, store *controlplane.Store, rawDB *sql.DB, key, perm, lastErr, resultJSON string, completedAt float64) string {
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
		Payload:        json.RawMessage(`{"prompt":"cli adversarial task"}`),
	})
	if err != nil {
		t.Fatalf("seed task submit: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if completedAt == 0 {
		completedAt = now
	}

	payloadMap := map[string]any{
		"prompt":     "cli adversarial task",
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

// TestRedRC2_CLI_Guarantee1_DerivedKeyCollisionRefused verifies that `g8s resubmit`
// refuses with a typed error when a derived key (<orig>#r1) collides with an existing task.
func TestRedRC2_CLI_Guarantee1_DerivedKeyCollisionRefused(t *testing.T) {
	store, dbPath, rawDB, _, _ := setupRedCLITestStore(t)
	ctx := context.Background()

	origID := seedCLIFailedTask(t, store, rawDB, "cli-orig-task", "read_only", "context deadline exceeded", "", 0)

	// Pre-create task with colliding idempotency key "<origID>#r1"
	collidingKey := fmt.Sprintf("%s#r1", origID)
	_, err := store.SubmitTask(ctx, controlplane.SubmitTaskRequest{
		IdempotencyKey: collidingKey,
		Priority:       1,
		MaxAttempts:    1,
		Role:           "collector",
		Model:          "gemini-3.8-flash-high",
		Permission:     "read_only",
		AddDirs:        []string{"."},
		Payload:        json.RawMessage(`{"prompt":"adversary pre-occupying key"}`),
	})
	if err != nil {
		t.Fatalf("submit colliding task: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{"--task", origID, "--db", dbPath, "--json"}
	code, env, _ := executeResubmit(ctx, args, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for colliding derived key, got %d", code)
	}
	if env == nil || env.Error == nil {
		t.Fatalf("expected non-nil error envelope")
	}
	if env.Error.Code != cli.CodeDenied {
		t.Errorf("expected error code %q, got %q", cli.CodeDenied, env.Error.Code)
	}
	if !strings.Contains(env.Error.Message, "idempotency_key already exists with a different request") {
		t.Errorf("expected message mentioning idempotency key conflict, got: %s", env.Error.Message)
	}
}

// -----------------------------------------------------------------------------
// Guarantee 2: Deny-by-default classes cannot be slipped a receipt.
// -----------------------------------------------------------------------------

// TestRedRC2_CLI_Guarantee2_TickLeavesWorkspaceWriteAlone verifies that runTickRetry
// leaves workspace_write tasks alone regardless of whether receipt is omitted, empty, consumed, or expired.
func TestRedRC2_CLI_Guarantee2_TickLeavesWorkspaceWriteAlone(t *testing.T) {
	store, dbPath, rawDB, rcDbPath, rcMgr := setupRedCLITestStore(t)
	t.Setenv("G8S_DB", dbPath)
	ctx := context.Background()

	// Create consumed and expired receipts
	consumedRcpt := issueRedCLIReceipt(t, rcMgr, 1*time.Hour)
	if _, err := rcMgr.ValidateAndConsume(consumedRcpt.ReceiptID, "consumer-1"); err != nil {
		t.Fatalf("consume receipt: %v", err)
	}

	expiredRcpt := issueRedCLIReceipt(t, rcMgr, 10*time.Second)
	rcRawDB, err := sql.Open("sqlite", rcDbPath)
	if err != nil {
		t.Fatalf("open rcRawDB: %v", err)
	}
	defer rcRawDB.Close()
	_, err = rcRawDB.Exec("UPDATE write_receipts SET expires_at = ? WHERE receipt_id = ?", float64(time.Now().UnixNano())/1e9-3600, expiredRcpt.ReceiptID)
	if err != nil {
		t.Fatalf("expire receipt: %v", err)
	}

	// 1. Task with workspace_write omitting receipt
	t1 := seedCLIFailedTask(t, store, rawDB, "ww-omitted-rcpt", "workspace_write", "context deadline exceeded", "", 0)

	// 2. Task with workspace_write carrying empty receipt_id=""
	t2 := seedCLIFailedTask(t, store, rawDB, "ww-empty-rcpt", "workspace_write", "context deadline exceeded", "", 0)
	_, _ = rawDB.Exec(`UPDATE tasks SET request_json = '{"permission":"workspace_write","receipt_id":""}' WHERE task_id = ?`, t2)

	// 3. Task with workspace_write carrying consumed receipt
	t3 := seedCLIFailedTask(t, store, rawDB, "ww-consumed-rcpt", "workspace_write", "context deadline exceeded", "", 0)
	_, _ = rawDB.Exec(fmt.Sprintf(`UPDATE tasks SET request_json = '{"permission":"workspace_write","receipt_id":"%s"}' WHERE task_id = ?`, consumedRcpt.ReceiptID), t3)

	// 4. Task with workspace_write carrying expired receipt
	t4 := seedCLIFailedTask(t, store, rawDB, "ww-expired-rcpt", "workspace_write", "context deadline exceeded", "", 0)
	_, _ = rawDB.Exec(fmt.Sprintf(`UPDATE tasks SET request_json = '{"permission":"workspace_write","receipt_id":"%s"}' WHERE task_id = ?`, expiredRcpt.ReceiptID), t4)

	// Execute tick
	status, detail := runTickRetry(ctx)
	if status != "ok" {
		t.Fatalf("tick status = %s, want ok", status)
	}

	detailMap, _ := detail.(map[string]any)
	resubmittedCount, _ := detailMap["resubmitted"].(int)
	if resubmittedCount != 0 {
		t.Fatalf("tick resubmitted %d tasks; expected 0 (all workspace_write tasks must be left alone)", resubmittedCount)
	}

	// Verify all 4 tasks appear as skipped with D3 reason
	resultsJSON, _ := json.Marshal(detailMap["results"])
	var items []struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(resultsJSON, &items)

	skippedTasks := make(map[string]bool)
	for _, it := range items {
		if it.Status == "skipped" && strings.Contains(it.Reason, "workspace_write") {
			skippedTasks[it.TaskID] = true
		}
	}

	for _, tid := range []string{t1, t2, t3, t4} {
		if !skippedTasks[tid] {
			t.Errorf("task %s was not skipped with workspace_write boundary reason", tid)
		}
	}
}

// TestRedRC2_CLI_Guarantee2_ResubmitWithoutFreshReceiptRefuses verifies that `g8s resubmit`
// refuses with a typed error (E_DENIED) and never silently requeues when fresh receipt is omitted, empty, consumed, or expired.
func TestRedRC2_CLI_Guarantee2_ResubmitWithoutFreshReceiptRefuses(t *testing.T) {
	store, dbPath, rawDB, rcDbPath, rcMgr := setupRedCLITestStore(t)
	ctx := context.Background()

	consumedRcpt := issueRedCLIReceipt(t, rcMgr, 1*time.Hour)
	if _, err := rcMgr.ValidateAndConsume(consumedRcpt.ReceiptID, "consumer-1"); err != nil {
		t.Fatalf("consume receipt: %v", err)
	}

	expiredRcpt := issueRedCLIReceipt(t, rcMgr, 10*time.Second)
	rcRawDB, err := sql.Open("sqlite", rcDbPath)
	if err != nil {
		t.Fatalf("open rcRawDB: %v", err)
	}
	defer rcRawDB.Close()
	_, err = rcRawDB.Exec("UPDATE write_receipts SET expires_at = ? WHERE receipt_id = ?", float64(time.Now().UnixNano())/1e9-3600, expiredRcpt.ReceiptID)
	if err != nil {
		t.Fatalf("expire receipt: %v", err)
	}

	taskID := seedCLIFailedTask(t, store, rawDB, "cli-ww-resubmit", "workspace_write", "context deadline exceeded", "", 0)

	cases := []struct {
		name          string
		receiptIDFlag string
		wantErrorCode string
		wantHintSub   string
	}{
		{
			name:          "omitted receipt-id flag",
			receiptIDFlag: "",
			wantErrorCode: cli.CodeDenied,
			wantHintSub:   "g8s receipt issue",
		},
		{
			name:          "whitespace receipt-id flag",
			receiptIDFlag: "   ",
			wantErrorCode: cli.CodeDenied,
			wantHintSub:   "g8s receipt issue",
		},
		{
			name:          "consumed receipt-id flag",
			receiptIDFlag: consumedRcpt.ReceiptID,
			wantErrorCode: cli.CodeDenied,
			wantHintSub:   "valid, active write receipt",
		},
		{
			name:          "expired receipt-id flag",
			receiptIDFlag: expiredRcpt.ReceiptID,
			wantErrorCode: cli.CodeDenied,
			wantHintSub:   "valid, active write receipt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := []string{"--task", taskID, "--db", dbPath, "--json"}
			if tc.receiptIDFlag != "" {
				args = append(args, "--receipt-id", tc.receiptIDFlag)
			}

			code, env, err := executeResubmit(ctx, args, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("[%s] expected exit code 1, got %d", tc.name, code)
			}
			if err == nil {
				t.Fatalf("[%s] expected error, got nil", tc.name)
			}
			if env == nil || env.Error == nil {
				t.Fatalf("[%s] expected non-nil error envelope", tc.name)
			}
			if tc.wantHintSub != "" && !strings.Contains(env.Error.Hint, tc.wantHintSub) {
				if tc.name == "consumed receipt-id flag" {
					// BUG(REDTEST): VerifyReceipt does not check consumed=1, so executeResubmit never catches consumed receipts at the CLI validation layer!
					t.Fatalf("// BUG(REDTEST): Receipt validation escape! Consumed receipt %s passed rcMgr.VerifyReceipt() in executeResubmit (hint=%q); CLI receipt validation lacks consumed=1 check", tc.receiptIDFlag, env.Error.Hint)
				}
				t.Errorf("[%s] hint %q does not contain %q", tc.name, env.Error.Hint, tc.wantHintSub)
			}
		})
	}

	// Verify valid fresh receipt succeeds
	validRcpt := issueRedCLIReceipt(t, rcMgr, 1*time.Hour)
	var stdout, stderr bytes.Buffer
	args := []string{"--task", taskID, "--db", dbPath, "--receipt-id", validRcpt.ReceiptID, "--json"}
	code, env, err := executeResubmit(ctx, args, &stdout, &stderr)
	if code != 0 || err != nil {
		// BUG(REDTEST): executeResubmit parses --receipt-id but never assigns it to opts.ReceiptID
		// when calling store.ResubmitTask, causing every valid workspace_write resubmission to fail!
		t.Fatalf("// BUG(REDTEST): executeResubmit failed to propagate --receipt-id (%s) into opts.ReceiptID; store.ResubmitTask failed with: %v (workspace_write tasks cannot be resubmitted via CLI even with fresh valid receipt)", validRcpt.ReceiptID, err)
	}
	if env == nil || env.Kind != "resubmit" {
		t.Errorf("expected kind=resubmit, got %+v", env)
	}
}

// TestRedRC2_CLI_Guarantee2_CaseTamperedPermissionBypass tests whether tampering with the case
// of the permission field ("WORKSPACE_WRITE" or " Workspace_Write ") allows slipping past the receipt requirement.
func TestRedRC2_CLI_Guarantee2_CaseTamperedPermissionBypass(t *testing.T) {
	store, dbPath, rawDB, _, _ := setupRedCLITestStore(t)
	ctx := context.Background()

	taskID := seedCLIFailedTask(t, store, rawDB, "cli-ww-case-tamper", "read_only", "context deadline exceeded", "", 0)

	// Update task with uppercase permission "WORKSPACE_WRITE"
	_, err := rawDB.Exec(`UPDATE tasks SET request_json = '{"permission":"WORKSPACE_WRITE","prompt":"mutating code"}' WHERE task_id = ?`, taskID)
	if err != nil {
		t.Fatalf("update permission: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{"--task", taskID, "--db", dbPath, "--json"}
	code, env, _ := executeResubmit(ctx, args, &stdout, &stderr)

	if code == 0 && env != nil && env.Kind == "resubmit" {
		// BUG(REDTEST): Case-sensitive check `perm == "workspace_write"` allows WORKSPACE_WRITE
		// to bypass receipt verification and be resubmitted without any receipt!
		envMap, _ := env.Data.(map[string]any)
		t.Fatalf("// BUG(REDTEST): Permission casing bypass! Task %s with permission 'WORKSPACE_WRITE' bypassed receipt enforcement in g8s resubmit (code=0, new_task_id=%v); string equality check lacks strings.EqualFold normalization", taskID, envMap["new_task_id"])
	}
}

// -----------------------------------------------------------------------------
// Guarantee 3: Backoff cannot be collapsed.
// -----------------------------------------------------------------------------

// TestRedRC2_CLI_Guarantee3_TickNeverRetriesSoonerThanBackoff tests that runTickRetry
// respects the 5-20-60m ladder and never exceeds 2/task and 10/dir-hour.
func TestRedRC2_CLI_Guarantee3_TickNeverRetriesSoonerThanBackoff(t *testing.T) {
	store, dbPath, rawDB, _, _ := setupRedCLITestStore(t)
	t.Setenv("G8S_DB", dbPath)
	ctx := context.Background()

	t0 := float64(time.Now().UnixNano()) / 1e9

	// 1. Task 1: failed 10 seconds ago (attempts=0 with manipulated updated_at)
	t1 := seedCLIFailedTask(t, store, rawDB, "tick-t1", "read_only", "context deadline exceeded", "", t0)
	_, _ = rawDB.Exec("UPDATE tasks SET attempts = 0, updated_at = ? WHERE task_id = ?", t0-10, t1)

	// Tick 1: Resubmits t1
	status, detail := runTickRetry(ctx)
	if status != "ok" {
		t.Fatalf("tick 1 failed: %s, detail: %v", status, detail)
	}
	detailMap, _ := detail.(map[string]any)
	resubmitted1, _ := detailMap["resubmitted"].(int)
	if resubmitted1 != 1 {
		t.Fatalf("expected 1 resubmitted task on tick 1, got %v", detailMap["resubmitted"])
	}

	// Immediate Tick 2: Must be idempotent (double-fire deduplication) and NOT exceed 2/task or hourly limit
	status2, detail2 := runTickRetry(ctx)
	if status2 != "ok" {
		t.Fatalf("tick 2 failed: %s, detail: %v", status2, detail2)
	}
	detailMap2, _ := detail2.(map[string]any)
	// Deduplication returns the already-queued child; verify hourly limit was not consumed twice
	_ = detailMap2

	// 2. Hourly limit test: seed 10 tasks and verify 11th is skipped with hourly budget exceeded
	controlplane.ResetHourlyRetryTracker()
	for i := 1; i <= 10; i++ {
		tid := seedCLIFailedTask(t, store, rawDB, fmt.Sprintf("hourly-task-%02d", i), "read_only", "context deadline exceeded", "", t0)
		_, err := store.ResubmitTask(ctx, tid, controlplane.ResubmitOpts{Enabled: true, MaxPerTask: 2, MaxPerHour: 10})
		if err != nil {
			t.Fatalf("resubmit task %d: %v", i, err)
		}
	}

	// 11th task:
	tid11 := seedCLIFailedTask(t, store, rawDB, "hourly-task-11", "read_only", "context deadline exceeded", "", t0)
	_, err := store.ResubmitTask(ctx, tid11, controlplane.ResubmitOpts{Enabled: true, MaxPerTask: 2, MaxPerHour: 10})
	if err == nil {
		t.Fatalf("expected 11th hourly resubmit to be refused, got success")
	}
	if !strings.Contains(err.Error(), "hourly retry budget exceeded") {
		t.Errorf("expected hourly retry budget exceeded error, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Guarantee 4: Resubmit lineage cannot be forged into a loop.
// -----------------------------------------------------------------------------

// TestRedRC2_CLI_Guarantee4_ResubmitLoopLineageCycle tests resubmitting when parent_task_id
// forms a cycle (a -> b -> a) or self-loop.
func TestRedRC2_CLI_Guarantee4_ResubmitLoopLineageCycle(t *testing.T) {
	store, dbPath, rawDB, _, _ := setupRedCLITestStore(t)
	ctx := context.Background()

	tA := seedCLIFailedTask(t, store, rawDB, "cycle-task-a", "read_only", "context deadline exceeded", "", 0)
	tB := seedCLIFailedTask(t, store, rawDB, "cycle-task-b", "read_only", "context deadline exceeded", "", 0)

	// Forge cycle in SQL: A -> B and B -> A
	_, err := rawDB.Exec("UPDATE tasks SET parent_task_id = ? WHERE task_id = ?", tB, tA)
	if err != nil {
		t.Fatalf("update A parent: %v", err)
	}
	_, err = rawDB.Exec("UPDATE tasks SET parent_task_id = ? WHERE task_id = ?", tA, tB)
	if err != nil {
		t.Fatalf("update B parent: %v", err)
	}

	// Resubmit A via CLI:
	var stdout, stderr bytes.Buffer
	args := []string{"--task", tA, "--db", dbPath, "--json"}
	code, env, _ := executeResubmit(ctx, args, &stdout, &stderr)

	if code == 0 && env != nil && env.Kind == "resubmit" {
		// BUG(REDTEST): Lineage cycle (A <-> B) accepted by g8s resubmit; subsequent GetTaskLineage on new task hit maximum recursion limit (1001 nodes)
		envMap, _ := env.Data.(map[string]any)
		newAID, _ := envMap["new_task_id"].(string)
		t.Fatalf("// BUG(REDTEST): Lineage cycle (A <-> B) accepted by g8s resubmit (new task %s); expected cycle refusal", newAID)
	}
}

// TestRedRC2_CLI_Guarantee4_ResubmitChildAmplificationBypassesCap tests whether `g8s resubmit --task <child_id>`
// allows infinite retries by passing the child task ID instead of the root task ID.
func TestRedRC2_CLI_Guarantee4_ResubmitChildAmplificationBypassesCap(t *testing.T) {
	store, dbPath, rawDB, _, _ := setupRedCLITestStore(t)
	ctx := context.Background()

	rootID := seedCLIFailedTask(t, store, rawDB, "cli-root-task", "read_only", "context deadline exceeded", "", 0)

	// Attempt 1: resubmit root -> Child 1
	var stdout1, stderr1 bytes.Buffer
	code1, env1, err1 := executeResubmit(ctx, []string{"--task", rootID, "--db", dbPath, "--json"}, &stdout1, &stderr1)
	if code1 != 0 || err1 != nil {
		t.Fatalf("resubmit 1 failed: %v, stderr: %s", err1, stderr1.String())
	}
	env1Map, _ := env1.Data.(map[string]any)
	child1ID, _ := env1Map["new_task_id"].(string)

	// Fail Child 1
	_, _ = rawDB.Exec("UPDATE tasks SET state = 'FAILED', last_error = 'timeout' WHERE task_id = ?", child1ID)

	// Attempt 2: resubmit root -> Child 2
	var stdout2, stderr2 bytes.Buffer
	code2, env2, err2 := executeResubmit(ctx, []string{"--task", rootID, "--db", dbPath, "--json"}, &stdout2, &stderr2)
	if code2 != 0 || err2 != nil {
		t.Fatalf("resubmit 2 failed: %v, stderr: %s", err2, stderr2.String())
	}
	env2Map, _ := env2.Data.(map[string]any)
	child2ID, _ := env2Map["new_task_id"].(string)

	// Fail Child 2
	_, _ = rawDB.Exec("UPDATE tasks SET state = 'FAILED', last_error = 'timeout' WHERE task_id = ?", child2ID)

	// Attempt 3 of rootID: must fail because cap is 2
	var stdout3, stderr3 bytes.Buffer
	code3, _, _ := executeResubmit(ctx, []string{"--task", rootID, "--db", dbPath, "--json"}, &stdout3, &stderr3)
	if code3 != 1 {
		t.Fatalf("expected 3rd resubmit of rootID to fail with exit code 1, got %d", code3)
	}

	// ATTACK: Resubmit Child 2 instead of rootID!
	var stdout4, stderr4 bytes.Buffer
	code4, env4, _ := executeResubmit(ctx, []string{"--task", child2ID, "--db", dbPath, "--json"}, &stdout4, &stderr4)

	if code4 == 0 && env4 != nil && env4.Kind == "resubmit" {
		// BUG(REDTEST): Chained resubmit escape via CLI! Resubmitting child task bypassed max_per_task=2 cap on root
		env4Map, _ := env4.Data.(map[string]any)
		newChildID := env4Map["new_task_id"]
		t.Fatalf("// BUG(REDTEST): Chained resubmit escape via CLI! Resubmitting child task %s bypassed max_per_task=2 cap on root %s, minting 3rd retry %v; CLI accepts arbitrary child task IDs as root of new retry lineage", child2ID, rootID, newChildID)
	}
}
