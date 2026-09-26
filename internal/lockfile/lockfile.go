// Package lockfile provides non-blocking exclusive advisory file locks used
// to arbitrate concurrent supervisor sessions over one checkout (#393).
//
// The lock lives on the open file descriptor: the kernel releases it when
// the owning process dies — crash-safe by construction, no stale-lock
// reaping, no TOCTOU window between check and take. The marker file itself
// persists and its content is irrelevant.
package lockfile

import (
	"errors"
	"os"
)

// ErrContended reports that another process holds the lock.
var ErrContended = errors.New("lockfile: held by another process")

// Lock is an exclusive advisory lock on a file path, held by an open file
// descriptor until Release or process death. Holding the *os.File pins the
// descriptor against the runtime finalizer; Release closes it explicitly
// (runtime.KeepAlive is therefore unnecessary).
type Lock struct {
	f *os.File
}

// Release drops the lock and closes the descriptor. It is safe to call on a
// nil Lock or twice.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.unlock()
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}
