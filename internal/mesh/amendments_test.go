package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func amendmentStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, n := range []string{"s", "r", "other"} {
		msgMustExec(t, s, "register", map[string]any{"node_id": n, "incarnation": "i"})
	}
	return s
}

func amendmentSend(id, sender string) map[string]any {
	return map[string]any{"node_id": sender, "incarnation": "i", "message_id": id, "to_node": "r", "to_incarnation": "i", "kind": "event", "payload": map[string]any{"value": id}}
}

func amendmentSeq(t *testing.T, raw json.RawMessage) int64 {
	t.Helper()
	var v struct{ Seq int64 }
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Seq
}

func amendmentInbox(t *testing.T, s *Store) []Message {
	t.Helper()
	var v struct{ Messages []Message }
	if err := json.Unmarshal(msgMustExec(t, s, "inbox", map[string]any{"node_id": "r", "incarnation": "i"}), &v); err != nil {
		t.Fatal(err)
	}
	return v.Messages
}

func TestMeshCumulativeAckPrefixAndReplay(t *testing.T) {
	s := amendmentStore(t)
	first := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("a", "s")))
	msgMustExec(t, s, "send", amendmentSend("dropped", "s"))
	last := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("c", "s")))
	// Consumer skips the intermediate delivery and advances its cumulative watermark.
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "seq": last})
	if got := amendmentInbox(t, s); len(got) != 0 {
		t.Fatalf("prefix not acknowledged: %+v", got)
	}
	if got := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("a", "s"))); got != first {
		t.Fatalf("replay seq=%d want %d", got, first)
	}
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "seq": first})
	if got := amendmentInbox(t, s); len(got) != 0 {
		t.Fatalf("duplicate delivery: %+v", got)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 3 {
		t.Fatalf("side effects count=%d err=%v", count, err)
	}
}

func TestMeshCumulativeAckPeerIsolation(t *testing.T) {
	s := amendmentStore(t)
	msgMustExec(t, s, "send", amendmentSend("a", "s"))
	msgMustExec(t, s, "send", amendmentSend("b", "other"))
	last := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("c", "s")))
	if err := msgExecErr(t, s, "ack", map[string]any{"node_id": "other", "incarnation": "i", "seq": last}); err == nil {
		t.Fatal("non-recipient ACK accepted")
	}
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "seq": last})
	if got := amendmentInbox(t, s); len(got) != 1 || got[0].MessageID != "b" {
		t.Fatalf("cross-peer ACK: %+v", got)
	}
}

func TestMeshFlowWindowSlides(t *testing.T) {
	s := amendmentStore(t)
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "transport": map[string]any{"window_size": 2}})
	first := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("a", "s")))
	last := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("b", "s")))
	err := msgExecErr(t, s, "send", amendmentSend("c", "s"))
	var full *FlowWindowError
	if !errors.As(err, &full) || full.Window != 2 {
		t.Fatalf("want window refusal, got %v", err)
	}
	if got := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("a", "s"))); got != first {
		t.Fatal("full-window replay changed seq")
	}
	msgMustExec(t, s, "send", amendmentSend("independent", "other"))
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "seq": last})
	msgMustExec(t, s, "send", amendmentSend("c", "s"))
	if got := amendmentInbox(t, s); len(got) != 2 {
		t.Fatalf("unexpected pending: %+v", got)
	}
}

func retryPolicy(t *testing.T, s *Store) {
	t.Helper()
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "transport": map[string]any{"window_size": 1, "retry_budget": 2, "cool_off_seconds": 10}})
}

func TestMeshRetryBudgetAckRecovery(t *testing.T) {
	s := amendmentStore(t)
	retryPolicy(t, s)
	old := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("old", "s")))
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "seq": old})
	seq := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("a", "s")))
	if err := msgExecErr(t, s, "send", amendmentSend("b", "s")); err == nil || !strings.Contains(err.Error(), "FLOW_WINDOW_FULL") {
		t.Fatalf("first failure: %v", err)
	}
	err := msgExecErr(t, s, "send", amendmentSend("b", "s"))
	var degraded *PeerDegradedError
	if !errors.As(err, &degraded) {
		t.Fatalf("budget not exhausted: %v", err)
	}
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "seq": old})
	if err := msgExecErr(t, s, "send", amendmentSend("b", "s")); err == nil || !strings.Contains(err.Error(), "PEER_DEGRADED") {
		t.Fatalf("old ACK restored peer: %v", err)
	}
	msgMustExec(t, s, "send", amendmentSend("independent", "other"))
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "seq": seq})
	msgMustExec(t, s, "send", amendmentSend("b", "s"))
	if err := msgExecErr(t, s, "send", amendmentSend("c", "s")); err == nil || !strings.Contains(err.Error(), "FLOW_WINDOW_FULL") {
		t.Fatalf("ack failed to reset budget: %v", err)
	}
}

func TestMeshRetryCoolOffAndDurability(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, n := range []string{"s", "r"} {
		msgMustExec(t, s, "register", map[string]any{"node_id": n, "incarnation": "i"})
	}
	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }
	retryPolicy(t, s)
	msgMustExec(t, s, "send", amendmentSend("a", "s"))
	for range 2 {
		if err := msgExecErr(t, s, "send", amendmentSend("b", "s")); err == nil {
			t.Fatal("accepted overfull send")
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	if err := msgExecErr(t, s, "send", amendmentSend("b", "s")); err == nil || !strings.Contains(err.Error(), "PEER_DEGRADED") {
		t.Fatalf("lost degradation on reopen: %v", err)
	}
	now = now.Add(10 * time.Second)
	if err := msgExecErr(t, s, "send", amendmentSend("b", "s")); err == nil || !strings.Contains(err.Error(), "FLOW_WINDOW_FULL") {
		t.Fatalf("cool-off did not release retry: %v", err)
	}
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "message_id": "a"})
	msgMustExec(t, s, "send", amendmentSend("b", "s"))
}

func TestMeshFlowWindowConcurrentHandles(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	for _, n := range []string{"s", "r"} {
		msgMustExec(t, a, "register", map[string]any{"node_id": n, "incarnation": "i"})
	}
	msgMustExec(t, a, "register", map[string]any{"node_id": "r", "incarnation": "i", "transport": map[string]any{"window_size": 1}})
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	start := make(chan struct{})
	results := make(chan error, 2)
	for i, store := range []*Store{a, b} {
		raw, err := json.Marshal(amendmentSend(fmt.Sprintf("race-%d", i), "s"))
		if err != nil {
			t.Fatal(err)
		}
		go func() { <-start; _, err := store.Execute(context.Background(), "send", raw); results <- err }()
	}
	close(start)
	wins, refusals := 0, 0
	for range 2 {
		err := <-results
		var full *FlowWindowError
		if err == nil {
			wins++
		} else if errors.As(err, &full) {
			refusals++
		} else {
			t.Fatalf("unexpected admission error: %v", err)
		}
	}
	if wins != 1 || refusals != 1 {
		t.Fatalf("wins=%d refusals=%d", wins, refusals)
	}
	if got := amendmentInbox(t, a); len(got) != 1 {
		t.Fatalf("overfull window: %+v", got)
	}
}
