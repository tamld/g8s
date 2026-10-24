package verifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoDMechanicalBinding_PositiveCheckBinding verifies that a declared criterion
// binds to an authentic, passing check record generated during this verification invocation (#648).
func TestDoDMechanicalBinding_PositiveCheckBinding(t *testing.T) {
	items := []BriefDoDItem{
		{ID: "D1", Description: "Verifier unit tests pass", VerifierHint: "gotest:./internal/verifier"},
	}

	trustedEv := TrustedEvidence{
		TaskID:      "task-positive-01",
		Attempt:     "task-positive-01-1",
		HeadSHA:     "1234567890abcdef1234567890abcdef12345678",
		TreeSHA:     "abcdef1234567890abcdef1234567890abcdef12",
		RawLogSHA:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		WorktreeDir: t.TempDir(),
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
		},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, nil, true, trustedEv)
	if !ok {
		t.Fatalf("expected positive binding to succeed, got ok=false, matrix=%+v", matrix)
	}
	if len(matrix) != 1 {
		t.Fatalf("expected 1 item, got %d", len(matrix))
	}
	item := matrix[0]
	if item.ID != "D1" || item.Status != "MET" {
		t.Fatalf("expected D1 to be MET, got %+v", item)
	}
	if !strings.Contains(item.Evidence, "bound to check[go-test:./internal/verifier] ok=true") {
		t.Errorf("evidence missing check binding: %s", item.Evidence)
	}
	if !strings.Contains(item.Evidence, "task=task-positive-01") {
		t.Errorf("evidence missing task ID: %s", item.Evidence)
	}
	if !strings.Contains(item.Evidence, "attempt=task-positive-01-1") {
		t.Errorf("evidence missing attempt: %s", item.Evidence)
	}
	if !strings.Contains(item.Evidence, "head=1234567890abcdef1234567890abcdef12345678") {
		t.Errorf("evidence missing head SHA: %s", item.Evidence)
	}
	if !strings.Contains(item.Evidence, "log_sha256=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef") {
		t.Errorf("evidence missing log SHA: %s", item.Evidence)
	}
}

// TestDoDMechanicalBinding_PositiveTwoDistinctChecks verifies that two criteria with distinct
// exact checks both pass when both checks execute successfully, and that unrelated check success
// cannot satisfy another unexecuted criterion (#648).
func TestDoDMechanicalBinding_PositiveTwoDistinctChecks(t *testing.T) {
	items := []BriefDoDItem{
		{ID: "D1", Description: "Verifier unit tests pass", VerifierHint: "gotest:./internal/verifier"},
		{ID: "D2", Description: "CLI runner tests pass", VerifierHint: "gotest:./cmd/g8s"},
	}

	// Case 1: Both distinct checks execute and pass
	trustedEvBoth := TrustedEvidence{
		TaskID:    "task-pos-both",
		RawLogSHA: "logsha123456",
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
			{Type: "go-test", Target: "./cmd/g8s", OK: true, Detail: "ok"},
		},
	}
	matrixBoth, okBoth := EvaluateDoDMatrixWithEvidence(items, nil, true, trustedEvBoth)
	if !okBoth {
		t.Fatalf("expected both criteria to pass, got ok=false, matrix=%+v", matrixBoth)
	}
	if len(matrixBoth) != 2 || matrixBoth[0].Status != "MET" || matrixBoth[1].Status != "MET" {
		t.Fatalf("expected D1 and D2 MET, got %+v", matrixBoth)
	}
	if !strings.Contains(matrixBoth[0].Evidence, "./internal/verifier") || !strings.Contains(matrixBoth[1].Evidence, "./cmd/g8s") {
		t.Errorf("expected distinct check bindings, got %+v", matrixBoth)
	}

	// Case 2: Only D1 check executes; unrelated success cannot satisfy D2
	trustedEvOnlyD1 := TrustedEvidence{
		TaskID:    "task-pos-only-d1",
		RawLogSHA: "logsha123456",
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
		},
	}
	matrixPartial, okPartial := EvaluateDoDMatrixWithEvidence(items, nil, true, trustedEvOnlyD1)
	if okPartial {
		t.Fatalf("expected overall failure when D2 check missing, got ok=true")
	}
	if len(matrixPartial) != 2 {
		t.Fatalf("expected 2 matrix items, got %d", len(matrixPartial))
	}
	if matrixPartial[0].Status != "MET" {
		t.Errorf("expected D1 MET, got %+v", matrixPartial[0])
	}
	if matrixPartial[1].Status != "UNMET" {
		t.Errorf("expected D2 UNMET due to missing check, got %+v", matrixPartial[1])
	}
}

