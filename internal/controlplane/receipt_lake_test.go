package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReceiptLakeMigrationIdempotent(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()

	// Verify schema version is bumped to 5
	raw := openRawDB(t, path)
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d, want %d (5)", version, SchemaVersion)
	}

	// Verify all receipt lake columns exist
	colRows, err := raw.Query("PRAGMA table_info(tasks)")
	if err != nil {
		t.Fatalf("table_info(tasks): %v", err)
	}
	defer colRows.Close()
	cols := map[string]string{}
	for colRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := colRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = colType
	}
	if err := colRows.Err(); err != nil {
		t.Fatalf("iterate table_info: %v", err)
	}

	for _, col := range []string{"orchestrator_id", "worktree_id", "worker_name", "iter"} {
		if _, ok := cols[col]; !ok {
			t.Errorf("missing column %q in tasks table", col)
		}
	}

	// Verify index exists
	var indexCount int
	err = raw.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_tasks_orchestrator_iter'").Scan(&indexCount)
	if err != nil {
		t.Fatalf("query index: %v", err)
	}
	if indexCount != 1 {
		t.Errorf("idx_tasks_orchestrator_iter missing, count = %d", indexCount)
	}

	// Idempotency: Re-open the database; initialize should be a fast-path no-op
	reopened, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	reopened.Close()

	// Idempotency: Explicitly run migrateReceiptLake twice directly on the connection
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire raw connection: %v", err)
	}
	defer conn.Close()

	if err := migrateReceiptLake(conn); err != nil {
		t.Fatalf("migrateReceiptLake rerun 1 failed: %v", err)
	}
	if err := migrateReceiptLake(conn); err != nil {
		t.Fatalf("migrateReceiptLake rerun 2 failed: %v", err)
	}
}

func TestReceiptLakeMigrationFromV4(t *testing.T) {
	store, path := newTestStore(t)
	store.Close()

	// Simulate a v4 schema: drop the new columns and set user_version = 4
	raw := openRawDB(t, path)
	_, _ = raw.Exec("DROP INDEX IF EXISTS idx_tasks_orchestrator_iter")
	for _, col := range []string{"orchestrator_id", "worktree_id", "worker_name", "iter"} {
		if _, err := raw.Exec("ALTER TABLE tasks DROP COLUMN " + col); err != nil {
			t.Fatalf("drop column %s: %v", col, err)
		}
	}
	if _, err := raw.Exec("PRAGMA user_version = 4"); err != nil {
		t.Fatalf("set user_version = 4: %v", err)
	}
	raw.Close()

	// Open with NewControlPlane, which should detect v4 and migrate to v5
	migratedStore, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("open and migrate v4 db: %v", err)
	}
	defer migratedStore.Close()

	check := openRawDB(t, path)
	var version int
	if err := check.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 5 {
		t.Errorf("user_version = %d, want 5 after migration", version)
	}

	// Verify columns were added back
	colRows, err := check.Query("PRAGMA table_info(tasks)")
	if err != nil {
		t.Fatalf("table_info(tasks): %v", err)
	}
	defer colRows.Close()
	cols := map[string]bool{}
	for colRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := colRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = true
	}
	for _, col := range []string{"orchestrator_id", "worktree_id", "worker_name", "iter"} {
		if !cols[col] {
			t.Errorf("expected column %s present after v4 migration", col)
		}
	}
}

