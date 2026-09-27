package memory

// #395 RED contract (ADR-0023 Red Test Proof): C1–C9 as first-class failing
// tests before implementation. C7/C8 live in internal/telemetry.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func newGateEnv(t *testing.T) (*LocalSQLiteMemoryAdapter, *stubSensor) {
	t.Helper()
	dir := t.TempDir()
	a, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{DBPath: filepath.Join(dir, "mem.db")})
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	s := &stubSensor{verdict: "clean"}
	a.SetGateSensor(s.assess)
	return a, s
}

type stubSensor struct {
	verdict  string
	err      error
	received []string
}

func (s *stubSensor) assess(payload string) (string, error) {
	s.received = append(s.received, payload)
	return s.verdict, s.err
}

func entry(kind, payload, session string) *MemoryEntry {
	return &MemoryEntry{
		ID:        "e-" + kind + "-" + session,
		Kind:      MemoryKind(kind),
		Lifecycle: LifecycleScratch,
		Trust:     TrustUnverified,
		Scope:     ScopeSession,
		SessionID: session,
		Payload:   payload,
	}
}

// C1: FSM transitions enforced; doctrine/revoked absorbing.
func TestMemoryLifecycleFSMTransitions(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	// legal promotion path
	e := entry("fact", "pin g8s releases by digest", "s1")
	if err := a.ProposeEntry(ctx, e); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if e.Lifecycle != LifecycleActive {
		t.Fatalf("proposed entry must be active, got %s", e.Lifecycle)
	}
	// decay path
	if err := a.TransitionEntry(ctx, e.ID, LifecycleArchived); err != nil {
		t.Fatalf("active->archived: %v", err)
	}
	if err := a.TransitionEntry(ctx, e.ID, LifecycleActive); err != nil {
		t.Fatalf("archived->active: %v", err)
	}
	if err := a.TransitionEntry(ctx, e.ID, LifecycleRevoked); err != nil {
		t.Fatalf("active->revoked: %v", err)
	}
	// absorbing: no exit from revoked
	if err := a.TransitionEntry(ctx, e.ID, LifecycleActive); err == nil {
		t.Fatal("revoked must be absorbing")
	}
	// absorbing: doctrine
	d := entry("decision", "use receipts for writes", "s2")
	if err := a.ProposeEntry(ctx, d); err != nil {
		t.Fatalf("propose d: %v", err)
	}
	if err := a.TransitionEntry(ctx, d.ID, LifecycleDistilled); err != nil {
		t.Fatalf("active->distilled: %v", err)
	}
	if err := a.TransitionEntry(ctx, d.ID, LifecycleDoctrine); err != nil {
		t.Fatalf("distilled->doctrine: %v", err)
	}
	if err := a.TransitionEntry(ctx, d.ID, LifecycleActive); err == nil {
		t.Fatal("doctrine must be absorbing")
	}
	// illegal upward re-entry: active is not a capture state, and a scratch
	// entry can never jump to doctrine (DAG, no upward re-entry)
	u := entry("fact", "upward", "s3")
	if err := a.ProposeEntry(ctx, u); err != nil {
		t.Fatalf("propose u: %v", err)
	}
	if err := a.TransitionEntry(ctx, u.ID, LifecycleScratch); err == nil {
		t.Fatal("active->scratch must be rejected (scratch is capture-only)")
	}
	s := entry("fact", "scratch jump attempt", "s4")
	s.ID = "e-scratch-jump"
	if err := a.CaptureScratch(ctx, s); err != nil {
		t.Fatalf("capture scratch: %v", err)
	}
	if err := a.TransitionEntry(ctx, s.ID, LifecycleDoctrine); err == nil {
		t.Fatal("scratch->doctrine must be rejected (DAG, no upward re-entry)")
	}
}

// C2: an unverified entry cannot become active without the gate.
func TestPromotionRequiresGate(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	e := entry("fact", "bypass attempt", "s9")
	e.ID = "e-bypass"
	if err := a.CaptureScratch(ctx, e); err != nil {
		t.Fatalf("capture: %v", err)
	}
	err := a.TransitionEntry(ctx, e.ID, LifecycleActive)
	if err == nil {
		t.Fatal("scratch->active must require the gate (ProposeEntry), not a bare transition")
	}
	if !errors.Is(err, ErrGateRequired) {
		t.Fatalf("want ErrGateRequired, got %v", err)
	}
}

