package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSendIdempotentEcho(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgMustExec(t, s, "register", map[string]any{"node_id": "sender", "incarnation": "s1"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "recip", "incarnation": "r1"})

	payload := json.RawMessage(`{"k":"v"}`)
	sendIn := map[string]any{
		"node_id": "sender", "incarnation": "s1", "message_id": "X",
		"to_node": "recip", "to_incarnation": "r1", "kind": "task", "payload": payload,
	}

	res1 := msgMustExec(t, s, "send", sendIn)
	var out1 struct{ Seq int64 `json:"seq"` }
	if err := json.Unmarshal(res1, &out1); err != nil || out1.Seq <= 0 {
		t.Fatalf("first send: err=%v seq=%d", err, out1.Seq)
	}

	res2 := msgMustExec(t, s, "send", sendIn)
	var out2 struct{ Seq int64 `json:"seq"` }
	if err := json.Unmarshal(res2, &out2); err != nil || out2.Seq != out1.Seq {
		t.Fatalf("expected same seq %d, got %d", out1.Seq, out2.Seq)
	}

	var rowCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id = ?`, "X").Scan(&rowCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly 1 row for message_id X, got %d", rowCount)
	}

	errDiff := msgExecErr(t, s, "send", map[string]any{
		"node_id": "sender", "incarnation": "s1", "message_id": "X",
		"to_node": "recip", "to_incarnation": "r1", "kind": "task",
		"payload": json.RawMessage(`{"k":"other"}`),
	})
	if errDiff == nil {
		t.Fatal("expected error on changed payload with same message_id")
	}
}

func TestOnlyRecipientCanAck(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgMustExec(t, s, "register", map[string]any{"node_id": "sender", "incarnation": "s1"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "recip", "incarnation": "r1"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "other", "incarnation": "o1"})

	msgMustExec(t, s, "send", map[string]any{
		"node_id": "sender", "incarnation": "s1", "message_id": "m-ack",
		"to_node": "recip", "to_incarnation": "r1", "kind": "task",
		"payload": map[string]any{"n": 1},
	})

	if err := msgExecErr(t, s, "ack", map[string]any{
		"node_id": "other", "incarnation": "o1", "message_id": "m-ack",
	}); err == nil {
		t.Fatal("expected non-recipient ack to fail")
	}

	msgMustExec(t, s, "ack", map[string]any{
		"node_id": "recip", "incarnation": "r1", "message_id": "m-ack",
	})
	msgMustExec(t, s, "ack", map[string]any{
		"node_id": "recip", "incarnation": "r1", "message_id": "m-ack",
	})
}

func TestClaimRevisionFence(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimTestSetupNode(t, s, "n1", "i1")

	staleErr := claimTestExecErr(t, s, "claim", map[string]any{
		"node_id": "n1", "incarnation": "i1", "task_id": "t-stale",
		"resource": "r1", "paths": []string{"pkg/a"},
		"context_revision": 99, "lease_seconds": 60,
	})
	if staleErr == nil || !strings.Contains(staleErr.Error(), "stale revision") {
		t.Fatalf("expected stale revision error, got %v", staleErr)
	}

	if _, err := claimTestExec(s, "claim", map[string]any{
		"node_id": "n1", "incarnation": "i1", "task_id": "t-ok",
		"resource": "r1", "paths": []string{"pkg/a"},
		"context_revision": 0, "lease_seconds": 60,
	}); err != nil {
		t.Fatalf("claim at current revision failed: %v", err)
	}
}

func claimTestExecErr(t *testing.T, s *Store, op string, val any) error {
	t.Helper()
	_, err := claimTestExec(s, op, val)
	return err
}

func TestClaimRaceSingleWinner(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 1; i <= 6; i++ {
		claimTestSetupNode(t, s, fmt.Sprintf("node-%d", i), fmt.Sprintf("inc-%d", i))
	}

	const paths = "shared/pkg/file.go"
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins int
	var overlapErrs int

	for i := 1; i <= 6; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			_, err := claimTestExec(s, "claim", map[string]any{
				"node_id": fmt.Sprintf("node-%d", i), "incarnation": fmt.Sprintf("inc-%d", i),
				"task_id": fmt.Sprintf("task-%d", i),
				"resource": "race-resource", "paths": []string{paths},
				"context_revision": 0, "lease_seconds": 60,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
				return
			}
			if strings.Contains(err.Error(), "claim conflict: path") {
				overlapErrs++
				return
			}
			t.Errorf("unexpected claim error: %v", err)
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", wins)
	}
	if overlapErrs != 5 {
		t.Fatalf("expected 5 path-overlap conflicts, got %d", overlapErrs)
	}
}

func TestWaitLongPollWake(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	msgMustExec(t, s, "register", map[string]any{"node_id": "sender", "incarnation": "s1"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "waiter", "incarnation": "w1"})

	var wr0 msgWaitResp
	if err := json.Unmarshal(msgMustExec(t, s, "wait", map[string]any{
		"node_id": "waiter", "incarnation": "w1", "timeout_ms": 0,
	}), &wr0); err != nil || !wr0.TimedOut || len(wr0.Messages) != 0 {
		t.Fatalf("timeout_ms=0 on empty inbox: want TimedOut=true, got %+v err=%v", wr0, err)
	}

	type waitResult struct {
		resp msgWaitResp
		err  error
		elapsed time.Duration
	}
	ch := make(chan waitResult, 1)
	waitStart := time.Now()
	go func() {
		t0 := time.Now()
		input, _ := json.Marshal(map[string]any{
			"node_id": "waiter", "incarnation": "w1", "timeout_ms": 5000,
		})
		raw, err := s.Execute(context.Background(), "wait", input)
		var wr msgWaitResp
		if raw != nil {
			_ = json.Unmarshal(raw, &wr)
		}
		ch <- waitResult{wr, err, time.Since(t0)}
	}()

	time.Sleep(150 * time.Millisecond)
	msgMustExec(t, s, "send", map[string]any{
		"node_id": "sender", "incarnation": "s1", "message_id": "wake-1",
		"to_node": "waiter", "to_incarnation": "w1", "kind": "notify",
		"payload": map[string]any{"ok": true},
	})

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("wait error: %v", r.err)
		}
		if r.resp.TimedOut {
			t.Fatalf("expected message, timed out: %+v", r.resp)
		}
		if len(r.resp.Messages) != 1 || r.resp.Messages[0].MessageID != "wake-1" {
			t.Fatalf("unexpected wait response: %+v", r.resp)
		}
		if r.elapsed >= 2*time.Second {
			t.Fatalf("wait took too long: %v", r.elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("wait did not complete within 2s (started %v ago)", time.Since(waitStart))
	}
}
