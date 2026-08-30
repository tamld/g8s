//go:build windows

package pathutil

import (
	"os"
	"path/filepath"
)

// DefaultDataDir returns the default Windows user data directory.
func DefaultDataDir() string {
	return dataDirForScope(ScopeUser)
}

// DefaultConfigDir returns the default Windows configuration directory (%APPDATA%\g8s).
func DefaultConfigDir() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
	}
	if appdata == "" {
		home, _ := os.UserHomeDir()
		appdata = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appdata, "g8s")
}

// DefaultCacheDir returns the default Windows cache directory.
func DefaultCacheDir() string {
	return filepath.Join(DefaultDataDir(), "cache")
}

// DefaultLogsDir returns the default Windows logs directory.
func DefaultLogsDir() string {
	return filepath.Join(DefaultDataDir(), "logs")
}

func stateDir() string {
	return DefaultDataDir()
}

func dataDirForScope(scope string) string {
	if scope == ScopeSystem {
		progFiles := os.Getenv("PROGRAMFILES")
		if progFiles == "" {
			progFiles = `C:\Program Files`
		}
		return filepath.Join(progFiles, "g8s")
	}

	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	if local == "" {
		home, _ := os.UserHomeDir()
		local = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(local, "Programs", "g8s")
}

func detectUserProfiles() []UserProfileInfo {
	var profiles []UserProfileInfo
	usersRoot := `C:\Users`
	if sysDrive := os.Getenv("SystemDrive"); sysDrive != "" {
		usersRoot = filepath.Join(sysDrive, "Users")
	}

	entries, err := os.ReadDir(usersRoot)
	if err != nil {
		// Fallback to current user profile only
		uprofile := os.Getenv("USERPROFILE")
		if uprofile != "" {
			dataDir := filepath.Join(uprofile, "AppData", "Local", "Programs", "g8s")
			cfgDir := filepath.Join(uprofile, "AppData", "Roaming", "g8s")
			_, errD := os.Stat(dataDir)
			profiles = append(profiles, UserProfileInfo{
				Username:  filepath.Base(uprofile),
				Profile:   uprofile,
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
		if name == "Public" || name == "Default" || name == "Default User" || name == "All Users" {
			continue
		}
		userDir := filepath.Join(usersRoot, name)
		dataDir := filepath.Join(userDir, "AppData", "Local", "Programs", "g8s")
		cfgDir := filepath.Join(userDir, "AppData", "Roaming", "g8s")
		_, errD := os.Stat(dataDir)
		profiles = append(profiles, UserProfileInfo{
			Username:  name,
			Profile:   userDir,
			DataDir:   dataDir,
			ConfigDir: cfgDir,
			Exists:    errD == nil,
		})
	}

	return profiles
}
