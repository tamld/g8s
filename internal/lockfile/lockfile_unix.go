//go:build unix

package lockfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// TryLock takes an exclusive advisory flock on path without blocking.
// Contention is reported as ErrContended (EWOULDBLOCK / EAGAIN).
//
// Assumption: local filesystem. On Linux NFS, flock is emulated via POSIX
// fcntl locks and same-process re-open may not contend — the plan's
// contention tests are the abort criterion that detects such a deployment.
func TryLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("lockfile: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lockfile: open: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// f is closed explicitly here: the lock was not taken, so there is
		// nothing for Release to own.
		cerr := f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrContended
		}
		if cerr != nil {
			err = fmt.Errorf("%v (close: %v)", err, cerr)
		}
		return nil, fmt.Errorf("lockfile: flock: %w", err)
	}
	return &Lock{f: f}, nil
}

// unlock releases the flock; the descriptor close in Release finishes it.
func (l *Lock) unlock() error {
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("lockfile: unlock: %w", err)
	}
	return nil
}
