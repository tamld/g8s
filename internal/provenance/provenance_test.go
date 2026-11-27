package provenance_test

// Red-first tests for per-message provenance classification (issue #977 PR-2,
// threat model docs/security/context-authority-firewall-threat-model.md §4
// step 1 / vector V5). The classifier is the vocabulary BELOW the DELTA-42
// premise gate (internal/premise tiers individual CLAIMS; provenance classes
// answer "where did this MESSAGE come from").
//
// Red-first: this file fails to compile until internal/provenance exists with
// the six-class enum and the deterministic classifier.
import (
	"testing"

	"github.com/tamld/g8s/internal/provenance"
)

func TestClassifyKnownSources(t *testing.T) {
	cases := []struct {
		source string
		want   provenance.Class
	}{
		// ContextPacket sections (internal/context broker wiring).
		{"vault_notes", provenance.RetrievedMemory},
		{"recent_outcomes", provenance.WorkerOutput},
		{"world_snippets", provenance.ExternalContent},
		{"som_phase", provenance.VerifiedPolicy},
		// Threat model §2 channel ids.
		{"operator", provenance.OperatorAuthority},
		{"s0_operator", provenance.OperatorAuthority},
		{"supervisor", provenance.OperatorAuthority},
		{"charter", provenance.OperatorAuthority},
		{"premise_probe", provenance.VerifiedPolicy},
		{"policy_registry", provenance.VerifiedPolicy},
		{"worker_task_result", provenance.WorkerOutput},
		{"finish_attempt", provenance.WorkerOutput},
		{"worker_telemetry", provenance.WorkerOutput},
		{"tool_result", provenance.ToolResult},
		{"mcp_tool", provenance.ToolResult},
		{"cli_output", provenance.ToolResult},
		{"vault", provenance.RetrievedMemory},
		{"lessons", provenance.RetrievedMemory},
		{"memory_promotion", provenance.RetrievedMemory},
		{"reach", provenance.ExternalContent},
		{"external_fetch", provenance.ExternalContent},
		{"web", provenance.ExternalContent},
	}
	for _, tc := range cases {
		if got := provenance.Classify(tc.source); got != tc.want {
			t.Errorf("Classify(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestClassifyNormalizesAndDefaultsConservatively(t *testing.T) {
	// Matching is case-insensitive on trimmed input.
	if got := provenance.Classify("  World_Snippets "); got != provenance.ExternalContent {
		t.Fatalf("Classify trimmed+folded = %q, want %q", got, provenance.ExternalContent)
	}
	// Unknown sources classify as EXTERNAL_CONTENT — the conservative
	// fail-closed default: content whose origin cannot be established is
	// treated as untrusted and carries the most restrictive label.
	for _, unknown := range []string{"", "bogus_channel", "freeform prose from nowhere"} {
		if got := provenance.Classify(unknown); got != provenance.ExternalContent {
			t.Errorf("Classify(%q) = %q, want conservative default %q", unknown, got, provenance.ExternalContent)
		}
	}
}

func TestClassEnumIsExactlyTheSixDocumentedClasses(t *testing.T) {
	// The enum is fixed by the PR-2 spec (issue #977): six classes. Pin the
	// strings — they are wire values (PR-3 consumers and the audit registry
	// match on these exact tokens).
	want := []provenance.Class{
		provenance.OperatorAuthority,
		provenance.VerifiedPolicy,
		provenance.WorkerOutput,
		provenance.ToolResult,
		provenance.RetrievedMemory,
		provenance.ExternalContent,
	}
	expect := []string{"OPERATOR_AUTHORITY", "VERIFIED_POLICY", "WORKER_OUTPUT", "TOOL_RESULT", "RETRIEVED_MEMORY", "EXTERNAL_CONTENT"}
	if len(want) != 6 {
		t.Fatalf("class enum drifted beyond the six documented classes: %v", want)
	}
	for i, c := range want {
		if string(c) != expect[i] {
			t.Fatalf("class %d = %q, want %q (wire values are load-bearing)", i, c, expect[i])
		}
	}
}

func TestLabelForCarriesClassAndSource(t *testing.T) {
	lbl := provenance.LabelFor("reach")
	if lbl.Class != provenance.ExternalContent || lbl.Source != "reach" {
		t.Fatalf("LabelFor(reach) = %+v, want {EXTERNAL_CONTENT reach}", lbl)
	}
}
