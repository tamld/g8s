//go:build linux

package sandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func runPreparedHelper(t *testing.T, prep *PreparedLaunch, workDir string) (string, string) {
	t.Helper()
	if prep.Cleanup != nil {
		defer prep.Cleanup()
	}
	cmd := exec.Command(prep.Argv[0], prep.Argv[1:]...)
	if workDir != "" {
		cmd.Dir = CanonicalPath(workDir)
	}
	cmd.Env = prep.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("runPreparedHelper (%s): err=%v stderr=%s", prep.Backend, err, stderr.String())
	}
	return stdout.String(), stderr.String()
}

func TestLinuxLandlockBackendDirect(t *testing.T) {
	abi := probeLandlockABI()
	if abi < 1 {
		t.Logf("Landlock LSM unavailable on kernel (abi=%d); skipping direct kernel test", abi)
		return
	}
	selfExe := selfHelperArgv(t)

	t.Run("workspace_write_blocks_escape_and_allows_worktree", func(t *testing.T) {
		wt := t.TempDir()
		outside := t.TempDir()
		runDir := t.TempDir()
		tmpDir := CanonicalPath(filepath.Join(runDir, "tmp"))
		if err := os.MkdirAll(tmpDir, 0o700); err != nil {
			t.Fatal(err)
		}
		gocacheDir := filepath.Join(tmpDir, "gocache")
		if err := os.MkdirAll(gocacheDir, 0o700); err != nil {
			t.Fatal(err)
		}

		insideFile := filepath.Join(wt, "ll-inside.txt")
		outsideFile := filepath.Join(outside, "ll-outside.txt")
		cfg := Config{
			Argv:        []string{selfExe, "-test.run=^TestSandboxHelperProcess$"},
			WorktreeDir: wt,
			RunDir:      runDir,
			Permission:  "workspace_write",
		}
		taskEnv := []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=write_both",
			"INSIDE_PATH=" + insideFile,
			"OUTSIDE_PATH=" + outsideFile,
		}
		env := ScrubEnv(os.Environ(), taskEnv, "", tmpDir, gocacheDir, "sbx-ll-write")
		writable := WritableDirsForConfig(cfg, tmpDir, gocacheDir)
		denyDirs, denyFiles := SensitiveDenySpec("", "", "")

		prep, err := buildLandlockLaunch(selfExe, abi, cfg, env, tmpDir, writable, denyDirs, denyFiles, "sbx-ll-write", makeLinuxCleanup("sbx-ll-write", tmpDir, false))
		if err != nil {
			t.Fatalf("buildLandlockLaunch: %v", err)
		}
		if prep.Mode != ModeKernelEnforced || prep.Backend != "landlock" {
			t.Fatalf("got mode=%q backend=%q, want kernel_enforced/landlock", prep.Mode, prep.Backend)
		}

		stdout, _ := runPreparedHelper(t, prep, wt)
		var res struct {
			InsideErr  string `json:"inside_err"`
			OutsideErr string `json:"outside_err"`
		}
		if uerr := json.Unmarshal([]byte(stdout), &res); uerr != nil {
			t.Fatalf("decode helper json (%q): %v", stdout, uerr)
		}
		if res.InsideErr != "" {
			t.Fatalf("landlock inside write failed: %s", res.InsideErr)
		}
		if res.OutsideErr == "" {
			t.Fatal("landlock allowed out-of-worktree write")
		}
		if _, statErr := os.Stat(outsideFile); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("outside file should not exist on disk, stat=%v", statErr)
		}
	})

	t.Run("read_only_blocks_worktree_mutation", func(t *testing.T) {
		wt := t.TempDir()
		runDir := t.TempDir()
		tmpDir := CanonicalPath(filepath.Join(runDir, "tmp"))
		if err := os.MkdirAll(tmpDir, 0o700); err != nil {
			t.Fatal(err)
		}
		gocacheDir := filepath.Join(tmpDir, "gocache")
		if err := os.MkdirAll(gocacheDir, 0o700); err != nil {
			t.Fatal(err)
		}
		existing := filepath.Join(wt, "immutable.txt")
		if err := os.WriteFile(existing, []byte("immutable"), 0o600); err != nil {
			t.Fatal(err)
		}

		cfg := Config{
			Argv:        []string{selfExe, "-test.run=^TestSandboxHelperProcess$"},
			WorktreeDir: wt,
			RunDir:      runDir,
			Permission:  "read_only",
		}
		taskEnv := []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=mutate_read_only",
			"TARGET_PATH=" + existing,
		}
		env := ScrubEnv(os.Environ(), taskEnv, "", tmpDir, gocacheDir, "sbx-ll-ro")
		writable := WritableDirsForConfig(cfg, tmpDir, gocacheDir)
		denyDirs, denyFiles := SensitiveDenySpec("", "", "")

		prep, err := buildLandlockLaunch(selfExe, abi, cfg, env, tmpDir, writable, denyDirs, denyFiles, "sbx-ll-ro", makeLinuxCleanup("sbx-ll-ro", tmpDir, false))
		if err != nil {
			t.Fatalf("buildLandlockLaunch: %v", err)
		}
		stdout, _ := runPreparedHelper(t, prep, wt)
		var res struct {
			WriteErr string `json:"write_err"`
		}
		if uerr := json.Unmarshal([]byte(stdout), &res); uerr != nil {
			t.Fatalf("decode helper json (%q): %v", stdout, uerr)
		}
		if res.WriteErr == "" {
			t.Fatal("landlock read_only allowed worktree mutation")
		}
		raw, rerr := os.ReadFile(existing)
		if rerr != nil || string(raw) != "immutable" {
			t.Fatalf("existing file altered: data=%q err=%v", string(raw), rerr)
		}
	})

	t.Run("blocks_credentials_and_f0_key_reads_while_allowing_pub", func(t *testing.T) {
		fakeHome := t.TempDir()
		fakeState := t.TempDir()
		wt := t.TempDir()
		runDir := t.TempDir()
		tmpDir := CanonicalPath(filepath.Join(runDir, "tmp"))
		if err := os.MkdirAll(tmpDir, 0o700); err != nil {
			t.Fatal(err)
		}
		gocacheDir := filepath.Join(tmpDir, "gocache")
		if err := os.MkdirAll(gocacheDir, 0o700); err != nil {
			t.Fatal(err)
		}

		for _, dir := range []string{
			filepath.Join(fakeHome, ".ssh"),
			filepath.Join(fakeHome, ".aws"),
			filepath.Join(fakeHome, ".config", "g8s"),
		} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		sshFile := filepath.Join(fakeHome, ".ssh", "id_test")
		awsFile := filepath.Join(fakeHome, ".aws", "credentials")
		provFile := filepath.Join(fakeHome, ".config", "g8s", "providers.json")
		keyFile := filepath.Join(fakeState, "f0-signing.key")
		pubFile := filepath.Join(fakeState, "f0-signing.pub")
		for _, item := range []struct {
			p, val string
		}{
			{sshFile, "SECRET-SSH-KEY"},
			{awsFile, "SECRET-AWS-CREDS"},
			{provFile, `{"secret":"sk-live"}`},
			{keyFile, "SECRET-F0-SIGNING-KEY"},
			{pubFile, "PUBLIC-F0-VERIFIER-HEX"},
		} {
			if err := os.WriteFile(item.p, []byte(item.val), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		cfg := Config{
			Argv:        []string{selfExe, "-test.run=^TestSandboxHelperProcess$"},
			WorktreeDir: wt,
			RunDir:      runDir,
			StateDir:    fakeState,
			Permission:  "read_only",
		}
		taskEnv := []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=read_paths",
			"HOME=" + fakeHome,
			"USERPROFILE=" + fakeHome,
			"CHECK_SSH=" + sshFile,
			"CHECK_AWS=" + awsFile,
			"CHECK_PROVIDERS=" + provFile,
			"CHECK_F0_KEY=" + keyFile,
			"CHECK_F0_PUB=" + pubFile,
		}
		env := ScrubEnv(os.Environ(), taskEnv, "", tmpDir, gocacheDir, "sbx-ll-cred")
		writable := WritableDirsForConfig(cfg, tmpDir, gocacheDir)
		denyDirs, denyFiles := SensitiveDenySpec(fakeHome, fakeState, "")

		prep, err := buildLandlockLaunch(selfExe, abi, cfg, env, tmpDir, writable, denyDirs, denyFiles, "sbx-ll-cred", makeLinuxCleanup("sbx-ll-cred", tmpDir, false))
		if err != nil {
			t.Fatalf("buildLandlockLaunch: %v", err)
		}
		stdout, _ := runPreparedHelper(t, prep, wt)
		var got map[string]bool
		if uerr := json.Unmarshal([]byte(stdout), &got); uerr != nil {
			t.Fatalf("decode helper json (%q): %v", stdout, uerr)
		}
		if !got["f0_pub_ok"] {
			t.Fatal("f0-signing.pub should stay readable under landlock")
		}
		for _, blockedKey := range []string{"ssh_ok", "aws_ok", "providers_ok", "f0_key_ok"} {
			if got[blockedKey] {
				t.Fatalf("landlock leaked %s (%+v)", blockedKey, got)
			}
		}
	})
}

