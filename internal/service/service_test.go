package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// fakeRunner records every command and can fail selected subcommands
// (keyed by argv[1]: print/bootstrap/kickstart/bootout).
type fakeRunner struct {
	cmds     [][]string
	errs     map[string]error
	onCmd    func(argv []string)
	timeouts []time.Duration
}

func (f *fakeRunner) Run(argv []string, timeout time.Duration) ([]byte, error) {
	f.cmds = append(f.cmds, argv)
	f.timeouts = append(f.timeouts, timeout)
	if f.onCmd != nil {
		f.onCmd(argv)
	}
	sub := ""
	if len(argv) > 1 {
		sub = argv[1]
	}
	if err, ok := f.errs[sub]; ok {
		return []byte("simulated failure"), err
	}
	return []byte{}, nil
}

func (f *fakeRunner) subcommands() []string {
	out := make([]string, 0, len(f.cmds))
	for _, argv := range f.cmds {
		if len(argv) > 1 {
			out = append(out, argv[1])
		}
	}
	return out
}

// serviceEnv wires a darwin-platform manager against temp paths with an
// executable stub binary.
type serviceEnv struct {
	t       *testing.T
	manager *Manager
	runner  *fakeRunner
	cfg     Config
}

func newServiceEnv(t *testing.T) *serviceEnv {
	t.Helper()
	root := t.TempDir()
	binary := filepath.Join(root, "bin", "g8s")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	// Pre-create the unit and log directories so fixtures can seed files or
	// plant symlinks inside them without depending on Install side effects.
	for _, dir := range []string{
		filepath.Join(root, "LaunchAgents"),
		filepath.Join(root, "Logs"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stub binary: %v", err)
	}
	cfg := Config{
		Label:         "com.test.g8s-worker",
		BinaryPath:    binary,
		DatabasePath:  filepath.Join(root, "state", "g8s.db"),
		PlistPath:     filepath.Join(root, "LaunchAgents", "com.test.g8s-worker.plist"),
		StdoutLogPath: filepath.Join(root, "Logs", "worker.out.log"),
		StderrLogPath: filepath.Join(root, "Logs", "worker.err.log"),
		PathEnv: strings.Join([]string{
			"/usr/bin", "/bin", filepath.Join(root, ".local", "bin"), "/usr/local/bin",
		}, string(os.PathListSeparator)),
		Home:     root,
		Platform: "darwin",
		Timeout:  5 * time.Second,
	}
	runner := &fakeRunner{}
	mgr, err := NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.WithRunner(runner)
	return &serviceEnv{t: t, manager: mgr, runner: runner, cfg: mgr.cfg}
}

func TestBuildPlistHardensTheUnitDefinition(t *testing.T) {
	env := newServiceEnv(t)
	payload, err := env.manager.buildPlist()
	if err != nil {
		t.Fatalf("buildPlist: %v", err)
	}
	text := string(payload)
	want := []string{
		"<key>Label</key>", "<string>com.test.g8s-worker</string>",
		"<key>KeepAlive</key>", "<true/>",
		"<key>ProcessType</key>", "<string>Background</string>",
		"<integer>63</integer>",
		"<string>--quiet</string>",
		env.manager.cfg.BinaryPath,
		env.manager.cfg.StdoutLogPath,
	}
	for _, fragment := range want {
		if !strings.Contains(text, fragment) {
			t.Fatalf("plist missing %q:\n%s", fragment, text)
		}
	}
	if strings.Contains(text, "RunAtLoad") {
		t.Fatal("unit must not run at load; it is kickstarted explicitly")
	}
	localBin := filepath.Join(env.manager.cfg.Home, ".local", "bin")
	sanitized := env.manager.sanitizedPathEnv()
	if !strings.Contains(sanitized, localBin) {
		t.Fatalf("sanitized PATH must preserve user localBin %s: %q", localBin, sanitized)
	}
	if !strings.Contains(sanitized, "/usr/bin") {
		t.Fatalf("sanitized PATH dropped unrelated entries: %q", sanitized)
	}
	upper := strings.ToUpper(text)
	if strings.Contains(upper, "API") || strings.Contains(upper, "TOKEN") {
		t.Fatal("encoded plist must never embed secret-shaped material")
	}
}

func TestInstallIssuesBootstrapThenKickstartWithHardenedFiles(t *testing.T) {
	env := newServiceEnv(t)
	env.runner.errs = map[string]error{"print": errors.New("not loaded")}
	if err := env.manager.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	subs := env.runner.subcommands()
	if len(subs) != 3 || subs[0] != "print" || subs[1] != "bootstrap" || subs[2] != "kickstart" {
		t.Fatalf("command sequence mismatch: %v", env.runner.subcommands())
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(env.manager.cfg.PlistPath)
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("plist mode = %v err=%v, want 0644", info.Mode().Perm(), err)
		}
		logInfo, err := os.Stat(env.manager.cfg.StdoutLogPath)
		if err != nil || logInfo.Mode().Perm() != 0o600 {
			t.Fatalf("stdout log mode = %v err=%v, want 0600", logInfo.Mode().Perm(), err)
		}
	}
	delete(env.runner.errs, "print")
	st, err := env.manager.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Loaded || st.Label != "com.test.g8s-worker" {
		t.Fatalf("status after install = %+v", st)
	}
}

