package receipt

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestConsumeHandleSynchronousFULL pins the durability pragma of the consume
// path (issue #984 C-2): a consume acknowledged to the caller must survive an
// OS crash / power loss. Under WAL + synchronous(NORMAL) the consume commit
// is not fsynced, so an acknowledged consume can roll back — turning an
// already-used (single-use) receipt back into a replayable one. Consume is
// rare and security-critical, so it runs on a dedicated handle whose DSN pins
// synchronous(FULL); every connection of that handle inherits the pragma, so
// inspecting one opened connection is representative.
func TestConsumeHandleSynchronousFULL(t *testing.T) {
	m := newTestManager(t)

	conn, err := m.consumeDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin consume-handle connection for pragma inspection: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var mode int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatalf("read PRAGMA synchronous on consume handle: %v", err)
	}
	if mode != synchronousModeFull {
		t.Fatalf("consume handle PRAGMA synchronous = %d (0=OFF, 1=NORMAL), want %d (FULL): "+
			"acknowledged consumes are not crash-durable under WAL+NORMAL", mode, synchronousModeFull)
	}
}

// TestIssuePoolSynchronousNORMAL pins the throughput posture of the issue hot
// path (issue #984 C-2 CI follow-up): bulk dispatch issues receipts over the
// pooled connections, and WAL + synchronous(NORMAL) keeps those commits off
// the per-commit fsync path. When FULL leaked into the issue pool, the
// windows-latest race job measured 1000 issues at ~52s (~52ms per fsync).
// A lost issue commit also fails closed (the receipt never validates), so
// NORMAL is the correct durability/perf trade-off here. The assertion runs
// after a real consume to prove the FULL pragma never leaks across handles.
func TestIssuePoolSynchronousNORMAL(t *testing.T) {
	m := newTestManager(t)

	r := mustIssue(t, m, "brain-main", []string{"src/**"}, 60*time.Second)
	if _, err := m.ValidateAndConsume(r.ReceiptID, "task-durability"); err != nil {
		t.Fatalf("ValidateAndConsume: unexpected error: %v", err)
	}

	conn, err := m.db.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin issue-pool connection for pragma inspection: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var mode int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatalf("read PRAGMA synchronous on issue pool: %v", err)
	}
	if mode != synchronousModeNormal {
		t.Fatalf("issue-pool PRAGMA synchronous = %d (0=OFF, 2=FULL), want %d (NORMAL): "+
			"per-commit fsyncs on the issue hot path blew the dispatch throughput budget", mode, synchronousModeNormal)
	}
}

// TestConsumeAfterRevokeStaysDurable checks the second consume-path writer:
// RevokeReceipt destroys live authorization state with the same ack-loss
// replay hazard as consume, so it must run on the FULL-sync handle. After a
// revoke, the consume handle must still be usable (handle health) and the
// issue pool must remain NORMAL (no pragma leakage).
func TestConsumeAfterRevokeStaysDurable(t *testing.T) {
	m := newTestManager(t)

	r := mustIssue(t, m, "brain-main", []string{"src/**"}, 60*time.Second)
	revoked, err := m.RevokeReceipt(r.ReceiptID)
	if err != nil {
		t.Fatalf("RevokeReceipt: unexpected error: %v", err)
	}
	if !revoked {
		t.Fatal("RevokeReceipt reported false for a live receipt")
	}
	var missing *NotFoundError
	if _, err := m.ValidateAndConsume(r.ReceiptID, "task-x"); !errors.As(err, &missing) {
		t.Fatalf("revoked receipt must be gone (not found), got: %v", err)
	}

	// A fresh receipt still consumes through the durability handle.
	r2 := mustIssue(t, m, "brain-main", []string{"src/**"}, 60*time.Second)
	if _, err := m.ValidateAndConsume(r2.ReceiptID, "task-y"); err != nil {
		t.Fatalf("ValidateAndConsume after revoke: unexpected error: %v", err)
	}

	conn, err := m.db.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin issue-pool connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var mode int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&mode); err != nil {
		t.Fatalf("read PRAGMA synchronous on issue pool: %v", err)
	}
	if mode != synchronousModeNormal {
		t.Fatalf("issue-pool PRAGMA synchronous = %d after consume+revoke, want %d (NORMAL)", mode, synchronousModeNormal)
	}
}
