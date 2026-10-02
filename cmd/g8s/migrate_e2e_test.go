package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/pathutil"
	_ "modernc.org/sqlite"
)

// createOldestV1DatabaseFixture creates a SQLite database file with the
// oldest supported control-plane schema (v1 layout from the Python/Go baseline),
// having user_version = 1 and only the initial minimal tasks / task_events /
// control_plane_maintenance tables without any subsequent migration columns.
func createOldestV1DatabaseFixture(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, "_pragma=foreign_keys(ON)"))
	if err != nil {
		t.Fatalf("create fixture db: %v", err)
	}
	defer db.Close()

	v1Schema := `
	CREATE TABLE tasks (
		task_id TEXT PRIMARY KEY,
		idempotency_key TEXT NOT NULL UNIQUE,
		schema_version TEXT NOT NULL,
		state TEXT NOT NULL,
		priority INTEGER NOT NULL,
		request_json TEXT NOT NULL,
		request_hash TEXT NOT NULL,
		result_json TEXT,
		result_hash TEXT,
		receipt_hash TEXT,
		attempts INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL,
		lease_owner TEXT,
		lease_token TEXT,
		lease_expires_at REAL,
		cancel_requested INTEGER NOT NULL DEFAULT 0,
		created_at REAL NOT NULL,
		updated_at REAL NOT NULL,
		completed_at REAL,
		last_error TEXT
	);
	CREATE INDEX idx_tasks_claim ON tasks(state, priority DESC, created_at ASC);
	CREATE TABLE task_events (
		event_id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
		timestamp REAL NOT NULL,
		event_type TEXT NOT NULL,
		actor TEXT NOT NULL,
		details_json TEXT NOT NULL
	);
	CREATE INDEX idx_task_events_task ON task_events(task_id, event_id);
	CREATE TABLE control_plane_maintenance (
		singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
		owner TEXT NOT NULL,
		expires_at REAL NOT NULL,
		updated_at REAL NOT NULL
	);
	PRAGMA user_version = 1;
	`

	if _, err := db.Exec(v1Schema); err != nil {
		t.Fatalf("apply v1 fixture schema: %v", err)
	}

	// Seed a legacy v1 task to ensure pre-existing rows survive migration
	seedStmt := `
	INSERT INTO tasks (
		task_id, idempotency_key, schema_version, state, priority,
		request_json, request_hash, attempts, max_attempts,
		cancel_requested, created_at, updated_at
	) VALUES (
		'legacy-v1-task', 'legacy-key-v1', '1.0', 'QUEUED', 10,
		'{"prompt":"legacy v1 prompt","role":"collector"}', 'hash-v1', 0, 3,
		0, 1000.0, 1000.0
	);`
	if _, err := db.Exec(seedStmt); err != nil {
		t.Fatalf("seed v1 task: %v", err)
	}
}

