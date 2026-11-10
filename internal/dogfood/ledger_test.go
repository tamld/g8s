package dogfood

import (
	"strings"
	"testing"
)

func TestClassifyCommit(t *testing.T) {
	for _, tc := range []struct {
		name, msg, kind, ref string
	}{
		{"receipt", "feat: work\n\nReceipt: task-123\n", "receipt", "task-123"},
		{"whitespace", "subject\r\n\r\n rEcEiPt :  task-1  \r\nSigned-off-by: A <a@example.org>\r\n", "receipt", "task-1"},
		{"direct", "subject\n\nPROVENANCE: f0-DIRECT  —  mechanical gate wiring", "f0_direct", "mechanical gate wiring"},
		{"missing", "subject", "unclassified", ""},
		{"subject spoof", "Receipt: task-1", "unclassified", ""},
		{"body spoof", "subject\n\nReceipt: task-1\n\nordinary body", "unclassified", ""},
		{"mixed prose", "subject\n\nexplanation\nReceipt: task-1", "unclassified", ""},
		{"empty receipt", "subject\n\nReceipt: ", "unclassified", ""},
		{"multiple tokens", "subject\n\nReceipt: task-1 task-2", "unclassified", ""},
		{"empty reason", "subject\n\nProvenance: F0-direct — ", "classification_error", E_PROVENANCE_TAG_EMPTY_REASON},
		{"wrong dash", "subject\n\nProvenance: F0-direct - wiring", "unclassified", ""},
		{"wrong kind", "subject\n\nProvenance: worker — wiring", "unclassified", ""},
		{"both", "subject\n\nReceipt: task-1\nProvenance: F0-direct — wiring", "unclassified", ""},
		{"repeated", "subject\n\nReceipt: task-1\nReceipt: task-2", "unclassified", ""},
		{"provenance receipt shorthand", "feat: x\n\nProvenance: Receipt 656d8e02 (cursor/composer-2.5)", "receipt", "656d8e02"},
		{"provenance worker task shorthand", "feat: x\n\nProvenance: worker task 98c88b6d via cursor/composer-2.5; F0 fixed gate", "receipt", "98c88b6d"},
		{"wrapped f0-direct reason", "docs: x\n\nProvenance: F0-direct — mechanical recording of an already-executed drill's\nresults into the spec's verification section.", "f0_direct", "mechanical recording of an already-executed drill's results into the spec's verification section."},
		{"wrapped prose after receipt still rejected", "feat: x\n\nReceipt: task-123\nextra prose without colon", "unclassified", ""},
		{"wrapped prose after signed-off-by rejected", "feat: x\n\nSigned-off-by: A <a@example.org>\nextra prose\nReceipt: task-123", "unclassified", ""},
		{"conflict receipt plus provenance receipt", "feat: x\n\nReceipt: task-123\nProvenance: Receipt 656d8e02", "unclassified", ""},
		{"invalid receipt punctuation", "subject\n\nReceipt: !!!!!!", "unclassified", ""},
		{"invalid receipt short alpha", "subject\n\nReceipt: abc", "unclassified", ""},
		{"invalid provenance receipt task bang", "subject\n\nProvenance: Receipt task-!", "unclassified", ""},
		{"invalid provenance worker task short", "subject\n\nProvenance: worker task abc via cursor", "unclassified", ""},
		{"invalid provenance receipt trailing semicolons", "subject\n\nProvenance: Receipt task-123;;;", "unclassified", ""},
		{"invalid provenance worker task trailing comma", "subject\n\nProvenance: worker task task-123, via cursor", "unclassified", ""},
		{"signed-off-by f0-direct spoof unfold rejected", "subject\n\nSigned-off-by: F0-direct — signer\nextra prose\nReceipt: task-123", "unclassified", ""},
		{"colon prose before receipt rejected", "subject\n\nThis is explanatory prose: not a trailer\nReceipt: task-123", "unclassified", ""},
		{"colon prose after receipt rejected", "subject\n\nReceipt: task-123\nThis is explanatory prose: after receipt", "unclassified", ""},
		{"provenance receipt annotation mentioning Receipt", "subject\n\nProvenance: Receipt task-123 (Receipt verified)", "receipt", "task-123"},
		{"provenance worker task annotation mentioning F0-direct", "subject\n\nProvenance: worker task task-123 (F0-direct review)", "receipt", "task-123"},
		{"provenance receipt two bare tokens rejected", "subject\n\nProvenance: Receipt task-123 task-456", "unclassified", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCommit(tc.msg); got != (Provenance{Kind: tc.kind, Ref: tc.ref}) {
				t.Fatalf("got %+v; want %s %q", got, tc.kind, tc.ref)
			}
		})
	}
}

