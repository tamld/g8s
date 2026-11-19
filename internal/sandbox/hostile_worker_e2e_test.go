package sandbox

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const hostileWorkerHelperEnv = "G8S_HOSTILE_SANDBOX_HELPER"

func TestHostileWorkerProbeHelper(t *testing.T) {
	if os.Getenv(hostileWorkerHelperEnv) != "1" {
		return
	}

	dialAddr := os.Getenv("G8S_HOSTILE_DIAL_ADDR")
	conn, dErr := net.DialTimeout("tcp", dialAddr, 1500*time.Millisecond)
	if dErr == nil {
		_ = conn.Close()
		fmt.Fprintln(os.Stdout, "DIAL_OK")
	} else {
		fmt.Fprintf(os.Stdout, "DIAL_BLOCKED:%v\n", dErr)
	}

	holdoutFile := os.Getenv("G8S_HOSTILE_HOLDOUT_FILE")
	if _, rErr := os.ReadFile(holdoutFile); rErr == nil {
		fmt.Fprintln(os.Stdout, "HOLDOUT_READ_OK")
	} else {
		fmt.Fprintf(os.Stdout, "HOLDOUT_READ_BLOCKED:%v\n", rErr)
	}

	outsideFile := os.Getenv("G8S_HOSTILE_OUTSIDE_FILE")
	if wErr := os.WriteFile(outsideFile, []byte("pwn"), 0o600); wErr == nil {
		fmt.Fprintln(os.Stdout, "OUTSIDE_WRITE_OK")
	} else {
		fmt.Fprintf(os.Stdout, "OUTSIDE_WRITE_BLOCKED:%v\n", wErr)
	}

	insideFile := os.Getenv("G8S_HOSTILE_INSIDE_FILE")
	if iErr := os.WriteFile(insideFile, []byte("ok"), 0o600); iErr == nil {
		fmt.Fprintln(os.Stdout, "INSIDE_WRITE_OK")
	} else {
		fmt.Fprintf(os.Stdout, "INSIDE_WRITE_FAILED:%v\n", iErr)
	}

	os.Exit(0)
}

