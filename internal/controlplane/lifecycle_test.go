package controlplane

import (
	"context"
	"errors"
	"testing"
)

// #346: delegated-write receipts consume exactly once; replay by another
// task trips ErrReceiptAlreadyConsumed; re-consume by the owner is
// idempotent; unknown receipts trip ErrReceiptUnknownOrExpired.
func TestConsumeWriteReceipt(t *testing.T) {
	cp, _ := newTestStore(t)
	ctx := context.Background()

	// First consume on a worker-only store lazily creates the receipt
	// schema (#346) and reports the receipt as unknown.
	if err := cp.ConsumeWriteReceipt(ctx, "rcpt-missing", "task-A"); !errors.Is(err, ErrReceiptUnknownOrExpired) {
		t.Fatalf("unknown receipt should trip ErrReceiptUnknownOrExpired, got %v", err)
	}

	now := float64(cp.clock().UnixNano()) / 1e9
	if _, err := cp.db.Exec(
		`INSERT INTO write_receipts (receipt_id, issuer, allowed_paths_json, expires_at, consumed, consumer_task_id, created_at)
		 VALUES (?, ?, ?, ?, 0, NULL, ?)`,
		"rcpt-test-1", "brain", `["README.vi.md"]`, now+600, now); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}

	if err := cp.ConsumeWriteReceipt(ctx, "rcpt-test-1", "task-A"); err != nil {
		t.Fatalf("first consume should succeed: %v", err)
	}
	if err := cp.ConsumeWriteReceipt(ctx, "rcpt-test-1", "task-A"); err != nil {
		t.Fatalf("re-consume by owning task should be idempotent: %v", err)
	}
	err := cp.ConsumeWriteReceipt(ctx, "rcpt-test-1", "task-B")
	if !errors.Is(err, ErrReceiptAlreadyConsumed) {
		t.Fatalf("replay by another task should trip ErrReceiptAlreadyConsumed, got %v", err)
	}
}
