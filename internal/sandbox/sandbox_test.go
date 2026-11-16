package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCanonicalPathAndWorktreeAdmin(t *testing.T) {
	if got := CanonicalPath(""); got != "" {
		t.Fatalf("CanonicalPath(\"\") = %q, want empty", got)
	}
	if got := CanonicalPath("bad\x00path"); got != "" {
		t.Fatalf("CanonicalPath with NUL = %q, want empty", got)
	}

	tmp := t.TempDir()
	missingChild := filepath.Join(tmp, "nested", "leaf.txt")
	canonChild := CanonicalPath(missingChild)
	if !strings.HasSuffix(canonChild, filepath.Join("nested", "leaf.txt")) {
		t.Fatalf("CanonicalPath(%q) = %q missing trailing suffix", missingChild, canonChild)
	}
	if runtime.GOOS == "darwin" && strings.HasPrefix(tmp, "/var/") && !strings.HasPrefix(canonChild, "/private/var/") {
		t.Fatalf("macOS CanonicalPath(%q) = %q did not resolve /var -> /private/var", missingChild, canonChild)
	}

	if got := ResolveGitWorktreeAdminDir(""); got != "" {
		t.Fatalf("ResolveGitWorktreeAdminDir(\"\") = %q, want empty", got)
	}

	regularRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(regularRepo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := ResolveGitWorktreeAdminDir(regularRepo); got != "" {
		t.Fatalf("regular repo ResolveGitWorktreeAdminDir = %q, want empty", got)
	}

	host := t.TempDir()
	adminDir := filepath.Join(host, ".git", "worktrees", "wt-rel")
	if err := os.MkdirAll(adminDir, 0o700); err != nil {
		t.Fatal(err)
	}
	wtRel := filepath.Join(host, "wt-rel")
	if err := os.MkdirAll(wtRel, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtRel, ".git"), []byte("gitdir: ../.git/worktrees/wt-rel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := ResolveGitWorktreeAdminDir(wtRel), CanonicalPath(adminDir); got != want {
		t.Fatalf("relative gitdir ResolveGitWorktreeAdminDir = %q, want %q", got, want)
	}

	wtAbs := filepath.Join(host, "wt-abs")
	if err := os.MkdirAll(wtAbs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtAbs, ".git"), []byte("gitdir: "+adminDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := ResolveGitWorktreeAdminDir(wtAbs), CanonicalPath(adminDir); got != want {
		t.Fatalf("absolute gitdir ResolveGitWorktreeAdminDir = %q, want %q", got, want)
	}
}

func TestScrubEnvAndProviderAllowlist(t *testing.T) {
	baseEnv := []string{
		"PATH=/usr/bin:/bin",
		"AWS_ACCESS_KEY_ID=akia-secret",
		"AWS_SECRET_ACCESS_KEY=aws-secret",
		"GITHUB_TOKEN=ghp-secret",
		"GH_TOKEN=gho-secret",
		"NPM_TOKEN=npm-secret",
		"SSH_AUTH_SOCK=/tmp/ssh.sock",
		"G8S_F0_PRIVATE_SECRET=f0-secret",
		"G8S_SERVE_TOKEN=serve-secret",
		"G8S_STRATUM_TOKEN=st1:S2:999:sig",
		"RANDOM_VENDOR_API_KEY=vendor-secret",
		"SERVICE_DB_PASSWORD=pw-secret",
		"OPENAI_API_KEY=sk-openai",
		"CURSOR_API_KEY=crsr-secret",
		"OPENCODE_BIN=/usr/local/bin/opencode",
	}

	envToMap := func(env []string) map[string]string {
		m := make(map[string]string, len(env))
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				m[k] = v
			}
		}
		return m
	}

	codexMap := envToMap(ScrubEnv(baseEnv, []string{"path=/custom/bin"}, "codex", "/tmp/iso", "/tmp/gocache", "sbx-123"))
	for _, stripped := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "GH_TOKEN",
		"NPM_TOKEN", "SSH_AUTH_SOCK", "G8S_F0_PRIVATE_SECRET", "G8S_SERVE_TOKEN",
		"G8S_STRATUM_TOKEN", "RANDOM_VENDOR_API_KEY", "SERVICE_DB_PASSWORD", "CURSOR_API_KEY",
	} {
		if _, exists := codexMap[stripped]; exists {
			t.Fatalf("expected %s to be stripped for codex, got value", stripped)
		}
	}
	if codexMap["OPENAI_API_KEY"] != "sk-openai" {
		t.Fatalf("expected OPENAI_API_KEY allowed for codex, got %q", codexMap["OPENAI_API_KEY"])
	}
	if codexMap["OPENCODE_BIN"] != "/usr/local/bin/opencode" {
		t.Fatalf("expected OPENCODE_BIN preserved, got %q", codexMap["OPENCODE_BIN"])
	}
	if codexMap["TMPDIR"] != "/tmp/iso" || codexMap["TMP"] != "/tmp/iso" || codexMap["TEMP"] != "/tmp/iso" {
		t.Fatalf("isolated temp vars mismatch: %+v", codexMap)
	}
	if codexMap["GOCACHE"] != "/tmp/gocache" || codexMap["G8S_SANDBOX_ID"] != "sbx-123" {
		t.Fatalf("cache/sandbox vars mismatch: %+v", codexMap)
	}
	if codexMap["path"] != "/custom/bin" {
		t.Fatalf("expected taskEnv override, got %q", codexMap["path"])
	}

	cursorMap := envToMap(ScrubEnv(baseEnv, []string{"GOCACHE=/explicit/cache"}, "cursor", "/tmp/iso", "/tmp/gocache", "sbx-456"))
	if cursorMap["CURSOR_API_KEY"] != "crsr-secret" {
		t.Fatalf("expected CURSOR_API_KEY allowed for cursor, got %q", cursorMap["CURSOR_API_KEY"])
	}
	if _, exists := cursorMap["OPENAI_API_KEY"]; exists {
		t.Fatal("expected OPENAI_API_KEY stripped for cursor")
	}
	if cursorMap["GOCACHE"] != "/explicit/cache" {
		t.Fatalf("expected explicit taskEnv GOCACHE preserved, got %q", cursorMap["GOCACHE"])
	}

	for _, tc := range []struct {
		provider string
		wantKey  string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"agy", "GEMINI_API_KEY"},
		{"qwen", "DASHSCOPE_API_KEY"},
		{"9router", "OPENROUTER_API_KEY"},
		{"opencode", "NVIDIA_API_KEY"},
	} {
		if !ProviderAllowedEnvKeys(tc.provider)[tc.wantKey] {
			t.Fatalf("provider %q missing %q in allowlist", tc.provider, tc.wantKey)
		}
	}
}

