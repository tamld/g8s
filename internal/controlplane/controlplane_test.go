package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := t.TempDir() + "/control-plane.sqlite3"
	store, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, path
}

func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

const expectedTaskColumns = "task_id|TEXT, parent_task_id|TEXT, idempotency_key|TEXT, schema_version|TEXT, state|TEXT, priority|INTEGER, request_json|TEXT, request_hash|TEXT, result_json|TEXT, result_hash|TEXT, receipt_hash|TEXT, attempts|INTEGER, max_attempts|INTEGER, lease_owner|TEXT, lease_token|TEXT, lease_expires_at|REAL, cancel_requested|INTEGER, created_at|REAL, updated_at|REAL, completed_at|REAL, last_error|TEXT"

func TestFreshDatabaseSchemaExact(t *testing.T) {
	_, path := newTestStore(t)
	db := openRawDB(t, path)

	rows, err := db.Query("SELECT name, type FROM sqlite_master WHERE type IN ('table','index') ORDER BY name")
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()
	objects := map[string]string{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			t.Fatalf("scan sqlite_master: %v", err)
		}
		objects[name] = kind
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_master: %v", err)
	}

	for _, want := range []string{"tasks", "task_events", "idx_tasks_claim", "idx_task_events_task"} {
		if _, ok := objects[want]; !ok {
			t.Errorf("missing object %q in schema", want)
		}
	}
	if _, ok := objects["control_plane_maintenance"]; !ok {
		t.Errorf("missing control_plane_maintenance table")
	}
	if _, ok := objects["write_receipts"]; ok {
		t.Errorf("write_receipts must be owned by internal/receipt, not controlplane (decision D2)")
	}

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d, want %d", version, SchemaVersion)
	}

	colRows, err := db.Query("PRAGMA table_info(tasks)")
	if err != nil {
		t.Fatalf("table_info(tasks): %v", err)
	}
	defer colRows.Close()
	var got []string
	for colRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := colRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		got = append(got, name+"|"+colType)
	}
	if err := colRows.Err(); err != nil {
		t.Fatalf("iterate table_info: %v", err)
	}
	if strings.Join(got, ", ") != expectedTaskColumns {
		t.Errorf("tasks columns mismatch:\n got:  %s\n want: %s", strings.Join(got, ", "), expectedTaskColumns)
	}
}

func TestFreshDatabaseFilePermissionsRestricted(t *testing.T) {
	_, path := newTestStore(t)
	if runtime.GOOS == "windows" {
		t.Skip("windows stat modes carry no POSIX permission bits")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("db file perms = %o, want 600", perm)
	}
}

func TestLegacyV1DatabaseMigratesParentColumn(t *testing.T) {
	store, path := newTestStore(t)
	store.Close()

	raw := openRawDB(t, path)
	if _, err := raw.Exec("ALTER TABLE tasks DROP COLUMN parent_task_id"); err != nil {
		t.Fatalf("simulate v1 layout (DROP COLUMN unsupported?): %v", err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatalf("set legacy version: %v", err)
	}
	raw.Close()

	reopened, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer reopened.Close()

	check := openRawDB(t, path)
	colRows, err := check.Query("PRAGMA table_info(tasks)")
	if err != nil {
		t.Fatalf("table_info after migration: %v", err)
	}
	hasParent := false
	for colRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := colRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan columns: %v", err)
		}
		if name == "parent_task_id" {
			hasParent = true
		}
	}
	colRows.Close()
	if !hasParent {
		t.Errorf("parent_task_id missing after migration from v1")
	}
	var version int
	if err := check.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d, want %d after migration", version, SchemaVersion)
	}
}

func TestUnsupportedSchemaVersionRejected(t *testing.T) {
	store, path := newTestStore(t)
	store.Close()

	raw := openRawDB(t, path)
	if _, err := raw.Exec("PRAGMA user_version = 9"); err != nil {
		t.Fatalf("set future version: %v", err)
	}
	raw.Close()

	if _, err := NewControlPlane(path, nil); err == nil {
		t.Fatalf("expected rejection of future schema version")
	} else if !strings.Contains(err.Error(), "unsupported control-plane schema version 9; expected 4") {
		t.Errorf("error mismatch: %v", err)
	}
}

func TestGetUnknownTaskReturnsNil(t *testing.T) {
	store, _ := newTestStore(t)
	task, err := store.GetTask(context.Background(), "missing-task-id")
	if err != nil {
		t.Fatalf("GetTask unknown id: %v", err)
	}
	if task != nil {
		t.Errorf("GetTask unknown id returned %+v, want nil", task)
	}
}

