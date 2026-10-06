package ladder

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEvidencePacketCompleteness(t *testing.T) {
	history := []RungRecord{
		{
			RungIndex:    0,
			TaskID:       "task-0",
			Role:         "diagnostician",
			Permission:   "read_only",
			Model:        "gemini-3.8-flash-high",
			Effort:       "low",
			Shape:        ShapeEffort,
			Verdict:      "class_checks_failed",
			Tokens:       800,
			InputTokens:  600,
			OutputTokens: 200,
			ChecksFailed: []string{"check-syntax"},
		},
		{
			RungIndex:    1,
			TaskID:       "task-1",
			Role:         "coder",
			Permission:   "workspace_write",
			Model:        "gemini-3.8-flash-high",
			Effort:       "medium",
			Shape:        ShapeEffort,
			Verdict:      "class_checks_failed",
			Tokens:       2500,
			InputTokens:  1800,
			OutputTokens: 700,
			ChecksFailed: []string{"check-syntax"},
		},
		{
			RungIndex:    2,
			TaskID:       "task-2",
			Role:         "coder",
			Permission:   "workspace_write",
			Model:        "gemini-3.8-flash-high",
			Effort:       "high",
			Shape:        ShapeEffort,
			Verdict:      "class_checks_failed",
			Tokens:       4200,
			InputTokens:  3000,
			OutputTokens: 1200,
			ChecksFailed: []string{"check-syntax"},
		},
	}

	packet := BuildEvidencePacket(
		"task-root",
		"feature",
		"hitl_ceiling_reached",
		"ladder reached ceiling: manual review required",
		[]string{"check-syntax"},
		history,
		10000,
	)

	// Verify cost receipt calculation
	wantTotalTokens := 800 + 2500 + 4200
	if packet.TotalTokens != wantTotalTokens {
		t.Fatalf("TotalTokens = %d, want %d", packet.TotalTokens, wantTotalTokens)
	}
	if packet.TotalRungs != 3 {
		t.Errorf("TotalRungs = %d, want 3", packet.TotalRungs)
	}

	// Verify completeness of every rung
	for i, r := range packet.LadderHistory {
		if r.Shape == "" {
			t.Errorf("rung %d missing Shape", i)
		}
		if r.Verdict == "" {
			t.Errorf("rung %d missing Verdict", i)
		}
		if r.Effort == "" {
			t.Errorf("rung %d missing Effort", i)
		}
		if r.Tokens <= 0 {
			t.Errorf("rung %d missing Tokens", i)
		}
	}

	// Verify JSON output
	var buf bytes.Buffer
	if err := packet.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var decoded EvidencePacket
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal decoded packet: %v", err)
	}
	if decoded.RootTaskID != "task-root" || decoded.TotalTokens != wantTotalTokens {
		t.Errorf("Decoded packet mismatch: %+v", decoded)
	}

	// Verify writing to file
	tmpFile := filepath.Join(t.TempDir(), "evidence.json")
	if err := packet.WriteToFile(tmpFile); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	fileBytes, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(fileBytes) == 0 {
		t.Fatalf("Empty evidence packet file written")
	}
}
