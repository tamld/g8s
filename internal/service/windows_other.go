//go:build !windows

package service

import "fmt"

// NewWindowsServiceManager returns an error when called on non-Windows operating systems.
func NewWindowsServiceManager(cfg Config, guard LifecycleGuard, runner Runner) (ServiceManager, error) {
	return nil, fmt.Errorf("windows service backend is only supported on Windows (got %s)", cfg.Platform)
}
