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
	"unicode/utf8"
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

// Helper-path coverage: rune-safe truncation, deterministic sort, budget
// drop order (outcomes before notes), huge SomPhase, fit boundary.
func TestTruncateRuneSafeMultibyte(t *testing.T) {
	s := strings.Repeat("đ perpetual context €", 400)
	got := truncateRuneSafe(s, 1000)
	if len(got) > 1000 {
		t.Fatalf("clamp exceeded budget: %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("clamp split a rune: invalid UTF-8")
	}
}

func TestSortStringsDeterministic(t *testing.T) {
	items := []string{"m", "a", "z"}
	SortStrings(items, func(a, b string) bool { return a < b })
	if items[0] != "a" || items[2] != "z" {
		t.Fatalf("sort not applied: %v", items)
	}
}

func TestAssembleBudgetDropsOutcomesBeforeNotes(t *testing.T) {
	notes := []string{strings.Repeat("n", 500), strings.Repeat("n", 500)}
	outcomes := make([]string, 20)
	for i := range outcomes {
		outcomes[i] = strings.Repeat("o", 500)
	}
	b := NewBroker(testSources(notes, nil, outcomes, nil, "", nil), nil)
	p := b.Assemble(context.Background())
	if len(p.VaultNotes) != 2 {
		t.Fatalf("notes must survive outcome dropping, got %d", len(p.VaultNotes))
	}
	if len(p.RecentOutcomes) >= 20 {
		t.Fatalf("outcomes must be dropped first under budget, got %d", len(p.RecentOutcomes))
	}
	if p.Bytes() > 4096 {
		t.Fatalf("packet still over budget: %d", p.Bytes())
	}
	if !p.Truncated {
		t.Fatal("budget drop must flag truncation")
	}
}

func TestAssembleHugeSomPhaseClamped(t *testing.T) {
	b := NewBroker(testSources(nil, nil, nil, nil, strings.Repeat("p", 5000), nil), nil)
	p := b.Assemble(context.Background())
	if p.Bytes() > maxPacketChars {
		t.Fatalf("huge SomPhase blew the budget: %d", p.Bytes())
	}
	if !p.Truncated {
		t.Fatal("clamp must flag truncation")
	}
}

func TestFitBoundaryAtFive(t *testing.T) {
	items := []string{"1", "2", "3", "4", "5"}
	kept, cut := fit(items)
	if cut || len(kept) != 5 {
		t.Fatalf("exactly-5 must not be flagged cut, got cut=%v len=%d", cut, len(kept))
	}
}
