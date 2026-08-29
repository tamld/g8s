package process

import (
	"os"
	"testing"
)

func TestNewLister(t *testing.T) {
	lister := NewLister()
	if lister == nil {
		t.Fatal("expected NewLister to return non-nil implementation")
	}
}

func TestProcessLister_ListCurrentProcess(t *testing.T) {
	lister := NewLister()
	procs, err := lister.List()
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}

	if len(procs) == 0 {
		t.Fatal("expected at least one running process")
	}

	selfPID := os.Getpid()
	found := false
	for _, p := range procs {
		if p.PID == selfPID {
			found = true
			if p.Binary == "" {
				t.Errorf("expected non-empty binary for current PID %d", selfPID)
			}
			break
		}
	}

	if !found {
		t.Errorf("current PID %d not found in process list (total %d processes)", selfPID, len(procs))
	}
}

func TestProcessLister_IsAlive(t *testing.T) {
	lister := NewLister()
	selfPID := os.Getpid()

	if !lister.IsAlive(selfPID) {
		t.Errorf("expected current PID %d to be alive", selfPID)
	}

	// Unlikely high PID
	if lister.IsAlive(999999999) {
		t.Errorf("expected PID 999999999 to not be alive")
	}
}