func TestRatio(t *testing.T) {
	r, f, u, n, ratio := Ratio([]Provenance{{Kind: "receipt"}, {Kind: "receipt"}, {Kind: "f0_direct"}, {Kind: "unclassified"}, {Kind: "invalid"}})
	if r != 2 || f != 1 || u != 2 || n != 5 || ratio != 2.0/3.0 {
		t.Fatalf("got %d %d %d %d %f", r, f, u, n, ratio)
	}
	for _, report := range [][]Provenance{nil, {{Kind: "unclassified"}}} {
		_, _, _, _, ratio := Ratio(report)
		if ratio != 0 {
			t.Fatalf("zero classified ratio = %f", ratio)
		}
	}
}

func TestUnclassifiedBreakdown(t *testing.T) {
	report := []Provenance{
		{Kind: "receipt", Ref: "task-1"},
		{Kind: "unclassified"},
		{Kind: "unclassified"},
		{Kind: "classification_error", Ref: E_PROVENANCE_TAG_EMPTY_REASON},
		{Kind: "f0_direct", Ref: "mechanical"},
		{Kind: "classification_error"},
	}
	counts := UnclassifiedBreakdown(report)
	if counts["subject-without-trailer"] != 2 {
		t.Fatalf("subject-without-trailer = %d, want 2", counts["subject-without-trailer"])
	}
	if counts[E_PROVENANCE_TAG_EMPTY_REASON] != 1 {
		t.Fatalf("coded error count = %d, want 1", counts[E_PROVENANCE_TAG_EMPTY_REASON])
	}
	if counts["unknown-classification-error"] != 1 {
		t.Fatalf("unknown-classification-error = %d, want 1 (empty Ref fallback)", counts["unknown-classification-error"])
	}
	if len(counts) != 3 {
		t.Fatalf("classified kinds must not be counted; got %v", counts)
	}
	if got := UnclassifiedBreakdown([]Provenance{{Kind: "receipt"}, {Kind: "f0_direct"}}); len(got) != 0 {
		t.Fatalf("all-classified report must yield empty breakdown, got %v", got)
	}
	if got := UnclassifiedBreakdown(nil); len(got) != 0 {
		t.Fatalf("nil report must yield empty breakdown, got %v", got)
	}
}

func TestFormatUnclassifiedBreakdown(t *testing.T) {
	if got := FormatUnclassifiedBreakdown([]Provenance{{Kind: "receipt"}}); got != "" {
		t.Fatalf("all-classified report must format empty, got %q", got)
	}
	report := []Provenance{
		{Kind: "unclassified"},
		{Kind: "classification_error", Ref: E_PROVENANCE_TAG_EMPTY_REASON},
		{Kind: "unclassified"},
	}
	out := FormatUnclassifiedBreakdown(report)
	for _, want := range []string{
		"### Unclassified breakdown",
		"| Issue | Count |",
		"| subject-without-trailer | 2 |",
		"| " + E_PROVENANCE_TAG_EMPTY_REASON + " | 1 |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("format output missing %q:\n%s", want, out)
		}
	}
	// Keys must render in sorted order for stable CI logs.
	if strings.Index(out, E_PROVENANCE_TAG_EMPTY_REASON) > strings.Index(out, "subject-without-trailer") {
		t.Fatalf("breakdown keys not sorted:\n%s", out)
	}
}