func TestListValidatesStateAndLimit(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	bogus := "BOGUS"
	if _, err := store.ListTasks(ctx, TaskFilter{State: &bogus}); err == nil ||
		!strings.Contains(err.Error(), "unknown task state: BOGUS") {
		t.Errorf("unknown state error mismatch: %v", err)
	}

	for _, bad := range []int{-5, 201} {
		if _, err := store.ListTasks(ctx, TaskFilter{Limit: bad}); err == nil ||
			!strings.Contains(err.Error(), "limit must be between 1 and 200") {
			t.Errorf("limit %d error mismatch: %v", bad, err)
		}
	}

	for _, good := range []int{1, 200} {
		if _, err := store.ListTasks(ctx, TaskFilter{Limit: good}); err != nil {
			t.Errorf("boundary limit %d rejected: %v", good, err)
		}
	}
}

func insertRawTask(t *testing.T, db *sql.DB, taskID, state string, createdAt float64) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO tasks(task_id, idempotency_key, schema_version, state, priority,
			request_json, request_hash, attempts, max_attempts, cancel_requested,
			created_at, updated_at)
		 VALUES (?, ?, 'agy.task.v1', ?, 0, '{}', 'hash', 0, 3, 0, ?, ?)`,
		taskID, taskID+"-key", state, createdAt, createdAt,
	)
	if err != nil {
		t.Fatalf("insert raw task %s: %v", taskID, err)
	}
}

func TestActiveTaskCountIgnoresListPageSize(t *testing.T) {
	store, path := newTestStore(t)
	raw := openRawDB(t, path)
	const total = 201
	for i := 0; i < total; i++ {
		state := StateQueued
		if i%2 == 0 {
			state = StateLeased
		} else {
			state = StateRunning
		}
		insertRawTask(t, raw, fmt.Sprintf("task-%03d", i), state, float64(i))
	}
	raw.Close()

	count, err := store.ActiveTaskCount(context.Background())
	if err != nil {
		t.Fatalf("ActiveTaskCount: %v", err)
	}
	if count != total {
		t.Errorf("ActiveTaskCount = %d, want %d", count, total)
	}

	listed, err := store.ListTasks(context.Background(), TaskFilter{Limit: 200})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(listed) != 200 {
		t.Errorf("ListTasks page size = %d, want 200", len(listed))
	}
}

func TestCanonicalJSONIsCompactSortedAndUnescaped(t *testing.T) {
	value := map[string]any{
		"b": 1,
		"a": "<script>&emoji-🧠</script>",
	}
	encoded, err := canonicalJSON(value)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	want := `{"a":"<script>&emoji-🧠</script>","b":1}`
	if encoded != want {
		t.Errorf("canonicalJSON =\n  %s\nwant:\n  %s", encoded, want)
	}
}

// fakeClock is an injectable deterministic clock (constitution axiom).
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestStoreWithClock(t *testing.T, clock *fakeClock) (*Store, string) {
	t.Helper()
	path := t.TempDir() + "/control-plane.sqlite3"
	store, err := NewControlPlane(path, clock.Now)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, path
}

// testScopeDir is a fragment-free path accepted by the harness add-dir gate.
const testScopeDir = "/tmp/g8s-cp-test-scope"

func submitReq(prompt string) SubmitTaskRequest {
	payload, _ := json.Marshal(map[string]any{"prompt": prompt, "role": "collector"})
	return SubmitTaskRequest{
		IdempotencyKey: "key-" + prompt,
		Payload:        payload,
		MaxAttempts:    1,
		Model:          "test-model",
		AddDirs:        []string{testScopeDir},
	}
}

func mustSubmit(t *testing.T, s *Store, req SubmitTaskRequest) *Task {
	t.Helper()
	task, err := s.SubmitTask(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	return task
}

func TestSubmitIsIdempotentForSameRequest(t *testing.T) {
	s, _ := newTestStore(t)

	first := mustSubmit(t, s, submitReq("same-task"))
	second := mustSubmit(t, s, submitReq("same-task"))

	if first.TaskID != second.TaskID {
		t.Errorf("dedup returned different task ids: %s vs %s", first.TaskID, second.TaskID)
	}
	if !second.Deduplicated {
		t.Error("second submit should be flagged deduplicated")
	}
	tasks, err := s.ListTasks(context.Background(), TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("task count = %d, want 1", len(tasks))
	}
}

func TestIdempotencyCollisionRejectsDifferentRequest(t *testing.T) {
	s, _ := newTestStore(t)
	mustSubmit(t, s, submitReq("collision"))

	conflicting := submitReq("collision")
	conflicting.Payload = json.RawMessage(`{"prompt":"Different task.","role":"collector"}`)
	_, err := s.SubmitTask(context.Background(), conflicting)
	if err == nil || !strings.Contains(err.Error(), "different request") {
		t.Errorf("err = %v, want 'different request'", err)
	}
}

func TestChildTaskPreservesLineageAndRequiresKnownParent(t *testing.T) {
	s, _ := newTestStore(t)
	parent := mustSubmit(t, s, submitReq("parent"))

	child := mustSubmit(t, s, SubmitTaskRequest{
		IdempotencyKey: "child",
		Payload:        json.RawMessage(`{"prompt":"Continue with clarified scope."}`),
		ParentTaskID:   &parent.TaskID,
		Model:          "test-model",
		AddDirs:        []string{testScopeDir},
	})
	if child.ParentTaskID == nil || *child.ParentTaskID != parent.TaskID {
		t.Errorf("child parent_task_id = %v, want %s", child.ParentTaskID, parent.TaskID)
	}

	orphanID := "00000000-0000-0000-0000-000000000000"
	orphan := submitReq("orphan")
	orphan.ParentTaskID = &orphanID
	_, err := s.SubmitTask(context.Background(), orphan)
	if err == nil || !strings.Contains(err.Error(), "unknown parent task") {
		t.Errorf("err = %v, want 'unknown parent task'", err)
	}
}

func TestIdempotencyCollisionRejectsDifferentParent(t *testing.T) {
	s, _ := newTestStore(t)
	firstParent := mustSubmit(t, s, submitReq("first-parent"))
	secondParent := mustSubmit(t, s, submitReq("second-parent"))

	mustSubmit(t, s, SubmitTaskRequest{
		IdempotencyKey: "lineage-collision",
		Payload:        submitReq("lineage-collision").Payload,
		ParentTaskID:   &firstParent.TaskID,
		Model:          "test-model",
		AddDirs:        []string{testScopeDir},
	})

	_, err := s.SubmitTask(context.Background(), SubmitTaskRequest{
		IdempotencyKey: "lineage-collision",
		Payload:        submitReq("lineage-collision").Payload,
		ParentTaskID:   &secondParent.TaskID,
		Model:          "test-model",
		AddDirs:        []string{testScopeDir},
	})
	if err == nil || !strings.Contains(err.Error(), "different request") {
		t.Errorf("err = %v, want 'different request'", err)
	}
}

func TestMaintenanceBlocksClaimsUntilOwnerReleasesIt(t *testing.T) {
	s, dbPath := newTestStore(t)
	ctx := context.Background()
	task := mustSubmit(t, s, submitReq("maintenance-block"))

	if _, err := openRawDB(t, dbPath).Exec(`
		INSERT INTO control_plane_maintenance(singleton, owner, expires_at, updated_at)
		VALUES (1, 'service-a', strftime('%s','now') + 10, strftime('%s','now'))`); err != nil {
		t.Fatalf("seed maintenance: %v", err)
	}

	claimed, err := s.ClaimTask(ctx, "worker-a", 10)
	if err != nil {
		t.Fatalf("ClaimTask under maintenance: %v", err)
	}
	if claimed != nil {
		t.Errorf("claim should return nil under active maintenance, got %s", claimed.TaskID)
	}
	got, _ := s.GetTask(ctx, task.TaskID)
	if got.State != StateQueued {
		t.Errorf("state = %s, want QUEUED", got.State)
	}
}

func TestPriorityControlsClaimOrder(t *testing.T) {
	s, _ := newTestStore(t)
	low := mustSubmit(t, s, func() SubmitTaskRequest {
		req := submitReq("low")
		req.Priority = -1
		return req
	}())
	high := mustSubmit(t, s, func() SubmitTaskRequest {
		req := submitReq("high")
		req.Priority = 10
		return req
	}())

	claimed, err := s.ClaimTask(context.Background(), "worker-a", 10)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed.TaskID != high.TaskID {
		t.Errorf("claimed %s, want high-priority %s", claimed.TaskID, high.TaskID)
	}
	gotLow, _ := s.GetTask(context.Background(), low.TaskID)
	if gotLow.State != StateQueued {
		t.Errorf("low state = %s, want QUEUED", gotLow.State)
	}
}

func TestConcurrentClaimHasSingleWinner(t *testing.T) {
	clock := newFakeClock()
	s, dbPath := newTestStoreWithClock(t, clock)
	task := mustSubmit(t, s, submitReq("single-winner"))

	const racers = 2
	release := make(chan struct{})
	results := make(chan *Task, racers)
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			racer, err := NewControlPlane(dbPath, clock.Now)
			if err != nil {
				errs <- err
				return
			}
			defer racer.Close()
			<-release
			claimed, err := racer.ClaimTask(context.Background(), fmt.Sprintf("worker-%d", i), 10)
			if err != nil {
				errs <- err
				return
			}
			results <- claimed
		}(i)
	}
	close(release)

	winners := 0
	for i := 0; i < racers; i++ {
		select {
		case err := <-errs:
			t.Fatalf("racer error: %v", err)
		case claimed := <-results:
			if claimed != nil && claimed.TaskID == task.TaskID {
				winners++
			}
		}
	}
	if winners != 1 {
		t.Errorf("winners = %d, want exactly 1", winners)
	}
}

func TestExpiredLeaseRequeuesThenExhaustsRetryBudget(t *testing.T) {
	clock := newFakeClock()
	s, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()
	req := submitReq("lease-expiry")
	req.MaxAttempts = 2
	mustSubmit(t, s, req)

	first, err := s.ClaimTask(ctx, "worker-a", 10)
	if err != nil || first == nil {
		t.Fatalf("first claim: %v (%v)", first, err)
	}
	if first.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", first.Attempts)
	}

	clock.Advance(11 * time.Second)
	requeued, err := s.ReconcileExpired(ctx)
	if err != nil {
		t.Fatalf("ReconcileExpired: %v", err)
	}
	if requeued != 1 {
		t.Errorf("reconciled = %d, want 1", requeued)
	}
	got, _ := s.GetTask(ctx, first.TaskID)
	if got.State != StateQueued {
		t.Errorf("state after first expiry = %s, want QUEUED", got.State)
	}

	second, err := s.ClaimTask(ctx, "worker-b", 10)
	if err != nil || second == nil {
		t.Fatalf("second claim: %v (%v)", second, err)
	}
	if second.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", second.Attempts)
	}
	clock.Advance(11 * time.Second)
	finalized, err := s.ReconcileExpired(ctx)
	if err != nil {
		t.Fatalf("ReconcileExpired: %v", err)
	}
	if finalized != 1 {
		t.Errorf("reconciled = %d, want 1", finalized)
	}
	done, _ := s.GetTask(ctx, first.TaskID)
	if done.State != StateFailed {
		t.Errorf("state after budget exhaustion = %s, want FAILED", done.State)
	}
}

func TestHeartbeatRejectsStaleLeaseToken(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	task := mustSubmit(t, s, submitReq("heartbeat"))
	claimed, err := s.ClaimTask(ctx, "worker-a", 10)
	if err != nil || claimed == nil || claimed.LeaseToken == nil {
		t.Fatalf("claim: %v (%v)", claimed, err)
	}

	if err := s.Heartbeat(ctx, task.TaskID, "worker-a", "", 10); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("empty-token heartbeat err = %v, want ErrLeaseLost", err)
	}
	if err := s.Heartbeat(ctx, task.TaskID, "worker-a", "wrong-token", 10); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("stale-token heartbeat err = %v, want ErrLeaseLost", err)
	}
	if err := s.Heartbeat(ctx, task.TaskID, "worker-a", *claimed.LeaseToken, 10); err != nil {
		t.Errorf("valid heartbeat err = %v, want nil", err)
	}
	if err := s.RenewHeartbeat(ctx, task.TaskID, "worker-a", 10); err != nil {
		t.Errorf("spec heartbeat for lease owner err = %v, want nil", err)
	}
}

func TestExecutionSignalDistinguishesCancelFromLeaseLoss(t *testing.T) {
	s, _ := newTestStore(t)
	task := mustSubmit(t, s, submitReq("execution-signal"))
	claimed, err := s.ClaimTask(context.Background(), "worker-a", 10)
	if err != nil || claimed == nil || claimed.LeaseToken == nil {
		t.Fatalf("claim: %v (%v)", claimed, err)
	}
	token := *claimed.LeaseToken
	if !s.StartTask(task.TaskID, "worker-a", token) {
		t.Fatal("StartTask should succeed for the lease owner")
	}

	if got := s.ExecutionSignal(task.TaskID, "worker-a", token); got != "active" {
		t.Errorf("signal = %q, want active", got)
	}
	if got := s.ExecutionSignal(task.TaskID, "worker-b", token); got != "lease_lost" {
		t.Errorf("wrong-worker signal = %q, want lease_lost", got)
	}
}

func mustClaimStart(t *testing.T, s *Store, workerID string) (*Task, string) {
	t.Helper()
	task, err := s.ClaimTask(context.Background(), workerID, 30)
	if err != nil || task == nil || task.LeaseToken == nil {
		t.Fatalf("claim: %v (%v)", task, err)
	}
	if !s.StartTask(task.TaskID, workerID, *task.LeaseToken) {
		t.Fatal("StartTask should succeed for the lease owner")
	}
	return task, *task.LeaseToken
}

func requestDoc(t *testing.T, task *Task) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(task.Request, &doc); err != nil {
		t.Fatalf("decode stored request: %v", err)
	}
	return doc
}

func TestFinishAttemptSucceedsRedactsPromptAndSealsReceipt(t *testing.T) {
	s, _ := newTestStore(t)
	_ = mustSubmit(t, s, submitReq("finish-and-seal"))
	task, token := mustClaimStart(t, s, "worker-1")

	finished, err := s.FinishAttempt(task.TaskID, "worker-1", token, FinishAttemptParams{
		Result:  json.RawMessage(`{"ok":true}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
	if finished.State != StateSucceeded || finished.CompletedAt == nil {
		t.Fatalf("state=%s completedAt=%v, want SUCCEEDED with timestamp", finished.State, finished.CompletedAt)
	}
	doc := requestDoc(t, finished)
	if _, hasPrompt := doc["prompt"]; hasPrompt {
		t.Error("finished task still stores the raw prompt")
	}
	if doc["prompt_redacted"] != true {
		t.Error("prompt_redacted flag missing")
	}
	if hash, ok := doc["prompt_hash"].(string); !ok || len(hash) != 64 {
		t.Errorf("prompt_hash = %v, want sha-256 hex", doc["prompt_hash"])
	}
	if finished.ReceiptHash == nil || *finished.ReceiptHash == "" {
		t.Error("final state must seal a receipt hash")
	}
}

