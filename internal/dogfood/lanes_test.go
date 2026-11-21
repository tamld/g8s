package dogfood

import (
	"encoding/json"
	"strings"
	"testing"
)

func receipt(ref string) Provenance { return Provenance{Kind: "receipt", Ref: ref} }
func f0direct() Provenance          { return Provenance{Kind: "f0_direct", Ref: "wiring"} }
func unclassified() Provenance      { return Provenance{Kind: "unclassified"} }
func classificationErr() Provenance { return Provenance{Kind: "classification_error", Ref: "E_TEST"} }

// TestClassifyCommitLane pins the lane routing used by the ledger (#971 WP2).
func TestClassifyCommitLane(t *testing.T) {
	tests := []struct {
		name    string
		summary string
		paths   []string
		want    string
	}{
		{"docs only prose", "update documentation bundle", []string{"docs/guide.md"}, "D"},
		{"markdown prose", "update spec document", []string{"specs/SPEC.md"}, "D"},
		{"security trust boundary", "tighten sandbox", []string{"internal/sandbox/policy.go"}, "S"},
		{"receipt boundary is security", "worker receipt scope", []string{"internal/receipt/receipt.go"}, "S"},
		{"feature default", "implement lane split ledger", []string{"internal/dogfood/lanes.go"}, "F"},
		{"empty paths default feature", "chore", nil, "F"},
		{"refactor declared", "refactor: split ledger helpers", []string{"internal/dogfood/ledger.go"}, "R"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCommitLane(tc.summary, tc.paths); got != tc.want {
				t.Fatalf("ClassifyCommitLane(%q, %v) = %q, want %q", tc.summary, tc.paths, got, tc.want)
			}
		})
	}
}

// TestIsRevertOrFixup covers the rework subject markers (#971 WP2).
func TestIsRevertOrFixup(t *testing.T) {
	yes := []string{
		`Revert "feat: x"`,
		"revert: bad change",
		"reverts commit abc123",
		"fixup! implement ledger",
		"squash! implement ledger",
		"Reapply \"feat: x\"",
	}
	for _, s := range yes {
		if !IsRevertOrFixup(s) {
			t.Fatalf("IsRevertOrFixup(%q) = false, want true", s)
		}
	}
	no := []string{
		"feat: add revert handling for lane metrics",
		"fix: ledger crash on empty window",
		"docs: revertible semantics note",
	}
	for _, s := range no {
		if IsRevertOrFixup(s) {
			t.Fatalf("IsRevertOrFixup(%q) = true, want false", s)
		}
	}
}

// TestBuildLaneLedger_NonExemptSplit is the core WP2 assertion: Lane D, F, S
// are tracked SEPARATELY and Lane D stays auditable (not exempt).
func TestBuildLaneLedger_NonExemptSplit(t *testing.T) {
	records := []CommitRecord{
		{Subject: "docs: a", Paths: []string{"docs/a.md"}, Provenance: receipt("task-1"), Lane: "D", RevertFixup: false},
		{Subject: "docs: b", Paths: []string{"docs/b.md"}, Provenance: f0direct(), Lane: "D", RevertFixup: false},
		{Subject: "docs: c", Paths: []string{"docs/c.md"}, Provenance: unclassified(), Lane: "D"},
		{Subject: "feat: d", Paths: []string{"internal/x.go"}, Provenance: receipt("task-2"), Lane: "F"},
		{Subject: "fix: e", Paths: []string{"internal/y.go"}, Provenance: unclassified(), Lane: "F"},
		{Subject: `Revert "feat: d"`, Paths: []string{"internal/x.go"}, Provenance: receipt("task-3"), Lane: "F", RevertFixup: true},
		{Subject: "sandbox tighten", Paths: []string{"internal/sandbox/z.go"}, Provenance: receipt("task-4"), Lane: "S"},
	}

	rows := BuildLaneLedger(records)
	if len(rows) != 5 {
		t.Fatalf("expected 5 canonical lane rows, got %d", len(rows))
	}
	byLane := map[string]LaneLedgerRow{}
	for _, row := range rows {
		byLane[row.Lane] = row
		// Every lane is auditable — the WP2 non-exemption invariant.
		if !row.Auditable {
			t.Fatalf("lane %s must remain auditable (Lane D is NOT exempt)", row.Lane)
		}
		if strings.TrimSpace(row.ReviewDepth) == "" || strings.TrimSpace(row.LaneName) == "" {
			t.Fatalf("lane %s missing review depth or name", row.Lane)
		}
	}
	d := byLane["D"]
	if d.Total != 3 || d.Receipt != 1 || d.F0Direct != 1 || d.Unclassified != 1 {
		t.Fatalf("Lane D split wrong: %+v", d)
	}
	f := byLane["F"]
	if f.Total != 3 || f.Receipt != 2 || f.Unclassified != 1 || f.RevertFixup != 1 {
		t.Fatalf("Lane F split wrong: %+v", f)
	}
	s := byLane["S"]
	if s.Total != 1 || s.Receipt != 1 {
		t.Fatalf("Lane S split wrong: %+v", s)
	}
	if !strings.Contains(d.ReviewDepth, "G2") || strings.Contains(d.ReviewDepth, "sast-triad") {
		t.Fatalf("Lane D review depth should be the light ALDC bundle, got %q", d.ReviewDepth)
	}
	if !strings.Contains(s.ReviewDepth, "sast-triad") {
		t.Fatalf("Lane S review depth should be the p0 bundle, got %q", s.ReviewDepth)
	}
}

