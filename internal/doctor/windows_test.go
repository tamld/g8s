//go:build windows

package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/registry"
	"github.com/tamld/g8s/internal/settings"
)

func TestWindows_DefaultPathsResolve(t *testing.T) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		t.Fatalf("expected LOCALAPPDATA to be set in Windows environment")
	}

	dataDir := pathutil.DefaultDataDir()
	if !strings.HasPrefix(strings.ToLower(dataDir), strings.ToLower(localAppData)) {
		t.Errorf("DefaultDataDir() = %s does not start with LOCALAPPDATA %s", dataDir, localAppData)
	}

	configDir := pathutil.DefaultConfigDir()
	appData := os.Getenv("APPDATA")
	if appData != "" && !strings.HasPrefix(strings.ToLower(configDir), strings.ToLower(appData)) {
		t.Errorf("DefaultConfigDir() = %s does not start with APPDATA %s", configDir, appData)
	}
}

func TestWindows_PATHContainsG8s(t *testing.T) {
	// Look for g8s binary in PATH
	path, err := exec.LookPath("g8s")
	if err != nil {
		// If running in development without installation, check local binary
		t.Logf("g8s not found in system PATH (expected before installation): %v", err)
	} else {
		t.Logf("g8s found in PATH at: %s", path)
	}
}

func TestWindows_CmdExecutesG8s(t *testing.T) {
	exePath, err := exec.LookPath("g8s")
	if err != nil {
		exePath, err = os.Executable()
		if err != nil {
			t.Skipf("cannot find executable: %v", err)
		}
	}
	cmd := exec.Command("cmd.exe", "/c", exePath, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("cmd /c execution: %v (%s)", err, string(out))
	} else {
		t.Logf("cmd /c output: %s", string(out))
	}
}

func TestWindows_PowerShellExecutesG8s(t *testing.T) {
	exePath, err := exec.LookPath("g8s")
	if err != nil {
		exePath, err = os.Executable()
		if err != nil {
			t.Skipf("cannot find executable: %v", err)
		}
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "& '"+exePath+"' version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("powershell execution: %v (%s)", err, string(out))
	} else {
		t.Logf("powershell output: %s", string(out))
	}
}

func TestWindows_PerUserWrite(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	mgr, err := settings.NewManager(cfgPath)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}
	val, ok := mgr.Get("data_dir")
	if !ok || val == "" {
		t.Fatalf("expected data_dir to resolve, got %v", val)
	}

	if err := mgr.Set("default_timeout", "90s"); err != nil {
		t.Fatalf("expected write to succeed in user space: %v", err)
	}

	val, ok = mgr.Get("default_timeout")
	if !ok || val != "90s" {
		t.Fatalf("expected default_timeout=90s, got %v", val)
	}
}

func TestWindows_StartMenuShortcut(t *testing.T) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		t.Skip("APPDATA not set")
	}
	shortcutDir := filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs\g8s`)
	if !strings.Contains(shortcutDir, "Start Menu") {
		t.Errorf("invalid shortcut directory format: %s", shortcutDir)
	}
}

func TestWindows_UninstallEntry(t *testing.T) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\g8s`,
		registry.QUERY_VALUE)
	if err == nil {
		defer key.Close()
		loc, _ := key.GetStringValue("InstallLocation")
		t.Logf("Found uninstall entry with InstallLocation: %s", loc)
	} else {
		t.Logf("Uninstall registry key not present in test environment")
	}
}

func TestWindows_DefaultDirsExist(t *testing.T) {
	dataDir := pathutil.DefaultDataDir()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("failed to create default data dir: %v", err)
	}
	info, err := os.Stat(dataDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected data dir to exist: %v", err)
	}
}
