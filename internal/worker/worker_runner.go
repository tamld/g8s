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
	"github.com/tamld/g8s/internal/sandbox"
)

// processChild wraps an exec.Cmd whose process group the supervisor owns.
type processChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	code int
	// runDir is the attempt directory holding this child's child.pid and
	// child.run.json identity record (#980) — the post-run sweep retires
	// them from here.
	runDir string
	// job holds the Windows Job Object handle (KILL_ON_JOB_CLOSE, #415
	// PR-2); 0 on POSIX where containment is the process group.
	job             uintptr
	jobClose        sync.Once
	containmentMode string
	sandboxBackend  string
	policyReport    *sandbox.PolicyEnforcementReport
	sandboxCleanup  func()
	cleanupOnce     sync.Once
	// spawnCtx/cancel carry the spawn deadline (#986 A-13): cancel releases
	// the deadline timer when the child exits before opts.Timeout (collect
	// path and Terminate). Safe to call after cmd.Wait returns — the exec
	// ctx watcher has already exited by then, so it cannot trigger a
	// spurious Cmd.Cancel group kill on a normally-exited child.
	spawnCtx context.Context
	cancel   context.CancelFunc
}

func newProcessChild(cmd *exec.Cmd, job uintptr, containmentMode, sandboxBackend, runDir string, cleanup func(), spawnCtx context.Context, cancel context.CancelFunc) *processChild {
	c := &processChild{
		cmd:             cmd,
		done:            make(chan struct{}),
		runDir:          runDir,
		job:             job,
		containmentMode: containmentMode,
		sandboxBackend:  sandboxBackend,
		sandboxCleanup:  cleanup,
		spawnCtx:        spawnCtx,
		cancel:          cancel,
	}
	go func() {
		err := cmd.Wait()
		c.code = exitCodeOf(err)
		if c.cancel != nil {
			c.cancel() // release the spawn-deadline timer on early exit (A-13, #986)
		}
		c.closeSandbox()
		close(c.done)
	}()
	return c
}

// ContainmentInfo returns the truthful OS containment mode and sandbox backend.
func (c *processChild) ContainmentInfo() (string, string) {
	return c.containmentMode, c.sandboxBackend
}

// PolicyReport returns the unified PolicyEnforcementReport recorded at process prepare time.
func (c *processChild) PolicyReport() *sandbox.PolicyEnforcementReport {
	return c.policyReport
}

