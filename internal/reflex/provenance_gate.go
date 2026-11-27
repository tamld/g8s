// Authority Sink Gate — read-side provenance consumption (issue #977 PR-3).
//
// PR-2 (#977/#1035) landed the per-message provenance labels
// (internal/provenance, stamped into ContextPacket.SourceClasses by the
// Context Broker) and the declarative sink registry (.g8s/authority-sinks.yml
// with a mechanical reality test). PR-3 wires the CONSUMPTION half the threat
// model assigns to this slice
// (docs/security/context-authority-firewall-threat-model.md §4 step 2:
// "deterministic Authority Sink Gate decisions consuming the provenance
// labels + registry (per-message classes gate what may drive privileged
// actions)").
//
// Enforcement point: the supervisor policy engine's fast-path Write Receipt
// grant (EvaluatePolicy Rule 2 -> ActionGrantReceipt). This is the decision
// that drives the founding-instance sink of the threat model (receipt
// minting), and the only privileged-action decision that already RECEIVES a
// provenance-labeled ContextPacket (TriageRequest.ContextPacket, attached by
// the L1 pilot cmd/g8s/reflex.go and the worker L3 collector) — no new
// plumbing, and the check runs in deterministic code below the LLM sensor
// (ADR-0042 TRUST-G2: the trust question is never answered by inference).
//
// Policy (minimal, threat-model-derived — nothing invented):
//   - The provenance class of decision context is a server-derived fact,
//     never an input (mirrors PR-2's issuer rule): a section labeled with a
//     class that disagrees with the canonical class of its channel, or a
//     contributing section MISSING its label, is treated as unestablished
//     origin (V5: compressed context that drops source labels must never be
//     re-presented as high-trust).
//   - Content of unestablished origin (effective EXTERNAL_CONTENT — the
//     classifier's fail-closed default for unknown channels) may not drive
//     the receipt-grant fast-path; the verdict escalates to the human
//     operator (HITL) instead of granting.
//   - Trusted classes (OPERATOR_AUTHORITY / VERIFIED_POLICY / WORKER_OUTPUT /
//     TOOL_RESULT / RETRIEVED_MEMORY) keep their existing downstream
//     deterministic gates (worker acceptance, PromotionGate, lane router)
//     and do not block the grant — the packet's normal situational sections.
//   - Registry statuses are consumed fail-closed: a sink id outside the
//     reflex-driven table, or registered unguarded, can never be driven.
//   - nil ContextPacket = legacy v1 request (Constitution Axiom 5
//     byte-compatibility): behavior is unchanged pre-PR-3.
package reflex

import (
	"fmt"
	"sort"

	g8scontext "github.com/tamld/g8s/internal/context"
	"github.com/tamld/g8s/internal/provenance"
)

// Typed Authority Sink Gate refusal codes (E_* tokens, harness code style).
const (
	// CodeSinkUnknownAuthority: the sink id is not in the reflex-driven
	// registry table — fail-closed (a renamed or mistyped sink can never
	// silently bypass the gate).
	CodeSinkUnknownAuthority = "E_SINK_UNKNOWN_AUTHORITY"
	// CodeSinkUnguarded: the registry records the sink as unguarded.
	CodeSinkUnguarded = "E_SINK_UNGUARDED"
	// CodeProvenanceLabelMissing: a contributing packet section carries
	// content without the PR-2 label contract (provenance loss, V5).
	CodeProvenanceLabelMissing = "E_PROVENANCE_LABEL_MISSING"
	// CodeExternalContentDriveForbidden: an effective EXTERNAL_CONTENT
	// section (labeled external, unknown channel, or forged label) may not
	// drive the sink.
	CodeExternalContentDriveForbidden = "E_EXTERNAL_CONTENT_DRIVE_FORBIDDEN"
)

// TriageGrantReceiptSinkID is the registry sink id (.g8s/authority-sinks.yml)
// for the supervisor policy engine's fast-path Write Receipt grant — the
// authorization decision feeding receipt issuance.
const TriageGrantReceiptSinkID = "triage_grant_receipt"

// reflexDrivenSinks mirrors the .g8s/authority-sinks.yml `status` field for
// the privileged sinks the reflex policy engine drives. The registry file is
// the SSoT; TestReflexDrivenSinks_MatchRegistry pins this table to it
// mechanically, so a registry status edit without the code side (or vice
// versa) fails CI.
var reflexDrivenSinks = map[string]string{
	TriageGrantReceiptSinkID: "gated",
}

