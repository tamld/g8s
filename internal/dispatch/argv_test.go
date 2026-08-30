package dispatch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildWorkerArgv(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte("run collector task"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	opts := WorkerArgvOptions{
		Binary:          "/usr/local/bin/g8s-worker",
		PromptFile:      promptPath,
		Model:           "gemini-3.7-flash-high",
		Role:            "collector",
		Permission:      "read_only",
		Timeout:         "30s",
		ResultPath:      "/tmp/result.json",
		AddDirs:         []string{"/tmp/workspace"},
		SkipPermissions: true,
	}

	argv := BuildWorkerArgv(opts)
	want := []string{
		"/usr/local/bin/g8s-worker",
		"--prompt", "run collector task",
		"--model", "gemini-3.7-flash-high",
		"--add-dir", "/tmp/workspace",
		"--dangerously-skip-permissions",
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
		"--output-format", "stream-json",
		"--print-timeout", "30m",
	}

	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("BuildWorkerArgv() = %v\nwant: %v", argv, want)
	}
}
