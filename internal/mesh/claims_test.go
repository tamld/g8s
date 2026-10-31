package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

const claimDummySHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const claimDummySHA2 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

func claimTestSetupNode(t *testing.T, s *Store, node, inc string) {
	t.Helper()
	_, err := s.db.Exec("INSERT OR REPLACE INTO nodes(node_id, incarnation, capabilities, observed_at) VALUES(?, ?, '{}', ?)",
		node, inc, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("setup node: %v", err)
	}
}

func claimTestExec(s *Store, op string, val any) (json.RawMessage, error) {
	b, _ := json.Marshal(val)
	return s.Execute(context.Background(), op, b)
}

func TestMeshOverlappingClaimsSingleWinner(t *testing.T) {
	dir, err := os.MkdirTemp("", "mesh-claim-w-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	claimTestSetupNode(t, s1, "node-1", "inc-1")
	claimTestSetupNode(t, s1, "node-2", "inc-2")

	var wg sync.WaitGroup
	var wins int
	var mu sync.Mutex
	for i, st := range []*Store{s1, s2} {
		wg.Add(1)
		n, inc, task, store := fmt.Sprintf("node-%d", i+1), fmt.Sprintf("inc-%d", i+1), fmt.Sprintf("task-%d", i+1), st
		go func() {
			defer wg.Done()
			_, err := claimTestExec(store, "claim", map[string]any{
				"node_id": n, "incarnation": inc, "task_id": task,
				"resource": "r1", "paths": []string{"src/common/code.go"},
				"context_revision": 0, "lease_seconds": 60,
			})
			mu.Lock()
			if err == nil {
				wins++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("expected 1 winner, got %d", wins)
	}
}

func TestMeshDisjointClaims(t *testing.T) {
	dir, _ := os.MkdirTemp("", "mesh-claim-d-*")
	defer os.RemoveAll(dir)
	s, _ := Open(dir)
	defer s.Close()
	claimTestSetupNode(t, s, "n1", "i1")

	for _, p := range []string{"pkg/a", "pkg/b"} {
		if _, err := claimTestExec(s, "claim", map[string]any{
			"node_id": "n1", "incarnation": "i1", "task_id": "task-" + p,
			"resource": "r1", "paths": []string{p}, "context_revision": 0,
		}); err != nil {
			t.Fatalf("claim %s failed: %v", p, err)
		}
	}
}

func TestMeshAcceptRevisionAndIdempotence(t *testing.T) {
	dir, _ := os.MkdirTemp("", "mesh-claim-a-*")
	defer os.RemoveAll(dir)
	s, _ := Open(dir)
	defer s.Close()
	claimTestSetupNode(t, s, "n1", "i1")

	if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t1", "resource": "r1", "paths": []string{"pkg/a"}, "context_revision": 0}); err != nil {
		t.Fatal(err)
	}
	res1, err := claimTestExec(s, "accept", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t1", "context_revision": 0, "artifact_ref": "ref-1", "artifact_sha256": claimDummySHA})
	if err != nil {
		t.Fatal(err)
	}
	var a1 claimAcceptResponse
	_ = json.Unmarshal(res1, &a1)
	if a1.Revision != 1 {
		t.Fatalf("want rev 1, got %d", a1.Revision)
	}

	if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t2", "resource": "r1", "paths": []string{"pkg/b"}, "context_revision": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := claimTestExec(s, "accept", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t2", "context_revision": 1, "artifact_ref": "ref-2", "artifact_sha256": claimDummySHA}); err != nil {
		t.Fatal(err)
	}

	resRetry, err := claimTestExec(s, "accept", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t1", "context_revision": 0, "artifact_ref": "ref-1", "artifact_sha256": claimDummySHA})
	if err != nil {
		t.Fatalf("idempotent accept: %v", err)
	}
	var aRetry claimAcceptResponse
	_ = json.Unmarshal(resRetry, &aRetry)
	if aRetry.Revision != 1 {
		t.Fatalf("want original rev 1, got %d", aRetry.Revision)
	}

	if _, err := claimTestExec(s, "accept", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t1", "context_revision": 0, "artifact_ref": "changed", "artifact_sha256": claimDummySHA}); err == nil {
		t.Fatal("expected error on changed artifact")
	}
}

func TestMeshLeaseExpiry(t *testing.T) {
	dir, _ := os.MkdirTemp("", "mesh-claim-e-*")
	defer os.RemoveAll(dir)
	s, _ := Open(dir)
	defer s.Close()
	claimTestSetupNode(t, s, "n1", "i1")
	claimTestSetupNode(t, s, "n2", "i2")

	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }

	if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t1", "resource": "r1", "paths": []string{"pkg/c"}, "context_revision": 0, "lease_seconds": 10}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(15 * time.Second)

	if _, err := claimTestExec(s, "accept", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t1", "context_revision": 0, "artifact_ref": "r", "artifact_sha256": claimDummySHA}); err == nil {
		t.Fatal("expected accept to fail on expiry")
	}
	if _, err := claimTestExec(s, "release", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "t1"}); err == nil {
		t.Fatal("expected release to fail on expiry")
	}
	if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "n2", "incarnation": "i2", "task_id": "t2", "resource": "r1", "paths": []string{"pkg/c"}, "context_revision": 0, "lease_seconds": 60}); err != nil {
		t.Fatalf("reclaim after expiry failed: %v", err)
	}
}