func TestReinstallBootsOutBeforeBootstrap(t *testing.T) {
	env := newServiceEnv(t)
	if err := os.WriteFile(env.manager.cfg.PlistPath, []byte("<plist>old</plist>\n"), 0o644); err != nil {
		t.Fatalf("seed plist: %v", err)
	}
	if err := env.manager.Install(); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	subs := env.runner.subcommands()
	bootoutIdx, bootstrapIdx := -1, -1
	for i, sub := range subs {
		switch sub {
		case "bootout":
			bootoutIdx = i
		case "bootstrap":
			bootstrapIdx = i
		}
	}
	if bootoutIdx == -1 || bootstrapIdx == -1 || bootoutIdx > bootstrapIdx {
		t.Fatalf("bootout must precede bootstrap on reinstall: %v", subs)
	}
}

func TestSymlinkedBinaryResolvesToCanonicalPath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-g8s")
	link := filepath.Join(root, "g8s-link")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write real binary: %v", err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	mgr, err := NewManager(Config{
		Label: "com.test.g8s-worker", BinaryPath: link,
		PlistPath: filepath.Join(root, "w.plist"),
		Platform:  "darwin", Home: root,
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// macOS temp dirs live behind the /var -> /private/var symlink, so the
	// canonical form must be resolved through EvalSymlinks as well.
	wantCanonical, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("resolve expected canonical path: %v", err)
	}
	if mgr.cfg.BinaryPath != wantCanonical {
		t.Fatalf("binary = %q, want canonical %q", mgr.cfg.BinaryPath, wantCanonical)
	}
}

func TestWorldWritableBinaryRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("world-writable rejection is POSIX-only and is skipped on windows by design")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "g8s")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o777); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	// os.WriteFile masks the requested mode with the process umask; chmod
	// explicitly so the group/world-writable bits survive regardless of env.
	if err := os.Chmod(binary, 0o777); err != nil {
		t.Fatalf("chmod binary: %v", err)
	}
	_, err := NewManager(Config{Label: "l", BinaryPath: binary, Platform: "darwin", Home: root}, nil)
	if err == nil || !strings.Contains(err.Error(), "group/world-writable") {
		t.Fatalf("want group/world-writable rejection, got %v", err)
	}
}

