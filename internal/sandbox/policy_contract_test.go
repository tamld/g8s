package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEvaluatePolicyContract_DecisionMatrix(t *testing.T) {
	t.Run("require_kernel_enforced_rejects_policy_only_before_spawn", func(t *testing.T) {
		report := EvaluatePolicyContract(RequestedPolicy{
			Permission:            "workspace_write",
			Role:                  "implementer",
			ReceiptID:             "rcpt-1",
			AllowedPaths:          []string{"pkg/**"},
			RequireKernelEnforced: true,
			TransportKind:         "process",
		}, ModePolicyOnly, "linux_policy_only", false, false)

		if report.Evaluated.Allowed {
			t.Fatalf("expected Allowed=false when kernel_enforced is unavailable")
		}
		if !report.RejectedBeforeSpawn || !report.Evaluated.RejectedBeforeSpawn {
			t.Fatalf("expected RejectedBeforeSpawn=true, got report=%+v", report)
		}
		if report.Evaluated.RejectionCode != ErrKernelSandboxRequired.Error() {
			t.Fatalf("RejectionCode = %q, want %q", report.Evaluated.RejectionCode, ErrKernelSandboxRequired.Error())
		}
		if report.EpistemicClass != EpistemicMeasuredMetric || report.Effective.EpistemicClass != EpistemicMeasuredMetric {
			t.Fatalf("expected epistemic_class=%q, got top=%q eff=%q",
				EpistemicMeasuredMetric, report.EpistemicClass, report.Effective.EpistemicClass)
		}
	})

	t.Run("http_proxy_without_os_socket_block_reports_mediated_only_and_rejects_strict_network", func(t *testing.T) {
		mediated := EvaluatePolicyContract(RequestedPolicy{
			Permission:    "read_only",
			TransportKind: "request",
		}, ModePolicyOnly, "http_request_transport", true, false)

		if mediated.RejectedBeforeSpawn {
			t.Fatalf("expected mediated report without RequireNetworkEnforced to allow: %+v", mediated)
		}
		if mediated.NetworkEnforcement != NetworkMediatedOnly || mediated.Effective.NetworkEnforcement != NetworkMediatedOnly {
			t.Fatalf("NetworkEnforcement = %q / %q, want %q",
				mediated.NetworkEnforcement, mediated.Effective.NetworkEnforcement, NetworkMediatedOnly)
		}

		strictNet := EvaluatePolicyContract(RequestedPolicy{
			Permission:             "read_only",
			TransportKind:          "request",
			RequireNetworkEnforced: true,
		}, ModePolicyOnly, "http_request_transport", true, false)
		if !strictNet.RejectedBeforeSpawn || strictNet.Evaluated.RejectionCode != ErrNetworkEnforcementRequired.Error() {
			t.Fatalf("expected E_NETWORK_ENFORCEMENT_REQUIRED rejection, got %+v", strictNet)
		}
	})

	t.Run("os_socket_blocked_reports_enforced_network", func(t *testing.T) {
		enforced := EvaluatePolicyContract(RequestedPolicy{
			Permission:             "workspace_write",
			HoldoutDenyDirs:        []string{"/tmp/holdout-eval"},
			RequireKernelEnforced:  true,
			RequireNetworkEnforced: true,
			TransportKind:          "process",
		}, ModeKernelEnforced, "bwrap", true, true)

		if enforced.RejectedBeforeSpawn || !enforced.Evaluated.Allowed {
			t.Fatalf("expected enforced report allowed, got %+v", enforced)
		}
		if enforced.NetworkEnforcement != NetworkEnforced || enforced.Effective.NetworkEnforcement != NetworkEnforced {
			t.Fatalf("expected NetworkEnforcement=%q, got %q", NetworkEnforced, enforced.NetworkEnforcement)
		}
		if !enforced.Effective.HoldoutDenied || !enforced.Effective.DiffFirewallActive {
			t.Fatalf("expected HoldoutDenied=true and DiffFirewallActive=true, got %+v", enforced.Effective)
		}
	})

	t.Run("policy_only_never_claims_holdout_denied", func(t *testing.T) {
		rep := EvaluatePolicyContract(RequestedPolicy{
			Permission:      "workspace_write",
			HoldoutDenyDirs: []string{"/tmp/holdout-eval"},
			NoSandbox:       true,
			TransportKind:   "process",
		}, ModePolicyOnly, "disabled", false, false)

		if rep.Effective.HoldoutDenied {
			t.Fatalf("policy_only mode must never overclaim holdout_denied=true")
		}
	})
}