func TestReceiptLakeMetricQuery(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()

	raw := openRawDB(t, path)

	orchID := "orch-123"
	wtID := "wt-456"
	worker := "agy"
	iter := 2

	// Submit task with receipt lake metadata
	req := SubmitTaskRequest{
		IdempotencyKey: "test-lake-key-1",
		Priority:       10,
		MaxAttempts:    3,
		Payload:        json.RawMessage(`{"prompt":"test prompt"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{t.TempDir()},
		OrchestratorID: &orchID,
		WorktreeID:     &wtID,
		WorkerName:     &worker,
		Iter:           iter,
	}

	task, err := store.SubmitTask(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	if task.OrchestratorID == nil || *task.OrchestratorID != orchID {
		t.Errorf("OrchestratorID = %v, want %s", task.OrchestratorID, orchID)
	}
	if task.WorktreeID == nil || *task.WorktreeID != wtID {
		t.Errorf("WorktreeID = %v, want %s", task.WorktreeID, wtID)
	}
	if task.WorkerName == nil || *task.WorkerName != worker {
		t.Errorf("WorkerName = %v, want %s", task.WorkerName, worker)
	}
	if task.Iter != iter {
		t.Errorf("Iter = %d, want %d", task.Iter, iter)
	}

	// Insert several tasks with different worker names and states directly
	now := float64(time.Now().UnixNano()) / 1e9
	testTasks := []struct {
		id     string
		worker string
		state  string
		orchID string
		iter   int
	}{
		{"t1", "agy", StateSucceeded, "orch-1", 1},
		{"t2", "agy", StateSucceeded, "orch-1", 2},
		{"t3", "agy", StateFailed, "orch-1", 3},
		{"t4", "codex", StateSucceeded, "orch-2", 1},
		{"t5", "agy", StateSucceeded, "orch-2", 1},
		{"t6", "claude-cli", StateQueued, "orch-2", 1},
	}

	for _, tt := range testTasks {
		_, err := raw.Exec(`
			INSERT INTO tasks(
				task_id, idempotency_key, schema_version, state, priority,
				request_json, request_hash, attempts, max_attempts, cancel_requested,
				created_at, updated_at, orchestrator_id, worktree_id, worker_name, iter
			) VALUES (?, ?, 'agy.task.v1', ?, 0, '{}', 'hash', 0, 3, 0, ?, ?, ?, 'wt', ?, ?)`,
			tt.id, tt.id+"-key", tt.state, now, now, tt.orchID, tt.worker, tt.iter)
		if err != nil {
			t.Fatalf("insert task %s: %v", tt.id, err)
		}
	}

	// Run metric query: SELECT COUNT(*) FROM tasks WHERE worker_name='agy' AND (state='succeeded' OR state='SUCCEEDED')
	var count int
	err = raw.QueryRow(
		"SELECT COUNT(*) FROM tasks WHERE worker_name = 'agy' AND (state = 'succeeded' OR state = 'SUCCEEDED')",
	).Scan(&count)
	if err != nil {
		t.Fatalf("metric query: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 succeeded agy tasks, got %d", count)
	}

	// Verify querying via lower(state) works seamlessly:
	err = raw.QueryRow(
		"SELECT COUNT(*) FROM tasks WHERE worker_name = 'agy' AND lower(state) = 'succeeded'",
	).Scan(&count)
	if err != nil {
		t.Fatalf("metric query lower(state): %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 succeeded agy tasks with lower(state), got %d", count)
	}

	// Verify line / list task retrieval includes the fields
	gotTask, err := store.GetTask(context.Background(), task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask == nil || gotTask.WorkerName == nil || *gotTask.WorkerName != "agy" {
		t.Errorf("GetTask worker_name mismatch: %+v", gotTask)
	}

	tasks, err := store.ListTasks(context.Background(), TaskFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Errorf("ListTasks returned empty slice")
	}
}

func TestStoreEdgeCasesAndValidation(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// deref test
	if deref(nil) != "" {
		t.Errorf("deref(nil) should be empty")
	}
	s := "hello"
	if deref(&s) != "hello" {
		t.Errorf("deref(&s) should be hello")
	}

	// Lineage with empty task ID
	lineage, err := store.GetTaskLineage(ctx, "")
	if err != nil || lineage != nil {
		t.Errorf("GetTaskLineage empty: got (%v, %v), want (nil, nil)", lineage, err)
	}

	// ListChildTasks validation
	if _, err := store.ListChildTasks(ctx, ""); err == nil {
		t.Errorf("ListChildTasks with empty parent should error")
	}

	// ListTasks validation
	if _, err := store.ListTasks(ctx, TaskFilter{Limit: 0}); err != nil {
		t.Errorf("ListTasks with Limit 0 should default to 50: %v", err)
	}
	if _, err := store.ListTasks(ctx, TaskFilter{Limit: -1}); err == nil {
		t.Errorf("ListTasks with negative limit should error")
	}
	if _, err := store.ListTasks(ctx, TaskFilter{Limit: 500}); err == nil {
		t.Errorf("ListTasks with Limit > 200 should error")
	}
	invalidState := "NON_EXISTENT_STATE"
	if _, err := store.ListTasks(ctx, TaskFilter{State: &invalidState}); err == nil {
		t.Errorf("ListTasks with invalid state should error")
	}

	// ClaimTask validation
	if _, err := store.ClaimTask(ctx, "", 10); err == nil {
		t.Errorf("ClaimTask with empty workerID should error")
	}
	if _, err := store.ClaimTask(ctx, "w1", 0); err == nil {
		t.Errorf("ClaimTask with non-positive leaseDuration should error")
	}

	// Heartbeat validation
	if err := store.Heartbeat(ctx, "nonexistent", "w1", "", 10); err == nil {
		t.Errorf("Heartbeat with empty token should fail")
	}
	if err := store.Heartbeat(ctx, "nonexistent", "w1", "bad-token", 10); err == nil {
		t.Errorf("Heartbeat on nonexistent task should fail")
	}

	// ExecutionSignal
	if sig := store.ExecutionSignal("nonexistent", "w1", "tok"); sig != "lease_lost" {
		t.Errorf("ExecutionSignal on nonexistent task = %s, want lease_lost", sig)
	}

	// prepareSubmitRequest validation
	emptyStr := ""
	longKey := strings.Repeat("x", 201)
	if _, err := store.SubmitTask(ctx, SubmitTaskRequest{IdempotencyKey: ""}); err == nil {
		t.Errorf("SubmitTask empty key should fail")
	}
	if _, err := store.SubmitTask(ctx, SubmitTaskRequest{IdempotencyKey: longKey}); err == nil {
		t.Errorf("SubmitTask >200 char key should fail")
	}
	if _, err := store.SubmitTask(ctx, SubmitTaskRequest{IdempotencyKey: "k", ParentTaskID: &emptyStr}); err == nil {
		t.Errorf("SubmitTask empty ParentTaskID should fail")
	}
	if _, err := store.SubmitTask(ctx, SubmitTaskRequest{IdempotencyKey: "k", Priority: -101}); err == nil {
		t.Errorf("SubmitTask priority -101 should fail")
	}
	if _, err := store.SubmitTask(ctx, SubmitTaskRequest{IdempotencyKey: "k", Priority: 101}); err == nil {
		t.Errorf("SubmitTask priority 101 should fail")
	}
	if _, err := store.SubmitTask(ctx, SubmitTaskRequest{IdempotencyKey: "k", MaxAttempts: 15}); err == nil {
		t.Errorf("SubmitTask max_attempts > 10 should fail")
	}

	// Check checkSchemaVersion with negative version
	raw := openRawDB(t, path)
	conn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = -1"); err == nil {
		if err := checkSchemaVersion(conn); err == nil {
			t.Errorf("checkSchemaVersion with negative version should error")
		}
	}
}

func TestStoreLifecycleAndClaimBranches(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	// 1. SubmitTask parent not found
	missingParent := "non-existent-parent"
	_, err := store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "t-parent-err",
		Priority:       0,
		MaxAttempts:    3,
		ParentTaskID:   &missingParent,
		Payload:        json.RawMessage(`{"prompt":"hi"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{t.TempDir()},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown parent task") {
		t.Errorf("SubmitTask with missing parent want 'unknown parent task', got %v", err)
	}

	// 2. SubmitTask collision with different request payload
	t1, err := store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "idem-collision",
		Priority:       0,
		MaxAttempts:    3,
		Payload:        json.RawMessage(`{"prompt":"prompt 1"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{t.TempDir()},
	})
	if err != nil {
		t.Fatalf("SubmitTask t1: %v", err)
	}
	_, err = store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "idem-collision",
		Priority:       0,
		MaxAttempts:    3,
		Payload:        json.RawMessage(`{"prompt":"prompt 2 (different)"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{t.TempDir()},
	})
	if err == nil || !strings.Contains(err.Error(), "idempotency_key already exists with a different request") {
		t.Errorf("SubmitTask different payload collision want error, got %v", err)
	}

	// 3. ClaimTask candidate == nil
	emptyStore, _ := newTestStore(t)
	defer emptyStore.Close()
	candidate, err := emptyStore.ClaimTask(ctx, "worker-1", 30)
	if err != nil || candidate != nil {
		t.Errorf("ClaimTask on empty queue want (nil, nil), got (%v, %v)", candidate, err)
	}

	// 4. ClaimTask under active maintenance
	raw := openRawDB(t, path)
	now := float64(time.Now().UnixNano()) / 1e9
	_, err = raw.Exec("INSERT INTO control_plane_maintenance(singleton, owner, expires_at, updated_at) VALUES (1, 'maint-owner', ?, ?)", now+1000, now)
	if err != nil {
		t.Fatalf("insert maintenance: %v", err)
	}
	maintClaim, err := store.ClaimTask(ctx, "worker-1", 30)
	if err != nil || maintClaim != nil {
		t.Errorf("ClaimTask under active maintenance want (nil, nil), got (%v, %v)", maintClaim, err)
	}

	// 5. ClaimTask under expired maintenance (should clear maintenance and claim)
	_, err = raw.Exec("UPDATE control_plane_maintenance SET expires_at = ?", now-10)
	if err != nil {
		t.Fatalf("expire maintenance: %v", err)
	}
	claimed, err := store.ClaimTask(ctx, "worker-1", 30)
	if err != nil || claimed == nil || claimed.TaskID != t1.TaskID {
		t.Errorf("ClaimTask after expired maintenance failed: got (%v, %v)", claimed, err)
	}

	// 6. StartTask
	if !store.StartTask(claimed.TaskID, "worker-1", *claimed.LeaseToken) {
		t.Errorf("StartTask on valid claim returned false")
	}
	if store.StartTask(claimed.TaskID, "wrong-worker", *claimed.LeaseToken) {
		t.Errorf("StartTask with wrong worker returned true")
	}
	if store.StartTask(claimed.TaskID, "worker-1", "wrong-token") {
		t.Errorf("StartTask with wrong token returned true")
	}

	// 7. ExecutionSignal on RUNNING task
	if sig := store.ExecutionSignal(claimed.TaskID, "worker-1", *claimed.LeaseToken); sig != "active" {
		t.Errorf("ExecutionSignal on active task = %s, want active", sig)
	}
	// Cancel requested signal
	_, _ = raw.Exec("UPDATE tasks SET cancel_requested = 1 WHERE task_id = ?", claimed.TaskID)
	if sig := store.ExecutionSignal(claimed.TaskID, "worker-1", *claimed.LeaseToken); sig != "cancel_requested" {
		t.Errorf("ExecutionSignal on cancel_requested task = %s, want cancel_requested", sig)
	}

	// 8. ReconcileExpired on cancel_requested expired task
	_, _ = raw.Exec("UPDATE tasks SET lease_expires_at = ? WHERE task_id = ?", now-10, claimed.TaskID)
	reconciledCount, err := store.ReconcileExpired(ctx)
	if err != nil || reconciledCount != 1 {
		t.Fatalf("ReconcileExpired want 1, got (%d, %v)", reconciledCount, err)
	}
	reconciledTask, err := store.GetTask(ctx, claimed.TaskID)
	if err != nil || reconciledTask.State != StateCancelled {
		t.Errorf("reconciledTask state = %v, want CANCELLED", reconciledTask.State)
	}
}

func TestMigrateSupervisorSchemaMissingColumns(t *testing.T) {
	store, path := newTestStore(t)
	store.Close()

	raw := openRawDB(t, path)
	defer raw.Close()
	ctx := context.Background()

	// Drop parent_task_id from supervisor_tasks, payload_json from supervisor_decisions,
	// and false_escalation_rate from supervisor_metrics
	_, err := raw.Exec("ALTER TABLE supervisor_tasks DROP COLUMN parent_task_id")
	if err != nil {
		t.Fatalf("drop parent_task_id: %v", err)
	}
	_, err = raw.Exec("ALTER TABLE supervisor_decisions DROP COLUMN payload_json")
	if err != nil {
		t.Fatalf("drop payload_json: %v", err)
	}
	_, err = raw.Exec("ALTER TABLE supervisor_metrics DROP COLUMN false_escalation_rate")
	if err != nil {
		t.Fatalf("drop false_escalation_rate: %v", err)
	}

	conn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Close()

	if err := migrateSupervisorSchema(conn); err != nil {
		t.Fatalf("migrateSupervisorSchema: %v", err)
	}

	// Verify columns restored
	colRows, err := raw.Query("PRAGMA table_info(supervisor_tasks)")
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	hasParent := false
	for colRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		_ = colRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk)
		if name == "parent_task_id" {
			hasParent = true
		}
	}
	colRows.Close()
	if !hasParent {
		t.Errorf("expected parent_task_id restored")
	}
}