func TestLinuxSandboxInternalsUnit(t *testing.T) {
	tok := []byte("G8S_SANDBOX_ID=abc123")
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"start_of_buffer", []byte("G8S_SANDBOX_ID=abc123\x00PATH=/bin"), true},
		{"middle_of_buffer", []byte("HOME=/h\x00G8S_SANDBOX_ID=abc123\x00PATH=/bin"), true},
		{"end_of_buffer", []byte("PATH=/bin\x00G8S_SANDBOX_ID=abc123"), true},
		{"prefix_mismatch", []byte("OTHER_G8S_SANDBOX_ID=abc123\x00"), false},
		{"suffix_mismatch", []byte("G8S_SANDBOX_ID=abc1234\x00"), false},
		{"missing", []byte("FOO=bar\x00BAZ=qux"), false},
		{"empty", nil, false},
	} {
		if got := hasExactNulEnvToken(tc.data, tok); got != tc.want {
			t.Errorf("hasExactNulEnvToken(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	tmpDir := filepath.Join(t.TempDir(), "own-tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cleanup := makeLinuxCleanup("", tmpDir, true)
	cleanup()
	if _, err := os.Stat(tmpDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ownTempDir %q removed by cleanup, stat=%v", tmpDir, err)
	}

	reapSubreaperChildren("")
	sweepLinuxSandboxProcesses("nonexistent-sbx-id-unit-test")

	if code := execShimPayload("{not-valid-json", applyLandlockPolicy); code != 126 {
		t.Fatalf("execShimPayload invalid json = %d, want 126", code)
	}
	if code := execShimPayload(`{"argv":[""]}`, applyLandlockPolicy); code != 126 {
		t.Fatalf("execShimPayload empty argv = %d, want 126", code)
	}
	if code := execShimPayload(`{"argv":["/bin/true"]}`, func(landlockPayload) error {
		return errors.New("boom")
	}); code != 126 {
		t.Fatalf("execShimPayload policy error = %d, want 126", code)
	}
	if code := execShimPayload(`{"argv":["/nonexistent/g8s-missing-bin"]}`, func(landlockPayload) error {
		return nil
	}); code != 127 {
		t.Fatalf("execShimPayload missing binary = %d, want 127", code)
	}
	okShimCfg, merr := json.Marshal(landlockPayload{
		Argv:        []string{"/bin/true"},
		WorktreeDir: t.TempDir(),
	})
	if merr != nil {
		t.Fatal(merr)
	}
	if code := execShimPayload(string(okShimCfg), func(landlockPayload) error { return nil }); code != 0 {
		t.Fatalf("execShimPayload /bin/true = %d, want 0", code)
	}

	if _, ok := probeBwrapWith(func(string) (string, error) { return "", errors.New("not found") }); ok {
		t.Fatal("probeBwrapWith expected false on lookup error")
	}
	if _, ok := probeBwrapWith(func(name string) (string, error) {
		if name == "bwrap" {
			return "/bin/false", nil
		}
		return "/bin/true", nil
	}); ok {
		t.Fatal("probeBwrapWith expected false when probe command fails")
	}
	if bin, ok := probeBwrapWith(func(string) (string, error) { return "/bin/true", nil }); !ok || bin != "/bin/true" {
		t.Fatalf("probeBwrapWith = (%q, %v), want (/bin/true, true)", bin, ok)
	}

	if got := resolveHomeDir(Config{TaskEnv: []string{"HOME=/tmp/task-home"}}); got != CanonicalPath("/tmp/task-home") {
		t.Fatalf("resolveHomeDir TaskEnv = %q", got)
	}
	if got := resolveHomeDir(Config{BaseEnv: []string{"USERPROFILE=/tmp/base-home"}}); got != CanonicalPath("/tmp/base-home") {
		t.Fatalf("resolveHomeDir BaseEnv = %q", got)
	}
	if got := resolveSSHAuthSock(Config{TaskEnv: []string{"SSH_AUTH_SOCK=/tmp/task.sock"}}); got != CanonicalPath("/tmp/task.sock") {
		t.Fatalf("resolveSSHAuthSock TaskEnv = %q", got)
	}
	if got := resolveSSHAuthSock(Config{BaseEnv: []string{"SSH_AUTH_SOCK=/tmp/base.sock"}}); got != CanonicalPath("/tmp/base.sock") {
		t.Fatalf("resolveSSHAuthSock BaseEnv = %q", got)
	}
	if got := resolveSSHAuthSock(Config{BaseEnv: []string{}}); got != "" {
		t.Fatalf("resolveSSHAuthSock empty BaseEnv = %q, want empty", got)
	}

	prepNoRun, perr := Prepare(Config{Argv: []string{"/bin/true"}, NoSandbox: true})
	if perr != nil {
		t.Fatalf("Prepare(NoSandbox, empty RunDir): %v", perr)
	}
	if strings.TrimSpace(prepNoRun.TempDir) == "" {
		t.Fatal("expected isolated TempDir when RunDir is empty")
	}
	prepNoRun.Cleanup()
	if _, serr := os.Stat(prepNoRun.TempDir); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("expected Prep cleanup to remove %q, stat=%v", prepNoRun.TempDir, serr)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	if err := addLandlockPathRule(-1, missing, unix.LANDLOCK_ACCESS_FS_READ_FILE, unix.LANDLOCK_ACCESS_FS_READ_DIR, unix.LANDLOCK_ACCESS_FS_READ_FILE); err != nil {
		t.Fatalf("addLandlockPathRule(ENOENT) returned error: %v", err)
	}

	cfg := Config{Argv: []string{"/bin/true"}, WorktreeDir: t.TempDir()}
	bwrapPrep := buildBwrapLaunch("/usr/bin/bwrap", cfg, []string{"K=V"}, "/tmp/iso", []string{cfg.WorktreeDir}, nil, nil, "sbx-u1", func() {})
	if bwrapPrep.Mode != ModeKernelEnforced || bwrapPrep.Backend != "bwrap" || bwrapPrep.SandboxID != "sbx-u1" {
		t.Fatalf("unexpected buildBwrapLaunch result: %+v", bwrapPrep)
	}
	polPrep := buildLinuxPolicyOnlyLaunch(cfg, []string{"K=V"}, "/tmp/iso", "sbx-u2", func() {})
	if polPrep.Mode != ModePolicyOnly || polPrep.Backend != "linux_policy_only" || polPrep.SandboxID != "sbx-u2" {
		t.Fatalf("unexpected buildLinuxPolicyOnlyLaunch result: %+v", polPrep)
	}
	llPrep, err := buildLandlockLaunch("", 1, cfg, []string{"K=V"}, "/tmp/iso", []string{cfg.WorktreeDir}, nil, nil, "sbx-u3", func() {})
	if err != nil {
		t.Fatalf("buildLandlockLaunch: %v", err)
	}
	if llPrep.Mode != ModeKernelEnforced || llPrep.Backend != "landlock" || len(llPrep.Env) < 3 {
		t.Fatalf("unexpected buildLandlockLaunch result: %+v", llPrep)
	}

	if abi := probeLandlockABI(); abi >= 1 {
		wt := t.TempDir()
		fd, berr := buildLandlockRuleset(landlockPayload{
			Argv:         []string{"/bin/true"},
			WorktreeDir:  wt,
			WritableDirs: []string{wt},
			SandboxID:    "sbx-ruleset-unit",
			ABI:          abi,
		})
		if berr != nil {
			t.Fatalf("buildLandlockRuleset: %v", berr)
		}
		if fd < 0 {
			t.Fatalf("expected valid ruleset fd, got %d", fd)
		}
		closeLandlockFD(fd)
	}

	step := 0
	waitReapedSubreaperChildren(func() (int, error) {
		step++
		switch step {
		case 1:
			return 42, nil
		case 2:
			return 0, unix.EINTR
		case 3:
			return 0, nil
		default:
			return -1, unix.ECHILD
		}
	})
	if step < 4 {
		t.Fatalf("waitReapedSubreaperChildren stopped early at step %d", step)
	}
	waitReapedSubreaperChildren(nil)

	terminateContainedProc(nil, nil)

	if sleepBin, serr := exec.LookPath("sleep"); serr == nil {
		sleepCmd := exec.Command(sleepBin, "10")
		sleepCmd.Env = []string{"G8S_SANDBOX_ID=unit-sweep-live"}
		if sErr := sleepCmd.Start(); sErr != nil {
			t.Fatalf("start sleep: %v", sErr)
		}
		sweepLinuxSandboxProcesses("unit-sweep-live")
		if wErr := sleepCmd.Wait(); wErr == nil {
			t.Fatal("expected sweepLinuxSandboxProcesses to terminate sleep subprocess")
		}
	}
}
