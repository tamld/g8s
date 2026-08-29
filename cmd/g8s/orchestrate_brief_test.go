package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/controlplane"
)

func TestParseBriefContent(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		fallbackTitle string
		wantTitle     string
		wantDoD       string
		wantPayload   string
	}{
		{
			name: "full markdown brief with title and dod",
			content: `# DEBT-25: Brief workflow integration

## Context
Orchestrator dispatches briefs through g8s.

## DoD
- [x] Brief file parsed
- [x] Dispatch re-issue verified
`,
			fallbackTitle: "fallback.md",
			wantTitle:     "DEBT-25: Brief workflow integration",
			wantDoD:       "- [x] Brief file parsed\n- [x] Dispatch re-issue verified",
			wantPayload: `# DEBT-25: Brief workflow integration

## Context
Orchestrator dispatches briefs through g8s.

## DoD
- [x] Brief file parsed
- [x] Dispatch re-issue verified`,
		},
		{
			name: "brief with definition of done heading",
			content: `# Feature Alpha

Task description.

### Definition of Done
- [ ] First check
- [ ] Second check
`,
			fallbackTitle: "fallback.md",
			wantTitle:     "Feature Alpha",
			wantDoD:       "- [ ] First check\n- [ ] Second check",
			wantPayload: `# Feature Alpha

Task description.

### Definition of Done
- [ ] First check
- [ ] Second check`,
		},
		{
			name: "brief without explicit heading or dod",
			content: `Plain task description without markdown title.
Line 2 of task.`,
			fallbackTitle: "task.md",
			wantTitle:     "task.md",
			wantDoD:       "- [ ] Brief execution completed",
			wantPayload: `Plain task description without markdown title.
Line 2 of task.`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title, payload, dod := parseBriefContent(tc.content, tc.fallbackTitle)
			if title != tc.wantTitle {
				t.Errorf("title = %q, want %q", title, tc.wantTitle)
			}
			if dod != tc.wantDoD {
				t.Errorf("dod = %q, want %q", dod, tc.wantDoD)
			}
			if payload != tc.wantPayload {
				t.Errorf("payload = %q, want %q", payload, tc.wantPayload)
			}
		})
	}
}