func TestReconcileExpiredFailedTask(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	raw := openRawDB(t, path)

	now := float64(time.Now().UnixNano()) / 1e9
	_, err := raw.Exec(`
		INSERT INTO tasks(
			task_id, idempotency_key, schema_version, state, priority,
			request_json, request_hash, attempts, max_attempts, cancel_requested,
			lease_owner, lease_token, lease_expires_at, created_at, updated_at
		) VALUES ('t-failed-reconcile', 'k-fail-rec', 'agy.task.v1', 'RUNNING', 0,
			'{"prompt":"secret-fail"}', 'hash-f', 3, 3, 0,
			'worker-1', 'tok-f', ?, ?, ?)`,
		now-10, now-50, now-50)
	if err != nil {
		t.Fatalf("insert expired task: %v", err)
	}

	count, err := store.ReconcileExpired(ctx)
	if err != nil || count != 1 {
		t.Fatalf("ReconcileExpired want 1, got (%d, %v)", count, err)
	}

	tFailed, err := store.GetTask(ctx, "t-failed-reconcile")
	if err != nil || tFailed == nil {
		t.Fatalf("GetTask: %v", err)
	}
	if tFailed.State != StateFailed {
		t.Errorf("tFailed state = %s, want FAILED", tFailed.State)
	}
	if tFailed.ReceiptHash == nil || *tFailed.ReceiptHash == "" {
		t.Errorf("tFailed ReceiptHash should be sealed")
	}
}

