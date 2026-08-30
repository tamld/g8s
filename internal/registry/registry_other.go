//go:build !windows

package registry

// Key represents a registry key handle stub on non-Windows platforms.
type Key struct{}

// OpenKey returns ErrNotSupported on non-Windows platforms.
func OpenKey(k KeyHandle, path string, access uint32) (*Key, error) {
	return nil, ErrNotSupported
}

// Close is a no-op on non-Windows platforms.
func (k *Key) Close() error {
	return nil
}

// GetStringValue returns ErrNotSupported on non-Windows platforms.
func (k *Key) GetStringValue(name string) (string, error) {
	return "", ErrNotSupported
}