func TestMeshClaimRejectsTraversalAndChangedReplay(t *testing.T) {
	dir, _ := os.MkdirTemp("", "mesh-claim-r-*")
	defer os.RemoveAll(dir)
	s, _ := Open(dir)
	defer s.Close()
	claimTestSetupNode(t, s, "n1", "i1")

	for _, bp := range [][]string{{"../bad"}, {"a/../../bad"}, {"/abs"}, {""}, {"."}, {"a\\b"}, {"*.go"}} {
		if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "tb", "resource": "r1", "paths": bp, "context_revision": 0}); err == nil {
			t.Fatalf("expected error on %v", bp)
		}
	}
	if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "to", "resource": "r1", "paths": []string{"pkg/ok"}, "context_revision": 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "to", "resource": "r1", "paths": []string{"pkg/diff"}, "context_revision": 0}); err == nil {
		t.Fatal("expected error on changed replay")
	}
	res, err := claimTestExec(s, "claim", map[string]any{"node_id": "n1", "incarnation": "i1", "task_id": "to", "resource": "r1", "paths": []string{"pkg/ok"}, "context_revision": 0})
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	var resp claimResponse
	if err := json.Unmarshal(res, &resp); err != nil || resp.Claim.TaskID != "to" {
		t.Fatalf("unexpected resp: %v", err)
	}
}

func TestMeshAllPeersSameRights(t *testing.T) {
	dir, _ := os.MkdirTemp("", "mesh-claim-p-*")
	defer os.RemoveAll(dir)
	s, _ := Open(dir)
	defer s.Close()
	for _, p := range []string{"p1", "p2", "p3"} {
		claimTestSetupNode(t, s, p, "i1")
	}

	for _, p := range []string{"p1", "p2"} {
		if _, err := claimTestExec(s, "claim", map[string]any{"node_id": p, "incarnation": "i1", "task_id": "t-" + p, "resource": "r1", "paths": []string{"sh/sc"}, "context_revision": 0}); err != nil {
			t.Fatal(err)
		}
		if _, err := claimTestExec(s, "release", map[string]any{"node_id": p, "incarnation": "i1", "task_id": "t-" + p}); err != nil {
			t.Fatalf("%s release failed: %v", p, err)
		}
	}
	if _, err := claimTestExec(s, "claim", map[string]any{"node_id": "p3", "incarnation": "i1", "task_id": "t-p3", "resource": "r1", "paths": []string{"sh/sc"}, "context_revision": 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := claimTestExec(s, "accept", map[string]any{"node_id": "p3", "incarnation": "i1", "task_id": "t-p3", "context_revision": 0, "artifact_ref": "p3-art", "artifact_sha256": claimDummySHA2}); err != nil {
		t.Fatalf("p3 accept failed: %v", err)
	}
}
