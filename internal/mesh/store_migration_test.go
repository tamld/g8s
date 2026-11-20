package mesh

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func ledgerUserVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return v
}

func ledgerHasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	return false
}

// TestMeshLedgerSchemaVersionRecorded verifies the mesh ledger stamps
// PRAGMA user_version with the current schema version so FUTURE schema
// additions can be detected and migrated on reopen (C-5, issue #984).
func TestMeshLedgerSchemaVersionRecorded(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if v := ledgerUserVersion(t, s.db); v != meshLedgerSchemaVersion {
		t.Fatalf("fresh store user_version = %d, want %d", v, meshLedgerSchemaVersion)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if v := ledgerUserVersion(t, s2.db); v != meshLedgerSchemaVersion {
		t.Fatalf("reopened store user_version = %d, want %d", v, meshLedgerSchemaVersion)
	}
}

// TestMeshLedgerMigrateLegacyMissingColumn simulates a FUTURE column addition:
// an existing ledger.db (current schema, real data) hand-altered to drop the
// column a newer binary expects. Opening the store must add the column back
// via the defensive PRAGMA table_info + ALTER TABLE scaffolding WITHOUT
// touching existing rows, stamp user_version, and keep INSERTs working (C-5).
func TestMeshLedgerMigrateLegacyMissingColumn(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	retentionSetupPair(t, s)
	retentionSend(t, s, "pre_migration_msg")
	retentionAck(t, s, "pre_migration_msg")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Hand-alter the DB into a legacy shape missing a later-added column.
	legacy, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`ALTER TABLE claims DROP COLUMN artifact_sha256`); err != nil {
		legacy.Close()
		t.Fatalf("simulate legacy schema: %v", err)
	}
	legacy.Close()

	// Register the migration the "newer binary" would ship, then open through
	// the real entry point.
	orig := ledgerColumnMigrations
	ledgerColumnMigrations = []ledgerColumnMigration{
		{table: "claims", column: "artifact_sha256", def: "TEXT NOT NULL DEFAULT ''"},
	}
	defer func() { ledgerColumnMigrations = orig }()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen migrated: %v", err)
	}
	defer s2.Close()

	if !ledgerHasColumn(t, s2.db, "claims", "artifact_sha256") {
		t.Fatal("expected migrated claims.artifact_sha256 column to be restored")
	}
	if v := ledgerUserVersion(t, s2.db); v != meshLedgerSchemaVersion {
		t.Fatalf("migrated store user_version = %d, want %d", v, meshLedgerSchemaVersion)
	}

	// Data must survive the migration untouched.
	var n int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id = 'pre_migration_msg'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("data loss after migration: count=%d err=%v", n, err)
	}

	// The migrated schema must accept INSERTs that reference the restored
	// column (the pre-fix failure mode: "no such column" bricks every claim).
	if _, err := s2.db.Exec(`INSERT OR IGNORE INTO claims(task_id, node_id, incarnation, resource, paths, context_revision, expires_at, state, artifact_ref, artifact_sha256)
		VALUES('migrated_task', 'sender', 's1', 'res', '[]', 0, 9999999999, 'released', '', 'abc')`); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
}

// TestMeshLedgerMigrationIdempotentReopen verifies a second Open on an
// already-migrated DB takes the fast path and does not re-apply migrations.
func TestMeshLedgerMigrationIdempotentReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	orig := ledgerColumnMigrations
	ledgerColumnMigrations = []ledgerColumnMigration{
		{table: "claims", column: "artifact_sha256", def: "TEXT NOT NULL DEFAULT ''"},
	}
	defer func() { ledgerColumnMigrations = orig }()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if v := ledgerUserVersion(t, s2.db); v != meshLedgerSchemaVersion {
		t.Fatalf("user_version = %d, want %d", v, meshLedgerSchemaVersion)
	}
}

// TestMeshLedgerRejectsNewerSchemaVersion protects against downgrade: a
// ledger written by a NEWER binary must not be silently "migrated" backwards.
func TestMeshLedgerRejectsNewerSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(dir)
	if err == nil {
		s.Close()
		t.Fatal("expected Open to reject a newer schema version")
	}
	if !strings.Contains(err.Error(), "mesh ledger schema version") {
		t.Fatalf("expected schema version error, got: %v", err)
	}
}

// TestMeshSnapshotClaimsBeyondLegacyLimit proves the snapshot claim list is
// not silently truncated at the legacy LIMIT 1000 (C-8, issue #984).
func TestMeshSnapshotClaimsBeyondLegacyLimit(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgMustExec(t, s, "register", map[string]any{"node_id": "claimer", "incarnation": "c1"})
	// The literal pins the legacy limit: shrinking snapshotClaimLimit below
	// the old 1000 cap makes this test fail again.
	const total = 1050
	for i := 0; i < total; i++ {
		msgMustExec(t, s, "claim", map[string]any{
			"node_id": "claimer", "incarnation": "c1",
			"task_id":          "snap_task_" + string(rune('a'+i%26)) + "_" + itoa(i),
			"resource":         "res_" + itoa(i),
			"paths":            []string{"p" + itoa(i) + ".txt"},
			"context_revision": 0,
			"lease_seconds":    300,
		})
	}

	var snap msgSnapResp
	if err := json.Unmarshal(msgMustExec(t, s, "snapshot", map[string]any{}), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Claims) != total {
		t.Fatalf("snapshot claims silently truncated: got %d, want %d", len(snap.Claims), total)
	}
}

// TestMeshLedgerBusyTimeout verifies the mesh ledger raises busy_timeout
// above the legacy 5s (C-6, issue #984): read paths write (keepalive inserts
// inside inbox polls) and msgWait polls every 100ms, so 5s is too tight.
func TestMeshLedgerBusyTimeout(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var ms int
	if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if ms != meshLedgerBusyTimeoutMs {
		t.Fatalf("busy_timeout = %dms, want %dms", ms, meshLedgerBusyTimeoutMs)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
