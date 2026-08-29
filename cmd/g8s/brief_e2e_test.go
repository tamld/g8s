package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/controlplane"
)

func TestBriefIssueAndConsumeE2E(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s-brief-test.db")
	t.Setenv("G8S_DB", dbPath)

	store, err := controlplane.NewControlPlane(dbPath, nil)
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	// 1. Prepare brief payload and dod files on disk
	briefPath := filepath.Join(tempDir, "brief.md")
	dodPath := filepath.Join(tempDir, "dod.md")

	briefContent := "# Task: DELTA-15 Orchestrator\nImplement structured task dispatching."
	dodContent := "- [ ] Unit tests pass\n- [ ] Integration tests green\n- [ ] Clean fmt"

	if err := os.WriteFile(briefPath, []byte(briefContent), 0o600); err != nil {
		t.Fatalf("write brief file: %v", err)
	}
	if err := os.WriteFile(dodPath, []byte(dodContent), 0o600); err != nil {
		t.Fatalf("write dod file: %v", err)
	}

	// 2. Issue brief via executeBriefIssue
	var issueBuf bytes.Buffer
	title := "DELTA-15"
	issuedBy := "sisyphus"
	ttl := 2 * time.Hour

	issuedBrief, err := executeBriefIssue(&issueBuf, store, title, briefContent, dodContent, issuedBy, ttl)
	if err != nil {
		t.Fatalf("executeBriefIssue failed: %v", err)
	}

	if !strings.HasPrefix(issuedBrief.ID, "brief-") {
		t.Errorf("expected brief ID prefix 'brief-', got %q", issuedBrief.ID)
	}
	if issuedBrief.Title != title {
		t.Errorf("Title = %q, want %q", issuedBrief.Title, title)
	}
	if issuedBrief.PayloadMD != briefContent {
		t.Errorf("PayloadMD = %q, want %q", issuedBrief.PayloadMD, briefContent)
	}
	if issuedBrief.DodMD != dodContent {
		t.Errorf("DodMD = %q, want %q", issuedBrief.DodMD, dodContent)
	}
	if issuedBrief.IssuedBy != issuedBy {
		t.Errorf("IssuedBy = %q, want %q", issuedBrief.IssuedBy, issuedBy)
	}
	if issuedBrief.Status != "active" {
		t.Errorf("Status = %q, want 'active'", issuedBrief.Status)
	}

	// Verify JSON shape printed to writer
	var parsedIssue brief.Brief
	if err := json.Unmarshal(issueBuf.Bytes(), &parsedIssue); err != nil {
		t.Fatalf("unmarshal issued JSON: %v (raw: %s)", err, issueBuf.String())
	}
	if parsedIssue.ID != issuedBrief.ID || parsedIssue.Status != "active" {
		t.Errorf("parsed JSON mismatch: %+v", parsedIssue)
	}

	// 3. Consume brief via executeBriefConsume
	var consumeBuf bytes.Buffer
	consumedBrief, err := executeBriefConsume(&consumeBuf, store, issuedBrief.ID)
	if err != nil {
		t.Fatalf("executeBriefConsume failed: %v", err)
	}

	if consumedBrief.ID != issuedBrief.ID {
		t.Errorf("consumed ID = %q, want %q", consumedBrief.ID, issuedBrief.ID)
	}
	if consumedBrief.Status != "consumed" {
		t.Errorf("consumed Status = %q, want 'consumed'", consumedBrief.Status)
	}

	// Verify JSON shape printed on consume
	var parsedConsume brief.Brief
	if err := json.Unmarshal(consumeBuf.Bytes(), &parsedConsume); err != nil {
		t.Fatalf("unmarshal consumed JSON: %v (raw: %s)", err, consumeBuf.String())
	}
	if parsedConsume.Status != "consumed" {
		t.Errorf("parsed consume JSON status = %q, want 'consumed'", parsedConsume.Status)
	}

	// 4. Subsequent consume should fail
	var secondConsumeBuf bytes.Buffer
	if _, err := executeBriefConsume(&secondConsumeBuf, store, issuedBrief.ID); err == nil {
		t.Errorf("second consume of brief %s should have failed", issuedBrief.ID)
	}
}

func TestBriefTTLExpiryE2E(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "g8s-brief-ttl.db")
	t.Setenv("G8S_DB", dbPath)

	now := time.Now()
	store, err := controlplane.NewControlPlane(dbPath, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewControlPlane: %v", err)
	}
	defer store.Close()

	var buf bytes.Buffer
	b, err := executeBriefIssue(&buf, store, "Short TTL Brief", "Payload", "DoD", "operator", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("executeBriefIssue: %v", err)
	}

	// Move clock forward
	now = now.Add(1 * time.Hour)

	var consumeBuf bytes.Buffer
	if _, err := executeBriefConsume(&consumeBuf, store, b.ID); err == nil {
		t.Errorf("expected consume to fail due to TTL expiry")
	}
}
