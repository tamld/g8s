package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/sandbox"
)

func TestWorker_PolicyEnforcementReportAndPreSpawnRejection(t *testing.T) {
	ctx := t.Context()
	selfExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	t.Run("require_kernel_sandbox_with_no_sandbox_fails_closed_before_spawn", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		wt := t.TempDir()
		ranMarker := filepath.Join(wt, "must_not_run.txt")

		sup := NewSupervisor(env.store, env.runDir,
			WithPollInterval(5*time.Millisecond),
			WithWorktreeIsolator(&fakeWorktreeIsolator{dir: wt}),
			WithRequireKernelSandbox(true),
			WithProviderCommandResolver(func(prompt, provider, model, timeout string) ([]string, error) {
				return []string{selfExe, "-test.run=^TestWorkerSandboxHelper$"}, nil
			}),
		)
		t.Setenv(workerSandboxHelperEnv, "1")
		t.Setenv("INSIDE_PATH", ranMarker)

		submitTask(t, env, "task-policy-reject-1", 3, map[string]any{
			"permission":  "workspace_write",
			"provider":    "opencode",
			"model":       "mock-v1",
			"role":        "implementer",
			"receipt_id":  "rcpt-strict-1",
			"no_sandbox":  true,
			"result_mode": "stdout",
		})

		task, rerr := sup.RunOnce(ctx, RunOptions{WorkerID: "w-policy-rej", LeaseSeconds: 10})
		if rerr != nil {
			t.Fatalf("RunOnce: %v", rerr)
		}
		if task == nil || task.State != controlplane.StateWorkerCompleted || task.Attempts != 1 {
			t.Fatalf("expected non-retryable WORKER_COMPLETED attempts=1 on pre-spawn policy rejection, got task=%+v", task)
		}
		if second, serr := sup.RunOnce(ctx, RunOptions{WorkerID: "w-policy-rej-2", LeaseSeconds: 10}); serr != nil || second != nil {
			t.Fatalf("expected queue empty (non-retryable), got second=%v err=%v", second, serr)
		}
		if _, statErr := os.Stat(ranMarker); !os.IsNotExist(statErr) {
			t.Fatalf("child process executed despite RejectedBeforeSpawn!")
		}

		var res struct {
			Status              string                           `json:"status"`
			RejectedBeforeSpawn bool                             `json:"rejected_before_spawn"`
			PolicyEnforcement   *sandbox.PolicyEnforcementReport `json:"policy_enforcement"`
		}
		if uerr := json.Unmarshal(task.Result, &res); uerr != nil {
			t.Fatalf("unmarshal result: %v (%s)", uerr, string(task.Result))
		}
		if res.Status != "sandbox_policy_rejected" || !res.RejectedBeforeSpawn {
			t.Fatalf("expected sandbox_policy_rejected & rejected_before_spawn=true, got %+v", res)
		}
		if res.PolicyEnforcement == nil || !res.PolicyEnforcement.RejectedBeforeSpawn {
			t.Fatalf("expected PolicyEnforcement.RejectedBeforeSpawn=true, got %+v", res.PolicyEnforcement)
		}
		if res.PolicyEnforcement.Evaluated.RejectionCode != sandbox.ErrKernelSandboxRequired.Error() {
			t.Fatalf("RejectionCode = %q, want %q", res.PolicyEnforcement.Evaluated.RejectionCode, sandbox.ErrKernelSandboxRequired.Error())
		}
	})

	t.Run("successful_attempt_emits_unified_policy_enforcement_report", func(t *testing.T) {
		env := newWorkerEnv(t, nil)
		wt := t.TempDir()
		holdoutDir := t.TempDir()
		okFile := filepath.Join(wt, "worker_ok.txt")
		t.Setenv(workerSandboxHelperEnv, "1")
		t.Setenv("INSIDE_PATH", okFile)
		t.Setenv("OUTSIDE_PATH", "")

		sup := NewSupervisor(env.store, env.runDir,
			WithPollInterval(5*time.Millisecond),
			WithWorktreeIsolator(&fakeWorktreeIsolator{dir: wt}),
			WithProviderCommandResolver(func(prompt, provider, model, timeout string) ([]string, error) {
				return []string{selfExe, "-test.run=^TestWorkerSandboxHelper$"}, nil
			}),
		)

		submitTask(t, env, "task-policy-ok-1", 1, map[string]any{
			"permission":    "workspace_write",
			"provider":      "opencode",
			"model":         "mock-v1",
			"role":          "implementer",
			"receipt_id":    "rcpt-policy-ok",
			"allowed_paths": []string{"pkg/**"},
			"holdout_dir":   holdoutDir,
			"result_mode":   "stdout",
		})

		task, rerr := sup.RunOnce(ctx, RunOptions{WorkerID: "w-policy-ok", LeaseSeconds: 10})
		if rerr != nil {
			t.Fatalf("RunOnce: %v", rerr)
		}
		if task == nil || len(task.Result) == 0 {
			t.Fatal("expected completed task result")
		}
		var res struct {
			PolicyEnforcement *sandbox.PolicyEnforcementReport `json:"policy_enforcement"`
		}
		if uerr := json.Unmarshal(task.Result, &res); uerr != nil {
			t.Fatalf("unmarshal result: %v (%s)", uerr, string(task.Result))
		}
		if res.PolicyEnforcement == nil {
			t.Fatalf("expected policy_enforcement in task result: %s", string(task.Result))
		}
		rep := res.PolicyEnforcement
		if rep.RejectedBeforeSpawn || !rep.Evaluated.Allowed {
			t.Fatalf("unexpected rejection in successful run: %+v", rep)
		}
		if rep.Requested.Permission != "workspace_write" || rep.Requested.ReceiptID != "rcpt-policy-ok" {
			t.Fatalf("unexpected RequestedPolicy: %+v", rep.Requested)
		}
		if len(rep.Requested.HoldoutDenyDirs) != 1 || rep.Requested.HoldoutDenyDirs[0] != sandbox.CanonicalPath(holdoutDir) {
			t.Fatalf("HoldoutDenyDirs = %v, want [%s]", rep.Requested.HoldoutDenyDirs, sandbox.CanonicalPath(holdoutDir))
		}
		if !rep.Effective.DiffFirewallActive || rep.EpistemicClass != sandbox.EpistemicMeasuredMetric {
			t.Fatalf("unexpected Effective / EpistemicClass: %+v", rep)
		}
	})
}
