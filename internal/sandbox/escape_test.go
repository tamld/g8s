package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sandboxHelperEnv = "GO_WANT_SANDBOX_HELPER"

// TestSandboxHelperProcess serves as a zero-dependency cross-platform helper
// subprocess invoked by escape tests via os.Executable().
func TestSandboxHelperProcess(t *testing.T) {
	if os.Getenv(sandboxHelperEnv) != "1" {
		return
	}
	action := os.Getenv("HELPER_ACTION")
	switch action {
	case "write_both":
		insidePath := os.Getenv("INSIDE_PATH")
		outsidePath := os.Getenv("OUTSIDE_PATH")
		res := map[string]any{
			"inside_err":  errString(os.WriteFile(insidePath, []byte("inside-ok"), 0o600)),
			"outside_err": errString(os.WriteFile(outsidePath, []byte("escaped"), 0o600)),
		}
		emitJSONAndExit(res, 0)
	case "mutate_read_only":
		target := os.Getenv("TARGET_PATH")
		err := os.WriteFile(target, []byte("mutated"), 0o600)
		emitJSONAndExit(map[string]any{"write_err": errString(err)}, 0)
	case "read_paths":
		out := map[string]bool{}
		for _, pair := range []struct {
			key string
			env string
		}{
			{"ssh_ok", "CHECK_SSH"},
			{"aws_ok", "CHECK_AWS"},
			{"providers_ok", "CHECK_PROVIDERS"},
			{"f0_key_ok", "CHECK_F0_KEY"},
			{"f0_pub_ok", "CHECK_F0_PUB"},
		} {
			p := os.Getenv(pair.env)
			data, err := os.ReadFile(p)
			out[pair.key] = err == nil && len(data) > 0
		}
		emitJSONAndExit(out, 0)
	case "dump_env":
		m := map[string]string{}
		for _, kv := range os.Environ() {
			if k, v, ok := strings.Cut(kv, "="); ok {
				m[k] = v
			}
		}
		emitJSONAndExit(m, 0)
	case "spawn_detached":
		selfExe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "self exe: %v\n", err)
			os.Exit(2)
		}
		cmd := exec.Command(selfExe, "-test.run=^TestSandboxHelperProcess$")
		cmd.Env = append(os.Environ(), "HELPER_ACTION=sleep_detached")
		configureDetachedHelper(cmd)
		devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err == nil {
			cmd.Stdin = devNull
			cmd.Stdout = devNull
			cmd.Stderr = devNull
		}
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "start detached: %v\n", err)
			os.Exit(3)
		}
		pidFile := os.Getenv("PID_FILE")
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write pidfile: %v\n", err)
			os.Exit(4)
		}
		os.Exit(0)
	case "sleep_detached":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	default:
		os.Exit(1)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func emitJSONAndExit(v any, code int) {
	data, err := json.Marshal(v)
	if err != nil {
		os.Exit(99)
	}
	_, _ = os.Stdout.Write(data)
	os.Exit(code)
}

func selfHelperArgv(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

func TestSandboxBlocksOutOfWorktreeWrite(t *testing.T) {
	wt := t.TempDir()
	outside := t.TempDir()
	runDir := t.TempDir()
	insideFile := filepath.Join(wt, "allowed.txt")
	outsideFile := filepath.Join(outside, "escaped.txt")

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	code, mode, backend, err := RunContained(ctx, Config{
		Argv:        []string{selfHelperArgv(t), "-test.run=^TestSandboxHelperProcess$"},
		WorktreeDir: wt,
		RunDir:      runDir,
		Permission:  "workspace_write",
		TaskEnv: []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=write_both",
			"INSIDE_PATH=" + insideFile,
			"OUTSIDE_PATH=" + outsideFile,
		},
	}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("RunContained code=%d err=%v mode=%s backend=%s stderr=%s", code, err, mode, backend, stderr.String())
	}

	var res struct {
		InsideErr  string `json:"inside_err"`
		OutsideErr string `json:"outside_err"`
	}
	if uerr := json.Unmarshal(stdout.Bytes(), &res); uerr != nil {
		t.Fatalf("decode helper json (%q): %v", stdout.String(), uerr)
	}
	if res.InsideErr != "" {
		t.Fatalf("in-worktree write failed: %s", res.InsideErr)
	}
	if _, statErr := os.Stat(insideFile); statErr != nil {
		t.Fatalf("inside file missing: %v", statErr)
	}

	if runtime.GOOS == "windows" {
		if mode != ModeJobObjectEnvScrubbed {
			t.Fatalf("expected ModeJobObjectEnvScrubbed on Windows, got %q", mode)
		}
		return
	}

	if mode != ModeKernelEnforced {
		t.Fatalf("expected ModeKernelEnforced on %s (%s), got %q", runtime.GOOS, backend, mode)
	}
	if res.OutsideErr == "" {
		t.Fatalf("out-of-worktree write succeeded under %s kernel sandbox", backend)
	}
	if _, statErr := os.Stat(outsideFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside file should not exist on disk, stat=%v", statErr)
	}
}