func TestOrchestrateBriefFileRoundtrip(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "brief-roundtrip.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	briefFilePath := filepath.Join(tempDir, "brief.md")
	briefContent := `# DEBT-25: Integration Brief

## Overview
Test brief execution roundtrip.

## DoD
- [ ] Status equals consumed
- [ ] All checks green
`
	if err := os.WriteFile(briefFilePath, []byte(briefContent), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// 1. Issue brief via executeOrchestrateBriefFile (plain ID output)
	var outBuf bytes.Buffer
	b, err := executeOrchestrateBriefFile(
		&outBuf,
		store,
		briefFilePath,
		"sisyphus",
		"2h",
		"",
		"",
		false,
	)
	if err != nil {
		t.Fatalf("executeOrchestrateBriefFile failed: %v", err)
	}

	if !strings.HasPrefix(b.ID, "brief-") {
		t.Fatalf("expected brief ID prefix 'brief-', got %q", b.ID)
	}
	if b.Title != "DEBT-25: Integration Brief" {
		t.Errorf("Title = %q, want %q", b.Title, "DEBT-25: Integration Brief")
	}
	if b.Status != "active" {
		t.Errorf("Status = %q, want 'active'", b.Status)
	}
	if b.IssuedBy != "sisyphus" {
		t.Errorf("IssuedBy = %q, want 'sisyphus'", b.IssuedBy)
	}

	printedID := strings.TrimSpace(outBuf.String())
	if printedID != b.ID {
		t.Errorf("printed output = %q, want %q", printedID, b.ID)
	}

	// 2. Consume the issued brief
	consumed, err := brief.Consume(store, b.ID)
	if err != nil {
		t.Fatalf("brief.Consume failed: %v", err)
	}

	if consumed.ID != b.ID {
		t.Errorf("consumed ID = %q, want %q", consumed.ID, b.ID)
	}
	if consumed.Status != "consumed" {
		t.Errorf("consumed Status = %q, want 'consumed'", consumed.Status)
	}
}

func TestOrchestrateDispatchReIssue(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "dispatch-reissue.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	// 1. Create and consume an original brief
	origTitle := "Original Crash Task"
	origPayload := "# Crash Context\nOriginal payload content must be preserved."
	origDoD := "- [x] Initial item\n- [ ] Pending item"
	origIssuer := "sisyphus"

	origBrief, err := brief.Issue(store, origTitle, origPayload, origDoD, origIssuer, 1*time.Hour)
	if err != nil {
		t.Fatalf("brief.Issue failed: %v", err)
	}

	// Mark as consumed to simulate finished/interrupted initial attempt
	if _, err := brief.Consume(store, origBrief.ID); err != nil {
		t.Fatalf("Consume failed: %v", err)
	}

	// 2. Re-issue via executeOrchestrateDispatch
	var outBuf bytes.Buffer
	reissuedBrief, err := executeOrchestrateDispatch(
		&outBuf,
		store,
		origBrief.ID,
		"sisyphus",
		"3h",
		false,
	)
	if err != nil {
		t.Fatalf("executeOrchestrateDispatch failed: %v", err)
	}

	// 3. Verify the original payload is loaded and preserved
	if reissuedBrief.ID == origBrief.ID {
		t.Errorf("expected new brief ID, got same as original: %q", reissuedBrief.ID)
	}
	if reissuedBrief.PayloadMD != origBrief.PayloadMD {
		t.Errorf("PayloadMD = %q, want original %q", reissuedBrief.PayloadMD, origBrief.PayloadMD)
	}
	if reissuedBrief.DodMD != origBrief.DodMD {
		t.Errorf("DodMD = %q, want original %q", reissuedBrief.DodMD, origBrief.DodMD)
	}
	if reissuedBrief.Title != origBrief.Title {
		t.Errorf("Title = %q, want original %q", reissuedBrief.Title, origBrief.Title)
	}
	if reissuedBrief.Status != "active" {
		t.Errorf("Status = %q, want 'active'", reissuedBrief.Status)
	}

	printedID := strings.TrimSpace(outBuf.String())
	if printedID != reissuedBrief.ID {
		t.Errorf("printed ID = %q, want %q", printedID, reissuedBrief.ID)
	}

	// 4. Verify the reissued brief can be consumed
	consumedReissued, err := brief.Consume(store, reissuedBrief.ID)
	if err != nil {
		t.Fatalf("brief.Consume(reissued) failed: %v", err)
	}
	if consumedReissued.Status != "consumed" {
		t.Errorf("reissued consumed Status = %q, want 'consumed'", consumedReissued.Status)
	}
}

func TestOrchestrateBriefFileJSONMode(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "brief-json.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	briefFilePath := filepath.Join(tempDir, "brief.md")
	if err := os.WriteFile(briefFilePath, []byte("# JSON Brief\nContent\n## DoD\n- [ ] Item"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var outBuf bytes.Buffer
	b, err := executeOrchestrateBriefFile(
		&outBuf,
		store,
		briefFilePath,
		"ci-bot",
		"1h",
		"Custom Title",
		"- [ ] Custom DoD",
		true,
	)
	if err != nil {
		t.Fatalf("executeOrchestrateBriefFile: %v", err)
	}

	var parsed brief.Brief
	if err := json.Unmarshal(outBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal json: %v\nOutput: %s", err, outBuf.String())
	}

	if parsed.ID != b.ID {
		t.Errorf("parsed ID = %q, want %q", parsed.ID, b.ID)
	}
	if parsed.Title != "Custom Title" {
		t.Errorf("parsed Title = %q, want 'Custom Title'", parsed.Title)
	}
	if parsed.DodMD != "- [ ] Custom DoD" {
		t.Errorf("parsed DodMD = %q, want '- [ ] Custom DoD'", parsed.DodMD)
	}
}

func TestOrchestrateDispatchJSONMode(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "dispatch-json.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	orig, err := brief.Issue(store, "Orig", "Payload", "DoD", "operator", time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var outBuf bytes.Buffer
	reissued, err := executeOrchestrateDispatch(&outBuf, store, orig.ID, "", "30m", true)
	if err != nil {
		t.Fatalf("executeOrchestrateDispatch: %v", err)
	}

	var parsed brief.Brief
	if err := json.Unmarshal(outBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal json: %v\nOutput: %s", err, outBuf.String())
	}

	if parsed.ID != reissued.ID {
		t.Errorf("parsed ID = %q, want %q", parsed.ID, reissued.ID)
	}
}

func TestOrchestrateBriefFileErrors(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "brief-err.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	var buf bytes.Buffer

	// Empty file path
	if _, err := executeOrchestrateBriefFile(&buf, store, "", "sisyphus", "2h", "", "", false); err == nil {
		t.Errorf("expected error on empty file path")
	}

	// Non-existent file
	if _, err := executeOrchestrateBriefFile(&buf, store, filepath.Join(tempDir, "none.md"), "sisyphus", "2h", "", "", false); err == nil {
		t.Errorf("expected error on non-existent file")
	}

	// Empty content file
	emptyFile := filepath.Join(tempDir, "empty.md")
	if err := os.WriteFile(emptyFile, []byte("   \n\t "), 0o600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}
	if _, err := executeOrchestrateBriefFile(&buf, store, emptyFile, "sisyphus", "2h", "", "", false); err == nil {
		t.Errorf("expected error on empty file content")
	}

	// Invalid TTL
	nonEmptyFile := filepath.Join(tempDir, "valid.md")
	if err := os.WriteFile(nonEmptyFile, []byte("# Title\nPayload"), 0o600); err != nil {
		t.Fatalf("write valid file: %v", err)
	}
	if _, err := executeOrchestrateBriefFile(&buf, store, nonEmptyFile, "sisyphus", "invalid-ttl", "", "", false); err == nil {
		t.Errorf("expected error on invalid TTL duration")
	}
}

func TestOrchestrateDispatchErrors(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "dispatch-err.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	var buf bytes.Buffer

	// Empty dispatch ID
	if _, err := executeOrchestrateDispatch(&buf, store, "", "sisyphus", "2h", false); err == nil {
		t.Errorf("expected error on empty dispatch ID")
	}

	// Non-existent dispatch ID
	if _, err := executeOrchestrateDispatch(&buf, store, "brief-nonexistent", "sisyphus", "2h", false); err == nil {
		t.Errorf("expected error on non-existent brief ID")
	}

	// Invalid TTL
	orig, err := brief.Issue(store, "Orig", "Payload", "DoD", "operator", time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := executeOrchestrateDispatch(&buf, store, orig.ID, "sisyphus", "invalid-ttl", false); err == nil {
		t.Errorf("expected error on invalid TTL duration for dispatch")
	}
}

func TestRunOrchestrateCLIBriefFileAndDispatch(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cli-brief-test.db")
	t.Setenv("G8S_DB", dbPath)

	briefPath := filepath.Join(tempDir, "cli-brief.md")
	briefContent := `# CLI Brief Test
## Overview
Testing CLI dispatch.
## DoD
- [x] CLI output captured
`
	if err := os.WriteFile(briefPath, []byte(briefContent), 0o600); err != nil {
		t.Fatalf("write brief: %v", err)
	}

	// 1. Test runOrchestrate with --brief-file
	out := captureStdout(t, func() {
		runOrchestrate([]string{
			"--brief-file", briefPath,
			"--issued-by", "sisyphus",
			"--ttl", "1h",
		})
	})

	briefID := strings.TrimSpace(out)
	if !strings.HasPrefix(briefID, "brief-") {
		t.Fatalf("expected brief ID from runOrchestrate --brief-file, got: %q", out)
	}

	// 2. Test runOrchestrate with --dispatch
	dispatchOut := captureStdout(t, func() {
		runOrchestrate([]string{
			"--dispatch", briefID,
			"--ttl", "2h",
		})
	})

	reissuedID := strings.TrimSpace(dispatchOut)
	if !strings.HasPrefix(reissuedID, "brief-") {
		t.Fatalf("expected reissued brief ID from runOrchestrate --dispatch, got: %q", dispatchOut)
	}
	if reissuedID == briefID {
		t.Fatalf("expected new brief ID, got identical ID %q", reissuedID)
	}

	// 3. Verify in store that reissued brief has identical payload
	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	reissuedRow, err := store.GetBrief(context.Background(), reissuedID)
	if err != nil {
		t.Fatalf("GetBrief reissued: %v", err)
	}
	if !strings.Contains(reissuedRow.PayloadMD, "Testing CLI dispatch.") {
		t.Errorf("reissued payload mismatch: %q", reissuedRow.PayloadMD)
	}
}
