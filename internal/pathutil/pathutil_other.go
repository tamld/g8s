//go:build !windows

package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDataDir returns the default POSIX data directory.
func DefaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "g8s")
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "g8s")
	}
	return filepath.Join(home, ".local", "share", "g8s")
}

// DefaultConfigDir returns the default POSIX configuration directory.
func DefaultConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "g8s")
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "g8s")
	}
	return filepath.Join(home, ".config", "g8s")
}

// DefaultCacheDir returns the default POSIX cache directory.
func DefaultCacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "g8s")
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches", "g8s")
	}
	return filepath.Join(home, ".cache", "g8s")
}

// DefaultLogsDir returns the default POSIX logs directory.
func DefaultLogsDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "g8s", "logs")
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Logs", "g8s")
	}
	return filepath.Join(home, ".local", "state", "g8s", "logs")
}

func stateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "g8s")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "g8s")
}

func dataDirForScope(scope string) string {
	if scope == ScopeSystem {
		return "/var/lib/g8s"
	}
	return DefaultDataDir()
}

func detectUserProfiles() []UserProfileInfo {
	var profiles []UserProfileInfo
	homeRoot := "/home"
	if runtime.GOOS == "darwin" {
		homeRoot = "/Users"
	}

	entries, err := os.ReadDir(homeRoot)
	if err != nil {
		home, _ := os.UserHomeDir()
		if home != "" {
			dataDir := DefaultDataDir()
			cfgDir := DefaultConfigDir()
			_, errD := os.Stat(dataDir)
			profiles = append(profiles, UserProfileInfo{
				Username:  filepath.Base(home),
				Profile:   home,
				DataDir:   dataDir,
				ConfigDir: cfgDir,
				Exists:    errD == nil,
			})
		}
		return profiles
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "Shared" || name == "lost+found" {
			continue
		}
		userHome := filepath.Join(homeRoot, name)
		var dataDir, cfgDir string
		if runtime.GOOS == "darwin" {
			dataDir = filepath.Join(userHome, "Library", "Application Support", "g8s")
			cfgDir = dataDir
		} else {
			dataDir = filepath.Join(userHome, ".local", "share", "g8s")
			cfgDir = filepath.Join(userHome, ".config", "g8s")
		}
		_, errD := os.Stat(dataDir)
		profiles = append(profiles, UserProfileInfo{
			Username:  name,
			Profile:   userHome,
			DataDir:   dataDir,
			ConfigDir: cfgDir,
			Exists:    errD == nil,
		})
	}

	return profiles
}
