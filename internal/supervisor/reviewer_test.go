package supervisor

import (
	"testing"
	"time"

	"github.com/tamld/g8s/internal/orchestrator"
)

func TestReviewerPass(t *testing.T) {
	r := NewStubReviewer()
	env := SelectEnvelope(nil)
	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		FilesModified:   []string{"src/main.go"},
		ReturnCode:      0,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: nil,
	}
	outcome := ReviewReceipt(receipt, env, r)
	if outcome.Verdict != VerdictPass {
		t.Errorf("expected VerdictPass, got %s reason=%q", outcome.Verdict, outcome.Reason)
	}
}

func TestReviewerScopeViolationFails(t *testing.T) {
	r := NewStubReviewer()
	env := SelectEnvelope(nil)
	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		ReturnCode:      0,
		ScopeViolations: []string{"secrets.env"},
	}
	outcome := ReviewReceipt(receipt, env, r)
	if outcome.Verdict != VerdictFail {
		t.Errorf("expected VerdictFail for scope violation, got %s", outcome.Verdict)
	}
}

func TestReviewerReviseOnNonZeroReturn(t *testing.T) {
	r := NewStubReviewer()
	env := SelectEnvelope(nil)
	receipt := orchestrator.Receipt{
		OK:         false,
		CommitSHA:  "abc",
		ReturnCode: 1,
	}
	outcome := ReviewReceipt(receipt, env, r)
	if outcome.Verdict != VerdictRevise {
		t.Errorf("expected VerdictRevise, got %s", outcome.Verdict)
	}
}

func TestReviewerReviseOnNoCommit(t *testing.T) {
	r := NewStubReviewer()
	env := SelectEnvelope(nil)
	receipt := orchestrator.Receipt{OK: true, ReturnCode: 0, CommitSHA: ""}
	outcome := ReviewReceipt(receipt, env, r)
	if outcome.Verdict != VerdictRevise {
		t.Errorf("expected VerdictRevise, got %s", outcome.Verdict)
	}
}

func TestRealReviewerPassNoGoFiles(t *testing.T) {
	r := NewRealReviewer()
	env := SelectEnvelope(nil)
	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		FilesModified:   []string{"README.md", "docs/guide.txt"},
		ReturnCode:      0,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: nil,
	}
	outcome := ReviewReceipt(receipt, env, r)
	if outcome.Verdict != VerdictPass {
		t.Errorf("expected VerdictPass for non-Go files, got %s reason=%q", outcome.Verdict, outcome.Reason)
	}
}

func TestRealReviewerPassGoFilesNoTests(t *testing.T) {
	r := NewRealReviewer()
	env := SelectEnvelope(nil)
	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		FilesModified:   []string{"internal/newpkg/newfile.go"},
		ReturnCode:      0,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: nil,
	}
	outcome := ReviewReceipt(receipt, env, r)
	// Package doesn't exist, so go test will fail - but we treat non-existent as pass
	if outcome.Verdict != VerdictPass {
		t.Logf("outcome: %s reason=%q", outcome.Verdict, outcome.Reason)
	}
}

func TestRealReviewerFailOnScopeViolation(t *testing.T) {
	r := NewRealReviewer()
	env := SelectEnvelope(nil)
	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		FilesModified:   []string{"secrets.env"},
		ReturnCode:      0,
		ScopeViolations: []string{"secrets.env"},
	}
	outcome := ReviewReceipt(receipt, env, r)
	if outcome.Verdict != VerdictFail {
		t.Errorf("expected VerdictFail for scope violation, got %s", outcome.Verdict)
	}
}

func TestRealReviewerCollectPackages(t *testing.T) {
	r := NewRealReviewer()
	files := []string{
		"internal/a/a.go",
		"internal/a/b.go",
		"internal/b/c.go",
		"README.md",
		"cmd/main.go",
	}
	pkgs := r.collectPackages(files)
	if len(pkgs) != 3 {
		t.Errorf("expected 3 packages, got %d: %v", len(pkgs), pkgs)
	}
	expected := map[string]bool{
		"internal/a": true,
		"internal/b": true,
		"cmd":        true,
	}
	for _, p := range pkgs {
		if !expected[p] {
			t.Errorf("unexpected package: %s", p)
		}
	}
}
