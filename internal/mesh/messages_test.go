package mesh

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func msgMustExec(t *testing.T, s *Store, op string, input any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s: %v", op, err)
	}
	res, err := s.Execute(context.Background(), op, b)
	if err != nil {
		t.Fatalf("exec %s: %v", op, err)
	}
	return res
}

func msgExecErr(t *testing.T, s *Store, op string, input any) error {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal %s: %v", op, err)
	}
	_, err = s.Execute(context.Background(), op, b)
	return err
}

func TestMeshMessageRoundTripAndDedup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgMustExec(t, s, "register", map[string]any{"node_id": "sender_1", "incarnation": "inc_s1"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "rec_1", "incarnation": "inc_r1"})

	p1 := json.RawMessage(`{"a": 1, "b": "val"}`)
	res1 := msgMustExec(t, s, "send", map[string]any{
		"node_id": "sender_1", "incarnation": "inc_s1", "message_id": "msg_1",
		"to_node": "rec_1", "to_incarnation": "inc_r1", "kind": "task", "payload": p1,
	})
	var out1 struct{ Seq int64 `json:"seq"` }
	if err := json.Unmarshal(res1, &out1); err != nil || out1.Seq <= 0 {
		t.Fatalf("send err: %v, seq: %d", err, out1.Seq)
	}

	// formatting-equivalent JSON
	resEq := msgMustExec(t, s, "send", map[string]any{
		"node_id": "sender_1", "incarnation": "inc_s1", "message_id": "msg_1",
		"to_node": "rec_1", "to_incarnation": "inc_r1", "kind": "task", "payload": json.RawMessage(`{ "b":  "val", "a":  1 }`),
	})
	var outEq struct{ Seq int64 `json:"seq"` }
	if err := json.Unmarshal(resEq, &outEq); err != nil || outEq.Seq != out1.Seq {
		t.Fatalf("expected dedup seq %d, got %d", out1.Seq, outEq.Seq)
	}

	// changed payload -> ID_CONFLICT
	errDiff := msgExecErr(t, s, "send", map[string]any{
		"node_id": "sender_1", "incarnation": "inc_s1", "message_id": "msg_1",
		"to_node": "rec_1", "to_incarnation": "inc_r1", "kind": "task", "payload": json.RawMessage(`{"a": 2}`),
	})
	if errDiff == nil || !strings.Contains(errDiff.Error(), "ID_CONFLICT") {
		t.Fatalf("expected ID_CONFLICT, got %v", errDiff)
	}

	// wrong recipient
	if err := msgExecErr(t, s, "send", map[string]any{
		"node_id": "sender_1", "incarnation": "inc_s1", "message_id": "msg_2",
		"to_node": "nonexistent", "to_incarnation": "inc_r1", "kind": "task", "payload": p1,
	}); err == nil {
		t.Fatal("expected wrong recipient error")
	}

	// inbox gets message
	var ib struct{ Messages []Message `json:"messages"` }
	if err := json.Unmarshal(msgMustExec(t, s, "inbox", map[string]any{"node_id": "rec_1", "incarnation": "inc_r1"}), &ib); err != nil || len(ib.Messages) != 1 {
		t.Fatalf("expected 1 message, got %+v", ib)
	}

	// wrong recipient ack rejected
	if err := msgExecErr(t, s, "ack", map[string]any{"node_id": "sender_1", "incarnation": "inc_s1", "message_id": "msg_1"}); err == nil {
		t.Fatal("expected non-recipient ack rejection")
	}

	// recipient ack idempotent
	msgMustExec(t, s, "ack", map[string]any{"node_id": "rec_1", "incarnation": "inc_r1", "message_id": "msg_1"})
	msgMustExec(t, s, "ack", map[string]any{"node_id": "rec_1", "incarnation": "inc_r1", "message_id": "msg_1"})

	// inbox empty
	if err := json.Unmarshal(msgMustExec(t, s, "inbox", map[string]any{"node_id": "rec_1", "incarnation": "inc_r1"}), &ib); err != nil || len(ib.Messages) != 0 {
		t.Fatalf("expected 0 messages after ack, got %d", len(ib.Messages))
	}

	// send after ack returns same sequence
	var outAck struct{ Seq int64 `json:"seq"` }
	if err := json.Unmarshal(msgMustExec(t, s, "send", map[string]any{
		"node_id": "sender_1", "incarnation": "inc_s1", "message_id": "msg_1",
		"to_node": "rec_1", "to_incarnation": "inc_r1", "kind": "task", "payload": p1,
	}), &outAck); err != nil || outAck.Seq != out1.Seq {
		t.Fatalf("expected same seq %d, got %d", out1.Seq, outAck.Seq)
	}
}

