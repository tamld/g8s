//go:build windows

package lockfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// TryLock takes an exclusive LockFileEx range lock on the first byte of path
// without blocking (LOCKFILE_FAIL_IMMEDIATELY). Contention is reported as
// ErrContended (ERROR_LOCK_VIOLATION). The handle stays open for the Lock's
// lifetime: the kernel releases it when the process dies — crash-safe by
// construction.
func TryLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("lockfile: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lockfile: open: %w", err)
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
	if err != nil {
		// f is closed explicitly here: the lock was not taken, so there is
		// nothing for Release to own. LockFileEx is synchronous on handles
		// opened without FILE_FLAG_OVERLAPPED, so contention surfaces as
		// ERROR_LOCK_VIOLATION, never ERROR_IO_PENDING.
		cerr := f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrContended
		}
		if cerr != nil {
			err = fmt.Errorf("%v (close: %v)", err, cerr)
		}
		return nil, fmt.Errorf("lockfile: LockFileEx: %w", err)
	}
	return &Lock{f: f}, nil
}

// unlock releases the byte-range lock; the descriptor close finishes it.
func (l *Lock) unlock() error {
	if err := windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, new(windows.Overlapped)); err != nil {
		return fmt.Errorf("lockfile: UnlockFileEx: %w", err)
	}
	return nil
}
