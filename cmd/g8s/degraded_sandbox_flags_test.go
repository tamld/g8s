package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/doctor"
	"github.com/tamld/g8s/internal/sandbox"
)

// TestSubmitAllowDegradedSandbox_PayloadPlumbing (#971 WP1 C-layer) proves the
// `g8s submit --allow-degraded-sandbox` opt-in is persisted into request_json
// so the W-layer sandbox gate reads taskRequest.AllowDegradedSandbox.
func TestSubmitAllowDegradedSandbox_PayloadPlumbing(t *testing.T) {
	binPath := buildG8sBinary(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "allow_degraded_sandbox.db")
	envVars := []string{
		"G8S_DB=" + dbPath,
		"G8S_PROVIDERS=" + filepath.Join(t.TempDir(), "absent-providers.json"),
	}

	t.Run("flag set persists allow_degraded_sandbox into request_json", func(t *testing.T) {
		env, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "degraded-sandbox-set",
			"--prompt", "Task with degraded sandbox opt-in",
			"--permission", "read_only",
			"--allow-degraded-sandbox",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d err=%+v raw=%s", code, env.Error, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "degraded-sandbox-set")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		v, ok := payload["allow_degraded_sandbox"]
		if !ok {
			t.Fatalf("allow_degraded_sandbox missing from request_json: %s", reqJSON)
		}
		b, ok := v.(bool)
		if !ok || !b {
			t.Errorf("allow_degraded_sandbox = %v, want true", v)
		}
	})

	t.Run("flag unset leaves allow_degraded_sandbox absent", func(t *testing.T) {
		_, code, raw := runSubmitCLI(t, binPath, envVars,
			"--idempotency-key", "degraded-sandbox-unset",
			"--prompt", "Task without degraded sandbox opt-in",
			"--permission", "read_only",
		)
		if code != 0 {
			t.Fatalf("submit failed: code=%d raw=%s", code, raw)
		}
		reqJSON := getTaskRequestJSON(t, dbPath, "degraded-sandbox-unset")
		var payload map[string]any
		if err := json.Unmarshal([]byte(reqJSON), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if _, ok := payload["allow_degraded_sandbox"]; ok {
			t.Errorf("allow_degraded_sandbox should be absent when flag unset, got: %v", payload["allow_degraded_sandbox"])
		}
	})
}

// TestWorkerAllowDegradedSandboxFlagAccepted (#971 WP1 C-layer) proves
// `g8s worker --allow-degraded-sandbox` parses and the worker loop still runs
// (drain on an empty queue), and that the G8S_ALLOW_DEGRADED_SANDBOX=1 env
// fallback is honored without the flag.
func TestWorkerAllowDegradedSandboxFlagAccepted(t *testing.T) {
	binPath := buildG8sBinary(t)

	runWorkerCLI := func(t *testing.T, envVars []string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(binPath, append([]string{"worker", "--json"}, args...)...)
		cmd.Env = append(os.Environ(), envVars...)
		out, err := cmd.CombinedOutput()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				t.Fatalf("run worker %v failed: %v", args, err)
			}
		}
		return strings.TrimSpace(string(out)), exitCode
	}

	t.Run("flag accepted and worker drains empty queue", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "worker_degraded_flag.db")
		raw, code := runWorkerCLI(t,
			[]string{"G8S_DB=" + dbPath, "G8S_PROVIDERS=" + filepath.Join(t.TempDir(), "absent-providers.json")},
			"--once", "--allow-degraded-sandbox",
		)
		if code != 0 {
			t.Fatalf("worker with --allow-degraded-sandbox failed: code=%d raw=%s", code, raw)
		}
		var env testEnvelope
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatalf("unmarshal worker json: %v (raw=%s)", err, raw)
		}
		if env.Kind != "worker_status" || !strings.Contains(string(env.Data), "drained") {
			t.Errorf("expected worker_status drained, got kind=%s data=%s", env.Kind, string(env.Data))
		}
	})

	t.Run("env fallback accepted and worker drains empty queue", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "worker_degraded_env.db")
		raw, code := runWorkerCLI(t,
			[]string{"G8S_DB=" + dbPath, "G8S_ALLOW_DEGRADED_SANDBOX=1", "G8S_PROVIDERS=" + filepath.Join(t.TempDir(), "absent-providers.json")},
			"--once",
		)
		if code != 0 {
			t.Fatalf("worker with G8S_ALLOW_DEGRADED_SANDBOX=1 failed: code=%d raw=%s", code, raw)
		}
		if !strings.Contains(raw, "drained") {
			t.Errorf("expected drained status in worker output: %s", raw)
		}
	})
}

// TestAllowDegradedSandboxRequested pins the #971 WP1 worker opt-in resolution:
// the --allow-degraded-sandbox flag or the G8S_ALLOW_DEGRADED_SANDBOX=1 env
// fallback enables it; nothing else does.
func TestAllowDegradedSandboxRequested(t *testing.T) {
	t.Run("flag alone enables", func(t *testing.T) {
		t.Setenv("G8S_ALLOW_DEGRADED_SANDBOX", "")
		if !allowDegradedSandboxRequested(true) {
			t.Error("allowDegradedSandboxRequested(true) = false, want true")
		}
	})
	t.Run("env fallback enables without flag", func(t *testing.T) {
		t.Setenv("G8S_ALLOW_DEGRADED_SANDBOX", "1")
		if !allowDegradedSandboxRequested(false) {
			t.Error("env G8S_ALLOW_DEGRADED_SANDBOX=1 should enable degraded sandbox")
		}
	})
	t.Run("neither flag nor env stays fail-closed", func(t *testing.T) {
		t.Setenv("G8S_ALLOW_DEGRADED_SANDBOX", "")
		if allowDegradedSandboxRequested(false) {
			t.Error("no flag + no env should stay fail-closed (false)")
		}
	})
	t.Run("env value other than 1 does not enable", func(t *testing.T) {
		t.Setenv("G8S_ALLOW_DEGRADED_SANDBOX", "true")
		if allowDegradedSandboxRequested(false) {
			t.Error("G8S_ALLOW_DEGRADED_SANDBOX=true must not enable (strict 1 semantics)")
		}
	})
}

