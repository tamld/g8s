package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
)

// processChild wraps an exec.Cmd whose process group the supervisor owns.
type processChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	code int
	// job holds the Windows Job Object handle (KILL_ON_JOB_CLOSE, #415
	// PR-2); 0 on POSIX where containment is the process group.
	job      uintptr
	jobClose sync.Once
}

func newProcessChild(cmd *exec.Cmd, job uintptr) *processChild {
	c := &processChild{cmd: cmd, done: make(chan struct{}), job: job}
	go func() {
		err := cmd.Wait()
		c.code = exitCodeOf(err)
		close(c.done)
	}()
	return c
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func (c *processChild) PID() int { return c.cmd.Process.Pid }

func (c *processChild) Done() <-chan struct{} { return c.done }

func (c *processChild) WaitCode() int {
	<-c.done
	return c.code
}

// Terminate escalates SIGTERM to SIGKILL across the whole process group,
// ignoring processes that already exited.
func (c *processChild) Terminate(grace time.Duration) {
	pid := c.cmd.Process.Pid
	if err := killProcessGroup(pid, syscallSIGTERM); err != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	timer := time.AfterFunc(grace, func() {
		if err := killProcessGroup(pid, syscallSIGKILL); err != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
	defer timer.Stop()
	select {
	case <-c.done:
	case <-time.After(grace + time.Second):
	}
	// #415 PR-2 (Windows): closing the Job Object handle hard-terminates
	// every process still in the job (KILL_ON_JOB_CLOSE) — the belt to the
	// taskkill suspenders. POSIX no-op.
	c.closeJob()
}

// processRunner is the production Runner: exec with POSIX process groups.
type processRunner struct{}

func (processRunner) Spawn(opts SpawnOptions) (Child, error) {
	if len(opts.Argv) == 0 {
		return nil, errors.New("empty argv")
	}

	// Verify executable identity (detect fake scripts like python3 -> Node)
	if err := verifyExecutableIdentity(opts.Argv[0]); err != nil {
		return nil, err
	}

	var cmd *exec.Cmd
	if opts.Timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		cmd = exec.CommandContext(ctx, opts.Argv[0], opts.Argv[1:]...)
		_ = cancel
	} else {
		cmd = exec.Command(opts.Argv[0], opts.Argv[1:]...)
	}
	configureSysProcAttr(cmd)
	cmd.Dir = opts.Dir
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pidPath := filepath.Join(opts.RunDir, "child.pid")
	if werr := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); werr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: write %s: %v\n", pidPath, werr)
	}
	// #415 PR-2 (Windows): contain the whole tree in a Job Object with
	// KILL_ON_JOB_CLOSE — the kernel kills every process (including
	// detached grandchildren) the moment the handle closes. POSIX returns
	// a zero handle (process-group containment applies instead).
	job, jerr := spawnJobHandle(cmd)
	if jerr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: job object containment unavailable: %v\n", jerr)
	}
	return newProcessChild(cmd, job), nil
}

func isAttemptResultOK(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var probe struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.OK
}

func extractWorkerAPIErrorLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "[API Error:") {
			return trimmed
		}
	}
	return ""
}

