// Package context is the Context Broker (ADR-0021 §8.2, #396): it assembles
// a bounded situational ContextPacket per deployment point from the knowledge
// vault (post promotion gate, #395), the telemetry ledger (recent outcomes,
// failure signatures), and the SOM state (current phase, DoR status).
//
// Contract: Assemble NEVER fails. Each source fails independently open — a
// broken source is skipped and reported as a `broker_failure` event on the
// sink; a healthy source still contributes. The packet is hard-capped at
// 4096 chars (mirrors ADR-0023 G6): top-N per source, ranked, truncated.
package context

import (
	"context"
	"sort"
	"strings"
)

// maxPacketChars is the hard packet budget (mirrors ADR-0023 G6).
const maxPacketChars = 4096

// maxPerSource bounds how many notes one source may contribute.
const maxPerSource = 5

// ContextPacket is the bounded situational context attached to a
// TriageRequest (v2).
type ContextPacket struct {
	VaultNotes     []string `json:"vault_notes,omitempty"`
	RecentOutcomes []string `json:"recent_outcomes,omitempty"`
	SomPhase       string   `json:"som_phase,omitempty"`
	Truncated      bool     `json:"truncated,omitempty"`
}

// Bytes reports the total rendered size of the packet for budget checks.
func (p *ContextPacket) Bytes() int {
	if p == nil {
		return 0
	}
	n := 0
	for _, s := range p.VaultNotes {
		n += len(s)
	}
	for _, s := range p.RecentOutcomes {
		n += len(s)
	}
	n += len(p.SomPhase)
	return n
}

// Sources carries one independently-failing fetcher per context source.
// Nil fetchers count as healthy-but-empty (source not wired).
type Sources struct {
	VaultNotes     func(ctx context.Context) ([]string, error)
	RecentOutcomes func(ctx context.Context) ([]string, error)
	SomPhase       func(ctx context.Context) (string, error)
}

// Broker assembles ContextPackets. The sink receives `broker_failure` events
// (DEGRADED observability); nil sink drops events.
type Broker struct {
	sources Sources
	sink    func(event, detail string)
}

// NewBroker builds a broker. A nil sink is allowed (events dropped).
func NewBroker(sources Sources, sink func(event, detail string)) *Broker {
	return &Broker{sources: sources, sink: sink}
}

func (b *Broker) fail(detail string) {
	if b.sink != nil {
		b.sink("broker_failure", detail)
	}
}

// Assemble builds the packet. It never returns an error: every failure mode
// degrades to fewer sections (fail-open per source), and the caller gets a
// packet that may be minimal but is never nil.
func (b *Broker) Assemble(ctx context.Context) *ContextPacket {
	p := &ContextPacket{}

	if b.sources.VaultNotes != nil {
		notes, err := b.sources.VaultNotes(ctx)
		if err != nil {
			b.fail("vault: " + err.Error())
		} else {
			var cut bool
			p.VaultNotes, cut = fit(notes)
			p.Truncated = p.Truncated || cut
		}
	}
	if b.sources.RecentOutcomes != nil {
		items, err := b.sources.RecentOutcomes(ctx)
		if err != nil {
			b.fail("telemetry: " + err.Error())
		} else {
			var cut bool
			p.RecentOutcomes, cut = fit(items)
			p.Truncated = p.Truncated || cut
		}
	}
	if b.sources.SomPhase != nil {
		phase, err := b.sources.SomPhase(ctx)
		if err != nil {
			b.fail("som: " + err.Error())
		} else {
			p.SomPhase = strings.TrimSpace(phase)
		}
	}

	// Hard budget: drop lowest-priority sections first (outcomes → notes),
	// then truncate the remainder rune-safely.
	for p.Bytes() > maxPacketChars {
		p.Truncated = true
		switch {
		case len(p.RecentOutcomes) > 0:
			p.RecentOutcomes = p.RecentOutcomes[:len(p.RecentOutcomes)-1]
		case len(p.VaultNotes) > 1:
			p.VaultNotes = p.VaultNotes[:len(p.VaultNotes)-1]
		default:
			if len(p.VaultNotes) == 1 {
				if rest := maxPacketChars - (len(p.SomPhase) + 16); rest > 0 && len(p.VaultNotes[0]) > rest {
					p.VaultNotes[0] = truncateRuneSafe(p.VaultNotes[0], rest)
				}
			}
			if len(p.SomPhase) > maxPacketChars {
				p.SomPhase = truncateRuneSafe(p.SomPhase, maxPacketChars)
				p.Truncated = true
			}
			return p
		}
	}
	return p
}

// fit keeps the top-N items and reports whether content was cut.
func fit(items []string) ([]string, bool) {
	if len(items) == 0 {
		return nil, false
	}
	if len(items) > maxPerSource {
		return items[:maxPerSource], true
	}
	return items, false
}

// truncateRuneSafe cuts s to at most n bytes without splitting a rune.
func truncateRuneSafe(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 {
		r := []rune(cut[len(cut)-1:])
		_ = r
		// drop bytes until the tail is valid UTF-8
		if validTail(cut) {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut
}

func validTail(s string) bool {
	for i := 0; i < len(s); {
		r := rune(s[i])
		size := 1
		switch {
		case r >= 0xF0:
			size = 4
		case r >= 0xE0:
			size = 3
		case r >= 0xC0:
			size = 2
		}
		if i+size > len(s) {
			return false
		}
		i += size
	}
	return true
}

// SortStrings is a small helper for source implementations that want
// deterministic ranking before handing notes to the broker.
func SortStrings(items []string, less func(a, b string) bool) {
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
}
