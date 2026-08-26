package service

import (
	"fmt"
	"runtime"
)

// NewPlatformServiceManager instantiates the appropriate OS service backend.
func NewPlatformServiceManager(cfg Config, guard LifecycleGuard, runner Runner) (ServiceManager, error) {
	platform := cfg.Platform
	if platform == "" {
		platform = runtime.GOOS
	}

	switch platform {
	case "darwin":
		mgr, err := NewManager(cfg, guard)
		if err != nil {
			return nil, err
		}
		if runner != nil {
			mgr.runner = runner
		}
		return mgr, nil
	case "linux":
		return NewSystemdManager(cfg, guard, runner)
	default:
		return nil, fmt.Errorf("unsupported platform for background daemon: %s", platform)
	}
}