func TestReceiptHashIsReproducibleFromUnsignedPayload(t *testing.T) {
	s, _ := newTestStore(t)
	mustSubmit(t, s, submitReq("receipt-reproducible"))
	task, token := mustClaimStart(t, s, "worker-1")
	if _, err := s.FinishAttempt(task.TaskID, "worker-1", token, FinishAttemptParams{
		Result: json.RawMessage(`{"ok":true}`), Success: true,
	}); err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	receipt, err := s.BuildReceipt(context.Background(), task.TaskID)
	if err != nil {
		t.Fatalf("BuildReceipt: %v", err)
	}
	if receipt["signed"] != false {
		t.Errorf("signed = %v, want false (unsigned receipts only in v0.1)", receipt["signed"])
	}
	stored := receipt["receipt_hash"].(string)

	unsigned := map[string]any{}
	for key, value := range receipt {
		if key != "receipt_hash" {
			unsigned[key] = value
		}
	}
	canonical, err := canonicalJSON(unsigned)
	if err != nil {
		t.Fatalf("canonicalize unsigned payload: %v", err)
	}
	recomputed, err := contentHash(json.RawMessage(canonical))
	if err != nil {
		t.Fatalf("rehash: %v", err)
	}
	if recomputed != stored {
		t.Errorf("receipt hash mismatch: stored %s recomputed %s", stored, recomputed)
	}

	freshTask, err := s.GetTask(context.Background(), task.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if freshTask.ReceiptHash == nil || *freshTask.ReceiptHash != stored {
		t.Fatalf("database sealed receipt_hash %v != BuildReceipt %s", freshTask.ReceiptHash, stored)
	}
}

func TestRetryableFinishKeepsPromptWhileQueued(t *testing.T) {
	s, _ := newTestStoreWithClock(t, newFakeClock())
	req := submitReq("retryable-work")
	req.MaxAttempts = 2
	mustSubmit(t, s, req)
	claimed, _ := mustClaimStart(t, s, "worker-1")

	requeued, err := s.FinishAttempt(claimed.TaskID, "worker-1", *claimed.LeaseToken, FinishAttemptParams{
		Result:    json.RawMessage(`{"status":"temporary_failure"}`),
		Retryable: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}
	if requeued.State != StateQueued {
		t.Fatalf("state = %s, want QUEUED", requeued.State)
	}
	if _, hasPrompt := requestDoc(t, requeued)["prompt"]; !hasPrompt {
		t.Error("requeued task must keep its prompt for the next attempt")
	}
	if requeued.ReceiptHash != nil {
		t.Error("non-final states must not seal a receipt")
	}
}

func TestFinishAttemptRejectsStaleLeaseOwnership(t *testing.T) {
	s, _ := newTestStore(t)
	_ = mustSubmit(t, s, submitReq("stale-finish"))
	task, token := mustClaimStart(t, s, "worker-1")

	if _, err := s.FinishAttempt(task.TaskID, "worker-impostor", token, FinishAttemptParams{Success: true}); err == nil ||
		!strings.Contains(err.Error(), "lease ownership lost") {
		t.Errorf("wrong-worker finish error = %v, want lease ownership lost", err)
	}
	if _, err := s.FinishAttempt(task.TaskID, "worker-1", "forged-token", FinishAttemptParams{Success: true}); err == nil ||
		!strings.Contains(err.Error(), "lease ownership lost") {
		t.Errorf("wrong-token finish error = %v, want lease ownership lost", err)
	}
}

func TestSpecWrappersAdoptCurrentLease(t *testing.T) {
	s, _ := newTestStore(t)
	_ = mustSubmit(t, s, submitReq("wrapper-complete"))
	ok, _ := mustClaimStart(t, s, "worker-1")
	if err := s.CompleteTask(context.Background(), ok.TaskID, TaskResult{Result: json.RawMessage(`{"done":1}`)}); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	final, _ := s.GetTask(context.Background(), ok.TaskID)
	if final.State != StateSucceeded {
		t.Fatalf("CompleteTask state = %s, want SUCCEEDED", final.State)
	}

	mustSubmit(t, s, submitReq("wrapper-fail"))
	fail, _ := mustClaimStart(t, s, "worker-2")
	if err := s.FailTask(context.Background(), fail.TaskID, "provider crashed", 2); err != nil {
		t.Fatalf("FailTask: %v", err)
	}
	final, _ = s.GetTask(context.Background(), fail.TaskID)
	if final.State != StateFailed {
		t.Fatalf("FailTask state = %s, want FAILED", final.State)
	}
	if final.LastError == nil || !strings.Contains(*final.LastError, "(exit code 2)") {
		t.Errorf("LastError = %v, want exit code detail", final.LastError)
	}
}

func TestCancelQueuedTaskIsTerminalWithReceipt(t *testing.T) {
	s, _ := newTestStore(t)
	task := mustSubmit(t, s, submitReq("cancel-me"))

	if err := s.CancelTask(context.Background(), task.TaskID, "superseded"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	cancelled, _ := s.GetTask(context.Background(), task.TaskID)
	if cancelled.State != StateCancelled || cancelled.CompletedAt == nil {
		t.Fatalf("state=%s completedAt=%v, want terminal CANCELLED", cancelled.State, cancelled.CompletedAt)
	}
	if _, hasPrompt := requestDoc(t, cancelled)["prompt"]; hasPrompt {
		t.Error("cancelled task must redact prompt")
	}
	if cancelled.ReceiptHash == nil {
		t.Error("cancelled task must seal a receipt")
	}
}

func TestCancelRunningTaskRequestsCooperativeStop(t *testing.T) {
	s, _ := newTestStore(t)
	_ = mustSubmit(t, s, submitReq("cooperative-cancel"))
	task, token := mustClaimStart(t, s, "worker-1")

	if err := s.CancelTask(context.Background(), task.TaskID, "shutdown"); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	running, _ := s.GetTask(context.Background(), task.TaskID)
	if running.State != StateRunning || !running.CancelRequested {
		t.Fatalf("state=%s cancelRequested=%v, want RUNNING + cancel_requested", running.State, running.CancelRequested)
	}
	if got := s.ExecutionSignal(task.TaskID, "worker-1", token); got != "cancel_requested" {
		t.Errorf("signal = %q, want cancel_requested", got)
	}
}

func TestPauseValidatesStateOwnershipAndRedacts(t *testing.T) {
	s, _ := newTestStore(t)
	task := mustSubmit(t, s, submitReq("pause-evidence"))
	task, token := mustClaimStart(t, s, "worker-1")

	if _, err := s.PauseTask(task.TaskID, "worker-1", token, "FAILED", nil, "nope"); err == nil ||
		!strings.Contains(err.Error(), "pause state must be NEEDS_INFO or BLOCKED") {
		t.Errorf("invalid pause state error = %v", err)
	}
	paused, err := s.PauseTask(task.TaskID, "worker-1", token, StateNeedsInfo,
		json.RawMessage(`{"question":"which region?"}`), "awaiting operator input")
	if err != nil {
		t.Fatalf("PauseTask: %v", err)
	}
	if paused.State != StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", paused.State)
	}
	if _, hasPrompt := requestDoc(t, paused)["prompt"]; hasPrompt {
		t.Error("paused task must redact prompt immediately")
	}
	if paused.ReceiptHash == nil {
		t.Error("paused task must seal a receipt even though it is not final")
	}
	if _, err := s.PauseTask(task.TaskID, "worker-x", "bad-token", StateBlocked, nil, "stale"); err == nil ||
		!strings.Contains(err.Error(), "lease ownership lost") {
		t.Errorf("stale pause error = %v", err)
	}
}

func TestResumeTaskLifecycle(t *testing.T) {
	s, _ := newTestStore(t)
	task := mustSubmit(t, s, submitReq("resume-lifecycle"))
	claimed, err := s.ClaimTask(context.Background(), "worker-1", 30)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	token := *claimed.LeaseToken
	if !s.StartTask(task.TaskID, "worker-1", token) {
		t.Fatal("StartTask failed")
	}

	paused, err := s.PauseTask(claimed.TaskID, "worker-1", token, StateNeedsInfo,
		json.RawMessage(`{"question":"which cluster?"}`), "needs cluster name")
	if err != nil {
		t.Fatalf("PauseTask: %v", err)
	}
	if paused.State != StateNeedsInfo {
		t.Fatalf("paused state = %s, want NEEDS_INFO", paused.State)
	}

	// Resuming task from NEEDS_INFO back to QUEUED
	resumed, err := s.ResumeTask(context.Background(), task.TaskID,
		json.RawMessage(`{"prompt":"use cluster us-east-1","model":"gemini-3.7-flash-high"}`), "operator provided cluster name")
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if resumed.State != StateQueued {
		t.Fatalf("resumed state = %s, want QUEUED", resumed.State)
	}
	if resumed.LeaseOwner != nil {
		t.Fatal("resumed task must have nil LeaseOwner")
	}

	// Verify task can now be claimed again by worker-2
	reclaimed, err := s.ClaimTask(context.Background(), "worker-2", 30)
	if err != nil || reclaimed == nil {
		t.Fatalf("reclaimed: %v", err)
	}
	if reclaimed.TaskID != task.TaskID {
		t.Fatalf("reclaimed task ID = %s, want %s", reclaimed.TaskID, task.TaskID)
	}
}

func TestMaintenanceLifecycleBlocksAndReleasesClaims(t *testing.T) {
	s, _ := newTestStore(t)
	mustSubmit(t, s, submitReq("queued-under-maintenance"))

	active, err := s.BeginMaintenance("operator-1", 60)
	if err != nil || active != 0 {
		t.Fatalf("BeginMaintenance active=%d err=%v, want 0/nil", active, err)
	}
	if claimed, _ := s.ClaimTask(context.Background(), "worker-1", 10); claimed != nil {
		t.Fatal("claims must be blocked during maintenance")
	}
	if _, err := s.BeginMaintenance("operator-2", 60); err == nil ||
		!strings.Contains(err.Error(), "already held by operator-1") {
		t.Errorf("second holder error = %v", err)
	}
	if released, err := s.EndMaintenance("not-the-owner"); err != nil || released {
		t.Fatalf("wrong-owner release = %v/%v, want false/nil", released, err)
	}
	if released, err := s.EndMaintenance("operator-1"); err != nil || !released {
		t.Fatalf("owner release = %v/%v, want true/nil", released, err)
	}
	if claimed, _ := s.ClaimTask(context.Background(), "worker-1", 10); claimed == nil {
		t.Fatal("claims must resume after maintenance ends")
	}
}

func TestSubmissionSafetyGatesRejectDisabledOptions(t *testing.T) {
	s, _ := newTestStore(t)

	noSandbox := submitReq("sandbox-escape")
	noSandbox.NoSandbox = true
	if _, err := s.SubmitTask(context.Background(), noSandbox); err == nil ||
		!strings.Contains(err.Error(), "no_sandbox is disabled") {
		t.Errorf("no_sandbox error = %v", err)
	}

	customBin := "/usr/local/bin/agy"
	agyReq := submitReq("custom-binary")
	agyReq.AgyBin = &customBin
	if _, err := s.SubmitTask(context.Background(), agyReq); err == nil ||
		!strings.Contains(err.Error(), "custom agy_bin is disabled") {
		t.Errorf("agy_bin error = %v", err)
	}
}

func TestScopeRootsRequiredAndDeniedPathsBlocked(t *testing.T) {
	s, _ := newTestStore(t)

	bare := submitReq("no-scope")
	bare.AddDirs = nil
	if _, err := s.SubmitTask(context.Background(), bare); err == nil ||
		!strings.Contains(err.Error(), "explicit scope root") {
		t.Errorf("empty add_dirs error = %v", err)
	}

	home, _ := os.UserHomeDir()
	denied := submitReq("home-scope")
	denied.AddDirs = []string{home + "/.ssh"}
	if _, err := s.SubmitTask(context.Background(), denied); err == nil ||
		!strings.Contains(err.Error(), "denied path fragment detected") {
		t.Errorf("denied add-dir error = %v", err)
	}

	traversal := submitReq("traversal-scope")
	traversal.AddDirs = []string{testScopeDir + "/../../.ssh"}
	if _, err := s.SubmitTask(context.Background(), traversal); err == nil ||
		!strings.Contains(err.Error(), "denied path fragment detected") {
		t.Errorf("traversal add-dir error = %v", err)
	}
}

func TestUnknownTaskErrorsOnFinishAndBuildReceipt(t *testing.T) {
	s, _ := newTestStore(t)
	ghost := "00000000-0000-0000-0000-000000000000"

	if _, err := s.FinishAttempt(ghost, "w", "t", FinishAttemptParams{Success: true}); err == nil ||
		!errors.Is(err, ErrUnknownTask) {
		t.Errorf("finish unknown error = %v, want ErrUnknownTask", err)
	}
	if _, err := s.BuildReceipt(context.Background(), ghost); err == nil ||
		!errors.Is(err, ErrUnknownTask) {
		t.Errorf("build receipt unknown error = %v, want ErrUnknownTask", err)
	}
	if err := s.CancelTask(context.Background(), ghost, "why"); err == nil ||
		!errors.Is(err, ErrUnknownTask) {
		t.Errorf("cancel unknown error = %v, want ErrUnknownTask", err)
	}
}

func TestEventsRecordLifecycleOrder(t *testing.T) {
	s, _ := newTestStore(t)
	task := mustSubmit(t, s, submitReq("audited"))
	claimed, _ := mustClaimStart(t, s, "worker-1")
	if _, err := s.FinishAttempt(claimed.TaskID, "worker-1", *claimed.LeaseToken,
		FinishAttemptParams{Result: json.RawMessage(`{}`), Success: true}); err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	events, err := s.Events(context.Background(), task.TaskID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	gotTypes := make([]string, 0, len(events))
	for _, e := range events {
		gotTypes = append(gotTypes, e.EventType)
	}
	want := []string{"task_submitted", "task_claimed", "task_started", "attempt_finished"}
	if strings.Join(gotTypes, ",") != strings.Join(want, ",") {
		t.Fatalf("event order = %v, want %v", gotTypes, want)
	}
	var details map[string]any
	if err := json.Unmarshal(events[3].Details, &details); err != nil {
		t.Fatalf("details decode: %v", err)
	}
	if details["success"] != true {
		t.Errorf("attempt_finished success = %v, want true", details["success"])
	}
}

func TestExpiryExhaustSealsReceiptAndRedacts(t *testing.T) {
	clock := newFakeClock()
	s, _ := newTestStoreWithClock(t, clock)
	req := submitReq("expiry-evidence")
	req.MaxAttempts = 1
	mustSubmit(t, s, req)

	if _, err := s.ClaimTask(context.Background(), "worker-1", 10); err != nil {
		t.Fatalf("claim: %v", err)
	}
	clock.Advance(11 * time.Second)
	if n, err := s.ReconcileExpired(context.Background()); err != nil || n != 1 {
		t.Fatalf("reconcile = %d/%v, want 1/nil", n, err)
	}

	exhausted, _ := s.ListTasks(context.Background(), TaskFilter{})
	if len(exhausted) != 1 || exhausted[0].State != StateFailed {
		t.Fatalf("state = %+v, want single FAILED task", exhausted)
	}
	if exhausted[0].ReceiptHash == nil {
		t.Error("exhausted task must seal a receipt via reconciler")
	}
	if _, hasPrompt := requestDoc(t, exhausted[0])["prompt"]; hasPrompt {
		t.Error("exhausted task must redact prompt via reconciler")
	}
}

func TestListChildTasksAndGetTaskLineage(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// 1. Submit root task
	rootReq := submitReq("root-task-1")
	rootTask := mustSubmit(t, s, rootReq)

	// 2. Submit 2 child tasks referencing root
	child1Req := submitReq("child-task-1")
	child1Req.ParentTaskID = &rootTask.TaskID
	child1Task := mustSubmit(t, s, child1Req)

	child2Req := submitReq("child-task-2")
	child2Req.ParentTaskID = &rootTask.TaskID
	child2Task := mustSubmit(t, s, child2Req)

	// 3. Submit grandchild task referencing child1
	grandchildReq := submitReq("grandchild-task-1")
	grandchildReq.ParentTaskID = &child1Task.TaskID
	grandchildTask := mustSubmit(t, s, grandchildReq)

	// Test ListChildTasks
	children, err := s.ListChildTasks(ctx, rootTask.TaskID)
	if err != nil {
		t.Fatalf("ListChildTasks: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("ListChildTasks count = %d, want 2", len(children))
	}
	if children[0].TaskID != child1Task.TaskID || children[1].TaskID != child2Task.TaskID {
		t.Errorf("child tasks mismatch: got [%s, %s]", children[0].TaskID, children[1].TaskID)
	}

	// Test GetTaskLineage on grandchild -> [root, child1, grandchild]
	lineage, err := s.GetTaskLineage(ctx, grandchildTask.TaskID)
	if err != nil {
		t.Fatalf("GetTaskLineage: %v", err)
	}
	if len(lineage) != 3 {
		t.Fatalf("lineage count = %d, want 3", len(lineage))
	}
	if lineage[0].TaskID != rootTask.TaskID || lineage[1].TaskID != child1Task.TaskID || lineage[2].TaskID != grandchildTask.TaskID {
		t.Errorf("lineage chain mismatch: got [%s, %s, %s], want [%s, %s, %s]",
			lineage[0].TaskID, lineage[1].TaskID, lineage[2].TaskID,
			rootTask.TaskID, child1Task.TaskID, grandchildTask.TaskID)
	}
}

func TestGetTaskLineageDeepTreeRecursiveCTE(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	const depth = 25
	var taskIDs []string

	var lastParentID *string
	for i := 0; i < depth; i++ {
		req := submitReq(fmt.Sprintf("deep-tree-task-%d", i))
		req.ParentTaskID = lastParentID
		task := mustSubmit(t, s, req)
		taskIDs = append(taskIDs, task.TaskID)
		lastParentID = &task.TaskID
	}

	// Fetch lineage of the deepest leaf task
	deepestID := taskIDs[len(taskIDs)-1]
	lineage, err := s.GetTaskLineage(ctx, deepestID)
	if err != nil {
		t.Fatalf("GetTaskLineage on depth %d: %v", depth, err)
	}

	if len(lineage) != depth {
		t.Fatalf("lineage length = %d, want %d", len(lineage), depth)
	}

	for i, task := range lineage {
		if task.TaskID != taskIDs[i] {
			t.Errorf("lineage[%d] = %s, want %s", i, task.TaskID, taskIDs[i])
		}
	}

	// Non-existent task returns empty lineage without error
	emptyLineage, err := s.GetTaskLineage(ctx, "non-existent-task-id-xyz")
	if err != nil {
		t.Fatalf("GetTaskLineage on non-existent task: %v", err)
	}
	if len(emptyLineage) != 0 {
		t.Errorf("expected empty lineage for unknown task, got %d items", len(emptyLineage))
	}
}
