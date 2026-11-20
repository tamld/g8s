package receipt

import (
	"context"
	"testing"
	"time"
)

// PRAGMA synchronous values (SQLite documented constants).
const (
	pragmaSynchronousOff    = 0
	pragmaSynchronousNormal = 1
	pragmaSynchronousFull   = 2
)

// TestReceiptsDBSynchronousFULL pins the durability pragma of the receipts
// ledger (issue #984 C-2): a consume acknowledged to the caller must survive
// an OS crash / power loss. With synchronous(NORMAL) in WAL mode the commit
// is not fsynced, so an acknowledged consume can be rolled back on power loss
// — turning an already-used (single-use) receipt back into a replayable one.
// Control-plane (store.go) and mesh (store.go) already run synchronous(FULL);
// the receipts ledger must match. Every pooled connection inherits the DSN
// pragma, so inspecting one opened connection is representative.
func TestReceiptsDBSynchronousFULL(t *testing.T) {
	m := newTestManager(t)

	conn, err := m.db.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin connection for pragma inspection: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var mode int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatalf("read PRAGMA synchronous: %v", err)
	}
	if mode != pragmaSynchronousFull {
		t.Fatalf("receipts.db PRAGMA synchronous = %d (0=OFF, 1=NORMAL), want %d (FULL): "+
			"acknowledged consumes are not crash-durable under WAL+NORMAL", mode, pragmaSynchronousFull)
	}
}

// TestReceiptsDBSynchronousFULLAfterConsume re-checks the pragma on a second,
// freshly pooled connection after a consume has run, guaranteeing the pragma
// is connection-level (DSN-applied) rather than set on one connection only.
func TestReceiptsDBSynchronousFULLAfterConsume(t *testing.T) {
	m := newTestManager(t)

	r := mustIssue(t, m, "brain-main", []string{"src/**"}, 60*time.Second)
	if _, err := m.ValidateAndConsume(r.ReceiptID, "task-durability"); err != nil {
		t.Fatalf("ValidateAndConsume: unexpected error: %v", err)
	}

	conn, err := m.db.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin second connection for pragma inspection: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var mode int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatalf("read PRAGMA synchronous: %v", err)
	}
	if mode != pragmaSynchronousFull {
		t.Fatalf("receipts.db PRAGMA synchronous = %d on pooled connection, want %d (FULL)", mode, pragmaSynchronousFull)
	}
}
