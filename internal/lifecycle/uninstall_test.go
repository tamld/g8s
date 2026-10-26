package lifecycle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeService struct {
	present     bool
	where       string
	uninstalled int
	err         error
}

func (f *fakeService) Detect() (bool, string) { return f.present, f.where }
func (f *fakeService) Uninstall() error {
	f.uninstalled++
	return f.err
}

func writeJSON(t *testing.T, p string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

type uninstallFixture struct {
	home, bin, cursor, agy, state, sess, repo string
	svc                                       *fakeService
}

func newUninstallFixture(t *testing.T) *uninstallFixture {
	t.Helper()
	home := t.TempDir()
	f := &uninstallFixture{home: home, svc: &fakeService{present: true, where: filepath.Join(home, "Library", "LaunchAgents", "com.g8s.worker.plist")}}
	f.bin = filepath.Join(home, ".local", "bin", "g8s")
	if err := os.MkdirAll(filepath.Dir(f.bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.bin, []byte("bin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BackupPath(f.bin), []byte("bak"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.cursor = filepath.Join(home, ".cursor", "mcp.json")
	writeJSON(t, f.cursor, map[string]any{
		"mcpServers": map[string]any{"g8s": map[string]any{"command": f.bin}, "other": map[string]any{"command": "x"}},
		"theme":      "dark",
	})
	f.agy = filepath.Join(home, ".gemini", "antigravity-cli", "mcp", "g8s.json")
	writeJSON(t, f.agy, map[string]any{"mcpServers": map[string]any{"g8s": map[string]any{"command": f.bin}}})
	f.state = filepath.Join(home, ".local", "state", "g8s")
	f.sess = filepath.Join(home, ".local", "state", "g8s-sess-abc")
	for _, d := range []string{filepath.Join(f.state, "worktrees", "wt1"), f.sess} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A user repository that must never be touched.
	f.repo = filepath.Join(home, "code", "myrepo")
	if err := os.MkdirAll(filepath.Join(f.repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *uninstallFixture) opts(purge bool) UninstallOptions {
	return UninstallOptions{
		BinaryPath: f.bin,
		GOOS:       "linux",
		MCPConfigs: []MCPConfig{
			{IDE: "cursor", Path: f.cursor},
			{IDE: "antigravity", Path: f.agy, Dedicated: true},
			{IDE: "windsurf", Path: filepath.Join(f.home, "missing.json")},
		},
		Service:    f.svc,
		StateDirs:  []string{f.state, filepath.Join(f.state, "logs"), f.sess, f.sess, filepath.Join(f.home, "nonexistent-g8s"), ""},
		PurgeState: purge,
		Home:       f.home,
	}
}

func kinds(items []UninstallItem) string {
	var parts []string
	for _, it := range items {
		parts = append(parts, it.Kind)
	}
	return strings.Join(parts, ",")
}

func TestPlanUninstallTable(t *testing.T) {
	cases := []struct {
		name      string
		purge     bool
		wantKinds string
		wantKept  int
	}{
		{"default keeps state", false, "service-unit,mcp-entry,mcp-entry,binary-backup,binary", 2},
		{"purge-state plans state dirs", true, "service-unit,mcp-entry,mcp-entry,state-dir,state-dir,binary-backup,binary", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newUninstallFixture(t)
			plan := PlanUninstall(f.opts(tc.purge))
			if !plan.DryRun {
				t.Error("plan must be dry-run")
			}
			if got := kinds(plan.Items); got != tc.wantKinds {
				t.Errorf("kinds=%s want %s", got, tc.wantKinds)
			}
			if len(plan.Kept) != tc.wantKept {
				t.Errorf("kept=%+v want %d", plan.Kept, tc.wantKept)
			}
			for _, it := range plan.Items {
				if it.Action != ActionWouldRemove {
					t.Errorf("item %+v not would_remove", it)
				}
			}
			// Planning never mutates.
			for _, p := range []string{f.bin, BackupPath(f.bin), f.agy, f.state, f.sess} {
				if _, err := os.Stat(p); err != nil {
					t.Errorf("plan mutated %s: %v", p, err)
				}
			}
			if f.svc.uninstalled != 0 {
				t.Error("plan invoked service uninstall")
			}
		})
	}
}

func TestApplyUninstallRemovesOnlyPlannedItems(t *testing.T) {
	f := newUninstallFixture(t)
	opts := f.opts(true)
	rep, err := ApplyUninstall(opts, PlanUninstall(opts))
	if err != nil {
		t.Fatalf("ApplyUninstall: %v", err)
	}
	if rep.DryRun {
		t.Error("applied report must not be dry-run")
	}
	for _, it := range rep.Items {
		if it.Action != ActionRemoved {
			t.Errorf("item %+v not removed", it)
		}
	}
	for _, p := range []string{f.bin, BackupPath(f.bin), f.agy, f.state, f.sess} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists (err=%v)", p, err)
		}
	}
	if f.svc.uninstalled != 1 {
		t.Errorf("service uninstall calls=%d want 1", f.svc.uninstalled)
	}
	cursor := readJSON(t, f.cursor)
	servers, ok := cursor["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("cursor mcpServers missing: %+v", cursor)
	}
	if _, ok := servers["g8s"]; ok {
		t.Error("cursor g8s entry not removed")
	}
	if _, ok := servers["other"]; !ok || cursor["theme"] != "dark" {
		t.Errorf("foreign cursor config lost: %+v", cursor)
	}
	if _, err := os.Stat(filepath.Join(f.repo, ".git")); err != nil {
		t.Errorf("user repo touched: %v", err)
	}
}

func TestApplyUninstallWindowsRenamesBinaryAside(t *testing.T) {
	f := newUninstallFixture(t)
	opts := f.opts(false)
	opts.GOOS = "windows"
	opts.Service = nil
	if err := os.WriteFile(f.bin+".uninstall-pending", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyUninstall(opts, PlanUninstall(opts)); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(f.bin + ".uninstall-pending"); err != nil || string(b) != "bin" {
		t.Errorf("pending=%q err=%v", b, err)
	}
	if _, err := os.Stat(f.bin); !os.IsNotExist(err) {
		t.Errorf("binary still at original path")
	}
}

func TestApplyUninstallReportsFailures(t *testing.T) {
	f := newUninstallFixture(t)
	f.svc.err = errors.New("launchctl refused")
	opts := f.opts(false)
	plan := PlanUninstall(opts)
	plan.Items = append(plan.Items, UninstallItem{Kind: "bogus", Path: "/x", Action: ActionWouldRemove})
	rep, err := ApplyUninstall(opts, plan)
	if err == nil || !strings.Contains(err.Error(), "launchctl refused") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err=%v", err)
	}
	failed := 0
	for _, it := range rep.Items {
		if it.Action == ActionFailed {
			failed++
		}
	}
	if failed != 2 {
		t.Errorf("failed items=%d want 2 (%+v)", failed, rep.Items)
	}
	// Remaining items still applied independently.
	if _, err := os.Stat(f.bin); !os.IsNotExist(err) {
		t.Error("binary removal should proceed despite service failure")
	}
	// Missing service hook at apply time is a failure, not a panic.
	opts.Service = nil
	if _, err := ApplyUninstall(opts, &UninstallReport{Items: []UninstallItem{{Kind: KindService, Path: "x"}}}); err == nil {
		t.Error("expected error for nil service hook")
	}
}

func TestPlanUninstallKeepsUnsafeBinaries(t *testing.T) {
	home := t.TempDir()
	repoBin := filepath.Join(home, "src", "g8s", "bin", "g8s")
	if err := os.MkdirAll(filepath.Join(home, "src", "g8s", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, bin, wantSub string
	}{
		{"dev build inside git work tree", repoBin, "git working tree"},
		{"homebrew cellar", "/opt/homebrew/Cellar/g8s/0.20.0/bin/g8s", "package-managed"},
		{"not named g8s", filepath.Join(home, "tool"), "not g8s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := PlanUninstall(UninstallOptions{BinaryPath: tc.bin, Home: home})
			if len(plan.Items) != 0 {
				t.Fatalf("unsafe binary planned for removal: %+v", plan.Items)
			}
			if len(plan.Kept) != 1 || !strings.Contains(plan.Kept[0].Detail, tc.wantSub) {
				t.Fatalf("kept=%+v want detail containing %q", plan.Kept, tc.wantSub)
			}
		})
	}
}

func TestSafeStateDirTable(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "g8s-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, dir string
		ok        bool
	}{
		{"xdg state", filepath.Join(home, ".local", "state", "g8s"), true},
		{"session state", filepath.Join(home, ".local", "state", "g8s-sess-x"), true},
		{"relative", "g8s", false},
		{"empty", "", false},
		{"no g8s in name", filepath.Join(home, ".local", "state"), false},
		{"home itself", home, false},
		{"git repository", repo, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := SafeStateDir(tc.dir, home); (err == nil) != tc.ok {
				t.Errorf("SafeStateDir(%q)=%v want ok=%v", tc.dir, err, tc.ok)
			}
		})
	}
	// A g8s-named ancestor of home is refused.
	g8sParent := filepath.Join(t.TempDir(), "g8s-root")
	if err := SafeStateDir(g8sParent, filepath.Join(g8sParent, "home", "u")); err == nil {
		t.Error("ancestor of home must be refused")
	}
}

func TestPlanUninstallRefusesUnsafeStateDir(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "g8s-checkout")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	plan := PlanUninstall(UninstallOptions{StateDirs: []string{repo}, PurgeState: true, Home: home})
	if len(plan.Items) != 0 || len(plan.Kept) != 1 || !strings.Contains(plan.Kept[0].Detail, "git repository") {
		t.Fatalf("plan=%+v", plan)
	}
	// ApplyUninstall re-checks safety even for a tampered plan.
	rep, err := ApplyUninstall(UninstallOptions{Home: home}, &UninstallReport{Items: []UninstallItem{{Kind: KindStateDir, Path: repo}}})
	if err == nil || rep.Items[0].Action != ActionFailed {
		t.Fatalf("tampered plan must fail: %+v %v", rep, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatal("repository was removed")
	}
}

func TestRemoveMCPEntryEdgeCases(t *testing.T) {
	dir := t.TempDir()
	// Dedicated file with extra top-level keys is rewritten, not deleted.
	p := filepath.Join(dir, "g8s.json")
	writeJSON(t, p, map[string]any{"mcpServers": map[string]any{"g8s": 1}, "note": "keep"})
	if err := removeMCPEntry(MCPConfig{Path: p, Dedicated: true}); err != nil {
		t.Fatal(err)
	}
	if m := readJSON(t, p); m["note"] != "keep" {
		t.Errorf("extra key lost: %+v", m)
	}
	// No mcpServers object: no-op.
	q := filepath.Join(dir, "plain.json")
	writeJSON(t, q, map[string]any{"x": 1})
	if err := removeMCPEntry(MCPConfig{Path: q}); err != nil {
		t.Fatal(err)
	}
	// Invalid JSON and missing file are errors.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeMCPEntry(MCPConfig{Path: bad}); err == nil {
		t.Error("invalid JSON must error")
	}
	if err := removeMCPEntry(MCPConfig{Path: filepath.Join(dir, "nope.json")}); err == nil {
		t.Error("missing file must error")
	}
	if mcpHasG8s(bad) || mcpHasG8s(q) || mcpHasG8s(filepath.Join(dir, "nope.json")) {
		t.Error("mcpHasG8s false positives")
	}
}

func TestDefaultDiscovery(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "state", "g8s-sess-zz"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfgs := DefaultMCPConfigs(home)
	if len(cfgs) == 0 {
		t.Fatal("no MCP config locations discovered")
	}
	dedicated := 0
	for _, c := range cfgs {
		if !strings.HasPrefix(c.Path, home) && !strings.Contains(c.Path, "Cursor") && !strings.Contains(c.Path, "Claude") {
			t.Errorf("config path %s outside home", c.Path)
		}
		if c.Dedicated {
			dedicated++
		}
	}
	if dedicated != 1 {
		t.Errorf("dedicated configs=%d want 1 (antigravity)", dedicated)
	}
	dirs := DefaultStateDirs(home)
	found := false
	for _, d := range dirs {
		if strings.HasSuffix(d, "g8s-sess-zz") {
			found = true
		}
	}
	if !found {
		t.Errorf("session dir not discovered in %v", dirs)
	}
}