func TestSensitiveDenyAndWritableDirs(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	sock := filepath.Join(t.TempDir(), "agent.sock")

	denyDirs, denyFiles := SensitiveDenySpec(home, state, sock)
	if !slices.Contains(denyDirs, CanonicalPath(filepath.Join(home, ".ssh"))) {
		t.Fatalf("expected .ssh in denyDirs: %v", denyDirs)
	}
	if !slices.Contains(denyDirs, CanonicalPath(filepath.Join(home, ".aws"))) {
		t.Fatalf("expected .aws in denyDirs: %v", denyDirs)
	}
	if !slices.Contains(denyFiles, CanonicalPath(filepath.Join(home, ".config", "g8s", "providers.json"))) {
		t.Fatalf("expected providers.json in denyFiles: %v", denyFiles)
	}
	if !slices.Contains(denyFiles, CanonicalPath(filepath.Join(state, "f0-signing.key"))) {
		t.Fatalf("expected f0-signing.key in denyFiles: %v", denyFiles)
	}
	if !slices.Contains(denyFiles, CanonicalPath(sock)) {
		t.Fatalf("expected sshAuthSock in denyFiles: %v", denyFiles)
	}
	if slices.Contains(denyFiles, CanonicalPath(filepath.Join(state, "f0-signing.pub"))) {
		t.Fatal("f0-signing.pub must NOT be in denyFiles")
	}

	wt := t.TempDir()
	runDir := t.TempDir()
	tmpDir := filepath.Join(runDir, "tmp")
	gocacheDir := filepath.Join(tmpDir, "gocache")
	extraDir := t.TempDir()

	rw := WritableDirsForConfig(Config{
		WorktreeDir:    wt,
		RunDir:         runDir,
		Permission:     "workspace_write",
		ExtraWriteDirs: []string{extraDir, extraDir},
	}, tmpDir, gocacheDir)
	if !slices.Contains(rw, CanonicalPath(wt)) || !slices.Contains(rw, CanonicalPath(runDir)) || !slices.Contains(rw, CanonicalPath(extraDir)) {
		t.Fatalf("workspace_write missing expected writable dirs: %v", rw)
	}

	ro := WritableDirsForConfig(Config{
		WorktreeDir:    wt,
		RunDir:         runDir,
		Permission:     "read_only",
		ExtraWriteDirs: []string{extraDir},
	}, tmpDir, gocacheDir)
	if slices.Contains(ro, CanonicalPath(wt)) {
		t.Fatalf("read_only must NOT include worktreeDir %q in writableDirs: %v", wt, ro)
	}
	if !slices.Contains(ro, CanonicalPath(runDir)) || !slices.Contains(ro, CanonicalPath(tmpDir)) {
		t.Fatalf("read_only missing runDir/tmpDir: %v", ro)
	}
}

