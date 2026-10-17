package probe

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIASTRuntimeHygieneSensor(t *testing.T) {
	if got := countEntries(""); got != 0 {
		t.Fatalf("countEntries(\"\") = %d, want 0", got)
	}
	if got := countEntries(filepath.Join(t.TempDir(), "nonexistent")); got != 0 {
		t.Fatalf("countEntries(nonexistent) = %d, want 0", got)
	}

	t.Setenv("G8S_TIER", "F1-BASELINE")
	tmpDir := t.TempDir()
	sensor := CaptureRuntimeSensor(tmpDir)

	if err := sensor.Verify(8); err != nil {
		t.Fatalf("clean baseline sensor verify failed: %v", err)
	}

	// Goroutine delta exceeding negative threshold must trigger goroutine leak error after polling
	if err := sensor.Verify(-1000); err == nil || !strings.Contains(err.Error(), "goroutine leak") {
		t.Fatalf("expected sensor to detect goroutine leak for impossible threshold, got %v", err)
	}

	// Leaked file inside tmpDir must be caught by Verify
	leakedFile := filepath.Join(tmpDir, "leaked.tmp")
	if err := os.WriteFile(leakedFile, []byte("leak"), 0o600); err != nil {
		t.Fatalf("write leaked file: %v", err)
	}
	if err := sensor.Verify(8); err == nil || !strings.Contains(err.Error(), "leaked") {
		t.Fatalf("expected sensor to detect leaked temp file, got %v", err)
	}
	if err := os.Remove(leakedFile); err != nil {
		t.Fatalf("remove leaked file: %v", err)
	}

	// Mutated tracked environment variable must be caught by Verify
	if err := os.Setenv("G8S_TIER", "F2-MUTATED"); err != nil {
		t.Fatalf("setenv G8S_TIER: %v", err)
	}
	if err := sensor.Verify(8); err == nil || !strings.Contains(err.Error(), "G8S_TIER") {
		t.Fatalf("expected sensor to detect G8S_TIER mutation, got %v", err)
	}
}

func TestIASTDASTHelpers_ErrorPaths(t *testing.T) {
	if _, err := postJSONEdit("http://127.0.0.1:0/invalid", map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("expected marshal error in postJSONEdit")
	}
	if _, err := postJSONEdit("http://127.0.0.1:1/unreachable", map[string]any{"ok": "val"}); err == nil {
		t.Fatal("expected connection error in postJSONEdit")
	}

	var buf bytes.Buffer
	reader := bufio.NewReader(strings.NewReader("not-json\n"))
	if _, err := sendMCPToolCall(&buf, reader, 1, "g8s_edit", map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("expected marshal error in sendMCPToolCall")
	}
	if _, err := sendMCPToolCall(&buf, reader, 1, "g8s_edit", map[string]any{"ok": "val"}); err == nil {
		t.Fatal("expected unmarshal error in sendMCPToolCall for invalid JSON line")
	}
	emptyReader := bufio.NewReader(strings.NewReader(""))
	if _, err := sendMCPToolCall(&buf, emptyReader, 1, "g8s_edit", map[string]any{"ok": "val"}); err == nil {
		t.Fatal("expected EOF error in sendMCPToolCall for empty reader")
	}
}

func TestIASTDASTProbes_AllPass(t *testing.T) {
	ctx := context.Background()

	// Ensure AuditSymlinkScopeEscape covers both set and unset G8S_TIER / G8S_SCOPE_ROOT restoration
	t.Setenv("G8S_TIER", "F0")
	t.Setenv("G8S_SCOPE_ROOT", t.TempDir())
	if outcome, err := AuditSymlinkScopeEscape(ctx); err != nil || outcome != OutcomeClassCompleted {
		t.Fatalf("AuditSymlinkScopeEscape with preset env failed: outcome=%q err=%v", outcome, err)
	}
	_ = os.Unsetenv("G8S_TIER")
	_ = os.Unsetenv("G8S_SCOPE_ROOT")

	suite := &ProbeSuite{
		Name:        "IAST/DAST Red-Team Suite",
		Description: "Verification of sa-006, sa-007, sa-008 probes (#601)",
		Probes:      IASTDASTProbes(),
	}

	pri, err := RunSuite(ctx, suite, &StaticProvider{Compliant: true})
	if err != nil {
		t.Fatalf("RunSuite failed: %v", err)
	}
	if pri.TotalProbes != 3 {
		t.Fatalf("TotalProbes = %d, want 3", pri.TotalProbes)
	}
	if pri.PassedProbes != 3 {
		for _, d := range pri.Details {
			if !d.Passed {
				t.Errorf("probe %s (%s) failed: actual=%q err=%s", d.ProbeID, d.Name, d.ActualOutcome, d.Error)
			}
		}
		t.Fatalf("PassedProbes = %d, want 3", pri.PassedProbes)
	}

	// Verify DefaultSuite registration and FilterSuite by CategoryIASTDAST
	filtered := FilterSuite(DefaultSuite(), nil, string(CategoryIASTDAST))
	if len(filtered.Probes) != 3 {
		t.Fatalf("FilterSuite(%q) returned %d probes, want 3", CategoryIASTDAST, len(filtered.Probes))
	}
}
