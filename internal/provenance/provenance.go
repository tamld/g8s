// Package provenance implements per-message provenance classification for
// coordinator input streams (issue #977 PR-2 — the labeling half of the
// Context Authority Firewall; threat model
// docs/security/context-authority-firewall-threat-model.md §4 step 1, vector
// V5 "summary losing provenance").
//
// Position in the stack: BELOW the DELTA-42 premise gate. internal/premise
// tiers individual CLAIMS (MECHANICAL_VERIFIED / OBSERVATIONAL / UNVERIFIED /
// CONTRADICTED) after deterministic probing; provenance classes answer the
// orthogonal, zero-cost question "which CHANNEL delivered this MESSAGE".
// Every message entering coordinator context carries one class from the fixed
// six-value enum.
//
// Composition, not rebuild: the existing mechanisms keep their roles — the
// ContextPacket's per-source section partition (internal/context), lessons
// ledger citation checks, PromotionGate lifecycle, and the DELTA-42 claim
// tiers all remain authoritative for their own surfaces. This package adds
// the shared vocabulary those layers can compose with.
//
// PR-2 ships LABELS ONLY: nothing in the runtime branches on a Class yet (the
// broker wiring in internal/context is additive; message strings are
// untouched). PR-3 wires deterministic Authority Sink Gate decisions on top.
package provenance

import "strings"

// Class is the provenance class of one coordinator input message. The six
// values are wire tokens (load-bearing for PR-3 consumers and the
// .g8s/authority-sinks.yml audit registry); the enum is closed.
type Class string

const (
	// OperatorAuthority marks content authored by the human principal (S0) or
	// the trusted F0 supervisor itself: operator directives, charters,
	// supervisor briefs, local operator policy.
	OperatorAuthority Class = "OPERATOR_AUTHORITY"
	// VerifiedPolicy marks content whose authority was machine-checked before
	// entering context: deterministic premise probes, the .g8s governance
	// registries, signed workflow state.
	VerifiedPolicy Class = "VERIFIED_POLICY"
	// WorkerOutput marks content produced by F1/F2 workers: task results,
	// FinishAttempt payloads, worker telemetry outcomes.
	WorkerOutput Class = "WORKER_OUTPUT"
	// ToolResult marks raw tool responses re-entering coordinator context:
	// MCP tool results, CLI stdout/stderr, fsops read/search output.
	ToolResult Class = "TOOL_RESULT"
	// RetrievedMemory marks content recalled from memory stores: knowledge
	// vault notes, the lessons ledger, promoted knowledge artifacts.
	RetrievedMemory Class = "RETRIEVED_MEMORY"
	// ExternalContent marks content fetched from outside the trust boundary:
	// reach/web fetches, external documents, anything of unestablished origin.
	ExternalContent Class = "EXTERNAL_CONTENT"
)

// Label is the per-message provenance stamp carried alongside content.
type Label struct {
	// Class is the message's provenance class (see the six-value enum).
	Class Class `json:"class"`
	// Source is the channel identifier the class was derived from.
	Source string `json:"source,omitempty"`
}

// knownSources maps channel identifiers to their provenance class. Keys cover
// the ContextPacket section names (internal/context) and the threat model §2
// channel-table ids. Lookup is case-insensitive on trimmed input.
var knownSources = map[string]Class{
	// ContextPacket sections (internal/context broker wiring).
	"vault_notes":     RetrievedMemory,
	"recent_outcomes": WorkerOutput,
	"world_snippets":  ExternalContent,
	"som_phase":       VerifiedPolicy,

	// Operator / supervisor channels (highest trust).
	"operator":    OperatorAuthority,
	"s0_operator": OperatorAuthority,
	"supervisor":  OperatorAuthority,
	"charter":     OperatorAuthority,
	"brief":       OperatorAuthority,

	// Machine-verified policy channels.
	"premise_probe":   VerifiedPolicy,
	"policy_registry": VerifiedPolicy,
	"trust_boundary":  VerifiedPolicy,

	// Worker channels.
	"worker_task_result": WorkerOutput,
	"finish_attempt":     WorkerOutput,
	"worker_telemetry":   WorkerOutput,

	// Tool channels.
	"tool_result": ToolResult,
	"mcp_tool":    ToolResult,
	"cli_output":  ToolResult,
	"fsops_read":  ToolResult,

	// Memory channels.
	"vault":            RetrievedMemory,
	"lessons":          RetrievedMemory,
	"memory_promotion": RetrievedMemory,

	// External channels.
	"reach":          ExternalContent,
	"external_fetch": ExternalContent,
	"web":            ExternalContent,
}

// Classify resolves the provenance class for a source channel identifier.
// Matching is case-insensitive on trimmed input.
//
// Unknown sources classify as ExternalContent — the conservative fail-closed
// default: content whose origin cannot be established is treated as untrusted
// and carries the most restrictive label a PR-3 consumer acts on. A classifier
// that defaulted unknowns to a trusted class would be an anti-laundering
// machine ("name your channel 'supervisor' and win").
func Classify(source string) Class {
	if c, ok := knownSources[strings.ToLower(strings.TrimSpace(source))]; ok {
		return c
	}
	return ExternalContent
}

// LabelFor returns the Label for a source channel identifier (Classify plus
// the source provenance, for stamps that want the origin recorded too).
func LabelFor(source string) Label {
	return Label{Class: Classify(source), Source: source}
}