// TestBuildCohortMetrics_InsufficientSample verifies the honest n<20 label:
// cohorts below the min sample carry insufficient_sample, not a bare rate.
func TestBuildCohortMetrics_InsufficientSample(t *testing.T) {
	var records []CommitRecord
	// 25 receipt-backed (3 reverts), 10 F0-direct (1 revert), 5 unclassified.
	for i := 0; i < 25; i++ {
		records = append(records, CommitRecord{Subject: "feat", Provenance: receipt("task-x"), Lane: "F", RevertFixup: i < 3})
	}
	for i := 0; i < 10; i++ {
		records = append(records, CommitRecord{Subject: "wiring", Provenance: f0direct(), Lane: "F", RevertFixup: i == 0})
	}
	for i := 0; i < 5; i++ {
		records = append(records, CommitRecord{Subject: "mystery", Provenance: unclassified(), Lane: "F"})
	}

	metrics := BuildCohortMetrics(records, 20)
	if len(metrics) != 3 {
		t.Fatalf("expected 3 cohorts, got %d", len(metrics))
	}
	byCohort := map[string]CohortMetric{}
	for _, m := range metrics {
		byCohort[m.Cohort] = m
	}
	r := byCohort[CohortReceiptBacked]
	if r.Commits != 25 || r.RevertFixupCommits != 3 || r.RateLabel != RateLabelMeasured {
		t.Fatalf("receipt cohort wrong: %+v", r)
	}
	if want := 3.0 / 25.0; r.RevertFixupRate != want {
		t.Fatalf("receipt revert rate = %v, want %v", r.RevertFixupRate, want)
	}
	f := byCohort[CohortF0Direct]
	if f.Commits != 10 || f.RateLabel != RateLabelInsufficientSample {
		t.Fatalf("F0 cohort must be labeled insufficient_sample at n=10: %+v", f)
	}
	if f.RevertFixupRate != 0 {
		t.Fatalf("insufficient_sample cohorts must not emit a bare rate: %+v", f)
	}
	u := byCohort[CohortUnclassified]
	if u.Commits != 5 || u.RateLabel != RateLabelInsufficientSample {
		t.Fatalf("unclassified cohort must be labeled insufficient_sample: %+v", u)
	}

	// Default min sample applies when 0 is passed.
	for _, m := range BuildCohortMetrics(records, 0) {
		if m.MinSample != DefaultCohortMinSample {
			t.Fatalf("default min sample not applied: %+v", m)
		}
	}
}

// TestBuildEscapeRateByLane checks per-lane escape labeling (#971 WP2).
func TestBuildEscapeRateByLane(t *testing.T) {
	var records []CommitRecord
	for i := 0; i < 20; i++ {
		records = append(records, CommitRecord{Subject: "feat", Provenance: receipt("task-x"), Lane: "F", RevertFixup: i < 2})
	}
	records = append(records, CommitRecord{Subject: "docs", Provenance: receipt("task-y"), Lane: "D"})
	rates := BuildEscapeRateByLane(records, 20)
	byLane := map[string]LaneEscapeRate{}
	for _, r := range rates {
		byLane[r.Lane] = r
	}
	f := byLane["F"]
	if f.RateLabel != RateLabelMeasured || f.EscapeRate != 0.1 {
		t.Fatalf("Lane F escape rate wrong: %+v", f)
	}
	d := byLane["D"]
	if d.RateLabel != RateLabelInsufficientSample {
		t.Fatalf("Lane D with n=1 must be insufficient_sample: %+v", d)
	}
}

