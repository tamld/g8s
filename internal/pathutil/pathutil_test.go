package pathutil

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathutil_TableDrivenOS(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		scope      string
		homeDir    string
		env        map[string]string
		wantData   string
		wantConfig string
		wantCache  string
		wantLogs   string
	}{
		{
			name:    "windows user scope with LOCALAPPDATA and APPDATA",
			goos:    "windows",
			scope:   ScopeUser,
			homeDir: `C:\Users\Alice`,
			env: map[string]string{
				"LOCALAPPDATA": `C:\Users\Alice\AppData\Local`,
				"APPDATA":      `C:\Users\Alice\AppData\Roaming`,
				"USERPROFILE":  `C:\Users\Alice`,
			},
			wantData:   `C:\Users\Alice\AppData\Local\Programs\g8s`,
			wantConfig: `C:\Users\Alice\AppData\Roaming\g8s`,
			wantCache:  `C:\Users\Alice\AppData\Local\Programs\g8s\cache`,
			wantLogs:   `C:\Users\Alice\AppData\Local\Programs\g8s\logs`,
		},
		{
			name:    "windows fallback when LOCALAPPDATA is empty",
			goos:    "windows",
			scope:   ScopeUser,
			homeDir: `C:\Users\Bob`,
			env: map[string]string{
				"USERPROFILE": `C:\Users\Bob`,
			},
			wantData:   `C:\Users\Bob\AppData\Local\Programs\g8s`,
			wantConfig: `C:\Users\Bob\AppData\Roaming\g8s`,
			wantCache:  `C:\Users\Bob\AppData\Local\Programs\g8s\cache`,
			wantLogs:   `C:\Users\Bob\AppData\Local\Programs\g8s\logs`,
		},
		{
			name:    "windows system scope",
			goos:    "windows",
			scope:   ScopeSystem,
			homeDir: `C:\Users\Alice`,
			env: map[string]string{
				"PROGRAMFILES": `C:\Program Files`,
			},
			wantData:   `C:\Program Files\g8s`,
			wantConfig: `C:\Users\Alice\AppData\Roaming\g8s`,
			wantCache:  `C:\Users\Alice\AppData\Local\Programs\g8s\cache`,
			wantLogs:   `C:\Users\Alice\AppData\Local\Programs\g8s\logs`,
		},
		{
			name:       "linux default XDG paths",
			goos:       "linux",
			scope:      ScopeUser,
			homeDir:    "/home/alice",
			env:        map[string]string{},
			wantData:   "/home/alice/.local/share/g8s",
			wantConfig: "/home/alice/.config/g8s",
			wantCache:  "/home/alice/.cache/g8s",
			wantLogs:   "/home/alice/.local/state/g8s/logs",
		},
		{
			name:    "linux custom XDG paths",
			goos:    "linux",
			scope:   ScopeUser,
			homeDir: "/home/alice",
			env: map[string]string{
				"XDG_DATA_HOME":   "/custom/data",
				"XDG_CONFIG_HOME": "/custom/config",
				"XDG_CACHE_HOME":  "/custom/cache",
				"XDG_STATE_HOME":  "/custom/state",
			},
			wantData:   "/custom/data/g8s",
			wantConfig: "/custom/config/g8s",
			wantCache:  "/custom/cache/g8s",
			wantLogs:   "/custom/state/g8s/logs",
		},
		{
			name:       "darwin default paths",
			goos:       "darwin",
			scope:      ScopeUser,
			homeDir:    "/Users/alice",
			env:        map[string]string{},
			wantData:   "/Users/alice/Library/Application Support/g8s",
			wantConfig: "/Users/alice/Library/Application Support/g8s",
			wantCache:  "/Users/alice/Library/Caches/g8s",
			wantLogs:   "/Users/alice/Library/Logs/g8s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envGetter := func(key string) string {
				return tt.env[key]
			}

			gotData := DataDirForOS(tt.goos, envGetter, tt.homeDir, tt.scope)
			if filepath.Clean(gotData) != filepath.Clean(tt.wantData) {
				t.Errorf("DataDirForOS() = %v, want %v", gotData, tt.wantData)
			}

			gotConfig := ConfigDirForOS(tt.goos, envGetter, tt.homeDir)
			if filepath.Clean(gotConfig) != filepath.Clean(tt.wantConfig) {
				t.Errorf("ConfigDirForOS() = %v, want %v", gotConfig, tt.wantConfig)
			}

			gotCache := CacheDirForOS(tt.goos, envGetter, tt.homeDir)
			if filepath.Clean(gotCache) != filepath.Clean(tt.wantCache) {
				t.Errorf("CacheDirForOS() = %v, want %v", gotCache, tt.wantCache)
			}

			gotLogs := LogsDirForOS(tt.goos, envGetter, tt.homeDir)
			if filepath.Clean(gotLogs) != filepath.Clean(tt.wantLogs) {
				t.Errorf("LogsDirForOS() = %v, want %v", gotLogs, tt.wantLogs)
			}
		})
	}
}

func TestPathutil_CurrentPlatformDefaults(t *testing.T) {
	dataDir := DefaultDataDir()
	if dataDir == "" {
		t.Fatalf("DefaultDataDir() returned empty")
	}

	configDir := DefaultConfigDir()
	if configDir == "" {
		t.Fatalf("DefaultConfigDir() returned empty")
	}

	cacheDir := DefaultCacheDir()
	if cacheDir == "" {
		t.Fatalf("DefaultCacheDir() returned empty")
	}

	logsDir := DefaultLogsDir()
	if logsDir == "" {
		t.Fatalf("DefaultLogsDir() returned empty")
	}

	stateDir := DefaultStateDir()
	if stateDir == "" {
		t.Fatalf("DefaultStateDir() returned empty")
	}

	dbPath := DefaultDatabasePath()
	if dbPath == "" {
		t.Fatalf("DefaultDatabasePath() returned empty")
	}

	evidenceDir := DefaultEvidenceDir()
	if evidenceDir == "" {
		t.Fatalf("DefaultEvidenceDir() returned empty")
	}
}

func TestPathutil_DatabasePathOverride(t *testing.T) {
	customDB := filepath.Join(t.TempDir(), "custom.db")
	t.Setenv("G8S_DB", customDB)

	got := DefaultDatabasePath()
	if got != customDB {
		t.Fatalf("expected DefaultDatabasePath() = %s, got %s", customDB, got)
	}

	gotForOS := DatabasePathForOS(runtime.GOOS, func(k string) string {
		if k == "G8S_DB" {
			return customDB
		}
		return ""
	}, "", ScopeUser)
	if gotForOS != customDB {
		t.Fatalf("expected DatabasePathForOS() = %s, got %s", customDB, gotForOS)
	}
}

func TestPathutil_DetectUserProfiles(t *testing.T) {
	profiles := DetectUserProfiles()
	// Must not panic and return at least 0 profiles
	if profiles == nil {
		t.Fatalf("expected non-nil profile list")
	}
}