func TestStoreLineageListActiveAndSchemaDirect(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	raw := openRawDB(t, path)
	now := float64(time.Now().UnixNano()) / 1e9

	// 1. Direct call to applyBaseSchema, applySupervisorSchema, migrateTasksTable, migrateReceiptLake
	conn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Close()

	if err := applyBaseSchema(conn); err != nil {
		t.Fatalf("applyBaseSchema: %v", err)
	}
	if err := applySupervisorSchema(conn); err != nil {
		t.Fatalf("applySupervisorSchema: %v", err)
	}
	if err := migrateTasksTable(conn); err != nil {
		t.Fatalf("migrateTasksTable: %v", err)
	}
	if err := migrateReceiptLake(conn); err != nil {
		t.Fatalf("migrateReceiptLake: %v", err)
	}

	// 2. Lineage CTE with hierarchy: root -> child -> grandchild
	pRoot := "root-1"
	pChild := "child-1"
	_, err = raw.Exec(`
		INSERT INTO tasks(task_id, idempotency_key, schema_version, state, priority, request_json, request_hash, attempts, max_attempts, cancel_requested, created_at, updated_at)
		VALUES ('root-1', 'k-root', 'agy.task.v1', 'SUCCEEDED', 0, '{}', 'h1', 0, 3, 0, ?, ?)`, now-30, now-30)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}
	_, err = raw.Exec(`
		INSERT INTO tasks(task_id, parent_task_id, idempotency_key, schema_version, state, priority, request_json, request_hash, attempts, max_attempts, cancel_requested, created_at, updated_at)
		VALUES ('child-1', 'root-1', 'k-child', 'agy.task.v1', 'SUCCEEDED', 0, '{}', 'h2', 0, 3, 0, ?, ?)`, now-20, now-20)
	if err != nil {
		t.Fatalf("insert child: %v", err)
	}
	_, err = raw.Exec(`
		INSERT INTO tasks(task_id, parent_task_id, idempotency_key, schema_version, state, priority, request_json, request_hash, attempts, max_attempts, cancel_requested, created_at, updated_at)
		VALUES ('grandchild-1', 'child-1', 'k-gchild', 'agy.task.v1', 'RUNNING', 0, '{}', 'h3', 0, 3, 0, ?, ?)`, now-10, now-10)
	if err != nil {
		t.Fatalf("insert grandchild: %v", err)
	}

	lineage, err := store.GetTaskLineage(ctx, "grandchild-1")
	if err != nil {
		t.Fatalf("GetTaskLineage: %v", err)
	}
	if len(lineage) != 3 {
		t.Fatalf("lineage length = %d, want 3", len(lineage))
	}
	if lineage[0].TaskID != "root-1" || lineage[1].TaskID != "child-1" || lineage[2].TaskID != "grandchild-1" {
		t.Errorf("lineage order incorrect: %v, %v, %v", lineage[0].TaskID, lineage[1].TaskID, lineage[2].TaskID)
	}

	// 3. ListChildTasks with children
	children, err := store.ListChildTasks(ctx, "root-1")
	if err != nil {
		t.Fatalf("ListChildTasks: %v", err)
	}
	if len(children) != 1 || children[0].TaskID != "child-1" {
		t.Errorf("ListChildTasks root-1 mismatch: %v", children)
	}

	// 4. ActiveTaskCount with LEASED and RUNNING tasks
	_, err = raw.Exec(`
		INSERT INTO tasks(task_id, idempotency_key, schema_version, state, priority, request_json, request_hash, attempts, max_attempts, cancel_requested, created_at, updated_at)
		VALUES ('leased-task-1', 'k-leased', 'agy.task.v1', 'LEASED', 0, '{}', 'h4', 0, 3, 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert leased: %v", err)
	}
	activeCount, err := store.ActiveTaskCount(ctx)
	if err != nil {
		t.Fatalf("ActiveTaskCount: %v", err)
	}
	if activeCount < 2 {
		t.Errorf("ActiveTaskCount = %d, want >= 2", activeCount)
	}

	// 5. ListTasks with State filter
	stRunning := StateRunning
	runningTasks, err := store.ListTasks(ctx, TaskFilter{State: &stRunning, Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks RUNNING: %v", err)
	}
	if len(runningTasks) == 0 {
		t.Errorf("expected at least 1 RUNNING task")
	}

	// 6. SubmitTask with valid ParentTaskID
	reqChild := SubmitTaskRequest{
		IdempotencyKey: "k-valid-child",
		ParentTaskID:   &pRoot,
		Priority:       5,
		MaxAttempts:    2,
		Payload:        json.RawMessage(`{"prompt":"child subtask"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{t.TempDir()},
	}
	tChild, err := store.SubmitTask(ctx, reqChild)
	if err != nil {
		t.Fatalf("SubmitTask with valid parent: %v", err)
	}
	if tChild.ParentTaskID == nil || *tChild.ParentTaskID != "root-1" {
		t.Errorf("ParentTaskID mismatch: %v", tChild.ParentTaskID)
	}

	// 7. Deduplicated SubmitTask with matching parent
	tChildDedup, err := store.SubmitTask(ctx, reqChild)
	if err != nil {
		t.Fatalf("SubmitTask dedup: %v", err)
	}
	if !tChildDedup.Deduplicated {
		t.Errorf("expected Deduplicated flag true")
	}
	// Deduplication with mismatched parent
	reqChildDiffParent := reqChild
	reqChildDiffParent.ParentTaskID = &pChild
	if _, err := store.SubmitTask(ctx, reqChildDiffParent); err == nil {
		t.Errorf("SubmitTask with mismatched parent should error")
	}

	// 8. ClaimTask when QUEUED task has cancel_requested = 1
	_, err = raw.Exec(`
		INSERT INTO tasks(task_id, idempotency_key, schema_version, state, priority, request_json, request_hash, attempts, max_attempts, cancel_requested, created_at, updated_at)
		VALUES ('t-cancel-queued', 'k-cancel-q', 'agy.task.v1', 'QUEUED', 100, '{}', 'h5', 0, 3, 1, ?, ?)`, now+100, now+100)
	if err != nil {
		t.Fatalf("insert cancel queued: %v", err)
	}
	// Should skip t-cancel-queued because cancel_requested = 1
	claimed, err := store.ClaimTask(ctx, "w-test", 60)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed != nil && claimed.TaskID == "t-cancel-queued" {
		t.Errorf("claimed task should not be cancel_requested task")
	}
}

