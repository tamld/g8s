package context

// #396 Context Broker (ADR-0021 §8.2): assembles a bounded ContextPacket per
// deployment point from Vault (#395 post-gate) + Telemetry (recent outcomes)
// + SOM state. Fail-open per source; total failure degrades to the v1
// minimal request.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func testSources(vault []string, vErr error, outcomes []string, oErr error, phase string, pErr error) Sources {
	return Sources{
		VaultNotes: func(ctx context.Context) ([]string, error) { return vault, vErr },
		RecentOutcomes: func(ctx context.Context) ([]string, error) {
			return outcomes, oErr
		},
		SomPhase: func(ctx context.Context) (string, error) { return phase, pErr },
	}
}

// Degraded paths: empty / broken sources produce a minimal packet, never an
// error — the broker's contract is fail-open per source.
func TestAssembleEmptyAndBrokenSources(t *testing.T) {
	b := NewBroker(testSources(nil, nil, nil, nil, "", nil), nil)
	p := b.Assemble(context.Background())
	if p.Bytes() != 0 || len(p.VaultNotes) != 0 || len(p.RecentOutcomes) != 0 || p.SomPhase != "" {
		t.Fatalf("all-empty sources must yield a minimal packet, got %+v", p)
	}

	var events []string
	sink := func(event, detail string) { events = append(events, event) }
	b = NewBroker(testSources(nil, errors.New("vault db locked"), nil, nil, "L2", nil), sink)
	p = b.Assemble(context.Background())
	if len(p.VaultNotes) != 0 {
		t.Fatalf("broken vault source must be skipped, got %+v", p.VaultNotes)
	}
	if p.SomPhase != "L2" {
		t.Fatalf("healthy source must still contribute, got %q", p.SomPhase)
	}
	found := false
	for _, e := range events {
		if e == "broker_failure" {
			found = true
		}
	}
	if !found {
		t.Fatalf("broken source must emit broker_failure telemetry, got %v", events)
	}
}

// Content flows through and the total packet respects the 4096-char budget
// (mirrors G6): top-N per source, ranked, truncated.
func TestAssembleBudgetCap(t *testing.T) {
	var vault []string
	for i := 0; i < 50; i++ {
		vault = append(vault, strings.Repeat("v", 300)+string(rune('A'+i%26))+string(rune('0'+i/26)))
	}
	var outcomes []string
	for i := 0; i < 30; i++ {
		outcomes = append(outcomes, strings.Repeat("o", 300))
	}
	b := NewBroker(testSources(vault, nil, outcomes, nil, "L1", nil), nil)
	p := b.Assemble(context.Background())
	if p.Bytes() > 4096 {
		t.Fatalf("packet = %d chars, want <= 4096", p.Bytes())
	}
	if len(p.VaultNotes) == 0 || len(p.RecentOutcomes) == 0 {
		t.Fatalf("budgeted packet must keep top-ranked items, got %+v", p)
	}
	if !p.Truncated {
		t.Fatal("packet must be flagged truncated when the budget cut content")
	}
}
