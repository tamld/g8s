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

	wtEmpty := filepath.Join(host, "wt-empty-gitdir")
	if err := os.MkdirAll(wtEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtEmpty, ".git"), []byte("gitdir:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveGitWorktreeAdminDir(wtEmpty); got != "" {
		t.Fatalf("empty gitdir ResolveGitWorktreeAdminDir = %q, want empty", got)
	}

	wtInvalid := filepath.Join(host, "wt-invalid-gitdir")
	if err := os.MkdirAll(wtInvalid, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtInvalid, ".git"), []byte("not-a-gitdir\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveGitWorktreeAdminDir(wtInvalid); got != "" {
		t.Fatalf("invalid gitdir ResolveGitWorktreeAdminDir = %q, want empty", got)
	}
}

// envToMap converts a KEY=VALUE environment slice into a map.
func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
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
		"GOCOVERDIR=/tmp/host-cov",
		"RANDOM_VENDOR_API_KEY=vendor-secret",
		"SERVICE_DB_PASSWORD=pw-secret",
		"OPENAI_API_KEY=sk-openai",
		"CURSOR_API_KEY=crsr-secret",
		"OPENCODE_BIN=/usr/local/bin/opencode",
	}

	codexMap := envToMap(ScrubEnv(baseEnv, []string{"path=/custom/bin"}, "codex", "/tmp/iso", "/tmp/gocache", "sbx-123"))
	for _, stripped := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "GH_TOKEN",
		"NPM_TOKEN", "SSH_AUTH_SOCK", "G8S_F0_PRIVATE_SECRET", "G8S_SERVE_TOKEN",
		"G8S_STRATUM_TOKEN", "GOCOVERDIR", "RANDOM_VENDOR_API_KEY", "SERVICE_DB_PASSWORD", "CURSOR_API_KEY",
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
		{"9router", "NINEROUTER_API_KEY"},
		{"opencode", "NVIDIA_API_KEY"},
	} {
		if !ProviderAllowedEnvKeys(tc.provider)[tc.wantKey] {
			t.Fatalf("provider %q missing %q in allowlist", tc.provider, tc.wantKey)
		}
	}

	nineRouterMap := envToMap(ScrubEnv([]string{
		"OPENAI_API_KEY=sk-openai",
		"ANTHROPIC_API_KEY=sk-ant",
		"OPENROUTER_API_KEY=sk-or",
		"NVIDIA_API_KEY=nvapi-1",
		"CUSTOM_PROXY_API_KEY=clip-secret",
		"NINEROUTER_API_KEY=nr-secret",
	}, nil, "9router", "/tmp/iso", "/tmp/gocache", "sbx-9r"))
	if nineRouterMap["NINEROUTER_API_KEY"] != "nr-secret" {
		t.Fatalf("expected NINEROUTER_API_KEY preserved for 9router, got %q", nineRouterMap["NINEROUTER_API_KEY"])
	}
	for _, stripped := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "NVIDIA_API_KEY", "CUSTOM_PROXY_API_KEY"} {
		if _, exists := nineRouterMap[stripped]; exists {
			t.Fatalf("expected %s stripped from 9router allowlist", stripped)
		}
	}
	if keys := ProviderAllowedEnvKeys("9router"); len(keys) != 1 || !keys["NINEROUTER_API_KEY"] {
		t.Fatalf("expected 9router allowlist strictly restricted to NINEROUTER_API_KEY, got %v", keys)
	}
	if len(ProviderAllowedEnvKeys("opencode")) != 4 || len(ProviderAllowedEnvKeys("commandcode")) != 4 ||
		ProviderAllowedEnvKeys("opencode")["CLIPROXY"+"_API_KEY"] || ProviderAllowedEnvKeys("commandcode")["CLIPROXY"+"_API_KEY"] {
		t.Fatalf("unexpected allowlist: opencode=%v commandcode=%v", ProviderAllowedEnvKeys("opencode"), ProviderAllowedEnvKeys("commandcode"))
	}
}

