package dispatch

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildWorkerArgv(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte("run collector task"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	opts := BuildWorkerArgvOptions{
		Binary:          "/usr/local/bin/g8s-worker",
		PromptFile:      promptPath,
		Model:           "gemini-3.8-flash-high",
		AddDirs:         []string{"/tmp/workspace"},
		SkipPermissions: true,
	}

	argv := BuildWorkerArgv(opts)
	want := []string{
		"/usr/local/bin/g8s-worker",
		"--prompt", "run collector task",
		"--model", "gemini-3.8-flash-high",
		"--add-dir", "/tmp/workspace",
		"--dangerously-skip-permissions",
		"--sandbox",
		"--output-format", "stream-json",
		"--print-timeout", "30m",
	}

	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("BuildWorkerArgv() = %v\nwant: %v", argv, want)
	}
}

func TestBuildWorkerArgvDefaults(t *testing.T) {
	opts := WorkerArgvOptions{
		Prompt: "explain the architecture",
	}

	argv := BuildWorkerArgv(opts)
	want := []string{
		"agy",
		"--prompt", "explain the architecture",
		"--sandbox",
		"--output-format", "stream-json",
		"--print-timeout", "30m",
	}

	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("BuildWorkerArgv() = %v\nwant: %v", argv, want)
	}
}

func TestBuildWorkerArgvPermissionReadOnly(t *testing.T) {
	opts := BuildWorkerArgvOptions{
		Prompt:     "read only scan",
		Permission: "read_only",
	}
	argv := BuildWorkerArgv(opts)
	found := false
	for _, a := range argv {
		if a == "--sandbox" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected --sandbox in argv for permission=read_only, got %v", argv)
	}
}

func TestBuildWorkerArgvPermissionWorkspaceWrite(t *testing.T) {
	opts := BuildWorkerArgvOptions{
		Prompt:     "write allowed",
		Permission: "workspace_write",
	}
	argv := BuildWorkerArgv(opts)
	for _, a := range argv {
		if a == "--sandbox" {
			t.Fatalf("expected NO --sandbox in argv for permission=workspace_write, got %v", argv)
		}
	}
}

func TestBuildWorkerArgvNoSandbox(t *testing.T) {
	opts := BuildWorkerArgvOptions{
		Prompt:    "no sandbox flag",
		NoSandbox: true,
	}
	argv := BuildWorkerArgv(opts)
	for _, a := range argv {
		if a == "--sandbox" {
			t.Fatalf("expected NO --sandbox in argv when NoSandbox=true, got %v", argv)
		}
	}
}

func TestBuildWorkerArgvAgreesWithAgyCLI(t *testing.T) {
	agy, err := exec.LookPath("agy")
	if err != nil {
		t.Skip("agy not in PATH")
	}
	// Run agy --help and parse the flag set
	out, err := exec.Command(agy, "--help").CombinedOutput()
	if err != nil && len(out) == 0 {
		t.Skip("failed to run agy --help")
	}
	helpText := string(out)

	args := BuildWorkerArgv(BuildWorkerArgvOptions{
		PromptFile: "/tmp/test-prompt.md",
		Model:      "gemini-3.8-flash-high",
	})
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") && arg != "--prompt" {
			flagName := strings.SplitN(arg, "=", 2)[0]
			// strip leading --
			name := strings.TrimPrefix(flagName, "--")
			if !strings.Contains(helpText, "--"+name) {
				t.Errorf("argv emits %q but agy --help does not list --%s", arg, name)
			}
		}
	}
}
