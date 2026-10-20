package main

import (
	"path/filepath"
	"testing"
)

func TestClaims_ProseFalsePositives(t *testing.T) {
	tempDir := t.TempDir()
	ledger := OpenLedgerAtPath(filepath.Join(tempDir, "ledger.jsonl"))

	corpus := []string{
		"Fixed-width font is used in tables.",
		"This is a closed-loop system design.",
		"Merged into one list of items.",
		"Fixed a typo in docs/README.md.",
		"Added a quick fix for layout.",
		"Let's discuss the overall roadmap.",
		"The closed issues were reviewed.",
		"Fixed bug in test runner",
		"Resolved lint issue",
	}

	for _, text := range corpus {
		violations, err := LintClaims(text, "gate", tempDir, "head1234", "github", ledger, nil)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", text, err)
		}
		if len(violations) > 0 {
			t.Errorf("false positive for %q: got %d violations: %+v", text, len(violations), violations)
		}
	}
}

func TestClaims_Negation(t *testing.T) {
	tempDir := t.TempDir()
	ledger := OpenLedgerAtPath(filepath.Join(tempDir, "ledger.jsonl"))

	negatedClaims := []string{
		"CI is not green yet",
		"tests chưa pass",
		"build is never green",
		"checks are failing",
		"CI is failing",
		"gate pending",
		"lint không ok",
		"test đỏ",
		"pipeline did not succeed",
	}

	for _, text := range negatedClaims {
		violations, err := LintClaims(text, "gate", tempDir, "head1234", "github", ledger, nil)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", text, err)
		}
		if len(violations) > 0 {
			t.Errorf("negated claim falsely triggered for %q: got %d violations: %+v", text, len(violations), violations)
		}
	}
}

func TestClaims_UncitedAndVietnamese(t *testing.T) {
	tempDir := t.TempDir()
	ledger := OpenLedgerAtPath(filepath.Join(tempDir, "ledger.jsonl"))

	uncited := []string{
		"CI is green",
		"all tests pass",
		"build succeeded",
		"gate is ok",
		"pipeline passed",
		"CI đã xanh",
		"build thành công",
		"test đạt",
	}

	for _, text := range uncited {
		violations, err := LintClaims(text, "gate", tempDir, "head1234", "github", ledger, nil)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", text, err)
		}
		if len(violations) != 1 {
			t.Errorf("expected 1 violation for uncited claim %q, got %d: %+v", text, len(violations), violations)
		}
	}
}

func TestClaims_EvidenceCitations(t *testing.T) {
	tempDir := t.TempDir()
	ledger := OpenLedgerAtPath(filepath.Join(tempDir, "ledger.jsonl"))

	headSHA := "1111222233334444555566667777888899990000"
	otherSHA := "aaaabbbbccccddddeeeeffff0000111122223333"

	// Add valid clean evidence
	_ = ledger.Append(&Entry{
		ID:     "ev-valid001",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "./..."},
	})
	// Add evidence for other SHA
	_ = ledger.Append(&Entry{
		ID:     "ev-other002",
		SHA:    otherSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "./..."},
	})
	// Add dirty evidence
	_ = ledger.Append(&Entry{
		ID:     "ev-dirty003",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  true,
		Cmd:    []string{"go", "test", "./..."},
	})
	// Add failing evidence
	_ = ledger.Append(&Entry{
		ID:     "ev-fail0004",
		SHA:    headSHA,
		Status: "FAIL",
		Dirty:  false,
		Cmd:    []string{"go", "test", "./..."},
	})

	// 1. Valid citation at head
	v1, err := LintClaims("All tests pass with ev-valid001", "gate", tempDir, headSHA, "github", ledger, nil)
	if err != nil || len(v1) != 0 {
		t.Errorf("expected 0 violations for valid citation, got %d (err=%v): %+v", len(v1), err, v1)
	}

	// 2. Citation for other SHA in "gate" mode -> fails
	v2, err := LintClaims("All tests pass with ev-other002", "gate", tempDir, headSHA, "github", ledger, nil)
	if err != nil || len(v2) != 1 {
		t.Errorf("expected 1 violation for other SHA in gate mode, got %d: %+v", len(v2), v2)
	}

	// 3. Citation for other SHA in "report" mode -> passes
	v3, err := LintClaims("All tests pass with ev-other002", "report", tempDir, headSHA, "github", ledger, nil)
	if err != nil || len(v3) != 0 {
		t.Errorf("expected 0 violations for other SHA in report mode, got %d: %+v", len(v3), v3)
	}

	// 4. Citation for dirty evidence in "gate" mode -> fails
	v4, err := LintClaims("All tests pass with ev-dirty003", "gate", tempDir, headSHA, "github", ledger, nil)
	if err != nil || len(v4) != 1 {
		t.Errorf("expected 1 violation for dirty evidence in gate mode, got %d: %+v", len(v4), v4)
	}

	// 5. Citation for failing evidence -> fails
	v5, err := LintClaims("All tests pass with ev-fail0004", "gate", tempDir, headSHA, "github", ledger, nil)
	if err != nil || len(v5) != 1 {
		t.Errorf("expected 1 violation for fail evidence, got %d: %+v", len(v5), v5)
	}
}

