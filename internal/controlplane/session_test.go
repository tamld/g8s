package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionsRegistryLifecycle(t *testing.T) {
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()

	if err := store.RegisterSession(ctx, Session{ID: "sA", Mode: SessionModeInPlace}); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	sess, err := store.GetSession(ctx, "sA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Status != SessionStatusActive {
		t.Fatalf("status = %q, want active", sess.Status)
	}
	if sess.Mode != SessionModeInPlace {
		t.Fatalf("mode = %q, want in_place", sess.Mode)
	}
	if want := clock.Now().UTC(); !sess.StartedAt.Equal(want) {
		t.Fatalf("started_at = %v, want %v", sess.StartedAt, want)
	}

	clock.Advance(5 * time.Second)
	if err := store.HeartbeatSession(ctx, "sA"); err != nil {
		t.Fatalf("HeartbeatSession: %v", err)
	}
	sess, err = store.GetSession(ctx, "sA")
	if err != nil {
		t.Fatalf("GetSession after heartbeat: %v", err)
	}
	if want := clock.Now().UTC(); !sess.HeartbeatAt.Equal(want) {
		t.Fatalf("heartbeat_at = %v, want %v", sess.HeartbeatAt, want)
	}

	if err := store.MarkSessionDead(ctx, "sA"); err != nil {
		t.Fatalf("MarkSessionDead: %v", err)
	}
	if err := store.HeartbeatSession(ctx, "sA"); !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("HeartbeatSession on dead session = %v, want ErrSessionNotActive", err)
	}

	// Re-registering a dead session revives it with a fresh start time.
	clock.Advance(10 * time.Second)
	if err := store.RegisterSession(ctx, Session{ID: "sA", Mode: SessionModeInPlace}); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	sess, err = store.GetSession(ctx, "sA")
	if err != nil {
		t.Fatalf("GetSession after revive: %v", err)
	}
	if sess.Status != SessionStatusActive {
		t.Fatalf("status after revive = %q, want active", sess.Status)
	}
	if want := clock.Now().UTC(); !sess.StartedAt.Equal(want) {
		t.Fatalf("started_at after revive = %v, want %v", sess.StartedAt, want)
	}
}

func TestSessionsRegistryValidation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if err := store.RegisterSession(ctx, Session{ID: ""}); err == nil {
		t.Fatal("RegisterSession with empty id = nil error, want error")
	}
	if err := store.HeartbeatSession(ctx, "nope"); !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("HeartbeatSession unknown = %v, want ErrSessionNotActive", err)
	}
	if err := store.MarkSessionDead(ctx, "nope"); !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("MarkSessionDead unknown = %v, want ErrSessionNotActive", err)
	}
	if _, err := store.GetSession(ctx, "nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetSession unknown = %v, want sql.ErrNoRows", err)
	}
}

func TestSessionsRegistryWorktreeMode(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if err := store.RegisterSession(ctx, Session{
		ID:           "sB",
		Mode:         SessionModeWorktree,
		WorktreePath: "/tmp/wt/sB",
	}); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	sess, err := store.GetSession(ctx, "sB")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Mode != SessionModeWorktree || sess.WorktreePath != "/tmp/wt/sB" {
		t.Fatalf("roundtrip = %+v, want worktree mode with path", sess)
	}
}

func TestListActiveSessions(t *testing.T) {
	clock := newFakeClock()
	store, _ := newTestStoreWithClock(t, clock)
	ctx := context.Background()

	for _, id := range []string{"s1", "s2", "s3"} {
		if err := store.RegisterSession(ctx, Session{ID: id}); err != nil {
			t.Fatalf("RegisterSession %s: %v", id, err)
		}
		// Distinct ticks so ORDER BY started_at has no ties — the registry
		// does not promise a tie order.
		clock.Advance(time.Second)
	}
	if err := store.MarkSessionDead(ctx, "s2"); err != nil {
		t.Fatalf("MarkSessionDead: %v", err)
	}
	active, err := store.ListActiveSessions(ctx)
	if err != nil {
		t.Fatalf("ListActiveSessions: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("len(active) = %d, want 2", len(active))
	}
	if active[0].ID != "s1" || active[1].ID != "s3" {
		t.Fatalf("active = [%s, %s], want [s1 s3] (started_at order)", active[0].ID, active[1].ID)
	}
}

func TestSessionsRegistryReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.sqlite3")
	store, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	ctx := context.Background()
	if err := store.RegisterSession(ctx, Session{ID: "persist"}); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	sess, err := reopened.GetSession(ctx, "persist")
	if err != nil {
		t.Fatalf("GetSession after reopen: %v", err)
	}
	if sess.Status != SessionStatusActive {
		t.Fatalf("status after reopen = %q, want active", sess.Status)
	}
}

func TestLegacyDatabaseMigratesSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.sqlite3")
	store, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Downgrade the database to its pre-#393 shape: no sessions registry,
	// write_receipts without the session_id provenance column, user_version 10.
	raw := openRawDB(t, path)
	for _, stmt := range []string{
		"DROP TABLE sessions",
		"DROP TABLE IF EXISTS write_receipts",
		`CREATE TABLE write_receipts (
			receipt_id         TEXT PRIMARY KEY,
			issuer             TEXT NOT NULL,
			allowed_paths_json TEXT NOT NULL,
			expires_at         REAL NOT NULL,
			consumed           INTEGER NOT NULL DEFAULT 0,
			consumer_task_id   TEXT,
			created_at         REAL NOT NULL
		)`,
		"INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, consumed, created_at) VALUES ('r1', 'brain', '[]', 9999999999, 0, 100)",
		"PRAGMA user_version = 10",
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("legacy setup %q: %v", stmt, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	migrated, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("reopen legacy db: %v", err)
	}
	defer migrated.Close()
	ctx := context.Background()

	// Sessions registry is back and usable.
	if err := migrated.RegisterSession(ctx, Session{ID: "post-mig"}); err != nil {
		t.Fatalf("RegisterSession after migration: %v", err)
	}
	// Legacy write_receipts rows gained the provenance column and are stampable.
	if err := migrated.AttachWriteReceiptSession(ctx, "r1", "post-mig"); err != nil {
		t.Fatalf("AttachWriteReceiptSession on migrated row: %v", err)
	}
}

func TestSessionsActivePartialIndex(t *testing.T) {
	store, path := newTestStore(t)
	_ = store
	raw := openRawDB(t, path)
	defer raw.Close()
	var sqlText string
	if err := raw.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_sessions_active'").Scan(&sqlText); err != nil {
		t.Fatalf("partial index missing: %v", err)
	}
	if want := "WHERE status = 'active'"; !strings.Contains(sqlText, want) {
		t.Fatalf("index sql = %q, want it to contain %q", sqlText, want)
	}
}

func TestAttachWriteReceiptSession(t *testing.T) {
	store, path := newTestStore(t)
	ctx := context.Background()

	raw := openRawDB(t, path)
	// The mirror table is ensured lazily by lifecycle paths; create it here
	// the same way before seeding a receipt row.
	if _, err := raw.Exec(writeReceiptsEnsureDDL); err != nil {
		t.Fatalf("ensure write_receipts: %v", err)
	}
	if _, err := raw.Exec(`
		INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, consumed, created_at)
		VALUES ('r-1', 'brain', '["./tests/*.py"]', 9999999999, 0, 100)`); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}
	raw.Close()

	if err := store.AttachWriteReceiptSession(ctx, "r-1", "sA"); err != nil {
		t.Fatalf("AttachWriteReceiptSession: %v", err)
	}
	check := openRawDB(t, path)
	defer check.Close()
	var stamped sql.NullString
	if err := check.QueryRow("SELECT session_id FROM write_receipts WHERE receipt_id = 'r-1'").Scan(&stamped); err != nil {
		t.Fatalf("read back session_id: %v", err)
	}
	if !stamped.Valid || stamped.String != "sA" {
		t.Fatalf("session_id = %v, want sA", stamped)
	}

	if err := store.AttachWriteReceiptSession(ctx, "missing", "sA"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("AttachWriteReceiptSession unknown receipt = %v, want sql.ErrNoRows", err)
	}
	if err := store.AttachWriteReceiptSession(ctx, "r-1", ""); err == nil {
		t.Fatal("AttachWriteReceiptSession empty session = nil error, want error")
	}
}
