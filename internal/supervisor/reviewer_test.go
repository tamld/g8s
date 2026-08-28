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
