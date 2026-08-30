// Package pathutil provides cross-platform path resolution for g8s data,
// configuration, cache, and state files across Windows, Linux, and macOS.
package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	ScopeUser   = "user"
	ScopeSystem = "system"
)

func joinPathForOS(goos string, elem ...string) string {
	if goos == "windows" {
		var parts []string
		for _, e := range elem {
			normalized := strings.ReplaceAll(e, "/", "\\")
			for _, p := range strings.Split(normalized, "\\") {
				if p != "" {
					parts = append(parts, p)
				}
			}
		}
		if len(parts) > 0 && strings.HasSuffix(parts[0], ":") {
			return parts[0] + "\\" + strings.Join(parts[1:], "\\")
		}
		return strings.Join(parts, "\\")
	}

	var parts []string
	for _, e := range elem {
		normalized := strings.ReplaceAll(e, "\\", "/")
		for _, p := range strings.Split(normalized, "/") {
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	return "/" + strings.Join(parts, "/")
}

// DefaultStateDir returns the platform-specific default state directory.
func DefaultStateDir() string {
	if env := os.Getenv("G8S_STATE_DIR"); env != "" {
		return env
	}
	return stateDir()
}

// DefaultDatabasePath returns the canonical path to the g8s database file.
// If G8S_DB environment variable is set, it takes precedence.
func DefaultDatabasePath() string {
	if env := os.Getenv("G8S_DB"); env != "" {
		return env
	}
	return filepath.Join(DefaultStateDir(), "g8s.db")
}

// DefaultEvidenceDir returns the canonical path to the evidence directory.
func DefaultEvidenceDir() string {
	return filepath.Join(DefaultStateDir(), "evidence")
}

// DataDirForScope returns the data directory for the given scope (user or system).
func DataDirForScope(scope string) string {
	return dataDirForScope(scope)
}

// DataDirForOS resolves data directory for a specific target OS and environment map.
func DataDirForOS(goos string, envGetter func(string) string, homeDir string, scope string) string {
	if envGetter == nil {
		envGetter = os.Getenv
	}
	if scope == "" {
		scope = ScopeUser
	}

	switch goos {
	case "windows":
		if scope == ScopeSystem {
			progFiles := envGetter("PROGRAMFILES")
			if progFiles == "" {
				progFiles = `C:\Program Files`
			}
			return joinPathForOS("windows", progFiles, "g8s")
		}
		local := envGetter("LOCALAPPDATA")
		if local == "" {
			profile := envGetter("USERPROFILE")
			if profile == "" {
				profile = homeDir
			}
			if profile == "" {
				profile = `C:\Users\Default`
			}
			local = joinPathForOS("windows", profile, "AppData", "Local")
		}
		return joinPathForOS("windows", local, "Programs", "g8s")

	case "darwin":
		if xdg := envGetter("XDG_DATA_HOME"); xdg != "" {
			return joinPathForOS("darwin", xdg, "g8s")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("darwin", homeDir, "Library", "Application Support", "g8s")

	default: // linux / posix
		if xdg := envGetter("XDG_DATA_HOME"); xdg != "" {
			return joinPathForOS("linux", xdg, "g8s")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("linux", homeDir, ".local", "share", "g8s")
	}
}

// ConfigDirForOS resolves config directory for a specific target OS and environment map.
func ConfigDirForOS(goos string, envGetter func(string) string, homeDir string) string {
	if envGetter == nil {
		envGetter = os.Getenv
	}

	switch goos {
	case "windows":
		appdata := envGetter("APPDATA")
		if appdata == "" {
			profile := envGetter("USERPROFILE")
			if profile == "" {
				profile = homeDir
			}
			if profile == "" {
				profile = `C:\Users\Default`
			}
			appdata = joinPathForOS("windows", profile, "AppData", "Roaming")
		}
		return joinPathForOS("windows", appdata, "g8s")

	case "darwin":
		if xdg := envGetter("XDG_CONFIG_HOME"); xdg != "" {
			return joinPathForOS("darwin", xdg, "g8s")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("darwin", homeDir, "Library", "Application Support", "g8s")

	default: // linux / posix
		if xdg := envGetter("XDG_CONFIG_HOME"); xdg != "" {
			return joinPathForOS("linux", xdg, "g8s")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("linux", homeDir, ".config", "g8s")
	}
}

// CacheDirForOS resolves cache directory for a specific target OS and environment map.
func CacheDirForOS(goos string, envGetter func(string) string, homeDir string) string {
	if envGetter == nil {
		envGetter = os.Getenv
	}

	switch goos {
	case "windows":
		dataDir := DataDirForOS("windows", envGetter, homeDir, ScopeUser)
		return joinPathForOS("windows", dataDir, "cache")

	case "darwin":
		if xdg := envGetter("XDG_CACHE_HOME"); xdg != "" {
			return joinPathForOS("darwin", xdg, "g8s")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("darwin", homeDir, "Library", "Caches", "g8s")

	default: // linux / posix
		if xdg := envGetter("XDG_CACHE_HOME"); xdg != "" {
			return joinPathForOS("linux", xdg, "g8s")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("linux", homeDir, ".cache", "g8s")
	}
}

// LogsDirForOS resolves logs directory for a specific target OS and environment map.
func LogsDirForOS(goos string, envGetter func(string) string, homeDir string) string {
	if envGetter == nil {
		envGetter = os.Getenv
	}

	switch goos {
	case "windows":
		dataDir := DataDirForOS("windows", envGetter, homeDir, ScopeUser)
		return joinPathForOS("windows", dataDir, "logs")

	case "darwin":
		if xdg := envGetter("XDG_STATE_HOME"); xdg != "" {
			return joinPathForOS("darwin", xdg, "g8s", "logs")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("darwin", homeDir, "Library", "Logs", "g8s")

	default: // linux / posix
		if xdg := envGetter("XDG_STATE_HOME"); xdg != "" {
			return joinPathForOS("linux", xdg, "g8s", "logs")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("linux", homeDir, ".local", "state", "g8s", "logs")
	}
}

// StateDirForOS resolves state directory for a specific target OS and environment map.
func StateDirForOS(goos string, envGetter func(string) string, homeDir string) string {
	if envGetter == nil {
		envGetter = os.Getenv
	}

	switch goos {
	case "windows":
		return DataDirForOS("windows", envGetter, homeDir, ScopeUser)

	case "darwin", "linux":
		fallthrough
	default:
		if xdg := envGetter("XDG_STATE_HOME"); xdg != "" {
			return joinPathForOS("linux", xdg, "g8s")
		}
		if homeDir == "" {
			homeDir, _ = os.UserHomeDir()
		}
		return joinPathForOS("linux", homeDir, ".local", "state", "g8s")
	}
}

// DatabasePathForOS resolves the database path for a given target OS and environment map.
func DatabasePathForOS(goos string, envGetter func(string) string, homeDir string, scope string) string {
	if envGetter == nil {
		envGetter = os.Getenv
	}
	if env := envGetter("G8S_DB"); env != "" {
		return env
	}
	return joinPathForOS(goos, StateDirForOS(goos, envGetter, homeDir), "g8s.db")
}

// UserProfileInfo contains discovery metadata for a user profile on the system.
type UserProfileInfo struct {
	Username  string `json:"username"`
	Profile   string `json:"profile"`
	DataDir   string `json:"data_dir"`
	ConfigDir string `json:"config_dir"`
	Exists    bool   `json:"exists"`
}

// DetectUserProfiles scans the host system for user profiles and their g8s data directories.
func DetectUserProfiles() []UserProfileInfo {
	return detectUserProfiles()
}