func TestSandboxBlocksReadOnlyWorktreeMutation(t *testing.T) {
	wt := t.TempDir()
	runDir := t.TempDir()
	existing := filepath.Join(wt, "existing.txt")
	if err := os.WriteFile(existing, []byte("immutable"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	code, mode, backend, err := RunContained(ctx, Config{
		Argv:        []string{selfHelperArgv(t), "-test.run=^TestSandboxHelperProcess$"},
		WorktreeDir: wt,
		RunDir:      runDir,
		Permission:  "read_only",
		TaskEnv: []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=mutate_read_only",
			"TARGET_PATH=" + existing,
		},
	}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("RunContained code=%d err=%v stderr=%s", code, err, stderr.String())
	}

	var res struct {
		WriteErr string `json:"write_err"`
	}
	if uerr := json.Unmarshal(stdout.Bytes(), &res); uerr != nil {
		t.Fatalf("decode helper json (%q): %v", stdout.String(), uerr)
	}

	if runtime.GOOS == "windows" {
		if mode != ModeJobObjectEnvScrubbed {
			t.Fatalf("expected ModeJobObjectEnvScrubbed on Windows, got %q", mode)
		}
		return
	}

	if mode != ModeKernelEnforced {
		t.Fatalf("expected ModeKernelEnforced, got %q", mode)
	}
	if res.WriteErr == "" {
		t.Fatalf("read_only worktree mutation succeeded under %s kernel sandbox", backend)
	}
	raw, err := os.ReadFile(existing)
	if err != nil || string(raw) != "immutable" {
		t.Fatalf("existing file altered: data=%q err=%v", string(raw), err)
	}
}

func TestSandboxBlocksCredentialAndF0KeyReads(t *testing.T) {
	fakeHome := t.TempDir()
	fakeState := t.TempDir()
	wt := t.TempDir()
	runDir := t.TempDir()

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

	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	code, mode, backend, err := RunContained(ctx, Config{
		Argv:        []string{selfHelperArgv(t), "-test.run=^TestSandboxHelperProcess$"},
		WorktreeDir: wt,
		RunDir:      runDir,
		StateDir:    fakeState,
		Permission:  "read_only",
		TaskEnv: []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=read_paths",
			"HOME=" + fakeHome,
			"USERPROFILE=" + fakeHome,
			"CHECK_SSH=" + sshFile,
			"CHECK_AWS=" + awsFile,
			"CHECK_PROVIDERS=" + provFile,
			"CHECK_F0_KEY=" + keyFile,
			"CHECK_F0_PUB=" + pubFile,
		},
	}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("RunContained code=%d err=%v stderr=%s", code, err, stderr.String())
	}

	var got map[string]bool
	if uerr := json.Unmarshal(stdout.Bytes(), &got); uerr != nil {
		t.Fatalf("decode helper json (%q): %v", stdout.String(), uerr)
	}
	if !got["f0_pub_ok"] {
		t.Fatalf("expected f0-signing.pub to remain readable under %s, got %+v", backend, got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if mode != ModeKernelEnforced {
		t.Fatalf("expected ModeKernelEnforced, got %q", mode)
	}
	for _, deniedKey := range []string{"ssh_ok", "aws_ok", "providers_ok", "f0_key_ok"} {
		if got[deniedKey] {
			t.Fatalf("expected %s to be denied by %s kernel sandbox, got %+v", deniedKey, backend, got)
		}
	}
}

func TestSandboxScrubsAmbientSecretsCrossPlatform(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret-aws")
	t.Setenv("GITHUB_TOKEN", "secret-ghp")
	t.Setenv("G8S_F0_SIGNING_SECRET", "secret-f0")
	t.Setenv("G8S_SERVE_TOKEN", "secret-serve")
	t.Setenv("UNRELATED_VENDOR_API_KEY", "secret-unrelated")
	t.Setenv("OPENAI_API_KEY", "allowed-codex-key")

	wt := t.TempDir()
	runDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	code, _, _, err := RunContained(ctx, Config{
		Argv:         []string{selfHelperArgv(t), "-test.run=^TestSandboxHelperProcess$"},
		WorktreeDir:  wt,
		RunDir:       runDir,
		Permission:   "workspace_write",
		ProviderName: "codex",
		TaskEnv: []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=dump_env",
		},
	}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("RunContained code=%d err=%v stderr=%s", code, err, stderr.String())
	}

	var childEnv map[string]string
	if uerr := json.Unmarshal(stdout.Bytes(), &childEnv); uerr != nil {
		t.Fatalf("decode child env (%q): %v", stdout.String(), uerr)
	}
	for _, secretKey := range []string{
		"AWS_SECRET_ACCESS_KEY",
		"GITHUB_TOKEN",
		"G8S_F0_SIGNING_SECRET",
		"G8S_SERVE_TOKEN",
		"UNRELATED_VENDOR_API_KEY",
	} {
		if v, exists := childEnv[secretKey]; exists && v != "" {
			t.Fatalf("host secret %s leaked into sandboxed child env", secretKey)
		}
	}
	if childEnv["OPENAI_API_KEY"] != "allowed-codex-key" {
		t.Fatalf("expected provider-scoped OPENAI_API_KEY allowed, got %q", childEnv["OPENAI_API_KEY"])
	}
	if childEnv["G8S_SANDBOX_ID"] == "" {
		t.Fatal("expected G8S_SANDBOX_ID in child env")
	}
	expectedTmp := CanonicalPath(filepath.Join(runDir, "tmp"))
	if CanonicalPath(childEnv["TMPDIR"]) != expectedTmp || CanonicalPath(childEnv["TEMP"]) != expectedTmp {
		t.Fatalf("expected isolated TMPDIR/TEMP=%q, got TMPDIR=%q TEMP=%q", expectedTmp, childEnv["TMPDIR"], childEnv["TEMP"])
	}
}

func TestSandboxTerminatesDetachedGrandchild(t *testing.T) {
	wt := t.TempDir()
	runDir := t.TempDir()
	pidFile := filepath.Join(wt, "grandchild.pid")

	prep, err := Prepare(Config{
		Argv:        []string{selfHelperArgv(t), "-test.run=^TestSandboxHelperProcess$"},
		WorktreeDir: wt,
		RunDir:      runDir,
		Permission:  "workspace_write",
		TaskEnv: []string{
			sandboxHelperEnv + "=1",
			"HELPER_ACTION=spawn_detached",
			"PID_FILE=" + pidFile,
		},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() {
		if prep.Cleanup != nil {
			prep.Cleanup()
		}
	}()

	cmd := exec.Command(prep.Argv[0], prep.Argv[1:]...)
	cmd.Dir = CanonicalPath(wt)
	cmd.Env = prep.Env
	configureContainedProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start contained helper: %v", err)
	}
	releaseHandle, attachErr := attachContainedHandle(cmd)
	if attachErr != nil {
		t.Logf("attach handle: %v", attachErr)
	}
	defer func() {
		if releaseHandle != nil {
			releaseHandle()
		}
	}()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper wait failed: %v", err)
	}

	rawPID, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read grandchild pidfile: %v", err)
	}
	grandPID, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil || grandPID <= 0 {
		t.Fatalf("invalid grandchild pid %q: %v", string(rawPID), err)
	}
	t.Cleanup(func() {
		forceKillTestPID(grandPID)
	})

	if prep.Backend != "landlock" && !isTestProcessAlive(grandPID) {
		t.Logf("detached grandchild exited early before cleanup (backend=%s)", prep.Backend)
	}

	if releaseHandle != nil {
		releaseHandle()
	}
	prep.Cleanup()

	deadline := time.Now().Add(5 * time.Second)
	for isTestProcessAlive(grandPID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if isTestProcessAlive(grandPID) {
		t.Fatalf("detached grandchild pid %d survived sandbox cleanup (backend=%s)", grandPID, prep.Backend)
	}
}
