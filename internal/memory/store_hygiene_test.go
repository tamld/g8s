package memory

// Red tests for #986 part-2 batch 2 (blind-audit registries C-7, C-11, C-12).
//
// C-7 — memory migrations swallow ALL ALTER errors (`_, _ =`): only the
// duplicate-column error that re-running an additive migration produces may
// be tolerated; every other failure (locked DB, corrupted schema, readonly
// file) must propagate out of initSchema.
//
// C-11 — CREATE UNIQUE INDEX (payload_hash, session_id) WHERE lifecycle NOT
// IN ('scratch','working') fails permanently on legacy databases that already
// contain duplicate active rows, bricking the DB. The migration must pre-dedup
// (archive duplicates, keep the newest) before creating the index.
//
// C-12 — ProposeEntry's tombstone check and INSERT/UPDATE are separate
// implicit transactions: a cross-process RevokeBySession completing between
// them gets bypassed and the revoked payload resurrects as a fresh active
// row. Tombstone check + promotion write must share one BEGIN IMMEDIATE
// transaction.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// legacyMemorySchema is a pre-migration memory_entries table (before the 15
// additive columns) plus the tombstones table — the shape an old binary left
// on disk.
const legacyMemorySchema = `
CREATE TABLE memory_entries (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	lifecycle TEXT NOT NULL,
	trust TEXT NOT NULL,
	salience INTEGER NOT NULL DEFAULT 0,
	last_used_at REAL NOT NULL DEFAULT 0,
	scope TEXT NOT NULL,
	session_id TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	promoted_by TEXT NOT NULL DEFAULT '',
	payload TEXT NOT NULL,
	payload_hash TEXT NOT NULL,
	created_at REAL NOT NULL,
	updated_at REAL NOT NULL
);
CREATE TABLE memory_tombstones (
	payload_hash TEXT PRIMARY KEY,
	reason TEXT NOT NULL DEFAULT '',
	created_at REAL NOT NULL
);
`

func createLegacyMemoryDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(legacyMemorySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	return db
}

// TestAddColumnToleratesOnlyDuplicateColumn (C-7): the migration helper
// tolerates the duplicate-column error of re-running an additive migration
// and propagates every other failure.
func TestAddColumnToleratesOnlyDuplicateColumn(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE probe (a TEXT)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := addColumnToleratingDuplicate(db, `ALTER TABLE probe ADD COLUMN b TEXT`); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := addColumnToleratingDuplicate(db, `ALTER TABLE probe ADD COLUMN b TEXT`); err != nil {
		t.Fatalf("duplicate-column migration must be tolerated, got: %v", err)
	}
	if err := addColumnToleratingDuplicate(db, `ALTER TABLE missing_table ADD COLUMN b TEXT`); err == nil {
		t.Fatal("non-duplicate migration failure must propagate, got nil")
	}
}