// SinkDecision is the deterministic Authority Sink Gate verdict for one
// (sink, decision-context) pair.
type SinkDecision struct {
	SinkID  string `json:"sink_id"`
	Allowed bool   `json:"allowed"`
	Code    string `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// MayDriveSink reports whether the given decision context (a provenance-
// labeled ContextPacket, nil = legacy v1 request) may drive the registry
// sink sinkID. Purely deterministic and fail-closed; it never consults an
// LLM and never returns an error — the decision and its typed code are the
// audit trail.
func MayDriveSink(sinkID string, packet *g8scontext.ContextPacket) SinkDecision {
	return MayDriveSinkIn(reflexDrivenSinks, sinkID, packet)
}

// MayDriveSinkIn is MayDriveSink against an explicit registry status table
// (the corpus and unit tests inject tables to cover the unguarded and
// unknown-sink fail-closed branches without editing the real registry).
func MayDriveSinkIn(table map[string]string, sinkID string, packet *g8scontext.ContextPacket) SinkDecision {
	status, ok := table[sinkID]
	if !ok {
		return SinkDecision{
			SinkID: sinkID, Allowed: false, Code: CodeSinkUnknownAuthority,
			Reason: fmt.Sprintf("authority sink %q is not in the reflex-driven registry table — fail-closed", sinkID),
		}
	}
	if status == "unguarded" {
		return SinkDecision{
			SinkID: sinkID, Allowed: false, Code: CodeSinkUnguarded,
			Reason: fmt.Sprintf("authority sink %q is registered unguarded — it must not be driven", sinkID),
		}
	}
	if section, code, reason, blocked := unestablishedOriginSection(packet); blocked {
		return SinkDecision{
			SinkID: sinkID, Allowed: false, Code: code,
			Reason: fmt.Sprintf("decision-context section %q may not drive sink %q: %s", section, sinkID, reason),
		}
	}
	return SinkDecision{
		SinkID: sinkID, Allowed: true,
		Reason: fmt.Sprintf("authority sink %q (status %s): decision context carries no unestablished-origin section", sinkID, status),
	}
}

// packetSections is the fixed broker section order for deterministic
// decisions and refusal reasons (map iteration is randomized).
var packetSections = []string{"vault_notes", "recent_outcomes", "world_snippets", "som_phase"}

// unestablishedOriginSection finds the first packet section whose effective
// provenance class is EXTERNAL_CONTENT (unestablished origin). A section
// qualifies when:
//   - it contributes content WITHOUT the PR-2 label (CodeProvenanceLabelMissing);
//   - its claimed label disagrees with the canonical class of its channel
//     (label forgery — the class is a server-derived fact);
//   - its claimed (or canonical) class is EXTERNAL_CONTENT, including the
//     classifier's fail-closed default for unknown channels.
func unestablishedOriginSection(p *g8scontext.ContextPacket) (section, code, reason string, blocked bool) {
	if p == nil {
		return "", "", "", false
	}
	ordered := append([]string(nil), packetSections...)
	extra := make([]string, 0, len(p.SourceClasses))
	for section := range p.SourceClasses {
		if !knownSection(section) {
			extra = append(extra, section)
		}
	}
	sort.Strings(extra) // deterministic order for unknown keys
	ordered = append(ordered, extra...)

	for _, section := range ordered {
		if !sectionContributes(p, section) {
			continue
		}
		claimed, labeled := p.SourceClasses[section]
		if !labeled {
			return section, CodeProvenanceLabelMissing,
				"contributing section carries no provenance label (PR-2 label contract violated — unestablished origin, threat model V5)", true
		}
		canonical := provenance.Classify(section)
		if claimed != canonical {
			return section, CodeExternalContentDriveForbidden,
				fmt.Sprintf("claimed class %s disagrees with the canonical class %s of the channel (labels are server-derived facts, never inputs)", claimed, canonical), true
		}
		if claimed == provenance.ExternalContent {
			return section, CodeExternalContentDriveForbidden,
				"content of unestablished origin (EXTERNAL_CONTENT)", true
		}
	}
	return "", "", "", false
}

func knownSection(section string) bool {
	for _, s := range packetSections {
		if s == section {
			return true
		}
	}
	return false
}

// sectionContributes reports whether the section actually carries content.
// A label on an empty section is stale (the broker stamps post-budget-cut)
// and cannot drive anything; an UNKNOWN section key with a label is treated
// as contributing (unestablished origin by construction — provenance.Classify
// defaults unknown channels to EXTERNAL_CONTENT).
func sectionContributes(p *g8scontext.ContextPacket, section string) bool {
	switch section {
	case "vault_notes":
		return len(p.VaultNotes) > 0
	case "recent_outcomes":
		return len(p.RecentOutcomes) > 0
	case "world_snippets":
		return len(p.WorldSnippets) > 0
	case "som_phase":
		return p.SomPhase != ""
	}
	return true
}
