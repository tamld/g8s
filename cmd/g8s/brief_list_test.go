package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/controlplane"
)

func TestBriefListExecution(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "briefs.db")
	now := time.Now()

	store, err := controlplane.NewControlPlane(dbPath, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new controlplane: %v", err)
	}
	defer store.Close()

	// Issue 2 briefs
	b1, err := brief.Issue(store, "Brief 1", "Payload 1", "DoD 1", "issuer-1", time.Hour)
	if err != nil {
		t.Fatalf("issue b1: %v", err)
	}
	b2, err := brief.Issue(store, "Brief 2", "Payload 2", "DoD 2", "issuer-2", time.Hour)
	if err != nil {
		t.Fatalf("issue b2: %v", err)
	}

	// Consume b2
	if _, err := brief.Consume(store, b2.ID); err != nil {
		t.Fatalf("consume b2: %v", err)
	}

	// Test 1: JSON mode for active
	var buf bytes.Buffer
	if err := executeBriefList(&buf, store, "active", 50, true); err != nil {
		t.Fatalf("executeBriefList active json: %v", err)
	}

	var activeList []brief.Brief
	if err := json.Unmarshal(buf.Bytes(), &activeList); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if len(activeList) != 1 || activeList[0].ID != b1.ID {
		t.Errorf("active list = %v, want [%s]", activeList, b1.ID)
	}

	// Test 2: JSON mode for consumed
	buf.Reset()
	if err := executeBriefList(&buf, store, "consumed", 50, true); err != nil {
		t.Fatalf("executeBriefList consumed json: %v", err)
	}

	var consumedList []brief.Brief
	if err := json.Unmarshal(buf.Bytes(), &consumedList); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if len(consumedList) != 1 || consumedList[0].ID != b2.ID {
		t.Errorf("consumed list = %v, want [%s]", consumedList, b2.ID)
	}

	// Test 3: Table mode
	buf.Reset()
	if err := executeBriefList(&buf, store, "all", 50, false); err != nil {
		t.Fatalf("executeBriefList table mode: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, b1.ID) || !strings.Contains(output, b2.ID) {
		t.Errorf("table output missing brief IDs: %s", output)
	}

	// Test 4: Empty list
	buf.Reset()
	if err := executeBriefList(&buf, store, "expired", 50, false); err != nil {
		t.Fatalf("executeBriefList empty: %v", err)
	}
	if !strings.Contains(buf.String(), "No expired briefs found") {
		t.Errorf("expected empty message, got: %s", buf.String())
	}
}