// TestMigrateE2E_OldDBToNewBinary tests the end-to-end upgrade path protecting
// the oldest users:
// 1. A legacy v1 database (user_version = 1) is present in the source dir.
// 2. The CLI migrate command (MigrateData) moves it to the canonical data dir.
// 3. The current binary opens the DB via controlplane.NewControlPlane.
// 4. Schema is automatically migrated from v1 to SchemaVersion (currently 11).
// 5. Existing legacy task is preserved.
// 6. SubmitTask -> ClaimTask -> FinishAttempt round-trip succeeds.
// 7. PRAGMA user_version == controlplane.SchemaVersion.
func TestMigrateE2E_OldDBToNewBinary(t *testing.T) {
	srcDir := t.TempDir()
	destDir := t.TempDir()

	srcDBFile := filepath.Join(srcDir, "g8s.db")
	createOldestV1DatabaseFixture(t, srcDBFile)

	// Verify fixture starts at user_version 1
	{
		db, err := sql.Open("sqlite", pathutil.SQLiteURI(srcDBFile, ""))
		if err != nil {
			t.Fatalf("open fixture db: %v", err)
		}
		var initialVersion int
		if err := db.QueryRow("PRAGMA user_version").Scan(&initialVersion); err != nil {
			db.Close()
			t.Fatalf("read initial user_version: %v", err)
		}
		db.Close()
		if initialVersion != 1 {
			t.Fatalf("fixture initial user_version = %d, want 1", initialVersion)
		}
	}

	// 1. Run the CLI file-level migration
	report, err := MigrateData(srcDir, destDir, false, false)
	if err != nil {
		t.Fatalf("MigrateData failed: %v", err)
	}
	if report.TotalFiles < 1 {
		t.Fatalf("expected at least 1 file migrated, got %d", report.TotalFiles)
	}

	destDBFile := filepath.Join(destDir, "g8s.db")

	// 2. Open migrated DB with current binary's NewControlPlane
	store, err := controlplane.NewControlPlane(destDBFile, nil)
	if err != nil {
		t.Fatalf("NewControlPlane on migrated v1 db failed: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	}()

	ctx := context.Background()

	// 3. Verify the pre-existing legacy task is preserved
	legacyTask, err := store.GetTask(ctx, "legacy-v1-task")
	if err != nil {
		t.Fatalf("GetTask for legacy task: %v", err)
	}
	if legacyTask == nil {
		t.Fatalf("legacy task not found after migration")
	}
	if legacyTask.TaskID != "legacy-v1-task" {
		t.Errorf("legacy task ID = %s, want legacy-v1-task", legacyTask.TaskID)
	}

	// 4. Execute SubmitTask + ClaimTask + FinishAttempt round-trip
	submitReq := controlplane.SubmitTaskRequest{
		IdempotencyKey: "migrated-e2e-task-001",
		Payload:        []byte(`{"prompt":"migrated e2e test","role":"collector"}`),
		MaxAttempts:    3,
		Model:          "smoke-model",
		Priority:       100, // Higher priority so it is claimed next
		AddDirs:        []string{"/tmp/g8s-cp-test-scope"},
	}
	submitted, err := store.SubmitTask(ctx, submitReq)
	if err != nil {
		t.Fatalf("SubmitTask on migrated DB failed: %v", err)
	}
	if submitted == nil {
		t.Fatalf("SubmitTask returned nil task")
	}

	claimed, err := store.ClaimTask(ctx, "worker-migrate-e2e", 60)
	if err != nil {
		t.Fatalf("ClaimTask on migrated DB failed: %v", err)
	}
	if claimed == nil {
		t.Fatalf("ClaimTask returned nil task")
	}
	if claimed.TaskID != submitted.TaskID {
		t.Fatalf("claimed task %s, want %s", claimed.TaskID, submitted.TaskID)
	}

	token := ""
	if claimed.LeaseToken != nil {
		token = *claimed.LeaseToken
	}
	if !store.StartTask(claimed.TaskID, "worker-migrate-e2e", token) {
		t.Fatalf("StartTask failed on migrated DB")
	}

	finished, err := store.FinishAttempt(claimed.TaskID, "worker-migrate-e2e", token, controlplane.FinishAttemptParams{
		Result:  json.RawMessage(`{"migrated_e2e":"ok"}`),
		Success: true,
	})
	if err != nil {
		t.Fatalf("FinishAttempt on migrated DB failed: %v", err)
	}
	if finished.State != controlplane.StateWorkerCompleted {
		t.Fatalf("finished task state = %s, want WORKER_COMPLETED", finished.State)
	}

	// 5. Assert PRAGMA user_version is bumped to current SchemaVersion
	{
		db, err := sql.Open("sqlite", pathutil.SQLiteURI(destDBFile, ""))
		if err != nil {
			t.Fatalf("open raw migrated db: %v", err)
		}
		defer db.Close()

		var finalVersion int
		if err := db.QueryRow("PRAGMA user_version").Scan(&finalVersion); err != nil {
			t.Fatalf("read final user_version: %v", err)
		}
		if finalVersion != controlplane.SchemaVersion {
			t.Fatalf("PRAGMA user_version after migration = %d, want %d", finalVersion, controlplane.SchemaVersion)
		}
	}
}
