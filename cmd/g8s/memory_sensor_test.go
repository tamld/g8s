package main

// #411b: the cmd memory adapter wires a real Jev sensor (reflex adapter).
// Contract pins: instant_kill → blocked, grant_receipt → clean, anything
// else → unrecognized (the promotion gate fails CLOSED on those), transport
// errors → fail-open unverified upstream.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/memory"
	"github.com/tamld/g8s/internal/reflex"
)

func TestMemorySensorAdapterVerdictMapping(t *testing.T) {
	cases := []struct {
		name      string
		action    reflex.TriageAction
		triageErr error
		want      string
		wantErr   bool
	}{
		{"instant kill blocks", reflex.ActionInstantKill, nil, "blocked", false},
		{"grant receipt cleans", reflex.ActionGrantReceipt, nil, "clean", false},
		{"escalate is unrecognized", reflex.ActionEscalateHITL, nil, "", true},
		{"transport error surfaces", "", errors.New("api down"), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sensor := memorySensorAdapter(func(ctx context.Context, req reflex.TriageRequest) (reflex.TriageVerdict, error) {
				return reflex.TriageVerdict{Action: tc.action}, tc.triageErr
			})
			verdict, err := sensor("naked payload text")
			if tc.wantErr && err == nil {
				t.Fatalf("want error for action %q", tc.action)
			}
			if !tc.wantErr && (err != nil || verdict != tc.want) {
				t.Fatalf("got verdict=%q err=%v", verdict, err)
			}
		})
	}
}

// End-to-end pin through the cmd wiring path: with a sensor attached, the
// promotion gate still enforces G1 (tombstoned payloads cannot re-enter).
func TestMemoryAdapterWithSensorStillEnforcesTombstones(t *testing.T) {
	adapter, err := memory.NewLocalSQLiteMemoryAdapter(memory.AdapterOptions{
		DBPath: filepath.Join(t.TempDir(), "memory.db"),
	})
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	defer adapter.Close()
	adapter.SetGateSensor(memorySensorAdapter(func(ctx context.Context, req reflex.TriageRequest) (reflex.TriageVerdict, error) {
		return reflex.TriageVerdict{Action: reflex.ActionGrantReceipt}, nil
	}))
	e := &memory.MemoryEntry{ID: "e-tomb", Kind: memory.KindFact, Scope: memory.ScopeSession, SessionID: "sx", Payload: "tombstone target"}
	if err := adapter.ProposeEntry(context.Background(), e); err != nil {
		t.Fatalf("first propose: %v", err)
	}
	if n, err := adapter.RevokeBySession(context.Background(), "sx"); err != nil || n != 1 {
		t.Fatalf("revoke: %d %v", n, err)
	}
	reborn := &memory.MemoryEntry{ID: "e-reborn", Kind: memory.KindFact, Scope: memory.ScopeSession, SessionID: "sy", Payload: "tombstone target"}
	if err := adapter.ProposeEntry(context.Background(), reborn); !errors.Is(err, memory.ErrTombstoned) {
		t.Fatalf("tombstone must block re-entry, got %v", err)
	}
}
