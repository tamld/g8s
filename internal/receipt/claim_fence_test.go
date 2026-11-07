package receipt

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestClaimIssuanceBounds(t *testing.T) {
	now := time.Unix(1700000000, 0)
	m, err := NewReceiptManager(filepath.Join(t.TempDir(), "receipts.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	b := &ClaimBinding{TaskID: "claim-task", NodeID: "node", Incarnation: "inc", Resource: "repo", Paths: []string{"src/a.go"}, ExpiresAt: now.Add(10 * time.Second)}
	paths, err := b.ClipPaths([]string{"src/**"})
	if err != nil || len(paths) != 1 || paths[0] != "src/a.go" {
		t.Fatalf("clip %v %v", paths, err)
	}
	r, err := m.IssueReceipt("f0", paths, time.Minute, withConcernBDisabled(), WithClaimBinding(b))
	if err != nil {
		t.Fatal(err)
	}
	if r.ExpiresAt.After(b.ExpiresAt) {
		t.Fatal("receipt exceeds claim lease")
	}
	loaded, err := m.VerifyReceipt(r.ReceiptID)
	if err != nil || loaded.ClaimBinding == nil || loaded.ClaimBinding.TaskID != b.TaskID {
		t.Fatalf("binding round-trip: %v %v", loaded, err)
	}
	for _, paths := range [][]string{{"src/**"}, {"src/b.go"}, {"src/a.go/../../escape"}} {
		if _, err := m.IssueReceipt("f0", paths, time.Minute, withConcernBDisabled(), WithClaimBinding(b)); !errors.Is(err, ErrClaimFenceOutOfScope) {
			t.Fatalf("mint %v: %v", paths, err)
		}
	}
	now = now.Add(5 * time.Second)
	next := *b
	next.ExpiresAt = now.Add(300 * time.Second)
	if err := m.RenewClaimBinding(r.ReceiptID, &next); err != nil {
		t.Fatal(err)
	}
	loaded, err = m.VerifyReceipt(r.ReceiptID)
	if err != nil || loaded.ExpiresAt.After(next.ExpiresAt) || !loaded.ExpiresAt.Equal(next.ExpiresAt) {
		t.Fatalf("renew: %v %v", loaded, err)
	}
	now = next.ExpiresAt
	if _, err := m.ValidateAndConsume(r.ReceiptID, "worker-task"); !errors.Is(err, ErrClaimFenceStale) {
		t.Fatalf("expired fence: %v", err)
	}
	list, err := m.ListReceipts("", time.Time{}, "")
	if err != nil || len(list) != 1 || list[0].Consumed {
		t.Fatalf("stale consumed: %v %v", list, err)
	}
}
