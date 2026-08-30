//go:build windows

package registry

import (
	"golang.org/x/sys/windows/registry"
)

// Key wraps a Windows registry key handle.
type Key struct {
	k registry.Key
}

// OpenKey opens a registry subkey under root key k.
func OpenKey(k KeyHandle, path string, access uint32) (*Key, error) {
	regKey, err := registry.OpenKey(registry.Key(k), path, access)
	if err != nil {
		return nil, err
	}
	return &Key{k: regKey}, nil
}

// Close releases the handle associated with key.
func (k *Key) Close() error {
	if k == nil {
		return nil
	}
	return k.k.Close()
}

// GetStringValue retrieves the string value for specified value name.
func (k *Key) GetStringValue(name string) (string, error) {
	if k == nil {
		return "", ErrNotSupported
	}
	val, _, err := k.k.GetStringValue(name)
	return val, err
}
