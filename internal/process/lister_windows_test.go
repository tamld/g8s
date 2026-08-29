//go:build windows

package process

import (
	"testing"
)

func TestParseTasklistCSV(t *testing.T) {
	mockCSV := []byte(`"System Idle Process","0","Services","0","8 K"
"System","4","Services","0","144 K"
"agy.exe","1234","Console","1","45,120 K","Running","DOMAIN\user","0:01:23","g8s worker"
"claude.exe","5678","Console","1","50,000 K","Running","DOMAIN\user","0:00:10","claude"
`)

	procs := parseTasklistCSV(mockCSV)
	if len(procs) != 3 { // System Idle Process has PID 0, skipped
		t.Fatalf("expected 3 valid processes, got %d", len(procs))
	}

	if procs[1].PID != 1234 || procs[1].Binary != "agy" || procs[1].User != `DOMAIN\user` {
		t.Errorf("unexpected proc 1: %+v", procs[1])
	}

	if procs[2].PID != 5678 || procs[2].Binary != "claude" {
		t.Errorf("unexpected proc 2: %+v", procs[2])
	}
}
