package provider

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestExecHandle_WaitRace is a table test that a fast-exiting process is not
// mislabeled TIMEOUT when ctx is already cancelled (#859).
func TestExecHandle_WaitRace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		shell      string
		wantStatus string
		wantExit   int
	}{
		{
			name:       "immediate_success_pre_cancelled_ctx",
			shell:      "exit 0",
			wantStatus: "COMPLETED",
			wantExit:   0,
		},
		{
			name:       "immediate_failure_pre_cancelled_ctx",
			shell:      "exit 7",
			wantStatus: "FAILED",
			wantExit:   7,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", tc.shell)
			configureSysProcAttr(cmd)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("start: %v", err)
			}
			h := newProcessHandle("race-test", cmd, time.Now(), &stdout, &stderr)
			<-h.done // process exited before Wait; ctx already cancelled (#859)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			receipt, waitErr := h.Wait(ctx)
			if receipt.Status == "TIMEOUT" {
				t.Fatalf("receipt must not be TIMEOUT, got %+v", receipt)
			}
			if receipt.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", receipt.Status, tc.wantStatus)
			}
			if receipt.ExitCode != tc.wantExit {
				t.Fatalf("exit code = %d, want %d", receipt.ExitCode, tc.wantExit)
			}
			if waitErr != nil && tc.wantStatus == "COMPLETED" {
				t.Fatalf("unexpected wait error on success exit: %v", waitErr)
			}
		})
	}
}
