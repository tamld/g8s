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

func TestRecordOutcome_SybilDuplicateSessionGuardAndCounterExampleDemotion(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	adapter, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath: filepath.Join(tmpDir, "memory.db"),
	})
	if err != nil {
		t.Fatalf("NewLocalSQLiteMemoryAdapter: %v", err)
	}
	defer adapter.Close()

	entry := &MemoryEntry{
		ID:        "mem-sybil-guard-01",
		Kind:      KindProcedure,
		Scope:     ScopeGlobal,
		SessionID: "sess-origin",
		Payload:   "Use lockfile.TryLock(path) instead of raw syscall.Flock",
		Preconditions: map[string]string{
			"file": "internal/lockfile/lockfile.go",
		},
	}
	if err := adapter.ProposeEntry(ctx, entry); err != nil {
		t.Fatalf("ProposeEntry: %v", err)
	}

	// 1. Worst Case (Sybil replay): Calling RecordOutcome 5 times with the SAME task/session ID
	// must only record 1 application and 1 first-pass success, never promoting to READY_FOR_DOCTRINE.
	for i := 0; i < 5; i++ {
		if _, err := adapter.RecordOutcome(ctx, []string{entry.ID}, "task-same-99", true); err != nil {
			t.Fatalf("RecordOutcome iter %d: %v", i, err)
		}
	}
	check1, err := adapter.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if check1.AppliedCount != 1 || check1.FirstPassSuccessCount != 1 {
		t.Fatalf("Sybil guard failed: expected AppliedCount=1 and FirstPassSuccessCount=1 after 5 duplicate calls from task-same-99, got %d / %d",
			check1.AppliedCount, check1.FirstPassSuccessCount)
	}
	if check1.ReviewFlag == ReviewFlagReadyForDoctrine {
		t.Fatalf("Sybil guard failed: single task re-verified 5 times must NEVER set READY_FOR_DOCTRINE")
	}

	// 2. Origin session replay must also be idempotent after 1 application
	for i := 0; i < 4; i++ {
		if _, err := adapter.RecordOutcome(ctx, []string{entry.ID}, "sess-origin", true); err != nil {
			t.Fatalf("RecordOutcome origin iter %d: %v", i, err)
		}
	}
	check2, err := adapter.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("GetEntry check2: %v", err)
	}
	if check2.FirstPassSuccessCount != 2 || check2.ReviewFlag == ReviewFlagReadyForDoctrine {
		t.Fatalf("expected FirstPassSuccessCount=2 and no READY_FOR_DOCTRINE after origin + 1 task, got count=%d flag=%q",
			check2.FirstPassSuccessCount, check2.ReviewFlag)
	}

	// 3. Even if a task passed earlier, a subsequent counter-example failure (!passedFirstTry)
	// must immediately demote ReviewFlag to NEEDS_DIALECTICAL_REVIEW.
	if _, err := adapter.RecordOutcome(ctx, []string{entry.ID}, "task-same-99", false); err != nil {
		t.Fatalf("RecordOutcome counter-example: %v", err)
	}
	check3, err := adapter.GetEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("GetEntry check3: %v", err)
	}
	if check3.FailureAfterRecallCount != 1 || check3.ReviewFlag != ReviewFlagNeedsDialecticalReview {
		t.Fatalf("expected counter-example failure to set FailureAfterRecallCount=1 and ReviewFlag=%q, got %+v",
			ReviewFlagNeedsDialecticalReview, check3)
	}
}

func TestRecordCrashAutopsy_AndLessonsBlockedRepeatPruning(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	adapter, err := NewLocalSQLiteMemoryAdapter(AdapterOptions{
		DBPath: filepath.Join(tmpDir, "memory.db"),
	})
	if err != nil {
		t.Fatalf("NewLocalSQLiteMemoryAdapter: %v", err)
	}
	defer adapter.Close()

	// 1. Sudden-Death CrashAutopsyHook (<200ms) with secret scrubbing (#695)
	crashRep, err := adapter.RecordCrashAutopsy(ctx, CrashContext{
		TaskID:     "task-crash-77",
		Command:    "worker",
		Source:     "panic",
		ErrorMsg:   "runtime error: nil pointer dereference with token=sk-live1234567890abcdef",
		StackTrace: "internal/worker/worker.go:142\nmain.go:40",
		File:       "internal/worker/worker.go",
	})
	if err != nil {
		t.Fatalf("RecordCrashAutopsy: %v", err)
	}
	if strings.Contains(crashRep.SanitizedLesson, "sk-live1234567890abcdef") {
		t.Fatalf("expected crash autopsy to scrub secret token, got %q", crashRep.SanitizedLesson)
	}
	if crashRep.ProposedEntry == nil || crashRep.ProposedEntry.ID == "" {
		t.Fatalf("expected proposed crash entry")
	}

	// 2. Simulate 1 high-ROI lesson (FirstPassSuccessCount=2) and 1 zero-ROI lesson (AppliedCount=3, FirstPassSuccessCount=0)
	zeroROI := &MemoryEntry{
		ID:        "mem-zero-roi-stale",
		Kind:      KindProcedure,
		Scope:     ScopeGlobal,
		SessionID: "sess-stale",
		Payload:   "Stale lesson that never prevents failures",
	}
	if err := adapter.ProposeEntry(ctx, zeroROI); err != nil {
		t.Fatalf("ProposeEntry zeroROI: %v", err)
	}

	// Crash entry succeeds on 2 distinct sessions -> ROI = 2
	if _, err := adapter.RecordOutcome(ctx, []string{crashRep.ProposedEntry.ID}, "sess-fix-1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.RecordOutcome(ctx, []string{crashRep.ProposedEntry.ID}, "sess-fix-2", true); err != nil {
		t.Fatal(err)
	}

	// Zero-ROI entry fails 3 times after recall -> AppliedCount=3, FirstPassSuccessCount=0
	for i := 1; i <= 3; i++ {
		if _, err := adapter.RecordOutcome(ctx, []string{zeroROI.ID}, "sess-fail", false); err != nil {
			t.Fatal(err)
		}
	}

	roiRep, err := adapter.ComputeLessonsBlockedRepeatROI(ctx, 3)
	if err != nil {
		t.Fatalf("ComputeLessonsBlockedRepeatROI: %v", err)
	}
	if roiRep.LessonsWithROIGtZero != 1 || roiRep.TotalBlockedRepeats != 2 {
		t.Fatalf("expected 1 lesson with ROI>0 and TotalBlockedRepeats=2, got %+v", roiRep)
	}
	if len(roiRep.ZeroROICandidates) != 1 || roiRep.ZeroROICandidates[0] != zeroROI.ID {
		t.Fatalf("expected zeroROI in ZeroROICandidates, got %+v", roiRep.ZeroROICandidates)
	}

	pruned, err := adapter.PruneZeroROILessons(ctx, 3)
	if err != nil {
		t.Fatalf("PruneZeroROILessons: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 zero-ROI lesson pruned, got %d", pruned)
	}
	archivedCheck, err := adapter.GetEntry(ctx, zeroROI.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archivedCheck.Lifecycle != LifecycleArchived {
		t.Fatalf("expected pruned lesson lifecycle=%s, got %s", LifecycleArchived, archivedCheck.Lifecycle)
	}
}
