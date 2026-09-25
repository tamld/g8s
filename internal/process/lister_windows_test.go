//go:build windows

package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTasklistCSV(t *testing.T) {
	mockCSV := []byte(`"System Idle Process","0","Services","0","8 K"
"System","4","Services","0","144 K"
"agy.exe","1234","Console","1","45,120 K","Running","DOMAIN\user","0:01:23","g8s worker"
"claude.exe","5678","Console","1","50,000 K","Running","DOMAIN\user","0:00:10","claude"
"invalid line"
"badpid.exe","abc"
`)

	procs := parseTasklistCSV(mockCSV)
	if len(procs) != 3 { // System Idle Process has PID 0, bad lines skipped
		t.Fatalf("expected 3 valid processes, got %d", len(procs))
	}

	if procs[1].PID != 1234 || procs[1].Binary != "agy" || procs[1].User != `DOMAIN\user` {
		t.Errorf("unexpected proc 1: %+v", procs[1])
	}

	if procs[2].PID != 5678 || procs[2].Binary != "claude" {
		t.Errorf("unexpected proc 2: %+v", procs[2])
	}
}

func TestParseCimJSON(t *testing.T) {
	mockJSON := []byte(`[
		{"ProcessId": 0, "ParentProcessId": 0, "Name": "System Idle Process", "CommandLine": null},
		{"ProcessId": 1234, "ParentProcessId": 4, "Name": "agy.exe", "CommandLine": "C:\\agy.exe --add-dir C:\\repo"},
		{"ProcessId": 5678, "ParentProcessId": 1234, "Name": "claude.exe", "CommandLine": null}
	]`)

	procs, err := parseCimJSON(mockJSON)
	if err != nil {
		t.Fatalf("parseCimJSON error: %v", err)
	}
	if len(procs) != 2 { // PID 0 is skipped
		t.Fatalf("expected 2 valid processes, got %d", len(procs))
	}

	if procs[0].PID != 1234 || procs[0].PPID != 4 || procs[0].Binary != "agy" || procs[0].CommandLine != "C:\\agy.exe --add-dir C:\\repo" {
		t.Errorf("unexpected proc 0: %+v", procs[0])
	}

	if procs[1].PID != 5678 || procs[1].PPID != 1234 || procs[1].Binary != "claude" || procs[1].CommandLine != "claude.exe" {
		t.Errorf("unexpected proc 1: %+v", procs[1])
	}

	// Single object format
	singleJSON := []byte(`{"ProcessId": 9999, "ParentProcessId": 1, "Name": "tool.exe", "CommandLine": "tool.exe run"}`)
	singleProcs, err := parseCimJSON(singleJSON)
	if err != nil {
		t.Fatalf("parseCimJSON single error: %v", err)
	}
	if len(singleProcs) != 1 || singleProcs[0].PID != 9999 || singleProcs[0].Binary != "tool" {
		t.Errorf("unexpected single proc: %+v", singleProcs)
	}
}

func TestTasklistLister_ResolveCWD_EdgeCases(t *testing.T) {
	lister := NewLister()
	if cwd := lister.ResolveCWD(0); cwd != "" {
		t.Errorf("expected empty CWD on Windows PID 0, got %s", cwd)
	}
	if lister.IsAlive(0) {
		t.Errorf("expected PID 0 to not be alive")
	}

	// Test ResolveCWD on current process
	selfPID := os.Getpid()
	selfCWD := lister.ResolveCWD(selfPID)
	if selfCWD == "" {
		t.Fatalf("expected non-empty CWD for current PID %d", selfPID)
	}

	expectedDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd error: %v", err)
	}

	if !strings.EqualFold(filepath.Clean(selfCWD), filepath.Clean(expectedDir)) {
		t.Errorf("ResolveCWD(%d) = %q; want %q", selfPID, selfCWD, expectedDir)
	}
}

func TestTasklistLister_List_Live(t *testing.T) {
	lister := NewLister()
	procs, err := lister.List()
	if err != nil {
		t.Fatalf("lister.List() error: %v", err)
	}
	if len(procs) == 0 {
		t.Fatalf("expected non-empty process list on Windows host")
	}

	// Verify our own process is found in the list
	selfPID := os.Getpid()
	found := false
	for _, p := range procs {
		if p.PID == selfPID {
			found = true
			if p.CommandLine == "" {
				t.Errorf("expected non-empty CommandLine for self PID %d", selfPID)
			}
			break
		}
	}
	if !found {
		t.Logf("self PID %d not found in process list, which may happen if run under test runner isolation", selfPID)
	}
}