func TestMeshQuotaOwnershipAndValidation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgMustExec(t, s, "register", map[string]any{"node_id": "n1", "incarnation": "inc1"})
	pct40, pct60, pct70 := 40.0, 60.0, 70.0
	obs := QuotaObservation{
		Source:     "src",
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
		Windows: []QuotaWindow{
			{Name: "w_obs", WindowMinutes: 60, UsedPercent: &pct40, RemainingPercent: &pct60, Status: "observed"},
			{Name: "w_unk", WindowMinutes: 0, Status: "unknown"},
		},
	}
	// null unknown accepted
	msgMustExec(t, s, "quota-publish", map[string]any{"node_id": "n1", "incarnation": "inc1", "observation": obs})

	// invalid percent (sum != 100) rejected
	invObs := obs
	invObs.Windows = []QuotaWindow{{Name: "w", WindowMinutes: 60, UsedPercent: &pct40, RemainingPercent: &pct70, Status: "observed"}}
	if err := msgExecErr(t, s, "quota-publish", map[string]any{"node_id": "n1", "incarnation": "inc1", "observation": invObs}); err == nil {
		t.Fatal("expected invalid percent rejection")
	}

	// unknown numeric rejected
	invUnk := obs
	invUnk.Windows = []QuotaWindow{{Name: "w", WindowMinutes: 0, UsedPercent: &pct40, Status: "unknown"}}
	if err := msgExecErr(t, s, "quota-publish", map[string]any{"node_id": "n1", "incarnation": "inc1", "observation": invUnk}); err == nil {
		t.Fatal("expected unknown numeric rejection")
	}

	// wrong incarnation ownership rejected
	if err := msgExecErr(t, s, "quota-publish", map[string]any{"node_id": "n1", "incarnation": "wrong", "observation": obs}); err == nil {
		t.Fatal("expected wrong incarnation rejection")
	}
}

func TestMeshInboxDurableReopen(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	msgMustExec(t, s1, "register", map[string]any{"node_id": "s", "incarnation": "s1"})
	msgMustExec(t, s1, "register", map[string]any{"node_id": "r", "incarnation": "r1"})
	msgMustExec(t, s1, "send", map[string]any{
		"node_id": "s", "incarnation": "s1", "message_id": "m1", "to_node": "r", "to_incarnation": "r1", "kind": "k",
		"payload": map[string]any{"x": 1},
	})
	s1.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	var ib struct{ Messages []Message `json:"messages"` }
	if err := json.Unmarshal(msgMustExec(t, s2, "inbox", map[string]any{"node_id": "r", "incarnation": "r1"}), &ib); err != nil || len(ib.Messages) != 1 || ib.Messages[0].MessageID != "m1" {
		t.Fatalf("inbox reopen failed: %+v", ib)
	}

	var snap msgSnapResp
	if err := json.Unmarshal(msgMustExec(t, s2, "snapshot", map[string]any{}), &snap); err != nil || len(snap.Nodes) != 2 {
		t.Fatalf("snapshot failed: %+v", snap)
	}
}

func TestMeshStaleIncarnation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgMustExec(t, s, "register", map[string]any{"node_id": "n1", "incarnation": "inc1"})
	pct := 50.0
	obs := QuotaObservation{
		Source:     "src",
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
		Windows:    []QuotaWindow{{Name: "w", WindowMinutes: 10, UsedPercent: &pct, RemainingPercent: &pct, Status: "observed"}},
	}
	msgMustExec(t, s, "quota-publish", map[string]any{"node_id": "n1", "incarnation": "inc1", "observation": obs})

	// New incarnation deletes old quota
	msgMustExec(t, s, "register", map[string]any{"node_id": "n1", "incarnation": "inc2"})

	var snap msgSnapResp
	if err := json.Unmarshal(msgMustExec(t, s, "snapshot", map[string]any{}), &snap); err != nil || len(snap.Quotas) != 0 {
		t.Fatalf("expected old quota deleted, got %d quotas", len(snap.Quotas))
	}

	// Operation with stale incarnation inc1 rejected
	if err := msgExecErr(t, s, "inbox", map[string]any{"node_id": "n1", "incarnation": "inc1"}); err == nil {
		t.Fatal("expected stale incarnation rejection")
	}
}

func TestMeshWaitCancellation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgMustExec(t, s, "register", map[string]any{"node_id": "sender", "incarnation": "s1"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "waiter", "incarnation": "w1"})

	// Immediate timeout
	var wr msgWaitResp
	if err := json.Unmarshal(msgMustExec(t, s, "wait", map[string]any{"node_id": "waiter", "incarnation": "w1", "timeout_ms": 50}), &wr); err != nil || !wr.TimedOut || len(wr.Messages) != 0 {
		t.Fatalf("expected timed_out true, got %+v", wr)
	}

	// Context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input, _ := json.Marshal(map[string]any{"node_id": "waiter", "incarnation": "w1", "timeout_ms": 5000})
	if _, err := s.Execute(ctx, "wait", input); err == nil {
		t.Fatal("expected context cancelled error")
	}

	// Receive during wait
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(150 * time.Millisecond)
		msgMustExec(t, s, "send", map[string]any{
			"node_id": "sender", "incarnation": "s1", "message_id": "w_msg", "to_node": "waiter", "to_incarnation": "w1",
			"kind": "notify", "payload": map[string]any{"ok": true},
		})
	}()

	var wr2 msgWaitResp
	if err := json.Unmarshal(msgMustExec(t, s, "wait", map[string]any{"node_id": "waiter", "incarnation": "w1", "timeout_ms": 3000}), &wr2); err != nil || wr2.TimedOut || len(wr2.Messages) != 1 || wr2.Messages[0].MessageID != "w_msg" {
		t.Fatalf("expected message during wait, got %+v", wr2)
	}
	wg.Wait()
}
