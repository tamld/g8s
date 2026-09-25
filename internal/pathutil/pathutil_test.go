package pathutil

import (
	"os"
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
		{
			name:    "darwin custom XDG override",
			goos:    "darwin",
			scope:   ScopeUser,
			homeDir: "/Users/alice",
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

			gotState := StateDirForOS(tt.goos, envGetter, tt.homeDir)
			if gotState == "" {
				t.Errorf("StateDirForOS() returned empty")
			}

			gotDB := DatabasePathForOS(tt.goos, envGetter, tt.homeDir, tt.scope)
			if gotDB == "" {
				t.Errorf("DatabasePathForOS() returned empty")
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

	userData := DataDirForScope(ScopeUser)
	if userData == "" {
		t.Fatalf("DataDirForScope(ScopeUser) returned empty")
	}

	sysData := DataDirForScope(ScopeSystem)
	if sysData == "" {
		t.Fatalf("DataDirForScope(ScopeSystem) returned empty")
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
	if profiles == nil {
		t.Fatalf("expected non-nil profile list")
	}

	// Create simulated profiles in a temp directory
	tempRoot := t.TempDir()
	userDir := filepath.Join(tempRoot, "testuser", ".local", "share", "g8s")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatalf("failed to create temp profile dir: %v", err)
	}
}

func TestPathutil_JoinPathForOS(t *testing.T) {
	winPath := joinPathForOS("windows", `C:\Users\Test`, "AppData", "Local")
	if winPath != `C:\Users\Test\AppData\Local` {
		t.Errorf("joinPathForOS(windows) = %s, want C:\\Users\\Test\\AppData\\Local", winPath)
	}

	linuxPath := joinPathForOS("linux", "/home/test", ".local", "share")
	if linuxPath != "/home/test/.local/share" {
		t.Errorf("joinPathForOS(linux) = %s, want /home/test/.local/share", linuxPath)
	}
}

func TestSQLiteURI(t *testing.T) {
	tests := []struct {
		name        string
		dbPath      string
		queryParams string
		want        string
	}{
		{
			name:        "in-memory with query",
			dbPath:      ":memory:",
			queryParams: "_pragma=foreign_keys(ON)",
			want:        "file::memory:?_pragma=foreign_keys(ON)",
		},
		{
			name:        "empty path",
			dbPath:      "",
			queryParams: "",
			want:        ":memory:",
		},
		{
			name:        "windows drive path with backslashes",
			dbPath:      `C:\Users\Alice\AppData\Local\g8s\g8s.db`,
			queryParams: "_txlock=immediate&_pragma=journal_mode(WAL)",
			want:        "file:///C:/Users/Alice/AppData/Local/g8s/g8s.db?_txlock=immediate&_pragma=journal_mode(WAL)",
		},
		{
			name:        "windows drive path with leading query mark",
			dbPath:      `C:\data\test.db`,
			queryParams: "?mode=ro",
			want:        "file:///C:/data/test.db?mode=ro",
		},
		{
			name:        "unix absolute path",
			dbPath:      "/var/lib/g8s/g8s.db",
			queryParams: "_pragma=busy_timeout(5000)",
			want:        "file:///var/lib/g8s/g8s.db?_pragma=busy_timeout(5000)",
		},
		{
			name:        "relative path",
			dbPath:      "test.db",
			queryParams: "_pragma=foreign_keys(ON)",
			want:        "file:test.db?_pragma=foreign_keys(ON)",
		},
		{
			name:        "injection attempt with question mark in filename",
			dbPath:      `C:\data\malicious?_pragma=journal_mode(OFF).db`,
			queryParams: "_pragma=busy_timeout(5000)",
			want:        "file:///C:/data/malicious%3F_pragma=journal_mode(OFF).db?_pragma=busy_timeout(5000)",
		},
		{
			name:        "injection attempt with hash in filename",
			dbPath:      `/var/db#frag.sqlite`,
			queryParams: "_txlock=immediate",
			want:        "file:///var/db%23frag.sqlite?_txlock=immediate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SQLiteURI(tt.dbPath, tt.queryParams)
			if got != tt.want {
				t.Errorf("SQLiteURI(%q, %q) = %q, want %q", tt.dbPath, tt.queryParams, got, tt.want)
			}
		})
	}
}
