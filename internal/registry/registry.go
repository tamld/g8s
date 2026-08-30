// Package registry provides a cross-platform wrapper around Windows registry operations
// for installer detection, path validation, and system environment checks.
package registry

import (
	"errors"
)

// KeyHandle represents a root registry key handle.
type KeyHandle uintptr

const (
	// LOCAL_MACHINE root key handle (HKEY_LOCAL_MACHINE)
	LOCAL_MACHINE KeyHandle = 0x80000002
	// CURRENT_USER root key handle (HKEY_CURRENT_USER)
	CURRENT_USER KeyHandle = 0x80000001

	// QUERY_VALUE permission flag
	QUERY_VALUE uint32 = 0x0001
)

// ErrNotSupported is returned when registry operations are invoked on non-Windows platforms.
var ErrNotSupported = errors.New("registry operations are only supported on windows")
