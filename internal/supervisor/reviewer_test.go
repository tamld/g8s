package supervisor

import (
	"os"
	"path/filepath"
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

	tmpDir := t.TempDir()
	// Create a minimal go.mod
	goMod := `module test

go 1.23
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	// Create a Go file with no tests
	goFile := `package test

func Hello() string {
	return "hello"
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(goFile), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		FilesModified:   []string{"main.go"},
		ReturnCode:      0,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: nil,
		WorktreePath:    tmpDir,
	}
	outcome := ReviewReceipt(receipt, env, r)
	if outcome.Verdict != VerdictPass {
		t.Fatalf("expected VerdictPass, got %s reason=%q", outcome.Verdict, outcome.Reason)
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
		filepath.Join("internal", "a", "a.go"),
		filepath.Join("internal", "a", "b.go"),
		filepath.Join("internal", "b", "c.go"),
		"README.md",
		filepath.Join("cmd", "main.go"),
	}
	pkgs := r.collectPackages(files)
	if len(pkgs) != 3 {
		t.Errorf("expected 3 packages, got %d: %v", len(pkgs), pkgs)
	}
	expected := map[string]bool{
		filepath.Join("internal", "a"): true,
		filepath.Join("internal", "b"): true,
		"cmd":                          true,
	}
	for _, p := range pkgs {
		if !expected[p] {
			t.Errorf("unexpected package: %s", p)
		}
	}
}

func TestRealReviewerReviseOnFailingGoTest(t *testing.T) {
	r := NewRealReviewer()

	tmpDir := t.TempDir()
	// Create a minimal go.mod
	goMod := `module test

go 1.23
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	// Create pkg/foo/foo.go
	if err := os.MkdirAll(filepath.Join(tmpDir, "pkg", "foo"), 0o755); err != nil {
		t.Fatalf("mkdir pkg/foo: %v", err)
	}
	goFile := `package foo

func Add(a, b int) int {
	return a + b
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "pkg", "foo", "foo.go"), []byte(goFile), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	// Create pkg/foo/foo_test.go with a failing test
	testFile := `package foo

import "testing"

func TestFail(t *testing.T) {
	t.Fatal("intentional failure")
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "pkg", "foo", "foo_test.go"), []byte(testFile), 0o644); err != nil {
		t.Fatalf("write foo_test.go: %v", err)
	}

	// Test with non-empty SelectedFields
	envWithFields := SelectEnvelope(map[string]bool{"unit_test": true})
	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		FilesModified:   []string{"pkg/foo/foo.go"},
		ReturnCode:      0,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: nil,
		WorktreePath:    tmpDir,
	}
	outcome := ReviewReceipt(receipt, envWithFields, r)
	if outcome.Verdict != VerdictRevise {
		t.Fatalf("expected VerdictRevise with SelectedFields, got %s reason=%q", outcome.Verdict, outcome.Reason)
	}

	// Test with empty SelectedFields
	envEmpty := SelectEnvelope(nil)
	receipt2 := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		FilesModified:   []string{"pkg/foo/foo.go"},
		ReturnCode:      0,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: nil,
		WorktreePath:    tmpDir,
	}
	outcome2 := ReviewReceipt(receipt2, envEmpty, r)
	if outcome2.Verdict != VerdictRevise {
		t.Fatalf("expected VerdictRevise with empty SelectedFields, got %s reason=%q", outcome2.Verdict, outcome2.Reason)
	}
}

type customFailingReviewer struct{}

func (f customFailingReviewer) Validate(env TaskEnvelope, receipt orchestrator.Receipt) (map[string]bool, error) {
	return map[string]bool{"unit_test": false}, nil
}

func TestReviewReceiptReviseWhenCustomReviewerReturnsFalse(t *testing.T) {
	env := SelectEnvelope(map[string]bool{"unit_test": true})
	receipt := orchestrator.Receipt{
		OK:              true,
		CommitSHA:       "abc123",
		FilesModified:   []string{"main.go"},
		ReturnCode:      0,
		StartedAt:       time.Now(),
		FinishedAt:      time.Now(),
		ScopeViolations: nil,
	}
	outcome := ReviewReceipt(receipt, env, customFailingReviewer{})
	if outcome.Verdict != VerdictRevise {
		t.Fatalf("expected VerdictRevise, got %s reason=%q", outcome.Verdict, outcome.Reason)
	}
	if outcome.Validated["unit_test"] != false {
		t.Fatalf("expected unit_test=false in validated, got %v", outcome.Validated)
	}
}
