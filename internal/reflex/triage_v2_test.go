package reflex

// #396: TriageRequest v2 backward-compat — the v1 JSON shape (four fields,
// no context_packet) parses into v2 with a nil packet, and a v2 round-trip
// preserves the packet. Byte-compatible per Constitution Axiom 5.

import (
	"encoding/json"
	"strings"
	"testing"

	g8scontext "github.com/tamld/g8s/internal/context"
)

func TestTriageRequestV1JSONParsesAsV2(t *testing.T) {
	v1 := `{"task_id":"t1","files_modified":["a.go"],"diff_summary":"s","allowed_paths":["*.go"]}`
	var req TriageRequest
	if err := json.Unmarshal([]byte(v1), &req); err != nil {
		t.Fatalf("v1 JSON must parse into v2: %v", err)
	}
	if req.TaskID != "t1" || req.ContextPacket != nil {
		t.Fatalf("v1 shape must yield nil packet, got %+v", req)
	}
}

func TestTriageRequestV2RoundTrip(t *testing.T) {
	req := TriageRequest{
		TaskID:      "t2",
		DiffSummary: "s",
		ContextPacket: &g8scontext.ContextPacket{
			SomPhase:       "L1",
			VaultNotes:     []string{"note-1"},
			RecentOutcomes: []string{"outcome-1"},
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal v2: %v", err)
	}
	if !strings.Contains(string(raw), "context_packet") {
		t.Fatalf("v2 JSON must carry context_packet: %s", raw)
	}
	var back TriageRequest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal v2: %v", err)
	}
	if back.ContextPacket == nil || back.ContextPacket.SomPhase != "L1" || len(back.ContextPacket.VaultNotes) != 1 {
		t.Fatalf("packet lost in round trip: %+v", back.ContextPacket)
	}
}
