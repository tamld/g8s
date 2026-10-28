package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/reflex"
	"github.com/tamld/g8s/internal/vault"
)

func TestExperientialAutopsyExtractsFailureToFixAndBlocksSecrets(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	v, err := vault.NewVault(filepath.Join(tmpDir, "vault.db"), nil)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	defer v.Close()

	adapter, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath: filepath.Join(tmpDir, "memory.db"),
		Vault:  v,
	})
	if err != nil {
		t.Fatalf("NewLocalSQLiteMemoryAdapter: %v", err)
	}
	defer adapter.Close()

	gate := reflex.NewReflexGate()
	adapter.SetGateSensor(func(nakedPayload string) (string, error) {
		return gate.EvaluateMemoryEntry(ctx, nakedPayload)
	})

	traj := AutopsyTrajectory{
		TaskID:            "task-fix-02",
		PreviousTaskID:    "task-fail-01",
		SessionID:         "sess-autopsy-1",
		FailedCheck:       "go_test",
		FailedExitCode:    1,
		DiagnosticExcerpt: "internal/fsops/fsops.go:142: old_string found 2 times (token sk-proj-1234567890abcdefghijklmnop)",
		FixSummary:        "Include surrounding function signature lines in old_string to disambiguate multi-match edit",
		Package:           "internal/fsops",
		File:              "internal/fsops/fsops.go",
		Symbol:            "Edit",
		TestFile:          "internal/fsops/batch_and_tier_test.go",
		TestCase:          "TestEditDiagnosticsLineNumbersAndNearestMatch",
		Preconditions: map[string]string{
			"domain": "agent-io",
		},
	}

	report, err := adapter.RunExperientialAutopsy(ctx, traj, true)
	if err != nil {
		t.Fatalf("RunExperientialAutopsy failed: %v", err)
	}
	if report == nil || report.ProposedEntry == nil {
		t.Fatalf("expected non-nil AutopsyReport and ProposedEntry")
	}
	// Secret token in diagnostic excerpt must be scrubbed before entering memory!
	if strings.Contains(report.ProposedEntry.Payload, "sk-proj-1234567890abcdefghijklmnop") {
		t.Fatalf("expected secret token to be redacted from autopsy payload, got: %s", report.ProposedEntry.Payload)
	}
	if report.ProposedEntry.Lifecycle != LifecycleActive {
		t.Fatalf("expected proposed autopsy entry to be active, got %s", report.ProposedEntry.Lifecycle)
	}
	if report.CandidateRecord.SpatialCoordinates.File != "internal/fsops/fsops.go" ||
		report.CandidateRecord.ForensicVerification.TestFile != "internal/fsops/batch_and_tier_test.go" {
		t.Fatalf("unexpected candidate record anchors: %+v", report.CandidateRecord)
	}
}

func TestExponentialMasteryOutcomeFeedbackAndCompounding(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	v, err := vault.NewVault(filepath.Join(tmpDir, "vault.db"), nil)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	defer v.Close()

	adapter, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath: filepath.Join(tmpDir, "memory.db"),
		Vault:  v,
	})
	if err != nil {
		t.Fatalf("NewLocalSQLiteMemoryAdapter: %v", err)
	}
	defer adapter.Close()

	entryGood := &MemoryEntry{
		ID:        "mem-mastery-good",
		Kind:      KindProcedure,
		Scope:     ScopeGlobal,
		SessionID: "sess-origin",
		Payload:   "Always verify feature-map.yml ownership before adding new CLI files",
		Preconditions: map[string]string{
			"feature": "FEAT-FEATUREMAP-01",
			"file":    "internal/featuremap/audit.go",
		},
	}
	if err := adapter.ProposeEntry(ctx, entryGood); err != nil {
		t.Fatalf("ProposeEntry good: %v", err)
	}

	entryFlawed := &MemoryEntry{
		ID:        "mem-mastery-flawed",
		Kind:      KindJudgment,
		Scope:     ScopeGlobal,
		SessionID: "sess-origin",
		Payload:   "Skip gofmt before git push to save time",
		Preconditions: map[string]string{
			"feature": "FEAT-PLATFORM-CLI-01",
		},
	}
	if err := adapter.ProposeEntry(ctx, entryFlawed); err != nil {
		t.Fatalf("ProposeEntry flawed: %v", err)
	}

	// 1. Record 3 first-pass verified successes across distinct sessions for entryGood
	for _, sess := range []string{"sess-apply-1", "sess-apply-2", "sess-apply-3"} {
		out, err := adapter.RecordOutcome(ctx, []string{entryGood.ID}, sess, true)
		if err != nil {
			t.Fatalf("RecordOutcome pass (%s): %v", sess, err)
		}
		if len(out.Updated) != 1 {
			t.Fatalf("expected 1 updated entry, got %d", len(out.Updated))
		}
	}

	updatedGood, err := adapter.GetEntry(ctx, entryGood.ID)
	if err != nil {
		t.Fatalf("GetEntry good: %v", err)
	}
	if updatedGood.AppliedCount != 3 || updatedGood.FirstPassSuccessCount != 3 {
		t.Fatalf("expected AppliedCount=3 and FirstPassSuccessCount=3, got %d / %d", updatedGood.AppliedCount, updatedGood.FirstPassSuccessCount)
	}
	if updatedGood.ReviewFlag != ReviewFlagReadyForDoctrine {
		t.Fatalf("expected ReviewFlag=%q after 3 first-pass successes, got %q", ReviewFlagReadyForDoctrine, updatedGood.ReviewFlag)
	}

	// 2. Record a counter-example failure for entryFlawed
	if _, err := adapter.RecordOutcome(ctx, []string{entryFlawed.ID}, "sess-apply-fail", false); err != nil {
		t.Fatalf("RecordOutcome fail: %v", err)
	}
	updatedFlawed, err := adapter.GetEntry(ctx, entryFlawed.ID)
	if err != nil {
		t.Fatalf("GetEntry flawed: %v", err)
	}
	if updatedFlawed.FailureAfterRecallCount != 1 {
		t.Fatalf("expected FailureAfterRecallCount=1, got %d", updatedFlawed.FailureAfterRecallCount)
	}
	if updatedFlawed.ReviewFlag != ReviewFlagNeedsDialecticalReview {
		t.Fatalf("expected ReviewFlag=%q on counter-example failure, got %q", ReviewFlagNeedsDialecticalReview, updatedFlawed.ReviewFlag)
	}

	// 3. Compound READY_FOR_DOCTRINE entries into the Knowledge Vault
	compounded, err := adapter.CompoundReadyEntries(ctx, CompoundOptions{
		DefaultFile:     "internal/featuremap/audit.go",
		DefaultSymbol:   "Audit",
		DefaultTestFile: "internal/featuremap/audit_test.go",
		DefaultTestCase: "TestAuditFeaturesASTAndLive",
	})
	if err != nil {
		t.Fatalf("CompoundReadyEntries: %v", err)
	}
	if len(compounded) != 1 || compounded[0].ID != entryGood.ID {
		t.Fatalf("expected entryGood to be compounded to DOCTRINE, got %+v", compounded)
	}

	afterCompound, err := adapter.GetEntry(ctx, entryGood.ID)
	if err != nil {
		t.Fatalf("GetEntry after compound: %v", err)
	}
	if afterCompound.Lifecycle != LifecycleDoctrine {
		t.Fatalf("expected entryGood lifecycle to be doctrine after compounding, got %s", afterCompound.Lifecycle)
	}
}