func TestHostileWorker_RealOSKernelNetworkAndHoldoutEnforcement(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	var acceptedConns atomic.Int64
	doneAccept := make(chan struct{})
	t.Cleanup(func() {
		if cerr := ln.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			t.Errorf("close listener: %v", cerr)
		}
		<-doneAccept
	})
	go func() {
		defer close(doneAccept)
		for {
			c, aErr := ln.Accept()
			if aErr != nil {
				return
			}
			acceptedConns.Add(1)
			_ = c.Close()
		}
	}()

	selfExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	wtDir := t.TempDir()
	runDir := t.TempDir()
	holdoutDir := t.TempDir()
	outsideDir := t.TempDir()

	holdoutFile := filepath.Join(holdoutDir, "secret.txt")
	if err := os.WriteFile(holdoutFile, []byte("TOP_SECRET_HOLDOUT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "escaped.txt")
	insideFile := filepath.Join(CanonicalPath(wtDir), "inside.txt")

	prep, prepErr := Prepare(Config{
		Argv:                   []string{selfExe, "-test.run=^TestHostileWorkerProbeHelper$"},
		WorktreeDir:            wtDir,
		RunDir:                 runDir,
		Permission:             "workspace_write",
		Role:                   "implementer",
		ReceiptID:              "rcpt-hostile-1",
		DenyDirs:               []string{holdoutDir},
		RequireKernelEnforced:  true,
		RequireNetworkEnforced: true,
		TaskEnv: []string{
			hostileWorkerHelperEnv + "=1",
			"G8S_HOSTILE_DIAL_ADDR=" + ln.Addr().String(),
			"G8S_HOSTILE_HOLDOUT_FILE=" + holdoutFile,
			"G8S_HOSTILE_OUTSIDE_FILE=" + outsideFile,
			"G8S_HOSTILE_INSIDE_FILE=" + insideFile,
		},
	})

	if runtime.GOOS == "windows" {
		if prepErr == nil {
			if prep != nil && prep.Cleanup != nil {
				prep.Cleanup()
			}
			t.Fatalf("expected Windows Prepare with strict kernel+network enforcement to reject before spawn")
		}
		var rej *PolicyRejectionError
		if !errors.As(prepErr, &rej) || !rej.Report.RejectedBeforeSpawn {
			t.Fatalf("expected PolicyRejectionError with RejectedBeforeSpawn=true on Windows, got %T: %v", prepErr, prepErr)
		}
		if !errors.Is(prepErr, ErrKernelSandboxRequired) && !errors.Is(prepErr, ErrNetworkEnforcementRequired) {
			t.Fatalf("expected kernel/network rejection sentinel on Windows, got %v", prepErr)
		}
		return
	}

	if prepErr != nil {
		var rej *PolicyRejectionError
		if runtime.GOOS == "linux" && errors.As(prepErr, &rej) && rej.Report.RejectedBeforeSpawn {
			// On Linux hosts without bwrap --unshare-net, strict network enforcement must fail closed pre-spawn.
			return
		}
		t.Fatalf("Prepare unexpected failure on %s: %v", runtime.GOOS, prepErr)
	}
	defer func() {
		if prep.Cleanup != nil {
			prep.Cleanup()
		}
	}()

	if prep.Mode != ModeKernelEnforced {
		t.Fatalf("Mode = %q, want %q", prep.Mode, ModeKernelEnforced)
	}
	if prep.PolicyReport == nil {
		t.Fatalf("expected non-nil PolicyReport")
	}
	if prep.PolicyReport.NetworkEnforcement != NetworkEnforced ||
		prep.PolicyReport.Effective.ProofSource != "os_kernel_probe" ||
		!prep.PolicyReport.Effective.HoldoutDenied {
		t.Fatalf("unexpected PolicyReport: %+v", prep.PolicyReport)
	}

	cmd := exec.Command(prep.Argv[0], prep.Argv[1:]...)
	cmd.Dir = CanonicalPath(wtDir)
	cmd.Env = prep.Env
	outBytes, runErr := cmd.CombinedOutput()
	out := string(outBytes)
	if runErr != nil {
		t.Fatalf("contained helper exited with error: %v\noutput:\n%s", runErr, out)
	}

	for _, want := range []string{
		"DIAL_BLOCKED:",
		"HOLDOUT_READ_BLOCKED:",
		"OUTSIDE_WRITE_BLOCKED:",
		"INSIDE_WRITE_OK",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("hostile worker probe missing %q (sandbox bypass!)\noutput:\n%s", want, out)
		}
	}
	if acceptedConns.Load() != 0 {
		t.Fatalf("host listener accepted %d outbound connection(s) from sandboxed worker!", acceptedConns.Load())
	}
	if _, statErr := os.Stat(outsideFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("hostile worker wrote file outside worktree (%s): statErr=%v", outsideFile, statErr)
	}
}

func TestPrepare_StrictNetworkEnforcementRejectsNoSandbox(t *testing.T) {
	wt := t.TempDir()
	runDir := t.TempDir()

	_, err := Prepare(Config{
		Argv:                   []string{"true"},
		WorktreeDir:            wt,
		RunDir:                 runDir,
		Permission:             "workspace_write",
		NoSandbox:              true,
		RequireNetworkEnforced: true,
	})
	if err == nil {
		t.Fatal("expected error when NoSandbox=true and RequireNetworkEnforced=true")
	}
	if !errors.Is(err, ErrNetworkEnforcementRequired) {
		t.Fatalf("expected ErrNetworkEnforcementRequired, got %v", err)
	}
	var rej *PolicyRejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("expected *PolicyRejectionError, got %T: %v", err, err)
	}
	if !rej.Report.RejectedBeforeSpawn || rej.Report.Effective.ProofSource != "sandbox_disabled" {
		t.Fatalf("unexpected PolicyRejectionError report: %+v", rej.Report)
	}
}
