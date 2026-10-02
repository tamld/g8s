package cleanup

import (
	"context"
	"testing"
	"time"
)

func TestScratchTargetEnablesSweepWithoutScratchFlag(t *testing.T) {
	staleTime := time.Now().Add(-8 * 24 * time.Hour)

	// Scenario 1: --target scratch-branch alone without ScratchEnabled (ScratchEnabled: false)
	// Direct sweep invocation with the target set and scratch enabled-by-target
	// -> the stale scratch branch IS swept.
	t.Run("target scratch-branch enables sweep without scratch flag", func(t *testing.T) {
		mock := &MockCleanupGitRunner{
			LocalBranchesRes:      []string{"blind/test-1"},
			BranchTipSHARes:       "abc123def4567890",
			BranchTipTimeRes:      staleTime,
			BranchesContainingRes: []string{"blind/test-1", "main"},
		}

		cfg := CleanupConfig{
			RepoDir:         "/repo",
			GitRunner:       mock,
			Targets:         []string{TargetScratchBranch},
			ScratchEnabled:  false, // operator did NOT pass --scratch
			ScratchPatterns: []string{"blind/*"},
			Clock:           time.Now,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("RunCleanupSweep error: %v", err)
		}

		if report.Summary[TargetScratchBranch] != 1 {
			t.Errorf("expected 1 scratch branch swept, got %d", report.Summary[TargetScratchBranch])
		}
		if len(mock.DeletedBranches) != 1 || mock.DeletedBranches[0] != "blind/test-1" {
			t.Errorf("expected blind/test-1 to be deleted, got %v", mock.DeletedBranches)
		}
	})

	// Scenario 2: Existing --scratch behavior unchanged
	// When Targets is nil/empty and ScratchEnabled is true -> stale scratch branch IS swept.
	t.Run("existing --scratch flag sweeps when targets default", func(t *testing.T) {
		mock := &MockCleanupGitRunner{
			LocalBranchesRes:      []string{"blind/test-1"},
			BranchTipSHARes:       "abc123def4567890",
			BranchTipTimeRes:      staleTime,
			BranchesContainingRes: []string{"blind/test-1", "main"},
		}

		cfg := CleanupConfig{
			RepoDir:         "/repo",
			GitRunner:       mock,
			Targets:         nil, // default all targets
			ScratchEnabled:  true,
			ScratchPatterns: []string{"blind/*"},
			Clock:           time.Now,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("RunCleanupSweep error: %v", err)
		}

		if report.Summary[TargetScratchBranch] != 1 {
			t.Errorf("expected 1 scratch branch swept, got %d", report.Summary[TargetScratchBranch])
		}
		if len(mock.DeletedBranches) != 1 || mock.DeletedBranches[0] != "blind/test-1" {
			t.Errorf("expected blind/test-1 to be deleted, got %v", mock.DeletedBranches)
		}
	})

	// Scenario 3: When ScratchEnabled is false and no scratch-branch target -> NOT swept.
	t.Run("no scratch sweep when default targets and scratch flag false", func(t *testing.T) {
		mock := &MockCleanupGitRunner{
			LocalBranchesRes:      []string{"blind/test-1"},
			BranchTipSHARes:       "abc123def4567890",
			BranchTipTimeRes:      staleTime,
			BranchesContainingRes: []string{"blind/test-1", "main"},
		}

		cfg := CleanupConfig{
			RepoDir:         "/repo",
			GitRunner:       mock,
			Targets:         nil, // default all targets
			ScratchEnabled:  false,
			ScratchPatterns: []string{"blind/*"},
			Clock:           time.Now,
		}

		report, err := RunCleanupSweep(context.Background(), cfg)
		if err != nil {
			t.Fatalf("RunCleanupSweep error: %v", err)
		}

		if count := report.Summary[TargetScratchBranch]; count != 0 {
			t.Errorf("expected 0 scratch branch swept, got %d", count)
		}
		if len(mock.DeletedBranches) != 0 {
			t.Errorf("expected 0 branches deleted, got %v", mock.DeletedBranches)
		}
	})
}