func TestClaims_WorkflowRunCitations(t *testing.T) {
	tempDir := t.TempDir()
	ledger := OpenLedgerAtPath(filepath.Join(tempDir, "ledger.jsonl"))

	headSHA := "1111222233334444555566667777888899990000"
	otherSHA := "aaaabbbbccccddddeeeeffff0000111122223333"

	forge := NewFakeForge()
	forge.WorkflowRuns["101"] = FakeRun{HeadSHA: headSHA, Conclusion: "success"}
	forge.WorkflowRuns["102"] = FakeRun{HeadSHA: otherSHA, Conclusion: "success"}
	forge.WorkflowRuns["103"] = FakeRun{HeadSHA: headSHA, Conclusion: "failure"}

	// 1. Valid workflow run
	v1, err := LintClaims("CI is green https://github.com/tamld/g8s/actions/runs/101", "gate", tempDir, headSHA, "github", ledger, forge)
	if err != nil || len(v1) != 0 {
		t.Errorf("expected 0 violations for valid workflow run, got %d: %+v", len(v1), v1)
	}

	// 2. Workflow run on other SHA in gate mode
	v2, err := LintClaims("CI is green actions/runs/102", "gate", tempDir, headSHA, "github", ledger, forge)
	if err != nil || len(v2) != 1 {
		t.Errorf("expected 1 violation for workflow run on other SHA, got %d: %+v", len(v2), v2)
	}

	// 3. Workflow run conclusion failure
	v3, err := LintClaims("CI is green actions/runs/103", "gate", tempDir, headSHA, "github", ledger, forge)
	if err != nil || len(v3) != 1 {
		t.Errorf("expected 1 violation for failed workflow run, got %d: %+v", len(v3), v3)
	}

	// 4. Fake / missing run ID
	v4, err := LintClaims("CI is green actions/runs/999", "gate", tempDir, headSHA, "github", ledger, forge)
	if err != nil || len(v4) != 1 {
		t.Errorf("expected 1 violation for nonexistent run, got %d: %+v", len(v4), v4)
	}

	// 5. ci: none rejects workflow run citations
	v5, err := LintClaims("CI is green actions/runs/101", "gate", tempDir, headSHA, "none", ledger, forge)
	if err != nil || len(v5) != 1 {
		t.Errorf("expected 1 violation when ci: none, got %d: %+v", len(v5), v5)
	}
}

func TestClaims_StatusBlockAndFenced(t *testing.T) {
	tempDir := t.TempDir()
	ledger := OpenLedgerAtPath(filepath.Join(tempDir, "ledger.jsonl"))

	headSHA := "1111222233334444555566667777888899990000"
	_ = ledger.Append(&Entry{
		ID:     "ev-block01",
		SHA:    headSHA,
		Status: "PASS",
		Dirty:  false,
		Cmd:    []string{"go", "test", "./..."},
	})

	// Trusted status block
	validText := `
Here is the PR description:
<!-- f0:status -->
### F0 Verification Status
- CI checks: pass
- Evidence: ev-block01
<!-- /f0:status -->

And some other text.
`
	v1, err := LintClaims(validText, "gate", tempDir, headSHA, "github", ledger, nil)
	if err != nil || len(v1) != 0 {
		t.Errorf("expected 0 violations for valid status block, got %d: %+v", len(v1), v1)
	}

	// Tampered status block with invalid ev- ID
	tamperedText := `
<!-- f0:status -->
- CI check: pass
- Evidence: ev-tampered99
<!-- /f0:status -->
`
	v2, err := LintClaims(tamperedText, "gate", tempDir, headSHA, "github", ledger, nil)
	if err != nil || len(v2) == 0 {
		t.Errorf("expected violations for tampered status block, got %d", len(v2))
	}

	// Fenced code and quoted lines
	ignoredText := `
> In review: CI is green
> We checked that tests pass

` + "```" + `
tests pass
CI is green
` + "```" + `
`
	v3, err := LintClaims(ignoredText, "gate", tempDir, headSHA, "github", ledger, nil)
	if err != nil || len(v3) != 0 {
		t.Errorf("expected 0 violations for fenced code and quotes, got %d: %+v", len(v3), v3)
	}
}
