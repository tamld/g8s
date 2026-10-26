package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/lifecycle"
)

// fakeGitHub serves a v9.9.9 release whose archive targets the host platform.
func fakeGitHub(t *testing.T, bin []byte) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := "g8s"
	if runtime.GOOS == "windows" {
		name = "g8s.exe"
	}
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(bin)
	_ = tw.Close()
	_ = gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	assetName := lifecycle.AssetCandidates("v9.9.9", runtime.GOOS, runtime.GOARCH)[0]
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz fixture does not cover the windows zip layout; covered in internal/lifecycle")
	}

	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/tamld/g8s/releases/latest":
			_ = json.NewEncoder(w).Encode(lifecycle.Release{
				TagName: "v9.9.9",
				HTMLURL: srv.URL + "/release",
				Assets: []lifecycle.Asset{
					{Name: assetName, URL: srv.URL + "/dl/archive"},
					{Name: lifecycle.ChecksumsAsset, URL: srv.URL + "/dl/sums"},
				},
			})
		case "/dl/archive":
			_, _ = w.Write(archive)
		case "/dl/sums":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withLifecycleSeams(t *testing.T, srv *httptest.Server, target string) {
	t.Helper()
	oldBase, oldClient, oldTarget, oldSmoke := lifecycleAPIBase, lifecycleHTTPClient, lifecycleTargetPath, lifecycleSmoke
	lifecycleAPIBase = srv.URL
	lifecycleHTTPClient = func(time.Duration) lifecycle.HTTPDoer { return srv.Client() }
	lifecycleTargetPath = target
	lifecycleSmoke = func(string) error { return nil }
	t.Cleanup(func() {
		lifecycleAPIBase, lifecycleHTTPClient, lifecycleTargetPath, lifecycleSmoke = oldBase, oldClient, oldTarget, oldSmoke
	})
}

func TestRunUpdateCheckAndApplyOffline(t *testing.T) {
	srv := fakeGitHub(t, []byte("NEW-BINARY"))
	target := filepath.Join(t.TempDir(), "g8s")
	if err := os.WriteFile(target, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	withLifecycleSeams(t, srv, target)

	cases := []struct {
		name        string
		args        []string
		wantKind    string
		wantApplied bool
		wantContent string
	}{
		{"check json makes no changes", []string{"--check", "--json"}, `"check_only": true`, false, "OLD"},
		{"check human output", []string{"--check"}, "update available", false, "OLD"},
		{"apply installs verified binary", []string{"--json"}, `"applied": true`, true, "NEW-BINARY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdoutWithPterm(t, func() { runLifecycle("update", tc.args) })
			if !strings.Contains(out, tc.wantKind) {
				t.Fatalf("output missing %q:\n%s", tc.wantKind, out)
			}
			got, _ := os.ReadFile(target)
			if string(got) != tc.wantContent {
				t.Fatalf("target=%q want %q", got, tc.wantContent)
			}
		})
	}
	if b, err := os.ReadFile(lifecycle.BackupPath(target)); err != nil || string(b) != "OLD" {
		t.Fatalf("backup=%q err=%v", b, err)
	}
	// Human output for the applied path.
	out := captureStdoutWithPterm(t, func() { runLifecycle("update", []string{"--force"}) })
	if !strings.Contains(out, "verified") {
		t.Fatalf("human apply output missing verification line:\n%s", out)
	}
}

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("G8S_STATE_DIR", filepath.Join(home, ".local", "state", "g8s"))
	return home
}

func TestRunUninstallDryRunAndApply(t *testing.T) {
	home := isolateHome(t)
	agy := filepath.Join(home, ".gemini", "antigravity-cli", "mcp", "g8s.json")
	if err := os.MkdirAll(filepath.Dir(agy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agy, []byte(`{"mcpServers":{"g8s":{"command":"g8s"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".local", "state", "g8s")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}

	// Default (no --yes) is a dry-run listing and removes nothing.
	out := captureStdoutWithPterm(t, func() { runLifecycle("uninstall", []string{"--json", "--purge-state"}) })
	if !strings.Contains(out, `"kind": "uninstall_plan"`) || !strings.Contains(out, `"dry_run": true`) || !strings.Contains(out, agy) {
		t.Fatalf("unexpected plan output:\n%s", out)
	}
	if _, err := os.Stat(agy); err != nil {
		t.Fatal("dry-run removed MCP config")
	}
	// --dry-run wins over --yes.
	_ = captureStdoutWithPterm(t, func() { runLifecycle("uninstall", []string{"--yes", "--dry-run", "--purge-state"}) })
	if _, err := os.Stat(state); err != nil {
		t.Fatal("--dry-run --yes removed state")
	}
	// Human dry-run rendering.
	out = captureStdoutWithPterm(t, func() { runLifecycle("uninstall", nil) })
	if !strings.Contains(out, "DRY-RUN") {
		t.Fatalf("human output missing DRY-RUN banner:\n%s", out)
	}

	out = captureStdoutWithPterm(t, func() { runLifecycle("uninstall", []string{"--yes", "--purge-state", "--json"}) })
	if !strings.Contains(out, `"kind": "uninstall_result"`) {
		t.Fatalf("unexpected apply output:\n%s", out)
	}
	for _, p := range []string{agy, state} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s not removed (err=%v)", p, err)
		}
	}
	// The test binary itself (g8s.test) must never be selected for removal.
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(exe); err != nil {
			t.Fatalf("test executable removed: %v", err)
		}
	}
	out = captureStdoutWithPterm(t, func() { runLifecycle("uninstall", []string{"--yes"}) })
	if !strings.Contains(out, "uninstall result") {
		t.Fatalf("human apply output:\n%s", out)
	}
}

func TestServiceHookDetect(t *testing.T) {
	home := t.TempDir()
	h := newServiceHook(home, "/nonexistent/g8s")
	if present, _ := h.Detect(); present {
		t.Fatal("no unit file should be detected in empty home")
	}
	var unit string
	switch runtime.GOOS {
	case "darwin":
		unit = filepath.Join(home, "Library", "LaunchAgents", "com.g8s.worker.plist")
	case "linux":
		unit = filepath.Join(home, ".config", "systemd", "user", "g8s.service")
	default:
		t.Skip("unit-file detection covers darwin/linux")
	}
	if err := os.MkdirAll(filepath.Dir(unit), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if present, where := h.Detect(); !present || where != unit {
		t.Fatalf("Detect()=%v,%q want true,%q", present, where, unit)
	}
}