// TestDoctorHumanRendersSandboxCapabilityAndOperatingModes (#971 WP1 C-layer)
// proves the doctor human output renders the capability-probed sandbox preflight
// and the honest Mode 1 / Mode 2 operating-modes readiness matrix carried by the
// merged W-layer DoctorReport.
func TestDoctorHumanRendersSandboxCapabilityAndOperatingModes(t *testing.T) {
	report := &doctor.DoctorReport{
		SandboxCapability: &sandbox.SandboxCapability{
			Mode:    sandbox.CapabilityKernelEnforced,
			Backend: "test-backend",
			Reason:  "probe succeeded in test",
		},
		OperatingModes: &doctor.OperatingModesStatus{
			Mode1: doctor.OperatingModeReadiness{
				Mode:   "mode1",
				Name:   "Single-Provider Safe Wrapper",
				Ready:  true,
				Reason: "all requirements met",
				Requirements: []doctor.OperatingModeRequirement{
					{ID: "sandbox_capability", Met: true, Detail: "capability probe mode=kernel_enforced backend=test-backend"},
				},
			},
			Mode2: doctor.OperatingModeReadiness{
				Mode:   "mode2",
				Name:   "Multi-Provider Fleet",
				Ready:  false,
				Reason: "unmet requirements: sandbox_capability",
				Requirements: []doctor.OperatingModeRequirement{
					{ID: "sandbox_capability", Met: false, Detail: "no containment backend on this host"},
				},
			},
		},
	}

	out := string(captureStdoutFn(t, func() {
		printSandboxOperatingModesHuman(report)
	}))

	for _, want := range []string{
		"kernel_enforced",
		"test-backend",
		"probe succeeded in test",
		"mode1",
		"Single-Provider Safe Wrapper",
		"mode2",
		"Multi-Provider Fleet",
		"READY",
		"NOT READY",
		"unmet requirements: sandbox_capability",
		"sandbox_capability",
		"no containment backend on this host",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor human output missing %q; got:\n%s", want, out)
		}
	}
}

// TestDoctorHumanSkipsNilSandboxSections keeps the rendering additive: older
// DoctorReport fixtures without the W-layer fields must not panic or emit noise.
func TestDoctorHumanSkipsNilSandboxSections(t *testing.T) {
	out := string(captureStdoutFn(t, func() {
		printSandboxOperatingModesHuman(&doctor.DoctorReport{})
		printSandboxOperatingModesHuman(nil)
	}))
	if strings.TrimSpace(out) != "" {
		t.Errorf("nil sandbox sections must render nothing, got:\n%s", out)
	}
}

// TestDoctorJSONSurfacesSandboxCapabilityAndOperatingModes (#971 WP1 C-layer)
// proves the additive DoctorReport fields survive the doctor --json envelope so
// machines can read g8s doctor --json sandbox_capability / operating_modes.
func TestDoctorJSONSurfacesSandboxCapabilityAndOperatingModes(t *testing.T) {
	if runtime.GOOS == "windows" && os.Getenv("G8S_TEST_SKIP_WINDOWS_HEAVY") == "1" {
		t.Skip("skipped heavy windows e2e")
	}
	binPath := buildG8sBinary(t)
	stateDir := t.TempDir()
	cmd := exec.Command(binPath, "doctor", "--json")
	cmd.Env = append(os.Environ(),
		"G8S_STATE_DIR="+stateDir,
		"G8S_DB="+filepath.Join(stateDir, "doctor_sandbox.db"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("g8s doctor --json failed: %v\nOutput: %s", err, string(out))
	}
	var env struct {
		OK   bool   `json:"ok"`
		Kind string `json:"kind"`
		Data struct {
			SandboxCapability *sandbox.SandboxCapability `json:"sandbox_capability"`
			OperatingModes    *struct {
				Mode1 struct {
					Mode  string `json:"mode"`
					Ready bool   `json:"ready"`
				} `json:"mode1"`
				Mode2 struct {
					Mode  string `json:"mode"`
					Ready bool   `json:"ready"`
				} `json:"mode2"`
			} `json:"operating_modes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal doctor json: %v (raw=%s)", err, string(out))
	}
	if env.Data.SandboxCapability == nil {
		t.Fatal("doctor --json data.sandbox_capability must be populated (#971 WP1)")
	}
	switch env.Data.SandboxCapability.Mode {
	case sandbox.CapabilityKernelEnforced, sandbox.CapabilityMediated, sandbox.CapabilityUnavailable:
		// honest vocabulary
	default:
		t.Errorf("sandbox_capability.mode %q outside vocabulary", env.Data.SandboxCapability.Mode)
	}
	if env.Data.OperatingModes == nil {
		t.Fatal("doctor --json data.operating_modes must be populated (#971 WP1)")
	}
	if env.Data.OperatingModes.Mode1.Mode != "mode1" || env.Data.OperatingModes.Mode2.Mode != "mode2" {
		t.Errorf("unexpected operating_modes identifiers: %+v", env.Data.OperatingModes)
	}
}
