package context

// #396 Context Broker (ADR-0021 §8.2): assembles a bounded ContextPacket per
// deployment point from Vault (#395 post-gate) + Telemetry (recent outcomes)
// + SOM state. Fail-open per source; total failure degrades to the v1
// minimal request.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func testSources(vault []string, vErr error, outcomes []string, oErr error, world []string, wErr error, phase string, pErr error) Sources {
	return Sources{
		VaultNotes: func(ctx context.Context) ([]string, error) { return vault, vErr },
		RecentOutcomes: func(ctx context.Context) ([]string, error) {
			return outcomes, oErr
		},
		WorldSnippets: func(ctx context.Context) ([]string, error) { return world, wErr },
		SomPhase:      func(ctx context.Context) (string, error) { return phase, pErr },
	}
}

// Degraded paths: empty / broken sources produce a minimal packet, never an
// error — the broker's contract is fail-open per source.
func TestAssembleEmptyAndBrokenSources(t *testing.T) {
	b := NewBroker(testSources(nil, nil, nil, nil, nil, nil, "", nil), nil)
	p := b.Assemble(context.Background())
	if p.Bytes() != 0 || len(p.VaultNotes) != 0 || len(p.RecentOutcomes) != 0 || p.SomPhase != "" {
		t.Fatalf("all-empty sources must yield a minimal packet, got %+v", p)
	}

	var events []string
	sink := func(event, detail string) { events = append(events, event) }
	b = NewBroker(testSources(nil, errors.New("vault db locked"), nil, nil, nil, nil, "L2", nil), sink)
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
	b := NewBroker(testSources(vault, nil, outcomes, nil, nil, nil, "L1", nil), nil)
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
	notes := []string{strings.Repeat("n", 850), strings.Repeat("n", 850)}
	world := make([]string, 5)
	for i := range world {
		world[i] = strings.Repeat("w", 512)
	}
	outcomes := make([]string, 10)
	for i := range outcomes {
		outcomes[i] = strings.Repeat("o", 700)
	}
	b := NewBroker(testSources(notes, nil, outcomes, nil, world, nil, strings.Repeat("s", 100), nil), nil)
	p := b.Assemble(context.Background())
	if len(p.VaultNotes) != 2 {
		t.Fatalf("notes must survive lower-priority dropping, got %d", len(p.VaultNotes))
	}
	if len(p.WorldSnippets) >= len(world) {
		t.Fatalf("world snippets must drop after outcomes, got %d", len(p.WorldSnippets))
	}
	if len(p.RecentOutcomes) >= maxPerSource {
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
	b := NewBroker(testSources(nil, nil, nil, nil, nil, nil, strings.Repeat("p", 5000), nil), nil)
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

func TestAssembleWorldSnippetClamp512RuneSafe(t *testing.T) {
	long := strings.Repeat("đ", 600)
	b := NewBroker(testSources(nil, nil, nil, nil, []string{long}, nil, "", nil), nil)
	p := b.Assemble(context.Background())
	if len(p.WorldSnippets) != 1 {
		t.Fatalf("want one snippet, got %d", len(p.WorldSnippets))
	}
	if len(p.WorldSnippets[0]) > maxSnippetChars {
		t.Fatalf("snippet len %d exceeds %d", len(p.WorldSnippets[0]), maxSnippetChars)
	}
	if !utf8.ValidString(p.WorldSnippets[0]) {
		t.Fatal("clamp produced invalid UTF-8")
	}
}

func TestAssembleBudgetDropsWorldSnippetsBeforeVaultNotes(t *testing.T) {
	notes := []string{strings.Repeat("n", 900), strings.Repeat("n", 900)}
	world := make([]string, 5)
	for i := range world {
		world[i] = strings.Repeat("w", 512)
	}
	outcomes := []string{strings.Repeat("o", 400)}
	b := NewBroker(testSources(notes, nil, outcomes, nil, world, nil, "", nil), nil)
	p := b.Assemble(context.Background())
	if len(p.WorldSnippets) >= len(world) {
		t.Fatalf("world snippets should be dropped before vault notes, got %d", len(p.WorldSnippets))
	}
	if len(p.VaultNotes) != 2 {
		t.Fatalf("vault notes should survive world dropping, got %d", len(p.VaultNotes))
	}
	if p.Bytes() > 4096 {
		t.Fatalf("packet over budget: %d", p.Bytes())
	}
}

func TestAssembleWorldSnippetsFailOpenEmitsBrokerFailure(t *testing.T) {
	var events []string
	sink := func(event, detail string) { events = append(events, event+":"+detail) }
	b := NewBroker(testSources(nil, nil, nil, nil, nil, errors.New("bad json"), "", nil), sink)
	p := b.Assemble(context.Background())
	if len(p.WorldSnippets) != 0 {
		t.Fatalf("broken world source must be skipped, got %+v", p.WorldSnippets)
	}
	found := false
	for _, e := range events {
		if strings.Contains(e, "broker_failure:reach:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected broker_failure for reach source, got %v", events)
	}
}

func TestContextPacketBytesIncludesWorldSnippets(t *testing.T) {
	p := &ContextPacket{
		VaultNotes:     []string{"v"},
		RecentOutcomes: []string{"o"},
		WorldSnippets:  []string{"w1", "w2"},
		SomPhase:       "L1",
	}
	want := len("v") + len("o") + len("w1") + len("w2") + len("L1")
	if got := p.Bytes(); got != want {
		t.Fatalf("Bytes() = %d, want %d", got, want)
	}
}

func TestWorldSnippetsFromFile_FailOpenOnMissingEnvOrFile(t *testing.T) {
	t.Setenv("G8S_WORLD_SNIPPETS_PATH", "")
	got, err := WorldSnippetsFromFile(context.Background())
	if err != nil {
		t.Fatalf("missing env should be healthy-empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %v", got)
	}

	dir := t.TempDir()
	t.Setenv("G8S_WORLD_SNIPPETS_PATH", filepath.Join(dir, "missing.json"))
	got, err = WorldSnippetsFromFile(context.Background())
	if err != nil {
		t.Fatalf("missing file should be healthy-empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %v", got)
	}
}

func TestWorldSnippetsFromFile_ParsesJSONArrayAndCaps5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snippets.json")
	raw := `["a","b","c","d","e","f","g"]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("G8S_WORLD_SNIPPETS_PATH", path)
	got, err := WorldSnippetsFromFile(context.Background())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != maxPerSource {
		t.Fatalf("len = %d, want cap %d", len(got), maxPerSource)
	}
	if got[0] != "a" || got[4] != "e" {
		t.Fatalf("unexpected cap order: %v", got)
	}
}
