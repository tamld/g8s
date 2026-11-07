package dogfood

import "testing"

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
		{"empty reason", "subject\n\nProvenance: F0-direct — ", "unclassified", ""},
		{"wrong dash", "subject\n\nProvenance: F0-direct - wiring", "unclassified", ""},
		{"wrong kind", "subject\n\nProvenance: worker — wiring", "unclassified", ""},
		{"both", "subject\n\nReceipt: task-1\nProvenance: F0-direct — wiring", "unclassified", ""},
		{"repeated", "subject\n\nReceipt: task-1\nReceipt: task-2", "unclassified", ""},
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
