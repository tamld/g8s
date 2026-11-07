package mesh

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClaimHeartbeatRenewal(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimTestSetupNode(t, s, "n", "i")
	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }
	_, err = claimTestExec(s, "claim", map[string]any{"node_id": "n", "incarnation": "i", "task_id": "t", "resource": "r", "paths": []string{"src/a.go"}, "context_revision": 0, "lease_seconds": 10})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ActiveClaim(context.Background(), "t", "n", "i")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	next, err := s.RenewClaim(context.Background(), *c)
	if err != nil {
		t.Fatal(err)
	}
	if next.ExpiresAt != now.Unix()+300 {
		t.Fatalf("lease %d", next.ExpiresAt)
	}
	again, err := s.RenewClaim(context.Background(), *c)
	if err != nil || again.ExpiresAt != next.ExpiresAt {
		t.Fatalf("idempotent renewal: %v %v", again, err)
	}
	if _, err := s.db.Exec("UPDATE meta SET revision = 1 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveClaim(context.Background(), "t", "n", "i"); !errors.Is(err, ErrClaimFenceStale) {
		t.Fatalf("revision stale: %v", err)
	}
	if _, err := s.RenewClaim(context.Background(), *c); !errors.Is(err, ErrClaimFenceStale) {
		t.Fatalf("renew stale: %v", err)
	}
}
