package mesh

import (
	"encoding/json"
	"testing"
	"time"
)

func keepaliveStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }
	for _, n := range []string{"s", "r"} {
		msgMustExec(t, s, "register", map[string]any{"node_id": n, "incarnation": "i"})
	}
	return s, &now
}

func snapshotStale(t *testing.T, s *Store, node string) bool {
	t.Helper()
	var v struct {
		Nodes []struct {
			NodeID string `json:"node_id"`
			Stale  bool   `json:"stale"`
		}
	}
	if err := json.Unmarshal(msgMustExec(t, s, "snapshot", map[string]any{}), &v); err != nil {
		t.Fatal(err)
	}
	for _, n := range v.Nodes {
		if n.NodeID == node {
			return n.Stale
		}
	}
	t.Fatal("missing node")
	return false
}

func TestMeshKeepaliveStaleAndPong(t *testing.T) {
	s, now := keepaliveStore(t)
	*now = now.Add(30 * time.Second)
	msgMustExec(t, s, "inbox", map[string]any{"node_id": "s", "incarnation": "i"})
	probes := amendmentInbox(t, s)
	if len(probes) != 1 || probes[0].Kind != "ping" {
		t.Fatalf("idle ping missing: %+v", probes)
	}
	*now = now.Add(89 * time.Second)
	if snapshotStale(t, s, "r") {
		t.Fatal("premature stale")
	}
	pong := map[string]any{"node_id": "r", "incarnation": "i", "to_node": "s", "to_incarnation": "i", "message_id": "pong-1", "kind": "pong", "payload": map[string]any{"ping_id": probes[0].MessageID}}
	msgMustExec(t, s, "send", pong)
	*now = now.Add(time.Second)
	if snapshotStale(t, s, "r") {
		t.Fatal("pong did not preserve liveness")
	}
	msgMustExec(t, s, "inbox", map[string]any{"node_id": "s", "incarnation": "i"})
	*now = now.Add(30 * time.Second)
	msgMustExec(t, s, "inbox", map[string]any{"node_id": "s", "incarnation": "i"})
	*now = now.Add(90 * time.Second)
	if !snapshotStale(t, s, "r") {
		t.Fatal("unanswered peer not stale at threshold")
	}
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "next"})
	if snapshotStale(t, s, "r") {
		t.Fatal("old incarnation probe leaked")
	}
}

func TestMeshKeepaliveBypassesWindowAndBudget(t *testing.T) {
	s, _ := keepaliveStore(t)
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "transport": map[string]any{"window_size": 1, "retry_budget": 1}})
	msgMustExec(t, s, "send", amendmentSend("delivery", "s"))
	if msgExecErr(t, s, "send", amendmentSend("overflow", "s")) == nil {
		t.Fatal("missing suspension")
	}
	probe := amendmentSend("probe", "s")
	probe["kind"] = "ping"
	probe["payload"] = map[string]any{}
	msgMustExec(t, s, "register", map[string]any{"node_id": "s", "incarnation": "i", "transport": map[string]any{"window_size": 1, "retry_budget": 1}})
	reverse := map[string]any{"node_id": "r", "incarnation": "i", "to_node": "s", "to_incarnation": "i", "message_id": "reverse", "kind": "event", "payload": map[string]any{}}
	msgMustExec(t, s, "send", reverse)
	reverse["message_id"] = "reverse-overflow"
	if msgExecErr(t, s, "send", reverse) == nil {
		t.Fatal("reverse stream not suspended")
	}
	msgMustExec(t, s, "send", probe)
	pong := map[string]any{"node_id": "r", "incarnation": "i", "to_node": "s", "to_incarnation": "i", "message_id": "pong", "kind": "pong", "payload": map[string]any{"ping_id": "probe"}}
	msgMustExec(t, s, "send", pong)
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "message_id": "probe"})
	if msgExecErr(t, s, "send", amendmentSend("overflow", "s")) == nil {
		t.Fatal("probe ACK reset budget")
	}
	if msgExecErr(t, s, "send", reverse) == nil {
		t.Fatal("pong reset reverse retry budget")
	}
}

func TestMeshKeepalivePolicyAndDurability(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }
	for _, n := range []string{"s", "r"} {
		msgMustExec(t, s, "register", map[string]any{"node_id": n, "incarnation": "i"})
	}
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "transport": map[string]any{"keepalive_seconds": 2, "keepalive_threshold": 2}})
	now = now.Add(time.Second)
	msgMustExec(t, s, "inbox", map[string]any{"node_id": "s", "incarnation": "i"})
	if len(amendmentInbox(t, s)) != 0 {
		t.Fatal("early ping")
	}
	now = now.Add(time.Second)
	for range 3 {
		msgMustExec(t, s, "inbox", map[string]any{"node_id": "s", "incarnation": "i"})
	}
	if got := amendmentInbox(t, s); len(got) != 1 {
		t.Fatalf("unbounded probes: %+v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	now = now.Add(4 * time.Second)
	if !snapshotStale(t, s, "r") {
		t.Fatal("lost policy or outstanding probe on reopen")
	}
	if msgExecErr(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "transport": map[string]any{"keepalive_seconds": -1}}) == nil {
		t.Fatal("invalid interval accepted")
	}
}
