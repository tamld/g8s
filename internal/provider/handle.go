package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// extractCLIAPIErrorLine scans stderr/stdout for silent CLI agent API error lines
// (such as qwen printing "[API Error: 404 ...]" on stderr while exiting 0).
func extractCLIAPIErrorLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "[API Error:") {
			return trimmed
		}
	}
	return ""
}

// execHandle implements Handle for an OS subprocess.
type execHandle struct {
	mu       sync.Mutex
	provider string
	cmd      *exec.Cmd
	start    time.Time
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	done     chan struct{}
	waitErr  error
}

func newProcessHandle(provider string, cmd *exec.Cmd, start time.Time, stdout, stderr *bytes.Buffer) *execHandle {
	h := &execHandle{
		provider: provider,
		cmd:      cmd,
		start:    start,
		stdout:   stdout,
		stderr:   stderr,
		done:     make(chan struct{}),
	}
	go func() {
		h.waitErr = h.cmd.Wait()
		close(h.done)
	}()
	return h
}

// PID returns the OS process ID.
func (h *execHandle) PID() int {
	if h.cmd != nil && h.cmd.Process != nil {
		return h.cmd.Process.Pid
	}
	return 0
}

// Wait blocks until the process exits or ctx is cancelled.
func (h *execHandle) Wait(ctx context.Context) (Receipt, error) {
	select {
	case <-ctx.Done():
		_ = h.Cancel(context.Background())
		<-h.done
		return Receipt{
			Provider:   h.provider,
			Status:     "TIMEOUT",
			Stdout:     h.stdout.String(),
			Stderr:     h.stderr.String(),
			ExitCode:   -1,
			DurationMs: time.Since(h.start).Milliseconds(),
		}, ctx.Err()
	case <-h.done:
	}

	duration := time.Since(h.start).Milliseconds()
	stdoutStr := h.stdout.String()
	stderrStr := h.stderr.String()
	exitCode := 0
	status := "COMPLETED"
	waitErr := h.waitErr
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
		status = "FAILED"
	} else if apiErr := extractCLIAPIErrorLine(stderrStr + "\n" + stdoutStr); apiErr != "" && strings.TrimSpace(stdoutStr) == "" {
		exitCode = 1
		status = "FAILED"
		waitErr = fmt.Errorf("cli agent reported API error with empty output: %s", apiErr)
	}

	return Receipt{
		Provider:   h.provider,
		Status:     status,
		Stdout:     stdoutStr,
		Stderr:     stderrStr,
		ExitCode:   exitCode,
		DurationMs: duration,
	}, waitErr
}

// Cancel terminates the process group.
func (h *execHandle) Cancel(_ context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pid := h.PID()
	if pid <= 0 {
		return nil
	}
	return killProcessGroup(pid)
}

// StdoutStream returns a stream of stdout bytes.
func (h *execHandle) StdoutStream() io.ReadCloser {
	if h.stdout == nil {
		return io.NopCloser(bytes.NewReader(nil))
	}
	return io.NopCloser(bytes.NewReader(h.stdout.Bytes()))
}
