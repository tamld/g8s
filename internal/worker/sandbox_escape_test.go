package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const workerSandboxHelperEnv = "GO_WANT_WORKER_SANDBOX_HELPER"

// TestWorkerSandboxHelper executes in a child process during real Supervisor
// RunOnce containment integration tests.
func TestWorkerSandboxHelper(t *testing.T) {
	if os.Getenv(workerSandboxHelperEnv) != "1" || !slices.Contains(os.Args, "-test.run=^TestWorkerSandboxHelper$") {
		return
	}
	insidePath := os.Getenv("INSIDE_PATH")
	outsidePath := os.Getenv("OUTSIDE_PATH")
	insideErr := ""
	if insidePath != "" {
		if err := os.WriteFile(insidePath, []byte("allowed"), 0o600); err != nil {
			insideErr = err.Error()
		}
	}
	outsideErr := ""
	if outsidePath != "" {
		if err := os.WriteFile(outsidePath, []byte("escaped"), 0o600); err != nil {
			outsideErr = err.Error()
		}
	}
	res := map[string]string{
		"inside_err":  insideErr,
		"outside_err": outsideErr,
	}
	data, err := json.Marshal(res)
	if err != nil {
		os.Exit(99)
	}
	_, _ = os.Stdout.Write(data)
	os.Exit(0)
}

