package mesh

import (
	"encoding/json"
	"testing"
)

func deliveryState(t *testing.T, raw json.RawMessage, gated bool) {
	t.Helper()
	var v struct {
		Gated    bool `json:"gated"`
		Surfaced bool `json:"surfaced"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Gated != gated || v.Surfaced == gated {
		t.Fatalf("wrong delivery state: %s", raw)
	}
}

func TestMeshScreenStateGate(t *testing.T) {
	s := amendmentStore(t)
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "surface_state": "modal"})
	deliveryState(t, msgMustExec(t, s, "send", amendmentSend("gated", "s")), true)
	if got := amendmentInbox(t, s); len(got) != 0 {
		t.Fatalf("gated message surfaced: %+v", got)
	}
	assertSnapshotGate(t, s, true)
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "transport": map[string]any{"window_size": 1}})
	if msgExecErr(t, s, "send", amendmentSend("over-window", "s")) == nil {
		t.Fatal("gated delivery did not consume window")
	}
	probe := amendmentSend("surface-probe", "s")
	probe["kind"] = "ping"
	probe["payload"] = map[string]any{}
	msgMustExec(t, s, "send", probe)
	if got := amendmentInbox(t, s); len(got) != 1 || got[0].Kind != "ping" {
		t.Fatalf("gate swallowed probe: %+v", got)
	}
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "message_id": "surface-probe"})
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "surface_state": "ready"})
	if got := amendmentInbox(t, s); len(got) != 1 {
		t.Fatalf("ready did not surface: %+v", got)
	}
	assertSnapshotGate(t, s, false)
	deliveryState(t, msgMustExec(t, s, "send", amendmentSend("gated", "s")), false)
}

func assertSnapshotGate(t *testing.T, s *Store, gated bool) {
	t.Helper()
	var v struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(msgMustExec(t, s, "snapshot", map[string]any{}), &v); err != nil {
		t.Fatal(err)
	}
	for _, raw := range v.Messages {
		var m struct {
			MessageID string `json:"message_id"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m.MessageID == "gated" {
			deliveryState(t, raw, gated)
			return
		}
	}
	t.Fatalf("snapshot missing delivery: %+v", v)
}

func TestMeshScreenStateAckAndDurability(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, n := range []string{"s", "r"} {
		msgMustExec(t, s, "register", map[string]any{"node_id": n, "incarnation": "i"})
	}
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "surface_state": "busy"})
	msgMustExec(t, s, "send", amendmentSend("gated", "s"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotGate(t, s, true)
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i"})
	assertSnapshotGate(t, s, true)
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "message_id": "gated"})
	assertSnapshotGate(t, s, false)
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "surface_state": ""})
	deliveryState(t, msgMustExec(t, s, "send", amendmentSend("new", "s")), false)
	if msgExecErr(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "surface_state": "invalid"}) == nil {
		t.Fatal("invalid state accepted")
	}
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "i", "surface_state": "modal"})
	seq := amendmentSeq(t, msgMustExec(t, s, "send", amendmentSend("cumulative", "s")))
	msgMustExec(t, s, "ack", map[string]any{"node_id": "r", "incarnation": "i", "seq": seq})
	assertSnapshotGate(t, s, false)
	msgMustExec(t, s, "register", map[string]any{"node_id": "r", "incarnation": "replacement"})
	replacement := amendmentSend("replacement", "s")
	replacement["to_incarnation"] = "replacement"
	deliveryState(t, msgMustExec(t, s, "send", replacement), false)
}