func (c *processChild) closeSandbox() {
	c.cleanupOnce.Do(func() {
		if c.sandboxCleanup != nil {
			c.sandboxCleanup()
		}
	})
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

// RunDir exposes the attempt directory holding this child's child.pid and
// child.run.json so the post-run sweep can retire them (#980).
func (c *processChild) RunDir() string { return c.runDir }

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
	// Release the spawn-deadline timer even if the process outlived grace
	// (A-13, #986). The exec ctx watcher has already exited by the time
	// cmd.Wait returned, so this cannot trigger a spurious group kill; if
	// Wait has not returned, cancel also arms the deadline kill path.
	if c.cancel != nil {
		c.cancel()
	}
	// #415 PR-2 (Windows): closing the Job Object handle hard-terminates
	// every process still in the job (KILL_ON_JOB_CLOSE) — the belt to the
	// taskkill suspenders. POSIX no-op.
	c.closeJob()
	c.closeSandbox()
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

	argv := opts.Argv
	var cmdEnv []string
	var containmentMode, sandboxBackend string
	var policyReport *sandbox.PolicyEnforcementReport
	var sandboxCleanup func()

	if opts.Sandbox != nil {
		sbxCfg := *opts.Sandbox
		sbxCfg.Argv = append([]string{}, opts.Argv...)
		sbxCfg.TaskEnv = append([]string{}, opts.Env...)
		if strings.TrimSpace(sbxCfg.RunDir) == "" {
			sbxCfg.RunDir = opts.RunDir
		}
		prep, serr := sandbox.Prepare(sbxCfg)
		if serr != nil {
			return nil, serr
		}
		argv = prep.Argv
		cmdEnv = prep.Env
		containmentMode = string(prep.Mode)
		sandboxBackend = prep.Backend
		policyReport = prep.PolicyReport
		sandboxCleanup = prep.Cleanup
	} else if len(opts.Env) > 0 {
		cmdEnv = append(os.Environ(), opts.Env...)
	}

	var cmd *exec.Cmd
	var spawnCtx context.Context
	var spawnCancel context.CancelFunc
	if opts.Timeout > 0 {
		spawnCtx, spawnCancel = context.WithTimeout(context.Background(), opts.Timeout)
		cmd = exec.CommandContext(spawnCtx, argv[0], argv[1:]...)
		// A-13 (#986): the deadline kill must take down the whole process
		// group. The exec default (os.Process.Kill) only signals the direct
		// child, leaving grandchildren running past the deadline — mirror
		// Terminate's group-kill contract through the deadline path.
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			return killProcessGroup(cmd.Process.Pid, syscallSIGKILL)
		}
	} else {
		cmd = exec.Command(argv[0], argv[1:]...)
	}
	configureSysProcAttr(cmd)
	cmd.Dir = opts.Dir
	if len(cmdEnv) > 0 {
		cmd.Env = cmdEnv
	}
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		if spawnCancel != nil {
			spawnCancel() // release the deadline timer on the failed-spawn path (A-13)
		}
		if sandboxCleanup != nil {
			sandboxCleanup()
		}
		return nil, err
	}
	// Attach Windows JobObject (and resume CREATE_SUSPENDED threads) immediately
	// after cmd.Start() before any filesystem I/O so no child code can execute
	// outside the JobObject containment boundary.
	job, jerr := spawnJobHandle(cmd)
	if jerr != nil {
		if cmd.Process != nil {
			if kerr := cmd.Process.Kill(); kerr != nil && !errors.Is(kerr, os.ErrProcessDone) {
				fmt.Fprintf(os.Stderr, "[warn] worker: kill uncontained pid %d: %v\n", cmd.Process.Pid, kerr)
			}
		}
		if werr := cmd.Wait(); werr != nil {
			fmt.Fprintf(os.Stderr, "[warn] worker: wait uncontained child: %v\n", werr)
		}
		if spawnCancel != nil {
			spawnCancel() // release the deadline timer on the containment-failure path (A-13)
		}
		if sandboxCleanup != nil {
			sandboxCleanup()
		}
		return nil, fmt.Errorf("worker: job object containment failed: %w", jerr)
	}
	pidPath := filepath.Join(opts.RunDir, "child.pid")
	if werr := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); werr != nil {
		fmt.Fprintf(os.Stderr, "[warn] worker: write %s: %v\n", pidPath, werr)
	}
	// #980: record the spawn-time identity (spawning worker pid + start
	// times, attempt marker) beside child.pid so reapOrphans can verify the
	// recorded group before ever signaling it.
	writeChildRunRecord(opts.RunDir, cmd.Process.Pid, opts.Env)
	child := newProcessChild(cmd, job, containmentMode, sandboxBackend, opts.RunDir, sandboxCleanup, spawnCtx, spawnCancel)
	child.policyReport = policyReport
	return child, nil
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
			if req.ClaimBinding != nil {
				if err := renewClaimFence(ctx, req.ReceiptID, true); err != nil {
					return "claim_refused_fence: " + err.Error()
				}
			}
			if herr := s.cp.RenewHeartbeat(ctx, taskID, workerID, leaseSeconds); herr != nil {
				if errors.Is(herr, controlplane.ErrLeaseLost) {
					return fmt.Sprintf("lease_lost: heartbeat renewal: %v", herr)
				}
				if !errors.Is(herr, controlplane.ErrHeartbeatTransient) {
					return fmt.Sprintf("lease_lost: heartbeat renewal: %v", herr)
				}
				consecutiveErrors++

				// Transient DB exhaustion: authoritative re-check after one heartbeat interval of grace
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