func TestWorkerRecordsContainmentModeAndSuppressesReadOnlyYolo(t *testing.T) {
	ctx := context.Background()
	selfExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	t.Run("workspace_write_records_mode_and_blocks_out_of_worktree", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		wt := t.TempDir()
		outside := t.TempDir()
		insideFile := filepath.Join(wt, "ok.txt")
		outsideFile := filepath.Join(outside, "escape.txt")

		t.Setenv(workerSandboxHelperEnv, "1")
		t.Setenv("INSIDE_PATH", insideFile)
		t.Setenv("OUTSIDE_PATH", outsideFile)

		sup := NewSupervisor(env.store, env.runDir,
			WithPollInterval(5*time.Millisecond),
			WithWorktreeIsolator(&fakeWorktreeIsolator{dir: wt}),
			WithProviderCommandResolver(func(prompt, provider, model, timeout string) ([]string, error) {
				return []string{selfExe, "-test.run=^TestWorkerSandboxHelper$"}, nil
			}),
		)

		submitTask(t, env, "task-sbx-kernel", 1, map[string]any{
			"permission":  "workspace_write",
			"provider":    "opencode",
			"model":       "free",
			"prompt":      "verify sandbox containment",
			"result_mode": "stdout",
		})

		task, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-sbx-1", LeaseSeconds: 10})
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if task == nil || len(task.Result) == 0 {
			t.Fatal("expected completed task result")
		}

		var resultMap map[string]any
		if uerr := json.Unmarshal(task.Result, &resultMap); uerr != nil {
			t.Fatalf("decode task result: %v (%s)", uerr, string(task.Result))
		}
		wantMode := "kernel_enforced"
		if runtime.GOOS == "windows" {
			wantMode = "job_object_env_scrubbed"
		}
		if resultMap["containment_mode"] != wantMode {
			t.Fatalf("containment_mode = %v, want %q (result=%s)", resultMap["containment_mode"], wantMode, string(task.Result))
		}
		if s, ok := resultMap["sandbox_backend"].(string); !ok || s == "" {
			t.Fatalf("expected non-empty sandbox_backend, got %v", resultMap["sandbox_backend"])
		}
		if _, statErr := os.Stat(insideFile); statErr != nil {
			t.Fatalf("expected inside file created: %v (result=%s)", statErr, string(task.Result))
		}
		if runtime.GOOS != "windows" {
			stdoutStr, _ := resultMap["stdout"].(string)
			var helperRes struct {
				InsideErr  string `json:"inside_err"`
				OutsideErr string `json:"outside_err"`
			}
			if uerr := json.Unmarshal([]byte(stdoutStr), &helperRes); uerr != nil || helperRes.OutsideErr == "" {
				t.Fatalf("expected kernel sandbox outside_err in stdout (%q, decodeErr=%v, result=%s)", stdoutStr, uerr, string(task.Result))
			}
			if _, statErr := os.Stat(outsideFile); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("expected outside file blocked by kernel sandbox, stat=%v (result=%s)", statErr, string(task.Result))
			}
		}
	})

	t.Run("no_sandbox_records_policy_only", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		wt := t.TempDir()
		t.Setenv(workerSandboxHelperEnv, "1")
		t.Setenv("INSIDE_PATH", "")
		t.Setenv("OUTSIDE_PATH", "")

		sup := NewSupervisor(env.store, env.runDir,
			WithPollInterval(5*time.Millisecond),
			WithWorktreeIsolator(&fakeWorktreeIsolator{dir: wt}),
			WithProviderCommandResolver(func(prompt, provider, model, timeout string) ([]string, error) {
				return []string{selfExe, "-test.run=^TestWorkerSandboxHelper$"}, nil
			}),
		)

		submitTask(t, env, "task-sbx-nosbx", 1, map[string]any{
			"permission":  "workspace_write",
			"provider":    "opencode",
			"model":       "free",
			"prompt":      "verify policy fallback",
			"result_mode": "stdout",
			"no_sandbox":  true,
		})

		task, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-sbx-2", LeaseSeconds: 10})
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		var resultMap map[string]any
		if uerr := json.Unmarshal(task.Result, &resultMap); uerr != nil {
			t.Fatalf("decode task result: %v", uerr)
		}
		if resultMap["containment_mode"] != "policy_only" || resultMap["sandbox_backend"] != "disabled" {
			t.Fatalf("got containment_mode=%v sandbox_backend=%v, want policy_only/disabled",
				resultMap["containment_mode"], resultMap["sandbox_backend"])
		}
	})

	t.Run("read_only_suppresses_yolo_and_sets_plan_flags", func(t *testing.T) {
		roTemplates := DefaultCLIProviderTemplatesForPermission("read_only")
		for name, argv := range roTemplates {
			joined := strings.Join(argv, " ")
			if strings.Contains(joined, "--yolo") || strings.Contains(joined, "--dangerously-skip-permissions") {
				t.Fatalf("read_only template for %s must not include auto-write flag: %v", name, argv)
			}
		}
		if !strings.Contains(strings.Join(roTemplates["qwen"], " "), "--approval-mode plan") {
			t.Fatalf("expected qwen read_only template to pass --approval-mode plan: %v", roTemplates["qwen"])
		}
		if !strings.Contains(strings.Join(roTemplates["cursor"], " "), "--mode plan") {
			t.Fatalf("expected cursor read_only template to pass --mode plan: %v", roTemplates["cursor"])
		}
		if !strings.Contains(strings.Join(roTemplates["codex"], " "), "--sandbox read-only") {
			t.Fatalf("expected codex read_only template to pass --sandbox read-only: %v", roTemplates["codex"])
		}

		wwTemplates := DefaultCLIProviderTemplates()
		if !strings.Contains(strings.Join(wwTemplates["commandcode"], " "), "--yolo") {
			t.Fatalf("expected workspace_write template for commandcode to keep --yolo: %v", wwTemplates["commandcode"])
		}

		sanitized := sanitizeReadOnlyCLIArgv([]string{"qwen", "--yolo", "--dangerously-skip-permissions", "-p", "hi"}, "qwen", "read_only")
		joined := strings.Join(sanitized, " ")
		if strings.Contains(joined, "--yolo") || strings.Contains(joined, "--dangerously-skip-permissions") || !strings.Contains(joined, "--approval-mode plan") {
			t.Fatalf("unexpected sanitized qwen read_only argv: %v", sanitized)
		}

		env := newWorkerEnv(t, nil)
		wt := t.TempDir()
		sup := NewSupervisor(env.store, env.runDir,
			WithRunner(env.runner),
			WithWorktreeIsolator(&fakeWorktreeIsolator{dir: wt}),
			WithPollInterval(2*time.Millisecond),
			WithProviderCommandResolver(func(prompt, provider, model, timeout string) ([]string, error) {
				return []string{"commandcode", "--no-session", "--yolo", "-p", prompt}, nil
			}),
		)
		var captured []string
		done := make(chan struct{})
		close(done)
		env.runner.factory = func(opts SpawnOptions) Child {
			captured = append([]string{}, opts.Argv...)
			return &instantChild{done: done}
		}
		submitTask(t, env, "task-ro-no-yolo", 1, map[string]any{
			"permission":  "read_only",
			"provider":    "commandcode",
			"model":       "free",
			"prompt":      "inspect code",
			"result_mode": "stdout",
		})
		if _, err := sup.RunOnce(ctx, RunOptions{WorkerID: "w-sbx-3", LeaseSeconds: 10}); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if strings.Contains(strings.Join(captured, " "), "--yolo") {
			t.Fatalf("RunOnce did not strip --yolo on read_only task: %v", captured)
		}
	})
}
