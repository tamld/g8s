package dispatch

import (
	"reflect"
	"testing"
)

func TestBuildWorkerArgv(t *testing.T) {
	opts := WorkerArgvOptions{
		Binary:          "/usr/local/bin/g8s-worker",
		PromptFile:      "/tmp/prompt.txt",
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
		"--prompt-file", "/tmp/prompt.txt",
		"--model", "gemini-3.7-flash-high",
		"--role", "collector",
		"--permission", "read_only",
		"--timeout", "30s",
		"--out", "/tmp/result.json",
		"--add-dir", "/tmp/workspace",
		"--dangerously-skip-permissions",
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
	}

	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("BuildWorkerArgv() = %v\nwant: %v", argv, want)
	}
}
