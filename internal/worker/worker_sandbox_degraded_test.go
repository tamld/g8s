package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/sandbox"
)

// TestAttachSandboxDegraded pins the envelope honesty contract (#971 WP1):
// degraded attempts carry sandbox_degraded=true + reason; capable or refused
// attempts never fake a degradation.
func TestAttachSandboxDegraded(t *testing.T) {
	t.Run("degraded recorded with reason", func(t *testing.T) {
		env := map[string]any{}
		report := &sandbox.PolicyEnforcementReport{}
		report.Effective.SandboxDegraded = true
		report.Effective.SandboxDegradedReason = "windows job_object_env_scrubbed baseline"
		attachSandboxDegraded(env, report)
		if env["sandbox_degraded"] != true {
			t.Fatalf("sandbox_degraded = %v, want true", env["sandbox_degraded"])
		}
		if env["sandbox_degraded_reason"] != "windows job_object_env_scrubbed baseline" {
			t.Fatalf("sandbox_degraded_reason = %v", env["sandbox_degraded_reason"])
		}
	})

	t.Run("missing reason leaves field absent", func(t *testing.T) {
		env := map[string]any{}
		report := &sandbox.PolicyEnforcementReport{}
		report.Effective.SandboxDegraded = true
		attachSandboxDegraded(env, report)
		if _, ok := env["sandbox_degraded_reason"]; ok {
			t.Fatal("sandbox_degraded_reason must stay absent when the report has no reason")
		}
	})

	t.Run("kernel_enforced and nil reports stay silent", func(t *testing.T) {
		env := map[string]any{}
		attachSandboxDegraded(env, nil)
		attachSandboxDegraded(env, &sandbox.PolicyEnforcementReport{})
		if len(env) != 0 {
			t.Fatalf("expected no degraded fields, got %v", env)
		}
	})
}

// TestWorker_DegradedSandboxEnvelope is the portable end-to-end check (#971
// WP1): an explicit --no-sandbox workspace_write attempt proceeds, and the
// terminal envelope records sandbox_degraded=true with a machine-readable
// reason — degradation is never silent on any OS.
func TestWorker_DegradedSandboxEnvelope(t *testing.T) {
	ctx := t.Context()
	selfExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	env := newWorkerEnv(t, nil)
	wt := t.TempDir()
	okFile := filepath.Join(wt, "degraded_ok.txt")
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

	submitTask(t, env, "task-degraded-1", 1, map[string]any{
		"permission":    "workspace_write",
		"provider":      "opencode",
		"model":         "mock-v1",
		"role":          "implementer",
		"receipt_id":    "rcpt-degraded-1",
		"no_sandbox":    true,
		"result_mode":   "stdout",
		"allowed_paths": []string{"pkg/**"},
	})

	task, rerr := sup.RunOnce(ctx, RunOptions{WorkerID: "w-degraded", LeaseSeconds: 10})
	if rerr != nil {
		t.Fatalf("RunOnce: %v", rerr)
	}
	if task == nil || len(task.Result) == 0 {
		t.Fatal("expected completed task result")
	}
	var res struct {
		SandboxDegraded       bool                             `json:"sandbox_degraded"`
		SandboxDegradedReason string                           `json:"sandbox_degraded_reason"`
		PolicyEnforcement     *sandbox.PolicyEnforcementReport `json:"policy_enforcement"`
	}
	if uerr := json.Unmarshal(task.Result, &res); uerr != nil {
		t.Fatalf("unmarshal result: %v (%s)", uerr, string(task.Result))
	}
	if !res.SandboxDegraded {
		t.Fatalf("expected sandbox_degraded=true in envelope: %s", string(task.Result))
	}
	if !strings.Contains(res.SandboxDegradedReason, "no-sandbox") {
		t.Fatalf("expected operator-opt-out reason, got %q", res.SandboxDegradedReason)
	}
	if res.PolicyEnforcement == nil || !res.PolicyEnforcement.Effective.SandboxDegraded {
		t.Fatalf("policy_enforcement must mirror sandbox_degraded, got %+v", res.PolicyEnforcement)
	}
}

// TestWorker_CapableHostAutoKernelEnforcement (#971 WP1.3): on hosts whose
// capability probe reports kernel_enforced, a plain workspace_write attempt
// (no flags) auto-enforces the OS sandbox and the envelope carries NO
// sandbox_degraded field — the current capable-host behavior preserved.
func TestWorker_CapableHostAutoKernelEnforcement(t *testing.T) {
	if sandbox.ProbeCapability().Mode != sandbox.CapabilityKernelEnforced {
		t.Skip("host lacks kernel-enforced sandbox capability; covered by policy-contract unit tests")
	}
	ctx := t.Context()
	selfExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	env := newWorkerEnv(t, nil)
	wt := t.TempDir()
	okFile := filepath.Join(wt, "capable_ok.txt")
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

	submitTask(t, env, "task-capable-1", 1, map[string]any{
		"permission":    "workspace_write",
		"provider":      "opencode",
		"model":         "mock-v1",
		"role":          "implementer",
		"receipt_id":    "rcpt-capable-1",
		"result_mode":   "stdout",
		"allowed_paths": []string{"pkg/**"},
	})

	task, rerr := sup.RunOnce(ctx, RunOptions{WorkerID: "w-capable", LeaseSeconds: 10})
	if rerr != nil {
		t.Fatalf("RunOnce: %v", rerr)
	}
	if task == nil || len(task.Result) == 0 {
		t.Fatal("expected completed task result")
	}
	var res struct {
		SandboxDegraded   bool                             `json:"sandbox_degraded"`
		ContainmentMode   string                           `json:"containment_mode"`
		PolicyEnforcement *sandbox.PolicyEnforcementReport `json:"policy_enforcement"`
	}
	if uerr := json.Unmarshal(task.Result, &res); uerr != nil {
		t.Fatalf("unmarshal result: %v (%s)", uerr, string(task.Result))
	}
	if res.SandboxDegraded {
		t.Fatalf("capable host must not record sandbox_degraded: %s", string(task.Result))
	}
	if res.PolicyEnforcement == nil || res.PolicyEnforcement.Effective.ContainmentMode != sandbox.ModeKernelEnforced {
		t.Fatalf("capable host must auto-enforce kernel containment, got %+v", res.PolicyEnforcement)
	}
}
