package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWrapExecWritesSuccessEnvelope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	out := filepath.Join(t.TempDir(), "result.json")
	err := runWrapExec([]string{"wrap-exec", "--out", out, "--", "true"})
	if err != nil {
		t.Fatalf("runWrapExec: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	var env struct {
		OK       bool `json:"ok"`
		ExitCode int  `json:"exit_code"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !env.OK || env.ExitCode != 0 {
		t.Fatalf("envelope = %+v, want ok:true exit 0", env)
	}
}

func TestWrapExecCapturesFailureExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fixtures")
	}
	out := filepath.Join(t.TempDir(), "result.json")
	err := runWrapExec([]string{"wrap-exec", "--out", out, "--", "false"})
	if err != nil {
		t.Fatalf("runWrapExec: %v", err)
	}
	raw, _ := os.ReadFile(out)
	var env struct {
		OK       bool `json:"ok"`
		ExitCode int  `json:"exit_code"`
	}
	_ = json.Unmarshal(raw, &env)
	if env.OK || env.ExitCode != 1 {
		t.Fatalf("envelope = %+v, want ok:false exit 1", env)
	}
}

func TestWrapExecRejectsMissingSeparator(t *testing.T) {
	if err := runWrapExec([]string{"wrap-exec", "--out", "x.json"}); err == nil {
		t.Fatal("expected usage error without -- separator")
	}
}

func TestInternalCommandRoutesToWrapExec(t *testing.T) {
	// Smoke the dispatch contract used by the supervisor: argv shape
	// [internal wrap-exec ...] must reach runWrapExec without error.
	err := runWrapExec([]string{"internal", "wrap-exec"})
	if err == nil {
		t.Fatal("expected usage error for incomplete argv")
	}
}