func TestStoreDetailedCoverageBranches(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	raw := openRawDB(t, path)
	now := float64(time.Now().UnixNano()) / 1e9

	// 1. SubmitTask with all fields populated to exercise insertNewTask details branches
	orchID := "orch-full"
	wtID := "wt-full"
	workerName := "agy-full"
	pRoot := "root-p"
	_, _ = raw.Exec(`INSERT INTO tasks(task_id, idempotency_key, schema_version, state, priority, request_json, request_hash, attempts, max_attempts, cancel_requested, created_at, updated_at)
		VALUES ('root-p', 'k-root-p', 'agy.task.v1', 'SUCCEEDED', 0, '{}', 'hp', 0, 3, 0, ?, ?)`, now-10, now-10)

	fullReq := SubmitTaskRequest{
		IdempotencyKey: "k-full-fields",
		ParentTaskID:   &pRoot,
		Priority:       10,
		MaxAttempts:    3,
		Payload:        json.RawMessage(`{"prompt":"full prompt"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{t.TempDir()},
		OrchestratorID: &orchID,
		WorktreeID:     &wtID,
		WorkerName:     &workerName,
		Iter:           4,
	}
	tFull, err := store.SubmitTask(ctx, fullReq)
	if err != nil {
		t.Fatalf("SubmitTask fullReq: %v", err)
	}
	if tFull.Iter != 4 || tFull.WorkerName == nil || *tFull.WorkerName != "agy-full" {
		t.Errorf("tFull fields mismatch: %+v", tFull)
	}

	// 2. Claim, RenewHeartbeat, Heartbeat, and extendLease branches
	claimed, err := store.ClaimTask(ctx, "w-cov", 60)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimTask: got (%v, %v)", claimed, err)
	}

	// RenewHeartbeat success
	if err := store.RenewHeartbeat(ctx, claimed.TaskID, "w-cov", 30); err != nil {
		t.Errorf("RenewHeartbeat valid: %v", err)
	}
	// RenewHeartbeat wrong worker
	if err := store.RenewHeartbeat(ctx, claimed.TaskID, "wrong-w", 30); err == nil {
		t.Errorf("RenewHeartbeat wrong worker should error")
	}

	// Heartbeat success
	if err := store.Heartbeat(ctx, claimed.TaskID, "w-cov", *claimed.LeaseToken, 30); err != nil {
		t.Errorf("Heartbeat valid: %v", err)
	}
	// Heartbeat wrong token
	if err := store.Heartbeat(ctx, claimed.TaskID, "w-cov", "bad-tok", 30); err == nil {
		t.Errorf("Heartbeat wrong token should error")
	}

	// 3. ExecutionSignal states
	// When task is LEASED (not RUNNING), ExecutionSignal returns "lease_lost"
	if sig := store.ExecutionSignal(claimed.TaskID, "w-cov", *claimed.LeaseToken); sig != "lease_lost" {
		t.Errorf("ExecutionSignal on LEASED task = %s, want lease_lost", sig)
	}

	// Start task to make it RUNNING
	if !store.StartTask(claimed.TaskID, "w-cov", *claimed.LeaseToken) {
		t.Fatalf("StartTask failed")
	}
	// Mismatched token on RUNNING task
	if sig := store.ExecutionSignal(claimed.TaskID, "w-cov", "wrong-tok"); sig != "lease_lost" {
		t.Errorf("ExecutionSignal with wrong token = %s, want lease_lost", sig)
	}
	// Mismatched worker on RUNNING task
	if sig := store.ExecutionSignal(claimed.TaskID, "wrong-worker", *claimed.LeaseToken); sig != "lease_lost" {
		t.Errorf("ExecutionSignal with wrong worker = %s, want lease_lost", sig)
	}

	// 4. insertTaskEvent direct call with details
	tx, err := raw.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := insertTaskEvent(tx, claimed.TaskID, "custom_event", "test_actor", nil, now); err != nil {
		t.Errorf("insertTaskEvent with nil details: %v", err)
	}
	if err := insertTaskEvent(tx, claimed.TaskID, "custom_event_2", "test_actor", map[string]any{"k": "v"}, now); err != nil {
		t.Errorf("insertTaskEvent with map details: %v", err)
	}
	_ = tx.Commit()

	// 5. scanTask with result_json
	_, err = raw.Exec(`
		INSERT INTO tasks(task_id, idempotency_key, schema_version, state, priority, request_json, request_hash, result_json, attempts, max_attempts, cancel_requested, created_at, updated_at)
		VALUES ('t-with-res', 'k-with-res', 'agy.task.v1', 'SUCCEEDED', 0, '{}', 'hr', '{"output":"ok"}', 1, 3, 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert task with result: %v", err)
	}
	tWithRes, err := store.GetTask(ctx, "t-with-res")
	if err != nil || tWithRes == nil || len(tWithRes.Result) == 0 {
		t.Errorf("GetTask with result_json: got (%v, %v)", tWithRes, err)
	}
}

func TestStoreCanceledContextErrorsAndClosedConnErrors(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()

	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()

	req := SubmitTaskRequest{
		IdempotencyKey: "k-cancel-ctx",
		Priority:       0,
		MaxAttempts:    3,
		Payload:        json.RawMessage(`{"prompt":"test"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{t.TempDir()},
	}

	// 1. Canceled context on queries and transactions
	if _, err := store.SubmitTask(cancCtx, req); err == nil {
		t.Errorf("SubmitTask with canceled context should error")
	}
	if _, err := store.ClaimTask(cancCtx, "w1", 10); err == nil {
		t.Errorf("ClaimTask with canceled context should error")
	}
	if err := store.RenewHeartbeat(cancCtx, "t1", "w1", 10); err == nil {
		t.Errorf("RenewHeartbeat with canceled context should error")
	}
	if err := store.Heartbeat(cancCtx, "t1", "w1", "tok", 10); err == nil {
		t.Errorf("Heartbeat with canceled context should error")
	}
	if _, err := store.ReconcileExpired(cancCtx); err == nil {
		t.Errorf("ReconcileExpired with canceled context should error")
	}
	if _, err := store.ListChildTasks(cancCtx, "parent-1"); err == nil {
		t.Errorf("ListChildTasks with canceled context should error")
	}
	if _, err := store.GetTaskLineage(cancCtx, "task-1"); err == nil {
		t.Errorf("GetTaskLineage with canceled context should error")
	}
	if _, err := store.ActiveTaskCount(cancCtx); err == nil {
		t.Errorf("ActiveTaskCount with canceled context should error")
	}

	// 2. Closed connection schema error branches
	raw := openRawDB(t, path)
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	_ = conn.Close() // Close connection to test error branches

	if err := applyBaseSchema(conn); err == nil {
		t.Errorf("applyBaseSchema on closed conn should error")
	}
	if err := applySupervisorSchema(conn); err == nil {
		t.Errorf("applySupervisorSchema on closed conn should error")
	}
	if err := migrateTasksTable(conn); err == nil {
		t.Errorf("migrateTasksTable on closed conn should error")
	}
	if err := migrateSupervisorSchema(conn); err == nil {
		t.Errorf("migrateSupervisorSchema on closed conn should error")
	}
	if err := migrateReceiptLake(conn); err == nil {
		t.Errorf("migrateReceiptLake on closed conn should error")
	}
}

func TestMigrateTasksTableMissingParent(t *testing.T) {
	store, path := newTestStore(t)
	store.Close()
	raw := openRawDB(t, path)
	defer raw.Close()
	_, _ = raw.Exec("ALTER TABLE tasks DROP COLUMN parent_task_id")
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer conn.Close()
	if err := migrateTasksTable(conn); err != nil {
		t.Fatalf("migrateTasksTable: %v", err)
	}
}

func TestMigrateReceiptLakeMissingColumns(t *testing.T) {
	store, path := newTestStore(t)
	store.Close()
	raw := openRawDB(t, path)
	defer raw.Close()
	for _, col := range []string{"orchestrator_id", "worktree_id", "worker_name", "iter"} {
		_, _ = raw.Exec("ALTER TABLE tasks DROP COLUMN " + col)
	}
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer conn.Close()
	if err := migrateReceiptLake(conn); err != nil {
		t.Fatalf("migrateReceiptLake: %v", err)
	}
}

func TestStoreAdditionalCoverage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nilclock.db")

	// 1. NewControlPlane with nil clock (defaults to time.Now)
	store, err := NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane nil clock: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	// 2. checkSchemaVersion validation directly
	raw := openRawDB(t, dbPath)
	conn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	for v := 0; v <= 5; v++ {
		_, _ = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v))
		if err := checkSchemaVersion(conn); err != nil {
			t.Errorf("checkSchemaVersion(%d) should succeed, got %v", v, err)
		}
	}
	_, _ = conn.ExecContext(ctx, "PRAGMA user_version = 6")
	if err := checkSchemaVersion(conn); err == nil {
		t.Errorf("checkSchemaVersion(6) should fail")
	}

	// Restore version
	_, _ = conn.ExecContext(ctx, "PRAGMA user_version = 5")

	// 3. Normal retryable ReconcileExpired (requeues task)
	now := float64(time.Now().UnixNano()) / 1e9
	_, err = raw.Exec(`
		INSERT INTO tasks(
			task_id, idempotency_key, schema_version, state, priority,
			request_json, request_hash, attempts, max_attempts, cancel_requested,
			lease_owner, lease_token, lease_expires_at, created_at, updated_at
		) VALUES ('t-requeue-exp', 'k-req-exp', 'agy.task.v1', 'LEASED', 0,
			'{"prompt":"retryable"}', 'hash-r', 1, 3, 0,
			'worker-1', 'tok-r', ?, ?, ?)`,
		now-10, now-50, now-50)
	if err != nil {
		t.Fatalf("insert retryable task: %v", err)
	}

	requeued, err := store.ReconcileExpired(ctx)
	if err != nil || requeued != 1 {
		t.Fatalf("ReconcileExpired want 1, got (%d, %v)", requeued, err)
	}
	tRequeued, err := store.GetTask(ctx, "t-requeue-exp")
	if err != nil || tRequeued == nil || tRequeued.State != StateQueued {
		t.Errorf("tRequeued state = %v, want QUEUED", tRequeued)
	}

	// 4. Boundary priority and max_attempts submission
	reqBound1, err := store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "k-bound-min",
		Priority:       -100,
		MaxAttempts:    1,
		Payload:        json.RawMessage(`{"prompt":"min bounds"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{dir},
	})
	if err != nil || reqBound1 == nil {
		t.Errorf("SubmitTask min bounds: %v", err)
	}

	// 5. SubmitTask with MaxAttempts = 0 (defaults to 1)
	reqZeroAtt, err := store.SubmitTask(ctx, SubmitTaskRequest{
		IdempotencyKey: "k-zero-att",
		Priority:       0,
		MaxAttempts:    0,
		Payload:        json.RawMessage(`{"prompt":"zero att"}`),
		Model:          "claude-3-5-sonnet",
		AddDirs:        []string{dir},
	})
	if err != nil || reqZeroAtt == nil || reqZeroAtt.MaxAttempts != 1 {
		t.Errorf("SubmitTask MaxAttempts 0 want 1, got %+v", reqZeroAtt)
	}

	// 6. StartTask on QUEUED task (should fail affected != 1)
	if store.StartTask(reqZeroAtt.TaskID, "w1", "tok") {
		t.Errorf("StartTask on QUEUED task should return false")
	}

	// 7. ReconcileExpired on CancelRequested task in RUNNING state
	_, err = raw.Exec(`
		INSERT INTO tasks(
			task_id, idempotency_key, schema_version, state, priority,
			request_json, request_hash, attempts, max_attempts, cancel_requested,
			lease_owner, lease_token, lease_expires_at, created_at, updated_at
		) VALUES ('t-canc-rec', 'k-canc-rec', 'agy.task.v1', 'RUNNING', 0,
			'{"prompt":"canc-secret"}', 'hash-c', 1, 3, 1,
			'worker-canc', 'tok-c', ?, ?, ?)`,
		now-10, now-50, now-50)
	if err != nil {
		t.Fatalf("insert canc task: %v", err)
	}

	cancCount, err := store.ReconcileExpired(ctx)
	if err != nil || cancCount != 1 {
		t.Fatalf("ReconcileExpired want 1, got (%d, %v)", cancCount, err)
	}
	tCanc, err := store.GetTask(ctx, "t-canc-rec")
	if err != nil || tCanc == nil || tCanc.State != StateCancelled {
		t.Errorf("tCanc state = %v, want CANCELLED", tCanc)
	}
}

func TestInitializeMigrationErrorRollback(t *testing.T) {
	// 1. tasks is a VIEW causing migrateTasksTable failure in initialize
	dir1 := t.TempDir()
	db1 := filepath.Join(dir1, "view_tasks.db")
	raw1, err := sql.Open("sqlite", db1)
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, _ = raw1.Exec("CREATE VIEW tasks AS SELECT 1 AS id")
	raw1.Close()

	if _, err := NewControlPlane(db1, nil); err == nil {
		t.Errorf("NewControlPlane on view tasks should fail during initialize")
	}

	// 2. supervisor_tasks is a VIEW causing migrateSupervisorSchema failure in initialize
	dir2 := t.TempDir()
	db2 := filepath.Join(dir2, "view_sup.db")
	raw2, err := sql.Open("sqlite", db2)
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, _ = raw2.Exec("CREATE VIEW supervisor_tasks AS SELECT 1 AS id")
	raw2.Close()

	if _, err := NewControlPlane(db2, nil); err == nil {
		t.Errorf("NewControlPlane on view supervisor_tasks should fail during initialize")
	}

	// 3. idx_tasks_orchestrator_iter exists as a TABLE causing migrateReceiptLake failure in initialize
	dir3 := t.TempDir()
	db3 := filepath.Join(dir3, "table_idx.db")
	raw3, err := sql.Open("sqlite", db3)
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, _ = raw3.Exec("CREATE TABLE idx_tasks_orchestrator_iter (x TEXT)")
	raw3.Close()

	if _, err := NewControlPlane(db3, nil); err == nil {
		t.Errorf("NewControlPlane on colliding table index should fail during initialize")
	}
}