// awaitOutcome polls lease signals until the child exits or a terminal
// condition (cancel, timeout, lost lease, shutdown) fires.
func (s *Supervisor) awaitOutcome(
	ctx context.Context,
	child Child,
	taskID, workerID, token string,
	req taskRequest,
	leaseSeconds int,
) string {
	timeoutSeconds, terr := ParseDurationSeconds(req.Timeout)
	if terr != nil {
		return "invalid_timeout"
	}
	deadline := s.clock().Add(time.Duration(timeoutSeconds * float64(time.Second)))
	heartbeat := clampDuration(time.Duration(leaseSeconds)*time.Second/3, minHeartbeatInterval, maxHeartbeatInterval)
	nextBeat := s.clock().Add(heartbeat)

	consecutiveErrors := 0
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			return "cancelled"
		}
		t, gerr := s.cp.GetTask(ctx, taskID)
		if gerr != nil {
			consecutiveErrors++
			lastErr = gerr
			if consecutiveErrors >= 3 {
				// Second authoritative check after one heartbeat interval of grace before giving up
				select {
				case <-ctx.Done():
					return "cancelled"
				case <-child.Done():
					return ""
				case <-time.After(heartbeat):
				}
				authTask, authErr := s.cp.GetTask(ctx, taskID)
				if authErr != nil || authTask == nil {
					return fmt.Sprintf("lease_lost: 3 consecutive transient errors in GetTask: %v", lastErr)
				}
				authNowSec := float64(s.clock().UnixNano()) / 1e9
				if authTask.CancelRequested {
					return "cancelled"
				}
				if authTask.State != controlplane.StateRunning {
					return fmt.Sprintf("lease_lost: task state is %s", authTask.State)
				}
				if !sameLease(authTask, workerID, token) {
					return fmt.Sprintf("lease_lost: lease owner/token mismatch (owner=%s, want=%s)", derefString(authTask.LeaseOwner), workerID)
				}
				if authTask.LeaseExpiresAt != nil && *authTask.LeaseExpiresAt <= authNowSec {
					return fmt.Sprintf("lease_lost: lease expired at %f", *authTask.LeaseExpiresAt)
				}
				// Recovered during grace
				consecutiveErrors = 0
				t = authTask
			} else {
				// Transient error extends the loop
				select {
				case <-child.Done():
					return ""
				case <-time.After(s.pollInterval):
				}
				continue
			}
		} else {
			consecutiveErrors = 0
		}

		if t == nil {
			return "lease_lost: task not found in database"
		}
		if t.CancelRequested {
			return "cancelled"
		}

		now := s.clock()
		nowSec := float64(now.UnixNano()) / 1e9

		// Authoritative check on DB task state and lease
		if t.State != controlplane.StateRunning {
			return fmt.Sprintf("lease_lost: task state is %s", t.State)
		}
		if !sameLease(t, workerID, token) {
			return fmt.Sprintf("lease_lost: lease owner/token mismatch (owner=%s, want=%s)", derefString(t.LeaseOwner), workerID)
		}
		if t.LeaseExpiresAt != nil && *t.LeaseExpiresAt <= nowSec {
			return fmt.Sprintf("lease_lost: lease expired at %f", *t.LeaseExpiresAt)
		}

		if !now.Before(deadline) {
			return "timeout"
		}

		if !now.Before(nextBeat) {
			if herr := s.cp.RenewHeartbeat(ctx, taskID, workerID, leaseSeconds); herr != nil {
				consecutiveErrors++

				// Renewal error triggers a second authoritative check after one heartbeat interval of grace
				select {
				case <-ctx.Done():
					return "cancelled"
				case <-child.Done():
					return ""
				case <-time.After(heartbeat):
				}

				authTask, authErr := s.cp.GetTask(ctx, taskID)
				if authErr != nil {
					consecutiveErrors++
					lastErr = authErr
					if consecutiveErrors >= 3 {
						return fmt.Sprintf("lease_lost: 3 consecutive transient errors in lease renewal: %v", lastErr)
					}
					nextBeat = s.clock().Add(heartbeat)
					continue
				}

				authNowSec := float64(s.clock().UnixNano()) / 1e9
				if authTask == nil {
					return "lease_lost: task not found in database on authoritative re-check"
				}
				if authTask.CancelRequested {
					return "cancelled"
				}
				if authTask.State != controlplane.StateRunning {
					return fmt.Sprintf("lease_lost: task state is %s on authoritative re-check", authTask.State)
				}
				if !sameLease(authTask, workerID, token) {
					return fmt.Sprintf("lease_lost: lease owner/token mismatch on authoritative re-check (owner=%s, want=%s)", derefString(authTask.LeaseOwner), workerID)
				}
				if authTask.LeaseExpiresAt != nil && *authTask.LeaseExpiresAt <= authNowSec {
					return fmt.Sprintf("lease_lost: lease expired at %f", *authTask.LeaseExpiresAt)
				}

				// DB shows lease is still valid and not expired; continue loop
				consecutiveErrors = 0
			} else {
				consecutiveErrors = 0
			}
			nextBeat = s.clock().Add(heartbeat)
		}

		select {
		case <-child.Done():
			return ""
		case <-time.After(s.pollInterval):
		}
	}
}
