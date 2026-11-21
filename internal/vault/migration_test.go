package vault

// Red test for #986 part-2 batch 2 (blind-audit registry C-7): vault schema
// migrations must tolerate ONLY the duplicate-column error of re-running an
// additive migration and propagate every other failure — `_, _ =` swallowing
// everything can silently skip a migration and corrupt later inserts.

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestVaultAddColumnToleratesOnlyDuplicateColumn: the vault migration helper
// tolerates the duplicate-column error of re-running an additive migration
// and propagates every other failure.
func TestVaultAddColumnToleratesOnlyDuplicateColumn(t *testing.T) {
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