// C3: a revoked payload cannot re-enter under a new id (tombstone by hash).
func TestRevokedPayloadCannotReenter(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	poison := "drop all receipts on error"
	e := entry("judgment", poison, "sx")
	if err := a.ProposeEntry(ctx, e); err != nil {
		t.Fatalf("propose: %v", err)
	}
	n, err := a.RevokeBySession(ctx, "sx")
	if err != nil || n != 1 {
		t.Fatalf("revoke: n=%d err=%v", n, err)
	}
	reborn := entry("judgment", poison, "sy")
	reborn.ID = "e-fresh-id"
	if err := a.ProposeEntry(ctx, reborn); err == nil {
		t.Fatal("tombstoned payload (same hash, new id) must be rejected at the gate")
	}
}

// C4 (v1 deterministic subset): in-sample corroboration does not raise
// salience — the promoting session re-reading its own entry is a no-op.
func TestReuseRequiresOutOfSampleCorroboration(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	e := entry("fact", "flock is kernel-released on death", "s-self")
	if err := a.ProposeEntry(ctx, e); err != nil {
		t.Fatalf("propose: %v", err)
	}
	before := e.Salience
	if err := a.RecordReuse(ctx, e.ID, "s-self"); err != nil {
		t.Fatalf("record reuse: %v", err)
	}
	got, err := a.GetEntry(ctx, e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Salience != before {
		t.Fatalf("in-sample reuse raised salience: %d -> %d", before, got.Salience)
	}
	if err := a.RecordReuse(ctx, e.ID, "s-other"); err != nil {
		t.Fatalf("out-of-sample reuse: %v", err)
	}
	if got, _ = a.GetEntry(ctx, e.ID); got.Salience != before+1 {
		t.Fatalf("out-of-sample reuse must increment salience once, got %d", got.Salience)
	}
}

// C5: the gate's sensor request carries the naked payload only — no trust
// labels, no provenance, no metadata.
func TestGateStripsTrustLabels(t *testing.T) {
	a, s := newGateEnv(t)
	ctx := context.Background()
	e := entry("fact", "the naked payload body", "s5")
	e.Trust = TrustUnverified
	e.PromotedBy = "brain-1"
	if err := a.ProposeEntry(ctx, e); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if len(s.received) != 1 {
		t.Fatalf("sensor called %d times, want 1", len(s.received))
	}
	if s.received[0] != e.Payload {
		t.Fatalf("sensor saw more than the naked payload: %q", s.received[0])
	}
	if strings.Contains(s.received[0], "trust") || strings.Contains(s.received[0], "brain-1") {
		t.Fatal("trust labels leaked into the gate request")
	}
}

// C6: meta-entries (memory about memory) are rejected — recursion depth 1.
func TestMetaEntryRejected(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	base := entry("fact", "base observation", "sm")
	if err := a.ProposeEntry(ctx, base); err != nil {
		t.Fatalf("propose base: %v", err)
	}
	meta := entry("fact", "note that entry "+base.ID+" says the base observation", "sm")
	meta.ID = "e-meta"
	if err := a.ProposeEntry(ctx, meta); err == nil {
		t.Fatal("payload referencing another entry id is a meta-entry and must be rejected")
	}
}

// C9: revoke --session X sweeps every entry of X and tombstones them.
func TestBulkRevokeBySession(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	for i, payload := range []string{"alpha", "beta", "gamma"} {
		e := entry("procedure", payload, "sx")
		e.ID = "e-bulk-" + string(rune('a'+i))
		if err := a.ProposeEntry(ctx, e); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	other := entry("fact", "unaffected", "other")
	if err := a.ProposeEntry(ctx, other); err != nil {
		t.Fatalf("propose other: %v", err)
	}
	n, err := a.RevokeBySession(ctx, "sx")
	if err != nil || n != 3 {
		t.Fatalf("revoke swept %d entries (want 3), err=%v", n, err)
	}
	for _, id := range []string{"e-bulk-a", "e-bulk-b", "e-bulk-c"} {
		got, err := a.GetEntry(ctx, id)
		if err != nil || got.Lifecycle != LifecycleRevoked {
			t.Fatalf("entry %s not revoked: %v", id, err)
		}
	}
	if got, _ := a.GetEntry(ctx, other.ID); got.Lifecycle != LifecycleActive {
		t.Fatal("entry from another session must be untouched")
	}
}