func TestBuildSeatbeltBwrapAndLandlockPaths(t *testing.T) {
	prof, err := BuildSeatbeltProfile(
		[]string{`/tmp/wt-"quoted"`, `/tmp/run`},
		[]string{`/tmp/home/.ssh`},
		[]string{`/tmp/state/f0-signing.key`},
	)
	if err != nil {
		t.Fatalf("BuildSeatbeltProfile: %v", err)
	}
	for _, want := range []string{
		"(version 1)",
		"(deny file-write*)",
		`(subpath "/tmp/wt-\"quoted\"")`,
		"(deny file-read*",
		`(subpath "/tmp/home/.ssh")`,
		`(literal "/tmp/state/f0-signing.key")`,
	} {
		if !strings.Contains(prof, want) {
			t.Fatalf("Seatbelt profile missing %q:\n%s", want, prof)
		}
	}
	if _, err := BuildSeatbeltProfile([]string{"/tmp/bad\npath"}, nil, nil); err == nil {
		t.Fatal("expected newline path to be rejected by BuildSeatbeltProfile")
	}

	bwrapArgs := BuildBwrapArgs(
		"/usr/bin/bwrap",
		[]string{"sh", "-c", "true"},
		"",
		[]string{"/work/wt", "/run/tmp"},
		[]string{"/fakehome/u/.ssh", "/fakehome/u/.missing", "/work"},
		[]string{"/state/f0-signing.key", "/work/wt"},
		func(p string) (bool, bool) {
			switch p {
			case "/work/wt", "/run/tmp", "/fakehome/u/.ssh":
				return true, true
			case "/state/f0-signing.key":
				return false, true
			default:
				return false, false
			}
		},
	)
	joined := strings.Join(bwrapArgs, " ")
	for _, want := range []string{
		"--die-with-parent",
		"--ro-bind / /",
		"--bind /work/wt /work/wt",
		"--tmpfs /fakehome/u/.ssh",
		"--ro-bind /dev/null /state/f0-signing.key",
		"-- sh -c true",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("bwrap args missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, ".missing") {
		t.Fatalf("bwrap args should skip non-existent deny path: %s", joined)
	}
	if strings.Contains(joined, "--tmpfs /work ") {
		t.Fatalf("bwrap args should skip overlapping parent dir /work: %s", joined)
	}

	tree := CanonicalPath(t.TempDir())
	for _, sub := range []string{
		filepath.Join(tree, "home", "u", ".ssh"),
		filepath.Join(tree, "home", "u", "proj"),
		filepath.Join(tree, "state"),
		filepath.Join(tree, "usr", "bin"),
	} {
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	keyPath := filepath.Join(tree, "state", "f0-signing.key")
	pubPath := filepath.Join(tree, "state", "f0-signing.pub")
	if err := os.WriteFile(keyPath, []byte("priv"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubPath, []byte("pub"), 0o600); err != nil {
		t.Fatal(err)
	}
	allow, err := ComputeLandlockReadAllowPaths(
		tree,
		[]string{filepath.Join(tree, "home", "u", ".ssh")},
		[]string{keyPath},
		os.ReadDir,
	)
	if err != nil {
		t.Fatalf("ComputeLandlockReadAllowPaths: %v", err)
	}
	if slices.Contains(allow, filepath.Join(tree, "home", "u", ".ssh")) {
		t.Fatalf(".ssh must be excluded from allowPaths: %v", allow)
	}
	if slices.Contains(allow, keyPath) {
		t.Fatalf("f0-signing.key must be excluded from allowPaths: %v", allow)
	}
	if !slices.Contains(allow, pubPath) || !slices.Contains(allow, filepath.Join(tree, "usr")) {
		t.Fatalf("expected pubPath and usr in allowPaths: %v", allow)
	}

	emptyAllow, err := ComputeLandlockReadAllowPaths(tree, nil, nil, nil)
	if err != nil || !slices.Equal(emptyAllow, []string{tree}) {
		t.Fatalf("empty deny ComputeLandlockReadAllowPaths = (%v, %v), want [%s]", emptyAllow, err, tree)
	}
	outsideAllow, err := ComputeLandlockReadAllowPaths(filepath.Join(tree, "usr"), nil, []string{keyPath}, os.ReadDir)
	if err != nil || !slices.Equal(outsideAllow, []string{filepath.Join(tree, "usr")}) {
		t.Fatalf("disjoint root ComputeLandlockReadAllowPaths = (%v, %v)", outsideAllow, err)
	}
	wantErr := errors.New("boom")
	if _, err := ComputeLandlockReadAllowPaths(tree, nil, []string{keyPath}, func(string) ([]os.DirEntry, error) {
		return nil, wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("expected readDir error propagation, got %v", err)
	}
}

func TestPrepareValidationAndNoSandbox(t *testing.T) {
	if _, err := Prepare(Config{}); err == nil {
		t.Fatal("expected empty argv error")
	}
	for _, cfg := range []Config{
		{Argv: []string{"true"}, WorktreeDir: "/tmp/bad\nwt"},
		{Argv: []string{"true"}, RunDir: "/tmp/bad\x00run"},
		{Argv: []string{"true"}, StateDir: "/tmp/bad\rstate"},
		{Argv: []string{"true"}, AddDirs: []string{"/tmp/bad\nadd"}},
		{Argv: []string{"true"}, ExtraWriteDirs: []string{"/tmp/bad\nextra"}},
	} {
		if _, err := Prepare(cfg); err == nil {
			t.Fatalf("expected control character path error for %+v", cfg)
		}
	}

	stateDir := t.TempDir()
	prep, err := Prepare(Config{
		Argv:         []string{"sh", "-c", "true"},
		WorktreeDir:  t.TempDir(),
		StateDir:     stateDir,
		Permission:   "workspace_write",
		ProviderName: "codex",
		NoSandbox:    true,
	})
	if err != nil {
		t.Fatalf("Prepare NoSandbox: %v", err)
	}
	if prep.Mode != ModePolicyOnly || prep.Backend != "disabled" {
		t.Fatalf("got mode=%q backend=%q, want policy_only/disabled", prep.Mode, prep.Backend)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "sandbox-gocache")); err != nil {
		t.Fatalf("expected stateDir/sandbox-gocache to exist: %v", err)
	}
	if _, err := os.Stat(prep.TempDir); err != nil {
		t.Fatalf("expected TempDir to exist: %v", err)
	}
	prep.Cleanup()
	if _, err := os.Stat(prep.TempDir); !os.IsNotExist(err) {
		t.Fatalf("expected TempDir removed after Cleanup, err=%v", err)
	}
}

func TestRunContainedTimeoutAndNonZeroExit(t *testing.T) {
	wt := t.TempDir()
	runDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	code, _, _, err := RunContained(ctx, Config{
		Argv:        []string{selfHelperArgv(t), "-test.run=^TestSandboxHelperProcess$"},
		WorktreeDir: wt,
		RunDir:      runDir,
		Permission:  "read_only",
		BaseEnv:     os.Environ(),
		TaskEnv: []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=sleep_detached",
			"SSH_AUTH_SOCK=" + filepath.Join(t.TempDir(), "s.sock"),
		},
	}, &stdout, &stderr)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got err=%v code=%d", err, code)
	}

	codeNonZero, _, _, errNonZero := RunContained(context.Background(), Config{
		Argv:        []string{selfHelperArgv(t), "-test.run=^TestSandboxHelperProcess$"},
		WorktreeDir: wt,
		RunDir:      runDir,
		Permission:  "read_only",
		TaskEnv: []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=unknown_exit_1",
		},
	}, &stdout, &stderr)
	if errNonZero != nil || codeNonZero == 0 {
		t.Fatalf("expected non-zero exit without transport err, got code=%d err=%v", codeNonZero, errNonZero)
	}
}
