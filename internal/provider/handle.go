package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

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
	exitCode := 0
	status := "COMPLETED"
	if h.waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(h.waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
		status = "FAILED"
	}

	return Receipt{
		Provider:   h.provider,
		Status:     status,
		Stdout:     h.stdout.String(),
		Stderr:     h.stderr.String(),
		ExitCode:   exitCode,
		DurationMs: duration,
	}, h.waitErr
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
