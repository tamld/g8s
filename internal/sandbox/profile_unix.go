//go:build darwin || linux

package sandbox

import (
	"os"
	"strings"
)

func resolveEnvLookup(env []string, keys ...string) string {
	for _, want := range keys {
		for _, entry := range env {
			k, v, ok := strings.Cut(entry, "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), want) && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func resolveHomeDir(cfg Config) string {
	if v := resolveEnvLookup(cfg.TaskEnv, "HOME", "USERPROFILE"); v != "" {
		return CanonicalPath(v)
	}
	if v := resolveEnvLookup(cfg.BaseEnv, "HOME", "USERPROFILE"); v != "" {
		return CanonicalPath(v)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return CanonicalPath(home)
	}
	return ""
}

func resolveSSHAuthSock(cfg Config) string {
	if v := resolveEnvLookup(cfg.TaskEnv, "SSH_AUTH_SOCK"); v != "" {
		return CanonicalPath(v)
	}
	if cfg.BaseEnv != nil {
		if v := resolveEnvLookup(cfg.BaseEnv, "SSH_AUTH_SOCK"); v != "" {
			return CanonicalPath(v)
		}
		return ""
	}
	if v := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK")); v != "" {
		return CanonicalPath(v)
	}
	return ""
}
