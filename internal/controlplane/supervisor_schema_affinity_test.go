package controlplane

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// supervisorMigrationAffinities mirrors the fresh-shape DDL in
// applySupervisorSchema: every supervisor column and the declared SQLite
// affinity the migration must use when ALTER-adding it to a pre-v4 database
// (#986 C-13). Numeric columns must carry numeric affinity, not TEXT.
var supervisorMigrationAffinities = map[string]map[string]string{
	"supervisor_tasks": {
		"id": "TEXT", "state": "TEXT", "envelope_json": "TEXT",
		"approach_idx": "INTEGER", "attempt_idx": "INTEGER",
		"parent_task_id": "TEXT", "created_at": "REAL", "updated_at": "REAL",
		"session_id": "TEXT",
	},
	"supervisor_decisions": {
		"id": "TEXT", "task_id": "TEXT", "kind": "TEXT",
		"payload_json": "TEXT", "created_at": "REAL",
	},
	"supervisor_metrics": {
		"supervisor_task_id": "TEXT", "envelope_score": "REAL",
		"first_attempt_success": "INTEGER", "attempts_to_success": "INTEGER",
		"approaches_to_success": "INTEGER", "rca_confidence_avg": "REAL",
		"cycle_duration_seconds": "REAL", "escalation_count": "INTEGER",
		"false_escalation_rate": "REAL",
	},
}

func supervisorColumnTypes(t *testing.T, db *sql.DB, table string) map[string]string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	types := map[string]string{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan %s table_info: %v", table, err)
		}
		types[name] = strings.ToUpper(colType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s table_info: %v", table, err)
	}
	return types
}

func assertSupervisorAffinities(t *testing.T, db *sql.DB) {
	t.Helper()
	for table, wantCols := range supervisorMigrationAffinities {
		got := supervisorColumnTypes(t, db, table)
		for col, wantType := range wantCols {
			gotType, ok := got[col]
			if !ok {
				t.Errorf("%s.%s missing after migration", table, col)
				continue
			}
			if gotType != wantType {
				t.Errorf("%s.%s declared type = %q, want %q", table, col, gotType, wantType)
			}
		}
	}
}

// TestMigrateSupervisorSchemaNumericAffinity proves the supervisor migration
// adds missing numeric columns with numeric declared affinity instead of the
// blanket TEXT used before #986 C-13. The migration is driven against a
// legacy (pre-v4) database whose supervisor tables predate every column the
// migration owns, so each missing column goes through ALTER TABLE ADD COLUMN.
func TestMigrateSupervisorSchemaNumericAffinity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-supervisor.sqlite3")
	raw := openRawDB(t, path)
	defer func() { _ = raw.Close() }()

	// Legacy pre-v4 shape: only the TEXT identity columns exist; every
	// numeric column (and supervisor_tasks.session_id) is absent.
	legacy := []string{
		`CREATE TABLE supervisor_tasks (
			id            TEXT PRIMARY KEY,
			state         TEXT,
			envelope_json TEXT,
			parent_task_id TEXT
		)`,
		`CREATE TABLE supervisor_decisions (
			id     TEXT PRIMARY KEY,
			task_id TEXT,
			kind   TEXT
		)`,
		`CREATE TABLE supervisor_metrics (
			supervisor_task_id TEXT PRIMARY KEY
		)`,
	}
	for _, stmt := range legacy {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("create legacy table: %v", err)
		}
	}

	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := migrateSupervisorSchema(conn); err != nil {
		t.Fatalf("migrateSupervisorSchema: %v", err)
	}
	assertSupervisorAffinities(t, raw)

	// Idempotency: a second run must skip present columns (no error, and
	// affinities untouched).
	if err := migrateSupervisorSchema(conn); err != nil {
		t.Fatalf("second migrateSupervisorSchema: %v", err)
	}
	assertSupervisorAffinities(t, raw)
}

// TestMigrateSupervisorSchemaPreservesLegacyTextColumns documents the SQLite
// type-rewrite caveat (C-13): databases where an earlier migration already
// added a numeric column as TEXT keep TEXT affinity — the migration is
// idempotent and skips present columns, and SQLite cannot ALTER an existing
// column's declared type. Numeric affinity therefore applies only to columns
// this migration adds (fresh migrations / columns still missing).
func TestMigrateSupervisorSchemaPreservesLegacyTextColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-text.sqlite3")
	raw := openRawDB(t, path)
	defer func() { _ = raw.Close() }()

	// Legacy database where the old TEXT-only migration already ran on
	// supervisor_tasks.created_at; the other supervisor tables exist in
	// bare legacy shape (the real flow runs applySupervisorSchema before
	// the migration, so tables are present but may lack columns).
	legacy := []string{
		`CREATE TABLE supervisor_tasks (
			id TEXT PRIMARY KEY, state TEXT, envelope_json TEXT,
			parent_task_id TEXT, created_at TEXT
		)`,
		`CREATE TABLE supervisor_decisions (id TEXT PRIMARY KEY, task_id TEXT, kind TEXT)`,
		`CREATE TABLE supervisor_metrics (supervisor_task_id TEXT PRIMARY KEY)`,
	}
	for _, stmt := range legacy {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("create legacy table: %v", err)
		}
	}

	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := migrateSupervisorSchema(conn); err != nil {
		t.Fatalf("migrateSupervisorSchema: %v", err)
	}

	got := supervisorColumnTypes(t, raw, "supervisor_tasks")
	if gotType, ok := got["created_at"]; !ok {
		t.Errorf("created_at missing after migration")
	} else if gotType != "TEXT" {
		t.Errorf("legacy TEXT created_at was rewritten to %q; existing column types must be preserved", gotType)
	}
}