func TestFailedBootstrapRestoresPreviousPlistBytes(t *testing.T) {
	env := newServiceEnv(t)
	env.runner.errs = map[string]error{"print": errors.New("not loaded"), "bootstrap": fmt.Errorf("exit status 5")}
	previous := []byte("<plist>previous</plist>\n")
	if err := os.WriteFile(env.manager.cfg.PlistPath, previous, 0o644); err != nil {
		t.Fatalf("seed plist: %v", err)
	}
	err := env.manager.Install()
	if err == nil || !strings.Contains(err.Error(), "bootstrap failed") {
		t.Fatalf("want bootstrap failed, got %v", err)
	}
	restored, readErr := os.ReadFile(env.manager.cfg.PlistPath)
	if readErr != nil || string(restored) != string(previous) {
		t.Fatalf("previous plist bytes not restored: %q err=%v", restored, readErr)
	}
}

func TestLifecycleRefusesWhileTasksAreActiveWithoutAnyCommand(t *testing.T) {
	env := newServiceEnv(t)
	root := t.TempDir()
	store, err := controlplane.NewControlPlane(filepath.Join(root, "cp.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	payload, err := json.Marshal(map[string]any{"prompt": "p", "timeout": "5s"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: "busy", Payload: payload, Model: "gemini-3.7-flash-high",
		AddDirs: []string{"."}, Timeout: "5s",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := store.ClaimTask(context.Background(), "worker-1", 60); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_ = task
	guarded, err := NewManager(env.manager.cfg, store)
	if err != nil {
		t.Fatalf("guarded manager: %v", err)
	}
	guarded.WithRunner(env.runner)
	if err := guarded.Install(); err == nil || !strings.Contains(err.Error(), "leased or running") {
		t.Fatalf("want leased-or-running refusal, got %v", err)
	}
	if len(env.runner.cmds) != 0 {
		t.Fatalf("refused install must issue zero commands, got %v", env.runner.subcommands())
	}
}

func TestMaintenanceGateBlocksClaimsDuringInstall(t *testing.T) {
	env := newServiceEnv(t)
	root := t.TempDir()
	store, err := controlplane.NewControlPlane(filepath.Join(root, "cp.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	payload, err := json.Marshal(map[string]any{"prompt": "p", "timeout": "5s"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if _, err := store.SubmitTask(context.Background(), controlplane.SubmitTaskRequest{
		IdempotencyKey: "gate", Payload: payload, Model: "gemini-3.7-flash-high",
		AddDirs: []string{"."}, Timeout: "5s",
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	env.runner.errs = map[string]error{"print": errors.New("not loaded")}
	var claimsDuring []int
	env.runner.onCmd = func(argv []string) {
		if len(argv) > 1 && argv[1] == "bootstrap" {
			task, err := store.ClaimTask(context.Background(), "probe", 30)
			if err != nil || task != nil {
				t.Errorf("claim during maintenance window must yield nothing: got %v err=%v", task, err)
				return
			}
			claimsDuring = append(claimsDuring, 1)
		}
	}
	guarded, err := NewManager(env.manager.cfg, store)
	if err != nil {
		t.Fatalf("guarded manager: %v", err)
	}
	guarded.WithRunner(env.runner)
	if err := guarded.Install(); err != nil {
		t.Fatalf("install under maintenance window: %v", err)
	}
	if len(claimsDuring) != 1 {
		t.Fatalf("maintenance probe did not run inside bootstrap: %d", len(claimsDuring))
	}
	claimed, err := store.ClaimTask(context.Background(), "post-install", 30)
	if err != nil || claimed == nil {
		t.Fatalf("claims must resume after install completes: task=%v err=%v", claimed, err)
	}
}

func TestStdoutLogSymlinkRejectedAndVictimUntouched(t *testing.T) {
	env := newServiceEnv(t)
	victim := filepath.Join(env.manager.cfg.Home, "victim.txt")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	if err := os.Remove(env.manager.cfg.StdoutLogPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup seeded log: %v", err)
	}
	if err := os.Symlink(victim, env.manager.cfg.StdoutLogPath); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}
	err := env.manager.Install()
	if err == nil || !strings.Contains(err.Error(), "cannot prepare private service log") {
		t.Fatalf("want private-log refusal, got %v", err)
	}
	content, readErr := os.ReadFile(victim)
	if readErr != nil || string(content) != "precious" {
		t.Fatalf("victim file mutated: %q err=%v", content, readErr)
	}
	if len(env.runner.cmds) != 0 {
		t.Fatal("no init-system command may run once path safety fails")
	}
}

func TestPlistSymlinkRejectedAndVictimUntouched(t *testing.T) {
	env := newServiceEnv(t)
	victim := filepath.Join(env.manager.cfg.Home, "launch-victim.plist")
	if err := os.WriteFile(victim, []byte("keep-me"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	if err := os.Symlink(victim, env.manager.cfg.PlistPath); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}
	err := env.manager.Install()
	if err == nil || !strings.Contains(err.Error(), "symlinked LaunchAgent plist") {
		t.Fatalf("want plist-symlink refusal, got %v", err)
	}
	content, readErr := os.ReadFile(victim)
	if readErr != nil || string(content) != "keep-me" {
		t.Fatalf("victim plist mutated: %q err=%v", content, readErr)
	}
}

func TestRestartWithoutLoadedInstallFails(t *testing.T) {
	env := newServiceEnv(t)
	env.runner.errs = map[string]error{"print": errors.New("not loaded")}
	err := env.manager.Restart()
	if err == nil || !strings.Contains(err.Error(), "not installed and loaded") {
		t.Fatalf("want not-installed refusal, got %v", err)
	}
}

func TestExecRunnerTimeoutFailsClosed(t *testing.T) {
	_, err := execRunner{}.Run([]string{"sleep", "5"}, 150*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timed-out failure, got %v", err)
	}
}

func TestUninstallPreservesDatabaseAndRemovesUnit(t *testing.T) {
	env := newServiceEnv(t)
	if err := os.MkdirAll(filepath.Dir(env.manager.cfg.DatabasePath), 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.WriteFile(env.manager.cfg.DatabasePath, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatalf("seed database: %v", err)
	}
	if err := os.WriteFile(env.manager.cfg.PlistPath, []byte("<plist/>\n"), 0o644); err != nil {
		t.Fatalf("seed plist: %v", err)
	}
	if err := env.manager.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	foundBootout := false
	for _, sub := range env.runner.subcommands() {
		if sub == "bootout" {
			foundBootout = true
		}
	}
	if !foundBootout {
		t.Fatalf("uninstall must issue bootout: %v", env.runner.subcommands())
	}
	if _, err := os.Stat(env.manager.cfg.PlistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(env.manager.cfg.DatabasePath); err != nil {
		t.Fatalf("database must be preserved: %v", err)
	}
}

func TestStatusNeverCreatesDatabaseOrLeaksPrompt(t *testing.T) {
	env := newServiceEnv(t)
	env.runner.errs = map[string]error{"print": errors.New("not loaded")}
	st, err := env.manager.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Loaded || st.DatabaseExists {
		t.Fatalf("empty host status = %+v", st)
	}
	if _, statErr := os.Stat(env.manager.cfg.DatabasePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("status created the database file: %v", statErr)
	}
}

func TestStatusRejectsCorruptDatabase(t *testing.T) {
	env := newServiceEnv(t)
	if err := os.MkdirAll(filepath.Dir(env.manager.cfg.DatabasePath), 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.WriteFile(env.manager.cfg.DatabasePath, []byte("not sqlite"), 0o600); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}
	_, err := env.manager.Status()
	if err == nil || !strings.Contains(err.Error(), "cannot inspect control-plane state") {
		t.Fatalf("want corrupt-db failure, got %v", err)
	}
}

func TestUnsupportedPlatformRejectedFailClosed(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "g8s")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	_, err := NewManager(Config{Label: "l", BinaryPath: binary, Platform: "linux", Home: root}, nil)
	if err == nil || !strings.Contains(err.Error(), "only supported on macOS") {
		t.Fatalf("want macOS-only platform guard, got %v", err)
	}
}
