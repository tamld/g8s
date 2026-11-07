package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestFenceCrossClaimWriteRejected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, node := range []string{"a", "b"} {
		claimTestSetupNode(t, s, node, "inc")
		if _, err := claimTestExec(s, "claim", map[string]any{"node_id": node, "incarnation": "inc", "task_id": node, "resource": "repo", "paths": []string{"src/" + node + ".go"}, "context_revision": 0, "lease_seconds": 60}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ActiveClaim(context.Background(), "b", "b", "inc"); err != nil {
		t.Fatalf("legitimate B claim: %v", err)
	}
	if _, err := s.ActiveClaim(context.Background(), "a", "b", "inc"); !errors.Is(err, ErrClaimFenceStale) {
		t.Fatalf("B borrowed A's write authority: %v", err)
	}
}

func TestFenceStaleAfterAcceptRevisionBump(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimTestSetupNode(t, s, "node", "inc")
	// Keep a second, still-active claim to distinguish global revision fencing
	// from merely rejecting a claim whose state has become accepted.
	for _, task := range []string{"accepted", "late"} {
		if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "node", "incarnation": "inc", "task_id": task, "resource": "repo", "paths": []string{task}, "context_revision": 0, "lease_seconds": 60}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ActiveClaim(context.Background(), task, "node", "inc"); err != nil {
			t.Fatal(err)
		}
	}
	accept := map[string]any{"node_id": "node", "incarnation": "inc", "task_id": "accepted", "context_revision": 0, "artifact_ref": "artifact", "artifact_sha256": claimDummySHA}
	for i := 0; i < 2; i++ {
		raw, err := claimTestExec(s, "accept", accept)
		if err != nil {
			t.Fatal(err)
		}
		var result claimAcceptResponse
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if result.Revision != 1 {
			t.Fatalf("accept retry %d revision = %d", i, result.Revision)
		}
	}
	for _, task := range []string{"accepted", "late"} {
		if _, err := s.ActiveClaim(context.Background(), task, "node", "inc"); !errors.Is(err, ErrClaimFenceStale) {
			t.Fatalf("late write with old binding %s: %v", task, err)
		}
	}
}