func TestPrepare_RequireKernelEnforcedRejectsNoSandboxWithPolicyReport(t *testing.T) {
	wt := t.TempDir()
	runDir := t.TempDir()
	_, err := Prepare(Config{
		Argv:                  []string{"true"},
		WorktreeDir:           wt,
		RunDir:                runDir,
		Permission:            "workspace_write",
		NoSandbox:             true,
		RequireKernelEnforced: true,
	})
	if err == nil {
		t.Fatal("expected error when NoSandbox=true and RequireKernelEnforced=true")
	}
	if !errors.Is(err, ErrKernelSandboxRequired) {
		t.Fatalf("expected ErrKernelSandboxRequired, got %v", err)
	}
	var rej *PolicyRejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("expected *PolicyRejectionError, got %T: %v", err, err)
	}
	if !rej.Report.RejectedBeforeSpawn || rej.Report.Evaluated.RejectionCode != ErrKernelSandboxRequired.Error() {
		t.Fatalf("unexpected PolicyRejectionError report: %+v", rej.Report)
	}
	if rej.Report.Effective.ContainmentMode != ModePolicyOnly {
		t.Fatalf("Effective.ContainmentMode = %q, want %q", rej.Report.Effective.ContainmentMode, ModePolicyOnly)
	}
}

func TestPrepare_HoldoutDirKernelDenialPOSIX(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("POSIX kernel filesystem denial test requires darwin or linux")
	}
	wt := t.TempDir()
	runDir := t.TempDir()
	holdoutDir := t.TempDir()
	secretPath := filepath.Join(holdoutDir, "secret_eval.txt")
	if err := os.WriteFile(secretPath, []byte("HOLDOUT_SECRET_VALUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pwnedPath := filepath.Join(holdoutDir, "pwned.txt")

	script := `
cat_out=$(cat "$1" 2>&1)
cat_rc=$?
echo "pwn" > "$2" 2>&1
wr_rc=$?
printf "cat_rc=%d write_rc=%d cat_out=%s\n" "$cat_rc" "$wr_rc" "$cat_out"
`
	prep, err := Prepare(Config{
		Argv:        []string{"/bin/sh", "-c", script, "sh", secretPath, pwnedPath},
		WorktreeDir: wt,
		RunDir:      runDir,
		Permission:  "workspace_write",
		Role:        "implementer",
		ReceiptID:   "rcpt-holdout-test",
		DenyDirs:    []string{holdoutDir},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() {
		if prep.Cleanup != nil {
			prep.Cleanup()
		}
	}()

	if prep.Mode != ModeKernelEnforced {
		t.Skipf("host sandbox mode=%s backend=%s is not kernel_enforced", prep.Mode, prep.Backend)
	}
	if prep.PolicyReport == nil || !prep.PolicyReport.Effective.HoldoutDenied {
		t.Fatalf("expected PolicyReport.Effective.HoldoutDenied=true, got %+v", prep.PolicyReport)
	}

	cmd := exec.Command(prep.Argv[0], prep.Argv[1:]...)
	cmd.Dir = CanonicalPath(wt)
	cmd.Env = prep.Env
	outBytes, runErr := cmd.CombinedOutput()
	out := string(outBytes)
	if runErr != nil {
		t.Fatalf("unexpected outer shell error: %v (%s)", runErr, out)
	}
	if strings.Contains(out, "HOLDOUT_SECRET_VALUE") {
		t.Fatalf("sandboxed process read secret holdout file! output=%s", out)
	}
	if strings.Contains(out, "cat_rc=0") {
		t.Fatalf("expected holdout read rc != 0 under kernel denial, output=%s", out)
	}
	if _, statErr := os.Stat(pwnedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("sandboxed process created host file inside holdoutDir! stat=%v", statErr)
	}
	if prep.Backend != "bwrap" && strings.Contains(out, "write_rc=0") {
		t.Fatalf("expected holdout write rc != 0 on backend=%s, output=%s", prep.Backend, out)
	}
}
