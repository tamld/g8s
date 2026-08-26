package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReportErrorWritesToConfiguredWriter(t *testing.T) {
	var buf bytes.Buffer
	reportError(errors.New("boom"), &buf)
	want := fmt.Sprintf("%s: boom", AppName)
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("reportError output = %q, want it to contain %q", buf.String(), want)
	}
}

func TestDatabasePathHonorsEnvOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom", "gatekeepers.db")
	t.Setenv("G8S_DB", override)

	got, err := databasePath()
	if err != nil {
		t.Fatalf("databasePath() error = %v", err)
	}
	if got != override {
		t.Fatalf("databasePath() = %q, want env override %q", got, override)
	}
}

func TestDatabasePathDefaultsToHomeStateDirectory(t *testing.T) {
	t.Setenv("G8S_DB", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("os.UserHomeDir unavailable in this environment: %v", err)
	}

	got, err := databasePath()
	if err != nil {
		t.Fatalf("databasePath() error = %v", err)
	}
	want := filepath.Join(home, ".local", "state", "g8s", "g8s.db")
	if got != want {
		t.Fatalf("databasePath() = %q, want %q", got, want)
	}
	if info, err := os.Stat(filepath.Dir(got)); err != nil || !info.IsDir() {
		t.Fatalf("parent directory of default path was not created 0700: stat err=%v", err)
	}
}

func TestDatabasePathCreatesParentDirectoriesWithRestrictedPermissions(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "deep", "nested")
	t.Setenv("G8S_DB", filepath.Join(nested, "g8s.db"))

	if _, err := databasePath(); err != nil {
		t.Fatalf("databasePath() error = %v", err)
	}
	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat nested dir: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("created directory perms = %o, want no group/other access", perm)
		}
	}
}

type stringFlagAccumulator interface {
	String() string
	Set(string) error
}

func TestPathFlagsAccumulatesRepeatedValuesInOrder(t *testing.T) {
	var paths pathFlags

	for _, p := range []string{"/tmp/alpha", "/tmp/beta", "/tmp/gamma"} {
		if err := paths.Set(p); err != nil {
			t.Fatalf("Set(%q) error = %v", p, err)
		}
	}
	if paths.String() != "/tmp/alpha,/tmp/beta,/tmp/gamma" {
		t.Fatalf("String() = %q, want comma-joined ordered list", paths.String())
	}
	if len([]string(paths)) != 3 {
		t.Fatalf("len(paths) = %d, want 3", len([]string(paths)))
	}
	var _ stringFlagAccumulator = &paths
}

func TestPrintUsageMentionsEveryLiveCommand(t *testing.T) {
	usage := captureUsage(t)
	for _, cmd := range []string{"submit", "get", "resume", "tasks", "lineage", "children", "receipt", "doctor", "service", "analyze", "vault", "worker", "mcp", "roles", "permissions", "version"} {
		if !strings.Contains(usage, cmd) {
			t.Errorf("usage text missing live command %q", cmd)
		}
	}
}

// captureUsage redirects stdout while printUsage runs so the advertised
// command surface can be asserted without spawning a subprocess.
func captureUsage(t *testing.T) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	printUsage()
	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}