// TestDoDMechanicalBinding_PositiveArtifactSupportingEvidence verifies that an artifact specified
// in a criterion is validated directly on disk with authentic SHA-256 as supporting evidence
// alongside an exact matching check (#648).
func TestDoDMechanicalBinding_PositiveArtifactSupportingEvidence(t *testing.T) {
	tmpDir := t.TempDir()
	relPath := "docs/deliverable.json"
	fullPath := filepath.Join(tmpDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"version": 1, "approved": true}`)
	if err := os.WriteFile(fullPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	expectedSHA := hex.EncodeToString(h[:])

	// Criterion declares both an exact check and the supporting artifact
	items := []BriefDoDItem{
		{ID: "D1", Description: "Deliverable verified", VerifierHint: "gotest:./internal/verifier, file:" + relPath},
	}

	trustedEv := TrustedEvidence{
		TaskID:      "task-artifact-pos",
		WorktreeDir: tmpDir,
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
		},
	}

	reported := []DoDItemStatus{
		{ID: "D1", Status: "MET", Evidence: "sha256:" + expectedSHA},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, reported, true, trustedEv)
	if !ok {
		t.Fatalf("expected positive artifact verification to pass, got ok=false, matrix=%+v", matrix)
	}
	if len(matrix) != 1 || matrix[0].Status != "MET" {
		t.Fatalf("expected D1 MET, got %+v", matrix)
	}
	if !strings.Contains(matrix[0].Evidence, "bound to check[go-test:./internal/verifier] ok=true") {
		t.Errorf("expected bound check in evidence, got %s", matrix[0].Evidence)
	}
	if !strings.Contains(matrix[0].Evidence, "(verified on disk)") {
		t.Errorf("expected verified on disk in evidence, got %s", matrix[0].Evidence)
	}
	if !strings.Contains(matrix[0].Evidence, expectedSHA) {
		t.Errorf("expected disk SHA in evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_MultipleRequiredHintsAllMustPass verifies that multiple required
// hints in a criterion must all have matching passing checks (AND conjunction, not OR) (#648).
func TestDoDMechanicalBinding_MultipleRequiredHintsAllMustPass(t *testing.T) {
	items := []BriefDoDItem{
		{ID: "D1", Description: "Dual component tests", VerifierHint: "gotest:./pkg/one, gotest:./pkg/two"},
	}

	// Subtest 1: Only first check executed -> D1 UNMET
	evOnlyOne := TrustedEvidence{
		TaskID:    "t-multi-1",
		RawLogSHA: "sha1",
		Checks: []CheckResult{
			{Type: "go-test", Target: "./pkg/one", OK: true, Detail: "ok"},
		},
	}
	m1, ok1 := EvaluateDoDMatrixWithEvidence(items, nil, true, evOnlyOne)
	if ok1 || len(m1) != 1 || m1[0].Status != "UNMET" {
		t.Fatalf("expected D1 UNMET when only 1 of 2 required checks executed, got ok=%v, matrix=%+v", ok1, m1)
	}

	// Subtest 2: Both checks executed and passed -> D1 MET
	evBoth := TrustedEvidence{
		TaskID:    "t-multi-2",
		RawLogSHA: "sha2",
		Checks: []CheckResult{
			{Type: "go-test", Target: "./pkg/one", OK: true, Detail: "ok"},
			{Type: "go-test", Target: "./pkg/two", OK: true, Detail: "ok"},
		},
	}
	m2, ok2 := EvaluateDoDMatrixWithEvidence(items, nil, true, evBoth)
	if !ok2 || len(m2) != 1 || m2[0].Status != "MET" {
		t.Fatalf("expected D1 MET when both required checks executed, got ok=%v, matrix=%+v", ok2, m2)
	}
	if !strings.Contains(m2[0].Evidence, "./pkg/one") || !strings.Contains(m2[0].Evidence, "./pkg/two") {
		t.Errorf("expected evidence to cite both checks, got %s", m2[0].Evidence)
	}
}

// TestDoDMechanicalBinding_Negative_ExternalAbsolute verifies that an external absolute
// path in artifact hint is strictly rejected (#648).
func TestDoDMechanicalBinding_Negative_ExternalAbsolute(t *testing.T) {
	tmpDir := t.TempDir()
	items := []BriefDoDItem{
		{ID: "D1", Description: "Absolute file escape", VerifierHint: "gotest:./internal/verifier, file:/etc/passwd"},
	}

	trustedEv := TrustedEvidence{
		TaskID:      "t-neg-abs",
		WorktreeDir: tmpDir,
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
		},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, nil, true, trustedEv)
	if ok {
		t.Fatalf("expected absolute artifact path to be rejected, got ok=true")
	}
	if len(matrix) != 1 || matrix[0].Status != "UNMET" {
		t.Fatalf("expected D1 UNMET, got %+v", matrix)
	}
	if !strings.Contains(matrix[0].Evidence, "absolute paths outside trusted root are forbidden") {
		t.Errorf("expected absolute path rejection evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_Negative_Traversal verifies that directory traversal in an artifact
// hint (e.g. file:../outside.md) is strictly rejected (#648).
func TestDoDMechanicalBinding_Negative_Traversal(t *testing.T) {
	parentDir := t.TempDir()
	wtDir := filepath.Join(parentDir, "worktree")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create outside file in parentDir
	outsidePath := filepath.Join(parentDir, "outside.md")
	if err := os.WriteFile(outsidePath, []byte("outside content"), 0o644); err != nil {
		t.Fatal(err)
	}

	items := []BriefDoDItem{
		{ID: "D1", Description: "Traversal escape", VerifierHint: "gotest:./internal/verifier, file:../outside.md"},
	}

	trustedEv := TrustedEvidence{
		TaskID:      "t-neg-traversal",
		WorktreeDir: wtDir,
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
		},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, nil, true, trustedEv)
	if ok {
		t.Fatalf("expected traversal path to be rejected, got ok=true")
	}
	if len(matrix) != 1 || matrix[0].Status != "UNMET" {
		t.Fatalf("expected D1 UNMET, got %+v", matrix)
	}
	if !strings.Contains(matrix[0].Evidence, "directory traversal outside trusted root is forbidden") {
		t.Errorf("expected traversal rejection evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_Negative_SymlinkEscape verifies that an artifact symlink pointing
// outside the trusted root is strictly rejected (#648).
func TestDoDMechanicalBinding_Negative_SymlinkEscape(t *testing.T) {
	parentDir := t.TempDir()
	wtDir := filepath.Join(parentDir, "worktree")
	outsideDir := filepath.Join(parentDir, "outside")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	secretFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("secret payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create symlink inside wtDir pointing outside
	symlinkPath := filepath.Join(wtDir, "escape.txt")
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		t.Fatal(err)
	}

	items := []BriefDoDItem{
		{ID: "D1", Description: "Symlink escape test", VerifierHint: "gotest:./internal/verifier, file:escape.txt"},
	}

	trustedEv := TrustedEvidence{
		TaskID:      "t-neg-symlink",
		WorktreeDir: wtDir,
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
		},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, nil, true, trustedEv)
	if ok {
		t.Fatalf("expected symlink escape to be rejected, got ok=true")
	}
	if len(matrix) != 1 || matrix[0].Status != "UNMET" {
		t.Fatalf("expected D1 UNMET, got %+v", matrix)
	}
	if !strings.Contains(matrix[0].Evidence, "escapes trusted root") {
		t.Errorf("expected symlink escape evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_Negative_ArbitraryFileEvenWithCorrectHash verifies that an arbitrary
// file existing on disk with a correct hash alone CANNOT substitute a declared matching check (#648).
func TestDoDMechanicalBinding_Negative_ArbitraryFileEvenWithCorrectHash(t *testing.T) {
	tmpDir := t.TempDir()
	relPath := "docs/schema.json"
	fullPath := filepath.Join(tmpDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"version": 1}`)
	if err := os.WriteFile(fullPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	correctHash := hex.EncodeToString(h[:])

	// Criterion has only artifact hint, NO check hint
	items := []BriefDoDItem{
		{ID: "D1", Description: "Schema check", VerifierHint: "file:" + relPath},
	}

	// An unrelated check passed
	trustedEv := TrustedEvidence{
		TaskID:      "t-neg-arbitrary-file",
		WorktreeDir: tmpDir,
		Checks: []CheckResult{
			{Type: "go-test", Target: "./other/pkg", OK: true, Detail: "ok"},
		},
	}

	reported := []DoDItemStatus{
		{ID: "D1", Status: "MET", Evidence: "sha256:" + correctHash},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, reported, true, trustedEv)
	if ok {
		t.Fatalf("expected arbitrary file without matching check to fail, got ok=true")
	}
	if len(matrix) != 1 || matrix[0].Status != "UNMET" {
		t.Fatalf("expected D1 UNMET, got %+v", matrix)
	}
	if !strings.Contains(matrix[0].Evidence, "artifact existence alone cannot substitute declared matching check") {
		t.Errorf("expected check requirement evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_Negative_SubstringTargetCollision verifies that a check targeting
// ./pkg/security-old does NOT satisfy a criterion requiring ./pkg/security (#648).
func TestDoDMechanicalBinding_Negative_SubstringTargetCollision(t *testing.T) {
	items := []BriefDoDItem{
		{ID: "D1", Description: "Security subsystem", VerifierHint: "gotest:./pkg/security"},
	}

	// Only ./pkg/security-old check executed
	trustedEv := TrustedEvidence{
		TaskID: "t-neg-collision",
		Checks: []CheckResult{
			{Type: "go-test", Target: "./pkg/security-old", OK: true, Detail: "ok"},
		},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, nil, true, trustedEv)
	if ok {
		t.Fatalf("expected substring target collision to fail, got ok=true")
	}
	if len(matrix) != 1 || matrix[0].Status != "UNMET" {
		t.Fatalf("expected D1 UNMET, got %+v", matrix)
	}
	if !strings.Contains(matrix[0].Evidence, "no executed check matches required hint") {
		t.Errorf("expected no matching check evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_Negative_TypeOnlyHint verifies that type-only hints (e.g. gotest,
// script) without an explicit exact target are rejected (#648).
func TestDoDMechanicalBinding_Negative_TypeOnlyHint(t *testing.T) {
	typeOnlyHints := []string{"gotest", "gotest:", "go-test", "script", "diffguard", "canary"}

	for _, hint := range typeOnlyHints {
		items := []BriefDoDItem{
			{ID: "D1", Description: "Type only criterion", VerifierHint: hint},
		}

		trustedEv := TrustedEvidence{
			TaskID: "t-neg-typeonly",
			Checks: []CheckResult{
				{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
				{Type: "script", Target: "tools/check.sh", OK: true, Detail: "ok"},
			},
		}

		matrix, ok := EvaluateDoDMatrixWithEvidence(items, nil, true, trustedEv)
		if ok {
			t.Errorf("expected type-only hint %q to fail, got ok=true", hint)
		}
		if len(matrix) != 1 || matrix[0].Status != "UNMET" {
			t.Errorf("expected D1 UNMET for hint %q, got %+v", hint, matrix)
		}
		if !strings.Contains(matrix[0].Evidence, "type-only hint") {
			t.Errorf("expected type-only hint rejection evidence, got %s", matrix[0].Evidence)
		}
	}
}

// TestDoDMechanicalBinding_Negative_MismatchedStaleProvenance verifies that stale or
// mismatched hashes reported for an artifact are rejected (#648).
func TestDoDMechanicalBinding_Negative_MismatchedStaleProvenance(t *testing.T) {
	tmpDir := t.TempDir()
	relPath := "docs/schema.json"
	fullPath := filepath.Join(tmpDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(`{"version": 2}`), 0o644); err != nil {
		t.Fatal(err)
	}

	items := []BriefDoDItem{
		{ID: "D1", Description: "Schema check", VerifierHint: "gotest:./internal/verifier, file:" + relPath},
	}

	trustedEv := TrustedEvidence{
		TaskID:      "t-neg-mismatch",
		WorktreeDir: tmpDir,
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
		},
	}

	// Worker reports stale zero hash
	reported := []DoDItemStatus{
		{ID: "D1", Status: "MET", Evidence: "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, reported, true, trustedEv)
	if ok {
		t.Fatalf("expected mismatched hash to fail, got ok=true")
	}
	if len(matrix) != 1 || matrix[0].Status != "UNMET" {
		t.Fatalf("expected D1 UNMET for mismatched hash, got %+v", matrix)
	}
	if !strings.Contains(matrix[0].Evidence, "hash mismatch") {
		t.Errorf("expected hash mismatch evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_Negative_ChecksOKFalse verifies that checksOK=false unconditionally
// rejects all DoD items, even if individual checks succeeded or worker claimed MET (#648).
func TestDoDMechanicalBinding_Negative_ChecksOKFalse(t *testing.T) {
	items := []BriefDoDItem{
		{ID: "D1", Description: "Pass tests", VerifierHint: "gotest:./internal/verifier"},
	}

	trustedEv := TrustedEvidence{
		TaskID: "task-fail-01",
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/verifier", OK: true, Detail: "ok"},
		},
	}

	reported := []DoDItemStatus{
		{ID: "D1", Status: "MET", Evidence: "bound to check[gotest:./internal/verifier] ok=true"},
	}

	// Aggregate checks failed (e.g. holdout failed or canary survived)
	matrix, ok := EvaluateDoDMatrixWithEvidence(items, reported, false, trustedEv)
	if ok {
		t.Fatalf("expected checksOK=false to always reject, got ok=true")
	}
	if len(matrix) != 1 {
		t.Fatalf("expected 1 item, got %d", len(matrix))
	}
	if matrix[0].Status != "UNMET" {
		t.Errorf("expected D1 UNMET when checksOK=false, got %+v", matrix[0])
	}
	if !strings.Contains(matrix[0].Evidence, "mechanical checks failed") {
		t.Errorf("expected rejection evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_WorkerEvidenceNotAuthoritative verifies that worker evidence strings
// cannot substitute or override declared BriefDoDItem.VerifierHint (#648).
func TestDoDMechanicalBinding_WorkerEvidenceNotAuthoritative(t *testing.T) {
	items := []BriefDoDItem{
		{ID: "D1", Description: "Security subsystem", VerifierHint: "gotest:./internal/security"},
	}

	// Worker reports evidence claiming check for ./internal/other
	reported := []DoDItemStatus{
		{ID: "D1", Status: "MET", Evidence: "bound to check[gotest:./internal/other] ok=true"},
	}

	// Only ./internal/other executed
	trustedEv := TrustedEvidence{
		TaskID: "t-worker-authority",
		Checks: []CheckResult{
			{Type: "go-test", Target: "./internal/other", OK: true, Detail: "ok"},
		},
	}

	matrix, ok := EvaluateDoDMatrixWithEvidence(items, reported, true, trustedEv)
	if ok {
		t.Fatalf("expected worker evidence to NOT be authoritative, got ok=true")
	}
	if len(matrix) != 1 || matrix[0].Status != "UNMET" {
		t.Fatalf("expected D1 UNMET, got %+v", matrix)
	}
}

// TestDoDMechanicalBinding_RejectsWorkerProseAndBareWords verifies that bare words
// and worker prose are never accepted as proof of DoD criteria (#648).
func TestDoDMechanicalBinding_RejectsWorkerProseAndBareWords(t *testing.T) {
	items := []BriefDoDItem{
		{ID: "D1", Description: "Implement feature", VerifierHint: "gotest:./pkg/target"},
	}

	bannedInputs := []string{
		"ok",
		"pass",
		"not passed",
		"exit code 0",
		"done",
		"completed",
		"all good",
		"tests green",
		"unit test passed",
		"I verified this locally and it works",
	}

	for _, input := range bannedInputs {
		reported := []DoDItemStatus{
			{ID: "D1", Status: "MET", Evidence: input},
		}
		trustedEv := TrustedEvidence{
			TaskID:    "t-bare",
			RawLogSHA: "logsha12345678",
			Checks: []CheckResult{
				{Type: "go-test", Target: "./other/pkg", OK: true, Detail: "ok"},
			},
		}

		matrix, ok := EvaluateDoDMatrixWithEvidence(items, reported, true, trustedEv)
		if ok {
			t.Errorf("expected input %q to be rejected, got ok=true", input)
		}
		if len(matrix) != 1 || matrix[0].Status != "UNMET" {
			t.Errorf("expected D1 UNMET for input %q, got %+v", input, matrix)
		}
	}
}

// TestDoDMechanicalBinding_LegacyNoRecordCallFailsClosed verifies that legacy calls to
// EvaluateDoDMatrix without trusted check records fail closed for explicit DoD (#648).
func TestDoDMechanicalBinding_LegacyNoRecordCallFailsClosed(t *testing.T) {
	items := []BriefDoDItem{
		{ID: "D1", Description: "Explicit criterion"},
	}

	reported := []DoDItemStatus{
		{ID: "D1", Status: "MET", Evidence: "all pass"},
	}

	matrix, ok := EvaluateDoDMatrix(items, reported, true, "some-log-sha")
	if ok {
		t.Fatalf("expected legacy no-record call with explicit DoD to fail closed, got ok=true")
	}
	if len(matrix) != 1 || matrix[0].Status != "UNMET" {
		t.Errorf("expected D1 UNMET, got %+v", matrix)
	}
	if !strings.Contains(matrix[0].Evidence, "legacy call without trusted check records fails closed") {
		t.Errorf("expected legacy fail-closed evidence, got %s", matrix[0].Evidence)
	}
}

// TestDoDMechanicalBinding_AutomatedFallbackWithoutExplicitCriteria verifies that tasks
// without explicit DoD criteria pass if and only if actual mechanical checks pass (#648).
func TestDoDMechanicalBinding_AutomatedFallbackWithoutExplicitCriteria(t *testing.T) {
	// Case 1: len(items) == 0, checksOK == true -> passes
	matrixPass, okPass := EvaluateDoDMatrix(nil, nil, true, "logsha123")
	if !okPass || matrixPass != nil {
		t.Errorf("expected automated fallback to pass with nil matrix, got ok=%v matrix=%+v", okPass, matrixPass)
	}

	// Case 2: len(items) == 0, checksOK == false -> fails
	matrixFail, okFail := EvaluateDoDMatrix(nil, nil, false, "logsha123")
	if okFail {
		t.Errorf("expected automated fallback to fail when checksOK is false, got ok=%v", okFail)
	}
	_ = matrixFail
}

// TestDoDMechanicalBinding_EndToEndVerifyDoDBinding tests complete integration with v.Verify (#648).
func TestDoDMechanicalBinding_EndToEndVerifyDoDBinding(t *testing.T) {
	tmpDir := t.TempDir()
	regYAML := `
classes:
  - name: e2e-dod-class
    status: hard
    founding_catch: "#208"
    priority: 100
    paths:
      - "internal/verifier/**"
    checks:
      - type: go-test
        packages:
          - "./internal/verifier"
`

	mockRunner := func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return []byte("--- PASS: TestVerifier (0.01s)\nPASS\nok  internal/verifier 0.1s\n"), nil, 0, nil
	}

	v := NewVerifier(
		WithRepoRoot(tmpDir),
		WithRegistryData([]byte(regYAML)),
		WithCommandRunner(mockRunner),
	)

	target := TaskRef{
		ID:           "task-e2e-dod-01",
		ReceiptID:    "rcpt-e2e-01",
		AllowedPaths: []string{"internal/verifier/verifier.go"},
		DoDItems: []BriefDoDItem{
			{ID: "D1", Description: "Verifier unit tests pass", VerifierHint: "gotest:./internal/verifier"},
		},
	}

	verdict, err := v.Verify(target, TaskRef{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if verdict.Outcome != OutcomePass {
		t.Fatalf("expected OutcomePass, got %q", verdict.Outcome)
	}
	if len(verdict.DoDMatrix) != 1 {
		t.Fatalf("expected 1 item in DoDMatrix, got %d: %+v", len(verdict.DoDMatrix), verdict.DoDMatrix)
	}
	item := verdict.DoDMatrix[0]
	if item.ID != "D1" || item.Status != "MET" {
		t.Fatalf("expected D1 MET, got %+v", item)
	}
	if !strings.Contains(item.Evidence, "bound to check[go-test:./internal/verifier] ok=true") {
		t.Errorf("evidence missing check binding: %s", item.Evidence)
	}
	if !strings.Contains(item.Evidence, "task=task-e2e-dod-01") {
		t.Errorf("evidence missing task ID: %s", item.Evidence)
	}
}
