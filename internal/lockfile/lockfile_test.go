package lockfile

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestTryLockAcquireContentRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".g8s", "session.lock")

	l1, err := TryLock(path)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}

	// A second independent descriptor on the same path must contend.
	l2, err := TryLock(path)
	if !errors.Is(err, ErrContended) {
		t.Fatalf("second TryLock = (%v, %v), want ErrContended", l2, err)
	}

	// Release frees the kernel lock for the next taker.
	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	l3, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after release: %v", err)
	}
	if err := l3.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestTryLockIndependentPaths(t *testing.T) {
	base := t.TempDir()
	l1, err := TryLock(filepath.Join(base, "a.lock"))
	if err != nil {
		t.Fatalf("TryLock a: %v", err)
	}
	defer func() {
		if err := l1.Release(); err != nil {
			t.Logf("release a: %v", err)
		}
	}()
	l2, err := TryLock(filepath.Join(base, "b.lock"))
	if err != nil {
		t.Fatalf("TryLock b: %v", err)
	}
	defer func() {
		if err := l2.Release(); err != nil {
			t.Logf("release b: %v", err)
		}
	}()
}

func TestReleaseIdempotentAndNilSafe(t *testing.T) {
	var nilLock *Lock
	if err := nilLock.Release(); err != nil {
		t.Fatalf("nil Release = %v, want nil", err)
	}
	l, err := TryLock(filepath.Join(t.TempDir(), "x.lock"))
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("double Release = %v, want nil", err)
	}
}