// TestLegacyDBOpensCleanly (C-7 happy path + C-11): a pre-migration database
// with duplicate ACTIVE rows for the same (payload_hash, session_id) must
// open cleanly; the duplicates are pre-deduped — newest kept active, older
// duplicates archived — before the unique index is created.
func TestLegacyDBOpensCleanlyAndDedupsDuplicates(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	legacy := createLegacyMemoryDB(t, dbPath)

	const dupHash = "aaaa-bbbb"
	rows := []struct {
		id        string
		lifecycle string
		updatedAt float64
	}{
		{"dup-old", "active", 100},
		{"dup-new", "active", 200},
		{"solo", "active", 300},
		{"scratch-kept", "scratch", 400},
	}
	for _, r := range rows {
		session := "s-dup"
		if r.id == "solo" {
			session = "s-solo"
		}
		if _, err := legacy.Exec(`
			INSERT INTO memory_entries (id, kind, lifecycle, trust, scope, session_id, payload, payload_hash, created_at, updated_at)
			VALUES (?, 'fact', ?, 'unverified', 'session', ?, ?, ?, 1, ?)`,
			r.id, r.lifecycle, session, "payload-"+r.id, dupHash, r.updatedAt); err != nil {
			t.Fatalf("seed %s: %v", r.id, err)
		}
	}
	_ = legacy.Close()

	adapter, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{DBPath: dbPath})
	if err != nil {
		t.Fatalf("legacy DB with duplicate active rows must open cleanly, got: %v", err)
	}
	defer adapter.Close()

	ctx := context.Background()
	old, err := adapter.GetEntry(ctx, "dup-old")
	if err != nil {
		t.Fatalf("get dup-old: %v", err)
	}
	// The partial unique index covers every lifecycle except scratch/working
	// (including 'archived'), so demotion to scratch is the only state change
	// that removes a duplicate from the index without deleting it.
	if old.Lifecycle != LifecycleScratch {
		t.Errorf("older duplicate lifecycle = %s, want scratch (demoted out of the unique index)", old.Lifecycle)
	}
	if old.ReviewFlag == "" {
		t.Errorf("older duplicate review_flag empty — demoted duplicates should be flagged for review")
	}
	fresh, err := adapter.GetEntry(ctx, "dup-new")
	if err != nil {
		t.Fatalf("get dup-new: %v", err)
	}
	if fresh.Lifecycle != LifecycleActive {
		t.Errorf("newest duplicate lifecycle = %s, want active", fresh.Lifecycle)
	}
	scratch, err := adapter.GetEntry(ctx, "scratch-kept")
	if err != nil {
		t.Fatalf("get scratch-kept: %v", err)
	}
	if scratch.Lifecycle != LifecycleScratch {
		t.Errorf("scratch row lifecycle = %s, want scratch (dedup must not touch scratch/working)", scratch.Lifecycle)
	}
}

// TestProposeEntryDoesNotResurrectRevokedPayload (C-12): a revocation that
// completes while a ProposeEntry is in flight (between the gate checks and
// the promotion write) must not be bypassed — the tombstone check and the
// write share one transaction, so the propose observes the fresh tombstone
// and refuses.
func TestProposeEntryDoesNotResurrectRevokedPayload(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")

	// Two adapter instances over the same database file simulate two
	// processes (their in-process mutexes are independent).
	adapterA, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{DBPath: dbPath})
	if err != nil {
		t.Fatalf("adapter A: %v", err)
	}
	defer adapterA.Close()
	adapterB, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{DBPath: dbPath})
	if err != nil {
		t.Fatalf("adapter B: %v", err)
	}
	defer adapterB.Close()

	ctx := context.Background()

	// The victim session captured the payload earlier.
	victim := &MemoryEntry{
		ID:        "victim-scratch",
		Kind:      KindFact,
		Scope:     ScopeSession,
		SessionID: "S-victim",
		Payload:   "resurrect-me",
	}
	if err := adapterB.CaptureScratch(ctx, victim); err != nil {
		t.Fatalf("capture scratch: %v", err)
	}

	// Scheduling seam: the revocation completes exactly while the propose is
	// in flight, after its gate checks and before its write transaction.
	adapterA.proposeSeam = func() {
		if _, rerr := adapterB.RevokeBySession(ctx, "S-victim"); rerr != nil {
			t.Errorf("RevokeBySession: %v", rerr)
		}
	}

	attacker := &MemoryEntry{
		ID:        "attacker-entry",
		Kind:      KindFact,
		Scope:     ScopeSession,
		SessionID: "S-attacker",
		Payload:   "resurrect-me",
	}
	err = adapterA.ProposeEntry(ctx, attacker)
	if err == nil {
		t.Fatal("ProposeEntry succeeded after a concurrent revocation tombstoned the payload — resurrected")
	}
	if !errors.Is(err, ErrTombstoned) {
		t.Errorf("error = %v, want ErrTombstoned", err)
	}

	// No active entry with the revoked payload may exist afterward.
	entries, lerr := adapterB.ListEntries(ctx, MemoryFilter{Lifecycle: LifecycleActive})
	if lerr != nil {
		t.Fatalf("list active: %v", lerr)
	}
	for _, e := range entries {
		if e.Payload == "resurrect-me" {
			t.Errorf("revoked payload resurrected as active entry %s", e.ID)
		}
	}
}
