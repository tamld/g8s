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

func entry(kind MemoryKind, payload, session string) *MemoryEntry {
	return &MemoryEntry{
		ID:        "e-" + string(kind) + "-" + session,
		Kind:      kind,
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

// --- #395 review-hardening pins ---

// CRITICAL pin: the capture → promote flow (ADR §4). A payload captured as
// scratch in its own session must be PROMOTED (the row transitions), not
// dropped for a UNIQUE-index collision with a fabricated duplicate.
func TestProposePromotesCapturedScratchRow(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	e := entry("fact", "captured then promoted", "s-flow")
	if err := a.CaptureScratch(ctx, e); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := a.ProposeEntry(ctx, e); err != nil {
		t.Fatalf("propose after capture: %v", err)
	}
	got, err := a.GetEntry(ctx, e.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Lifecycle != LifecycleActive || got.ID != e.ID {
		t.Fatalf("scratch row must transition to active in place, got %s/%s", got.ID, got.Lifecycle)
	}
}

// The gate's primary "say no" path: a blocked verdict must reject.
func TestGateBlockedVerdictRejects(t *testing.T) {
	a, s := newGateEnv(t)
	a.SetGateSensor(s.assess)
	s.verdict = "blocked"
	if err := a.ProposeEntry(context.Background(), entry("fact", "poison attempt", "sb")); err == nil {
		t.Fatal("blocked verdict must reject promotion")
	}
}

// Sensor down (error) = DEGRADED: fail-open with trust=unverified + a
// broker_failure event on the sink.
func TestGateSensorDownFailsOpenUnverified(t *testing.T) {
	a, s := newGateEnv(t)
	a.SetGateSensor(s.assess)
	s.err = errors.New("sensor offline")
	var events []string
	a.SetGateEventSink(func(event, detail string) { events = append(events, event) })
	e := entry("fact", "entry during sensor outage", "sd")
	if err := a.ProposeEntry(context.Background(), e); err != nil {
		t.Fatalf("sensor-down must fail open: %v", err)
	}
	if e.Trust != TrustUnverified {
		t.Fatalf("fail-open must never grant trust, got %s", e.Trust)
	}
	found := false
	for _, ev := range events {
		if ev == "broker_failure" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DEGRADED must emit broker_failure telemetry, got %v", events)
	}
}

// Unknown sensor verdicts fail CLOSED (a drifted verdict must not silently
// promote).
func TestGateUnknownVerdictFailsClosed(t *testing.T) {
	a, s := newGateEnv(t)
	a.SetGateSensor(s.assess)
	s.verdict = "PROBABLY FINE"
	if err := a.ProposeEntry(context.Background(), entry("fact", "verdict drift", "su")); err == nil {
		t.Fatal("unrecognized verdict must fail closed")
	}
}

// G1 monotonicity: repeated revocation sweeps add nothing and never delete
// tombstones.
func TestTombstoneMonotonicity(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	e := entry("fact", "tombstone me once", "st")
	if err := a.ProposeEntry(ctx, e); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if n, _ := a.RevokeBySession(ctx, "st"); n != 1 {
		t.Fatalf("first sweep = %d", n)
	}
	if n, _ := a.RevokeBySession(ctx, "st"); n != 0 {
		t.Fatalf("second sweep must find nothing, got %d", n)
	}
	reborn := entry("fact", "tombstone me once", "st2")
	reborn.ID = "e-reborn"
	if err := a.ProposeEntry(ctx, reborn); err == nil {
		t.Fatal("tombstoned payload must stay rejected after re-sweep")
	}
}

func TestListEntriesFilters(t *testing.T) {
	a, _ := newGateEnv(t)
	ctx := context.Background()
	for _, seed := range []struct {
		id, session string
		kind        MemoryKind
	}{
		{"e-l1", "sA", KindFact},
		{"e-l2", "sA", KindJudgment},
		{"e-l3", "sB", KindFact},
	} {
		e := entry(seed.kind, "payload "+seed.id, seed.session)
		e.ID = seed.id
		if err := a.ProposeEntry(ctx, e); err != nil {
			t.Fatalf("propose %s: %v", seed.id, err)
		}
	}
	bySession, err := a.ListEntries(ctx, MemoryFilter{SessionID: "sA"})
	if err != nil || len(bySession) != 2 {
		t.Fatalf("session filter: %d entries (%v)", len(bySession), err)
	}
	byKind, err := a.ListEntries(ctx, MemoryFilter{SessionID: "sA", Kind: KindJudgment})
	if err != nil || len(byKind) != 1 || byKind[0].ID != "e-l2" {
		t.Fatalf("kind filter: %+v (%v)", byKind, err)
	}
	byState, err := a.ListEntries(ctx, MemoryFilter{Lifecycle: LifecycleActive})
	if err != nil || len(byState) != 3 {
		t.Fatalf("lifecycle filter: %d entries (%v)", len(byState), err)
	}
}
