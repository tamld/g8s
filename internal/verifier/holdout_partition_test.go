package verifier

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/sandbox"
)

func TestValidateHoldoutPartition_RejectsWorktreeOverlapAndSymlinks(t *testing.T) {
	wt := t.TempDir()
	insideHoldout := filepath.Join(wt, "holdout")
	if err := os.MkdirAll(insideHoldout, 0o700); err != nil {
		t.Fatal(err)
	}
	err := ValidateHoldoutPartition(wt, insideHoldout)
	if err == nil || !strings.Contains(err.Error(), CodeHoldoutNotOutOfWorktree) {
		t.Fatalf("expected %s for holdout inside worktree, got %v", CodeHoldoutNotOutOfWorktree, err)
	}

	// Worktree inside holdout must also fail.
	outerHoldout := t.TempDir()
	nestedWT := filepath.Join(outerHoldout, "wt")
	if err := os.MkdirAll(nestedWT, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHoldoutPartition(nestedWT, outerHoldout); err == nil || !strings.Contains(err.Error(), CodeHoldoutNotOutOfWorktree) {
		t.Fatalf("expected %s when worktree is inside holdout, got %v", CodeHoldoutNotOutOfWorktree, err)
	}

	// Symlink inside worktree pointing out of worktree must resolve & match or stay checked safely;
	// specifically a symlink outside pointing BACK inside worktree must be caught!
	if runtime.GOOS != "windows" {
		linkOutside := filepath.Join(t.TempDir(), "holdout-link")
		if sErr := os.Symlink(insideHoldout, linkOutside); sErr != nil {
			t.Fatalf("symlink: %v", sErr)
		}
		if err := ValidateHoldoutPartition(wt, linkOutside); err == nil || !strings.Contains(err.Error(), CodeHoldoutNotOutOfWorktree) {
			t.Fatalf("expected %s via symlinked holdout into worktree, got %v", CodeHoldoutNotOutOfWorktree, err)
		}
	}

	cleanHoldout := t.TempDir()
	if err := ValidateHoldoutPartition(wt, cleanHoldout); err != nil {
		t.Fatalf("expected disjoint holdout to pass, got %v", err)
	}
}

func TestVerify_OutOfWorktreeHoldoutDigestAndTamperVeto(t *testing.T) {
	const classYAML = `
classes:
  - name: core-go
    status: hard
    founding_catch: "#943 holdout verification test"
    priority: 10
    paths: ["pkg/**"]
    checks:
      - type: script
        run: "pass.sh"
`
	writePassScript := func(t *testing.T, wt string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(wt, "pass.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("rejects_holdout_inside_candidate_worktree", func(t *testing.T) {
		wt := t.TempDir()
		writePassScript(t, wt)
		contaminated := filepath.Join(wt, "holdout")
		if err := os.MkdirAll(contaminated, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(contaminated, "check.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		calledHoldout := false
		v := NewVerifier(
			WithRegistryData([]byte(classYAML)),
			WithRepoRoot(wt),
			WithCommandRunner(func(_ context.Context, name string, args ...string) ([]byte, []byte, int, error) {
				if strings.Contains(name, "holdout") || strings.Contains(strings.Join(args, " "), "holdout") {
					calledHoldout = true
				}
				return nil, nil, 0, nil
			}),
		)
		verdict, err := v.Verify(TaskRef{
			ID:           "t-holdout-contam",
			ReceiptID:    "r-1",
			AllowedPaths: []string{"pkg/a.go"},
			WorktreeDir:  wt,
			HoldoutDir:   contaminated,
		}, TaskRef{})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if verdict.Outcome != OutcomeFail || (verdict.HoldoutChecksOK != nil && *verdict.HoldoutChecksOK) {
			t.Fatalf("expected OutcomeFail and HoldoutChecksOK=false, got %+v", verdict)
		}
		if !strings.Contains(verdict.VetoReason, CodeHoldoutNotOutOfWorktree) {
			t.Fatalf("expected VetoReason containing %s, got %q", CodeHoldoutNotOutOfWorktree, verdict.VetoReason)
		}
		if calledHoldout {
			t.Fatal("contaminated holdout script must not execute")
		}
	})

	t.Run("valid_disjoint_holdout_records_sha256_digest_and_detects_in_eval_mutation", func(t *testing.T) {
		wt := t.TempDir()
		writePassScript(t, wt)
		holdoutDir := t.TempDir()
		scriptName := "check.sh"
		scriptPath := filepath.Join(holdoutDir, scriptName)
		if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		expectedDigest, err := HashHoldoutPartition(holdoutDir)
		if err != nil {
			t.Fatalf("HashHoldoutPartition: %v", err)
		}

		v := NewVerifier(
			WithRegistryData([]byte(classYAML)),
			WithRepoRoot(wt),
			WithCommandRunner(func(_ context.Context, _ string, _ ...string) ([]byte, []byte, int, error) {
				return []byte("ok\n"), nil, 0, nil
			}),
		)
		verdict, err := v.Verify(TaskRef{
			ID:           "t-holdout-ok",
			ReceiptID:    "r-2",
			AllowedPaths: []string{"pkg/a.go"},
			WorktreeDir:  wt,
			HoldoutDir:   holdoutDir,
		}, TaskRef{})
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if verdict.Outcome != OutcomePass || verdict.HoldoutChecksOK == nil || !*verdict.HoldoutChecksOK {
			t.Fatalf("expected OutcomePass + HoldoutChecksOK=true, got %+v", verdict)
		}
		if verdict.HoldoutDigest != expectedDigest || !strings.HasPrefix(verdict.HoldoutDigest, "sha256:") {
			t.Fatalf("HoldoutDigest = %q, want %q", verdict.HoldoutDigest, expectedDigest)
		}
		if verdict.EpistemicClass != sandbox.EpistemicMeasuredMetric {
			t.Fatalf("EpistemicClass = %q, want %q", verdict.EpistemicClass, sandbox.EpistemicMeasuredMetric)
		}

		// Now simulate holdout tampering during evaluation.
		vMutated := NewVerifier(
			WithRegistryData([]byte(classYAML)),
			WithRepoRoot(wt),
			WithCommandRunner(func(_ context.Context, _ string, _ ...string) ([]byte, []byte, int, error) {
				if err := os.WriteFile(filepath.Join(holdoutDir, "tamper.txt"), []byte("cheated\n"), 0o600); err != nil {
					return nil, nil, 1, err
				}
				return []byte("ok\n"), nil, 0, nil
			}),
		)
		tamperedVerdict, err := vMutated.Verify(TaskRef{
			ID:           "t-holdout-tamper",
			ReceiptID:    "r-3",
			AllowedPaths: []string{"pkg/a.go"},
			WorktreeDir:  wt,
			HoldoutDir:   holdoutDir,
		}, TaskRef{})
		if err != nil {
			t.Fatalf("Verify tampered: %v", err)
		}
		if tamperedVerdict.Outcome != OutcomeFail || (tamperedVerdict.HoldoutChecksOK != nil && *tamperedVerdict.HoldoutChecksOK) {
			t.Fatalf("expected OutcomeFail + HoldoutChecksOK=false on holdout mutation, got %+v", tamperedVerdict)
		}
		if !strings.Contains(tamperedVerdict.VetoReason, CodeHoldoutMutated) {
			t.Fatalf("expected %s veto reason, got %q", CodeHoldoutMutated, tamperedVerdict.VetoReason)
		}
	})
}