// TestScrubEnv_ConnectionStringSecretShapes pins the connection-string secret
// vocabulary (#985): DATABASE_URL-style keys, webhook URLs, DSNs, and NETRC
// must be scrubbed from the ambient environment for every provider, while
// non-secret connectivity vars (proxy settings) and the per-provider allowlist
// (base URLs) still flow, and spec.Env remains the operator override path.
func TestScrubEnv_ConnectionStringSecretShapes(t *testing.T) {
	baseEnv := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/operator",
		"DATABASE_URL=postgres://user:secret@db.internal:5432/app",
		"POSTGRES_URL=postgres://user:secret@db.internal:5432/app",
		"POSTGRESQL_URL=postgres://user:secret@db.internal:5432/app",
		"POSTGRES_DSN=host=db.internal user=app password=secret",
		"MONGODB_URI=mongodb://user:secret@mongo.internal:27017/app",
		"MONGO_URL=mongodb://user:secret@mongo.internal:27017/app",
		"REDIS_URL=redis://:secret@redis.internal:6379/0",
		"SMTP_URL=smtp://user:secret@smtp.internal:587",
		"SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T000/B000/XXXX",
		"SENTRY_DSN=https://key@o0.ingest.sentry.io/1",
		"NETRC=/home/operator/.netrc",
		"MQTT_URI=mqtt://user:secret@mqtt.internal:1883",
		// Non-secret connectivity vars that must survive the scrub:
		"HTTP_PROXY=http://proxy.internal:3128",
		"HTTPS_PROXY=http://proxy.internal:3128",
		"NO_PROXY=localhost,127.0.0.1",
		// Allowlisted provider base URLs that must survive per provider:
		"OPENAI_BASE_URL=https://gateway.internal/v1",
		"ANTHROPIC_BASE_URL=https://gateway.internal/v1",
		"CURSOR_API_BASE_URL=https://gateway.internal/v1",
		"GEMINI_API_KEY=gemini-provider-key",
	}

	secretKeys := []string{
		"DATABASE_URL", "POSTGRES_URL", "POSTGRESQL_URL", "POSTGRES_DSN",
		"MONGODB_URI", "MONGO_URL", "REDIS_URL", "SMTP_URL",
		"SLACK_WEBHOOK_URL", "SENTRY_DSN", "NETRC", "MQTT_URI",
	}

	for _, provider := range []string{"agy", "codex", "claude", "opencode", "commandcode", "qwen", "cursor", "9router"} {
		got := envToMap(ScrubEnv(baseEnv, nil, provider, "/tmp/iso", "/tmp/gocache", "sbx-"+provider))
		for _, key := range secretKeys {
			if v, exists := got[key]; exists {
				t.Errorf("provider %s: connection-string secret %s leaked (value %q)", provider, key, v)
			}
		}
		// Proxy variables carry endpoints, not credentials — they must survive.
		if got["HTTPS_PROXY"] != "http://proxy.internal:3128" || got["NO_PROXY"] != "localhost,127.0.0.1" {
			t.Errorf("provider %s: proxy vars unexpectedly stripped: %+v", provider, got)
		}
	}

	// Per-provider allowlist is the override path for connection-shaped URLs:
	codexMap := envToMap(ScrubEnv(baseEnv, nil, "codex", "", "", ""))
	if codexMap["OPENAI_BASE_URL"] != "https://gateway.internal/v1" {
		t.Errorf("expected OPENAI_BASE_URL allowlisted for codex, got %q", codexMap["OPENAI_BASE_URL"])
	}
	claudeMap := envToMap(ScrubEnv(baseEnv, nil, "claude", "", "", ""))
	if claudeMap["ANTHROPIC_BASE_URL"] != "https://gateway.internal/v1" {
		t.Errorf("expected ANTHROPIC_BASE_URL allowlisted for claude, got %q", claudeMap["ANTHROPIC_BASE_URL"])
	}
	cursorMap := envToMap(ScrubEnv(baseEnv, nil, "cursor", "", "", ""))
	if cursorMap["CURSOR_API_BASE_URL"] != "https://gateway.internal/v1" {
		t.Errorf("expected CURSOR_API_BASE_URL allowlisted for cursor, got %q", cursorMap["CURSOR_API_BASE_URL"])
	}
	agyMap := envToMap(ScrubEnv(baseEnv, nil, "agy", "", "", ""))
	if agyMap["GEMINI_API_KEY"] != "gemini-provider-key" {
		t.Errorf("expected GEMINI_API_KEY allowlisted for agy, got %q", agyMap["GEMINI_API_KEY"])
	}
	// OPENAI_BASE_URL is NOT on agy's allowlist — ambient value must be scrubbed.
	if _, exists := agyMap["OPENAI_BASE_URL"]; exists {
		t.Errorf("expected ambient OPENAI_BASE_URL stripped for agy (not allowlisted)")
	}

	// spec.Env (taskEnv) overrides scrubbing for operator-declared provider config:
	agyOverride := envToMap(ScrubEnv(baseEnv, []string{"OPENAI_BASE_URL=https://task-declared.internal/v1"}, "agy", "", "", ""))
	if agyOverride["OPENAI_BASE_URL"] != "https://task-declared.internal/v1" {
		t.Errorf("expected taskEnv OPENAI_BASE_URL override to win for agy, got %q", agyOverride["OPENAI_BASE_URL"])
	}
	// ...but even taskEnv cannot resurrect a secret-shaped key once it is the
	// declared vocabulary — taskEnv wins over scrubbing by design, so verify the
	// documented behavior: taskEnv entries are applied verbatim.
	agyTask := envToMap(ScrubEnv(nil, []string{"DATABASE_URL=postgres://task-declared@db.internal/app"}, "agy", "", "", ""))
	if agyTask["DATABASE_URL"] != "postgres://task-declared@db.internal/app" {
		t.Errorf("expected taskEnv entries applied verbatim, got %+v", agyTask)
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
	if strings.Contains(joined, "--tmpfs /tmp") {
		t.Fatalf("bwrap args must not shadow host /tmp with empty tmpfs: %s", joined)
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

func TestPrepare_RequireKernelEnforced_FailClosed(t *testing.T) {
	runDir := t.TempDir()
	_, err := Prepare(Config{
		Argv:                  []string{"sh", "-c", "true"},
		WorktreeDir:           t.TempDir(),
		RunDir:                runDir,
		StateDir:              t.TempDir(),
		Permission:            "read_only",
		NoSandbox:             true,
		RequireKernelEnforced: true,
	})
	if !errors.Is(err, ErrKernelSandboxRequired) {
		t.Fatalf("expected ErrKernelSandboxRequired when NoSandbox+RequireKernelEnforced, got %v", err)
	}

	for _, degraded := range []struct {
		mode    ContainmentMode
		backend string
	}{
		{ModePolicyOnly, "linux_policy_only"},
		{ModeJobObjectEnvScrubbed, "windows_job_object"},
	} {
		cleaned := false
		_, cerr := enforceContainmentRequirement(&PreparedLaunch{
			Argv:    []string{"true"},
			Mode:    degraded.mode,
			Backend: degraded.backend,
			Cleanup: func() { cleaned = true },
		}, true)
		if !errors.Is(cerr, ErrKernelSandboxRequired) {
			t.Fatalf("mode %s: expected ErrKernelSandboxRequired, got %v", degraded.mode, cerr)
		}
		if !cleaned {
			t.Fatalf("mode %s: expected Cleanup hook invoked on fail-closed rejection", degraded.mode)
		}
	}

	cleaned := false
	okLaunch, oerr := enforceContainmentRequirement(&PreparedLaunch{
		Argv:    []string{"true"},
		Mode:    ModeKernelEnforced,
		Backend: "seatbelt",
		Cleanup: func() { cleaned = true },
	}, true)
	if oerr != nil || okLaunch == nil || cleaned {
		t.Fatalf("expected ModeKernelEnforced accepted without cleanup, got launch=%v err=%v cleaned=%v", okLaunch, oerr, cleaned)
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

	if got := exitCodeFromWait(nil, nil); got != 0 {
		t.Fatalf("exitCodeFromWait(nil, nil) = %d, want 0", got)
	}
	if got := exitCodeFromWait(errors.New("boom"), context.Canceled); got != -1 {
		t.Fatalf("exitCodeFromWait(err, Canceled) = %d, want -1", got)
	}
	if got := exitCodeFromWait(errors.New("boom"), nil); got != 1 {
		t.Fatalf("exitCodeFromWait(err, nil) = %d, want 1", got)
	}
}