// TestClassifySandboxOutcome covers the SANDBOX ENFORCEMENT (workspace_write)
// outcome buckets (#971 WP2).
func TestClassifySandboxOutcome(t *testing.T) {
	kernelEnv := `{"ok":true,"status":"completed","containment_mode":"kernel_enforced"}`
	degradedEnv := `{"ok":true,"status":"completed","containment_mode":"policy_only","sandbox_degraded":true,"sandbox_degraded_reason":"host lacks kernel sandbox enforcement"}`
	windowsEnv := `{"ok":true,"containment_mode":"job_object_env_scrubbed","sandbox_degraded":true}`
	refusedEnv := `{"ok":false,"status":"sandbox_policy_rejected","rejected_before_spawn":true,"policy_enforcement":{"evaluated_policy":{"rejected_before_spawn":true,"rejection_code":"E_SANDBOX_DEGRADED_NOT_ALLOWED"},"effective_enforcement":{"containment_mode":"policy_only"}}}`
	readOnlyEnv := `{"ok":true,"containment_mode":"kernel_enforced"}`

	if got := ClassifySandboxOutcome("workspace_write", kernelEnv); got != SandboxOutcomeKernelEnforced {
		t.Fatalf("kernel envelope classified %q", got)
	}
	if got := ClassifySandboxOutcome("workspace_write", degradedEnv); got != SandboxOutcomeDegradedAllowed {
		t.Fatalf("degraded envelope classified %q", got)
	}
	if got := ClassifySandboxOutcome("workspace_write", windowsEnv); got != SandboxOutcomeDegradedAllowed {
		t.Fatalf("windows degraded envelope classified %q", got)
	}
	if got := ClassifySandboxOutcome("workspace_write", refusedEnv); got != SandboxOutcomeRefused {
		t.Fatalf("refused envelope classified %q", got)
	}
	if got := ClassifySandboxOutcome("read_only", readOnlyEnv); got != SandboxOutcomeUnclassified {
		t.Fatalf("non-workspace_write must be unclassified, got %q", got)
	}
	if got := ClassifySandboxOutcome("workspace_write", "not json"); got != SandboxOutcomeUnclassified {
		t.Fatalf("malformed envelope must be unclassified, got %q", got)
	}
	if got := ClassifySandboxOutcome("workspace_write", ""); got != SandboxOutcomeUnclassified {
		t.Fatalf("empty envelope must be unclassified, got %q", got)
	}
	// Old envelopes without the degraded field but with a below-kernel mode are
	// honestly unclassified, not silently counted as enforced.
	legacyEnv := `{"ok":true,"containment_mode":"policy_only"}`
	if got := ClassifySandboxOutcome("workspace_write", legacyEnv); got != SandboxOutcomeUnclassified {
		t.Fatalf("legacy below-kernel envelope must stay unclassified, got %q", got)
	}
}

// TestSummarizeSandboxEnforcement checks the metric fold (#971 WP2).
func TestSummarizeSandboxEnforcement(t *testing.T) {
	outcomes := []SandboxEnforcementOutcome{
		SandboxOutcomeKernelEnforced,
		SandboxOutcomeKernelEnforced,
		SandboxOutcomeDegradedAllowed,
		SandboxOutcomeRefused,
		SandboxOutcomeUnclassified,
	}
	s := SummarizeSandboxEnforcement(outcomes)
	want := SandboxEnforcementSummary{WorkspaceWriteAttempts: 5, KernelEnforced: 2, DegradedAllowed: 1, Refused: 1, Unclassified: 1}
	if s != want {
		t.Fatalf("summary = %+v, want %+v", s, want)
	}
	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workspace_write_attempts", "kernel_enforced", "degraded_allowed", "refused"} {
		if !strings.Contains(string(blob), key) {
			t.Fatalf("summary JSON missing %s: %s", key, blob)
		}
	}
}

// TestParseCommitRecords covers the shared NUL-framed parser used by the
// provenance tooling and scorecard lane analysis.
func TestParseCommitRecords(t *testing.T) {
	stream := "feat: add lane split\x01\nBody prose.\n\nReceipt: task-1\n\x04\ninternal/dogfood/lanes.go\n\x00" +
		"docs: guide\x01\nProse change.\n\nProvenance: F0-direct — wiring\n\x04\ndocs/guide.md\n\x00" +
		"Revert \"feat: add lane split\"\x01\nThis reverts commit abc.\n\x04\ninternal/dogfood/lanes.go\n"
	records := ParseCommitRecords(stream)
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	if records[0].Lane != "F" || records[0].Provenance.Kind != "receipt" || records[0].Provenance.Ref != "task-1" {
		t.Fatalf("record 0 wrong: %+v", records[0])
	}
	if records[1].Lane != "D" || records[1].Provenance.Kind != "f0_direct" {
		t.Fatalf("record 1 wrong: %+v", records[1])
	}
	if records[2].Lane != "F" || !records[2].RevertFixup {
		t.Fatalf("record 2 must be a Lane F revert: %+v", records[2])
	}
}

// TestFormatLaneLedger asserts the non-exempt Lane D note renders.
func TestFormatLaneLedger(t *testing.T) {
	rows := BuildLaneLedger(nil)
	text := FormatLaneLedger(rows)
	if !strings.Contains(text, "Lane D (prose) is auditable") {
		t.Fatal("lane ledger must state Lane D auditability explicitly")
	}
	if !strings.Contains(text, "| D |") || !strings.Contains(text, "| F |") || !strings.Contains(text, "| S |") {
		t.Fatal("lane ledger must render D, F, S rows separately")
	}
}
