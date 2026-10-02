package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstanceID_StableInSameStateDir(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", tempDir)

	id1 := InstanceID()
	if len(id1) != 32 {
		t.Fatalf("InstanceID() length = %d, want 32 (got %q)", len(id1), id1)
	}

	id2 := InstanceID()
	if id1 != id2 {
		t.Fatalf("InstanceID() unstable across calls in same state dir: %q vs %q", id1, id2)
	}

	filePath := filepath.Join(tempDir, "instance_id")
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read instance_id file: %v", err)
	}
	if string(data) != id1 {
		t.Fatalf("instance_id file content = %q, want %q", string(data), id1)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatalf("stat instance_id: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("instance_id permissions = %04o, want 0600", perm)
		}
	}
}

func TestInstanceID_DifferentStateDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	t.Setenv("G8S_STATE_DIR", dir1)
	id1 := InstanceID()

	t.Setenv("G8S_STATE_DIR", dir2)
	id2 := InstanceID()

	if id1 == id2 {
		t.Fatalf("InstanceID() across distinct state dirs must differ, got identical %q", id1)
	}
	if len(id1) != 32 || len(id2) != 32 {
		t.Fatalf("expected 32 hex chars for both IDs, got %d and %d", len(id1), len(id2))
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir2, "instance_id"))
		if err != nil {
			t.Fatalf("stat dir2 instance_id: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("instance_id in dir2 permissions = %04o, want 0600", perm)
		}
	}
}
