package receipt

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestFenceExpiredLeaseFailsClosed(t *testing.T) {
	now := time.Unix(1700000000, 0)
	m, err := NewReceiptManager(filepath.Join(t.TempDir(), "receipts.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	b := &ClaimBinding{TaskID: "task", NodeID: "node", Incarnation: "inc", Resource: "repo", Paths: []string{"src/a.go"}, ExpiresAt: now.Add(10 * time.Second)}
	r, err := m.IssueReceipt("brain", b.Paths, time.Minute, withConcernBDisabled(), WithClaimBinding(b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.VerifyReceipt(r.ReceiptID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Second)
	if err := b.Validate(now); !errors.Is(err, ErrClaimFenceStale) {
		t.Fatalf("expired binding: %v", err)
	}
	_, err = m.ValidateAndConsume(r.ReceiptID, "late-write")
	var expired *ExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("expired mutation authority accepted: %v", err)
	}
	var consumed int
	if err := m.db.QueryRow("SELECT consumed FROM write_receipts WHERE receipt_id = ?", r.ReceiptID).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if consumed != 0 {
		t.Fatalf("refused receipt consumed = %d", consumed)
	}
}
