package registry

import (
	"runtime"
	"testing"
)

func TestOpenKey(t *testing.T) {
	key, err := OpenKey(LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s`, QUERY_VALUE)
	if runtime.GOOS != "windows" {
		if err != ErrNotSupported {
			t.Fatalf("expected ErrNotSupported on non-windows, got %v", err)
		}
		if key != nil {
			t.Fatalf("expected nil key on non-windows")
		}
	} else {
		// On windows in test environment, key may or may not exist, but call must not panic
		if err == nil {
			_ = key.Close()
		}
	}
}

func TestKeyCloseAndGetOnStub(t *testing.T) {
	if runtime.GOOS != "windows" {
		k := &Key{}
		if err := k.Close(); err != nil {
			t.Fatalf("expected nil error on stub close, got %v", err)
		}
		if _, err := k.GetStringValue("DisplayName"); err != ErrNotSupported {
			t.Fatalf("expected ErrNotSupported on stub GetStringValue, got %v", err)
		}
	}
}
