// Package worker implements the DELTA-09 task supervisor: it claims leased
// tasks from the control plane, runs each attempt inside a private run
// directory and a killable POSIX process group, enforces timeouts against an
// injectable clock, and exports sealed evidence without ever persisting raw
// worker output.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
)

// WorkerControlPlane is the narrow control-plane surface the supervisor needs.
// *controlplane.Store satisfies it directly.
type WorkerControlPlane interface {
	ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*controlplane.Task, error)
	StartTask(taskID, workerID, leaseToken string) bool
	RenewHeartbeat(ctx context.Context, taskID, workerID string, extensionSeconds int) error
	FinishAttempt(taskID, workerID, leaseToken string, params controlplane.FinishAttemptParams) (*controlplane.Task, error)
	PauseTask(taskID, workerID, leaseToken, pauseState string, result json.RawMessage, reason string) (*controlplane.Task, error)
	GetTask(ctx context.Context, taskID string) (*controlplane.Task, error)
}

// taskRequest mirrors the worker-facing payload stored on every task.
type taskRequest struct {
	Prompt      string   `json:"prompt"`
	Model       string   `json:"model"`
	Role        string   `json:"role"`
	Permission  string   `json:"permission"`
	Timeout     string   `json:"timeout"`
	AddDirs     []string `json:"add_dirs"`
	NoSandbox   bool     `json:"no_sandbox"`
	SkipPermiss bool     `json:"skip_permissions"`
}

// Child is one spawned worker process observed by the supervisor.
type Child interface {
	PID() int
	Done() <-chan struct{}
	WaitCode() int
	Terminate(grace time.Duration)
}

// SpawnOptions carries everything a Runner needs to launch one attempt.
type SpawnOptions struct {
	Argv       []string
	Dir        string
	Stdout     io.Writer
	Stderr     io.Writer
	ResultPath string
	RunDir     string
}

// Runner spawns worker processes; tests inject fakes so no real binaries run.
type Runner interface {
	Spawn(opts SpawnOptions) (Child, error)
}

const (
	defaultPollInterval  = 200 * time.Millisecond
	defaultCaptureBytes  = 3 << 20
	minHeartbeatInterval = 100 * time.Millisecond
	maxHeartbeatInterval = 5 * time.Second
	terminateGrace       = 2 * time.Second
)

// Option customizes optional Supervisor dependencies.
type Option func(*Supervisor)

// WithClock injects the clock used for timeout and heartbeat arithmetic.
func WithClock(clock func() time.Time) Option { return func(s *Supervisor) { s.clock = clock } }

// WithRunner replaces the production process spawner.
func WithRunner(runner Runner) Option { return func(s *Supervisor) { s.runner = runner } }

// WithPollInterval sets how often lease signals are polled.
func WithPollInterval(d time.Duration) Option { return func(s *Supervisor) { s.pollInterval = d } }

// WithCaptureMaxBytes bounds captured stdout/stderr before sanitization.
func WithCaptureMaxBytes(n int) Option { return func(s *Supervisor) { s.captureMaxBytes = n } }

// WithBinaryPath pins the worker entrypoint used when building argv.
func WithBinaryPath(path string) Option { return func(s *Supervisor) { s.binaryPath = path } }

// Supervisor executes claimed tasks one attempt at a time with containment,
// bounded capture, and sealed evidence export.
type Supervisor struct {
	cp              WorkerControlPlane
	runRoot         string
	clock           func() time.Time
	runner          Runner
	pollInterval    time.Duration
	captureMaxBytes int
	binaryPath      string
}

// NewSupervisor builds a supervisor over the given control plane and run root.
func NewSupervisor(cp WorkerControlPlane, runRoot string, opts ...Option) *Supervisor {
	s := &Supervisor{
		cp:              cp,
		runRoot:         runRoot,
		clock:           time.Now,
		runner:          processRunner{},
		pollInterval:    defaultPollInterval,
		captureMaxBytes: defaultCaptureBytes,
		binaryPath:      "g8s",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RunOptions parameterize a single claim-and-execute cycle.
type RunOptions struct {
	WorkerID     string
	LeaseSeconds int
}

// processChild wraps an exec.Cmd whose process group the supervisor owns.
type processChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
	code int
}

func newProcessChild(cmd *exec.Cmd) *processChild {
	c := &processChild{cmd: cmd, done: make(chan struct{})}
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
	_ = killProcessGroup(pid, syscallSIGTERM)
	timer := time.AfterFunc(grace, func() {
		_ = killProcessGroup(pid, syscallSIGKILL)
	})
	defer timer.Stop()
	select {
	case <-c.done:
	case <-time.After(grace + time.Second):
	}
}

// processRunner is the production Runner: exec with POSIX process groups.
type processRunner struct{}

func (processRunner) Spawn(opts SpawnOptions) (Child, error) {
	if len(opts.Argv) == 0 {
		return nil, errors.New("empty argv")
	}
	cmd := exec.Command(opts.Argv[0], opts.Argv[1:]...)
	configureSysProcAttr(cmd)
	cmd.Dir = opts.Dir
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(opts.RunDir, "child.pid"),
		[]byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	return newProcessChild(cmd), nil
}

// workerResult is the JSON contract a worker writes to result.json.
type workerResult struct {
	OK                bool            `json:"ok"`
	Status            string          `json:"status"`
	Reason            string          `json:"reason,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	ContractViolation json.RawMessage `json:"contract_violation,omitempty"`
}

var fencedJSONPattern = regexp.MustCompile("(?s)" + "`" + `json\s*(\{.*?\})` + "`")

// RunOnce claims one task and drives a single supervised attempt to a
// terminal or paused outcome, cleaning private artifacts either way.
func (s *Supervisor) RunOnce(ctx context.Context, opts RunOptions) (*controlplane.Task, error) {
	task, err := s.cp.ClaimTask(ctx, opts.WorkerID, opts.LeaseSeconds)
	if err != nil || task == nil {
		return nil, err
	}
	token := derefString(task.LeaseToken)

	var req taskRequest
	if uerr := json.Unmarshal(task.Request, &req); uerr != nil {
		return nil, fmt.Errorf("decode task request: %w", uerr)
	}

	runDir := filepath.Join(s.runRoot, task.TaskID, fmt.Sprintf("attempt-%d", task.Attempts))
	if mkErr := os.MkdirAll(runDir, 0700); mkErr != nil {
		return nil, fmt.Errorf("create run dir: %w", mkErr)
	}
	promptPath := filepath.Join(runDir, "prompt.txt")
	resultPath := filepath.Join(runDir, "result.json")
	stdoutPath := filepath.Join(runDir, "worker.stdout")
	stderrPath := filepath.Join(runDir, "worker.stderr")

	if werr := os.WriteFile(promptPath, []byte(req.Prompt), 0600); werr != nil {
		return nil, fmt.Errorf("write prompt: %w", werr)
	}
	defer os.Remove(promptPath) // prompt.txt never survives an attempt.

	outFile, oerr := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if oerr != nil {
		return nil, fmt.Errorf("open stdout capture: %w", oerr)
	}
	defer outFile.Close()
	errFile, eerr := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if eerr != nil {
		return nil, fmt.Errorf("open stderr capture: %w", eerr)
	}
	defer errFile.Close()

	child, spawnErr := s.runner.Spawn(SpawnOptions{
		Argv:       s.buildArgv(req, promptPath, resultPath),
		Dir:        firstNonEmpty(firstOf(req.AddDirs), s.runRoot),
		Stdout:     outFile,
		Stderr:     errFile,
		ResultPath: resultPath,
		RunDir:     runDir,
	})

	if spawnErr != nil {
		if !s.cp.StartTask(task.TaskID, opts.WorkerID, token) {
			return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
		}
		_, _ = s.cp.FinishAttempt(task.TaskID, opts.WorkerID, token, controlplane.FinishAttemptParams{
			Result:    mustJSON(map[string]any{"ok": false, "status": "spawn_failed"}),
			Success:   false,
			Retryable: true,
			Err:       dispatch.SanitizeOutput(spawnErr.Error()),
		})
		return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
	}

	if !s.cp.StartTask(task.TaskID, opts.WorkerID, token) {
		child.Terminate(terminateGrace)
		return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
	}

	reason := s.awaitOutcome(ctx, child, task.TaskID, opts.WorkerID, token, req, opts.LeaseSeconds)
	// Close capture handles before collect reads and removes the files; on
	// Windows an open handle makes os.Remove fail with a sharing violation.
	_ = outFile.Close()
	_ = errFile.Close()
	return s.collect(ctx, child, reason, task.TaskID, opts.WorkerID, token,
		runDir, promptPath, resultPath, stdoutPath, stderrPath)
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

	for {
		if err := ctx.Err(); err != nil {
			return "cancelled"
		}
		t, gerr := s.cp.GetTask(ctx, taskID)
		if gerr != nil || t == nil || t.State != controlplane.StateRunning || !sameLease(t, workerID, token) {
			return "lease_lost"
		}
		if t.CancelRequested {
			return "cancelled"
		}
		now := s.clock()
		if !now.Before(deadline) {
			return "timeout"
		}
		if !now.Before(nextBeat) {
			if herr := s.cp.RenewHeartbeat(ctx, taskID, workerID, leaseSeconds); herr != nil {
				return "lease_lost"
			}
			nextBeat = now.Add(heartbeat)
		}
		select {
		case <-child.Done():
			return ""
		case <-time.After(s.pollInterval):
		}
	}
}

// collect terminates any surviving child, applies the terminal branch, and
// guarantees output capture files are removed and evidence exported.
func (s *Supervisor) collect(
	ctx context.Context,
	child Child,
	reason, taskID, workerID, token, runDir, promptPath, resultPath, stdoutPath, stderrPath string,
) (*controlplane.Task, error) {
	select {
	case <-child.Done():
	default:
		child.Terminate(terminateGrace)
	}
	code := child.WaitCode()

	stdoutRaw, _ := os.ReadFile(stdoutPath)
	stderrRaw, _ := os.ReadFile(stderrPath)
	stdoutText := dispatch.CaptureBounded(stdbuf(stdoutRaw), s.captureMaxBytes)
	stderrText := dispatch.CaptureBounded(stdbuf(stderrRaw), s.captureMaxBytes)
	os.Remove(stdoutPath) // worker output is never persisted.
	os.Remove(stderrPath)

	switch reason {
	case "lease_lost":
		return s.snapshot(ctx, taskID, runDir, promptPath)
	case "cancelled":
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    mustJSON(outcomeEnvelope(false, "cancelled", stdoutText, stderrText)),
			Success:   false,
			Retryable: false,
			Err:       "cancelled by orchestrator",
		})
		if ferr != nil {
			return nil, fmt.Errorf("finish cancelled attempt: %w", ferr)
		}
	case "timeout":
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    mustJSON(outcomeEnvelope(false, "timeout", stdoutText, stderrText)),
			Success:   false,
			Retryable: true,
			Err:       "execution deadline exceeded",
		})
		if ferr != nil {
			return nil, fmt.Errorf("finish timed-out attempt: %w", ferr)
		}
	case "invalid_timeout":
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    mustJSON(outcomeEnvelope(false, "invalid_result", stdoutText, stderrText)),
			Success:   false,
			Retryable: false,
			Err:       "task requested an invalid execution timeout",
		})
		if ferr != nil {
			return nil, fmt.Errorf("finish invalid-timeout attempt: %w", ferr)
		}
	default:
		wr := readWorkerResult(resultPath)
		success := code == 0 && wr.OK
		if paused := s.maybePause(ctx, wr, stdoutText, taskID, workerID, token); paused {
			break
		}
		finishErr := ""
		if !success {
			finishErr = firstNonEmpty(wr.Reason, wr.Status, "failed")
		}
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    mustResultJSON(wr, stdoutText, stderrText),
			Success:   success,
			Retryable: len(wr.ContractViolation) == 0,
			Err:       finishErr,
		})
		if ferr != nil {
			return nil, fmt.Errorf("finish attempt: %w", ferr)
		}
	}
	return s.snapshot(ctx, taskID, runDir, promptPath)
}

// maybePause transitions NEEDS_INFO/BLOCKED outcomes (declared in the result
// file or fenced inside captured stdout) into paused states, releasing the lease.
func (s *Supervisor) maybePause(ctx context.Context, wr workerResult, stdoutText, taskID, workerID, token string) bool {
	candidates := []workerResult{wr}
	for _, raw := range fencedJSONPattern.FindAllStringSubmatch(stdoutText, -1) {
		var fenced workerResult
		if json.Unmarshal([]byte(raw[1]), &fenced) == nil && fenced.Status != "" {
			candidates = append(candidates, fenced)
		}
	}
	for _, candidate := range candidates {
		if candidate.Status != controlplane.StateNeedsInfo && candidate.Status != controlplane.StateBlocked {
			continue
		}
		reason := firstNonEmpty(candidate.Reason, candidate.Summary, strings.ToLower(candidate.Status))
		payload := mustResultJSON(candidate, "", "")
		if _, err := s.cp.PauseTask(taskID, workerID, token, candidate.Status, payload, reason); err != nil {
			return false
		}
		return true
	}
	return false
}

func readWorkerResult(resultPath string) workerResult {
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		return workerResult{OK: false, Status: "invalid_result"}
	}
	var wr workerResult
	if json.Unmarshal(raw, &wr) != nil {
		return workerResult{OK: false, Status: "invalid_result"}
	}
	return wr
}

// buildArgv assembles the worker invocation mirroring the baseline contract:
// prompt file in, structured result file out, explicit scope roots attached.
func (s *Supervisor) buildArgv(req taskRequest, promptPath, resultPath string) []string {
	argv := []string{
		s.binaryPath,
		"--prompt-file", promptPath,
		"--model", req.Model,
		"--role", req.Role,
		"--permission", req.Permission,
		"--timeout", req.Timeout,
		"--out", resultPath,
	}
	for _, dir := range req.AddDirs {
		argv = append(argv, "--add-dir", dir)
	}
	if req.SkipPermiss {
		argv = append(argv, "--dangerously-skip-permissions")
	}
	return argv
}

// snapshot removes leftover artifacts, exports the sealed receipt, and
// returns the freshest task view.
func (s *Supervisor) snapshot(ctx context.Context, taskID, runDir string, leftovers ...string) (*controlplane.Task, error) {
	for _, path := range leftovers {
		os.Remove(path)
	}
	s.ExportReceipt(ctx, taskID, runDir)
	return s.cp.GetTask(ctx, taskID)
}

// ExportReceipt writes the redacted task snapshot as sealed evidence into the
// attempt directory; failures are swallowed so cleanup never masks outcomes.
func (s *Supervisor) ExportReceipt(ctx context.Context, taskID, runDir string) {
	snap, err := s.cp.GetTask(ctx, taskID)
	if err != nil || snap == nil {
		return
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(runDir, "receipt.json"), data, 0600)
}

// LoopOptions parameterize RunLoop.
type LoopOptions struct {
	WorkerID     string
	LeaseSeconds int
	Once         bool
}

// RunLoop drains claimable tasks until none remain, the context ends, or
// once-mode completes a single attempt. It returns a process-style exit code.
func (s *Supervisor) RunLoop(ctx context.Context, opts LoopOptions) int {
	s.reapOrphans()
	for {
		if ctx.Err() != nil {
			return ExitCodeForSignal(15)
		}
		task, err := s.RunOnce(ctx, RunOptions{WorkerID: opts.WorkerID, LeaseSeconds: opts.LeaseSeconds})
		if err != nil || task == nil {
			return 0
		}
		if opts.Once {
			switch task.State {
			case controlplane.StateSucceeded, controlplane.StateCancelled, controlplane.StateQueued:
				return 0
			default:
				return 1
			}
		}
	}
}

// reapOrphans kills process groups recorded by stale child.pid files left by
// crashed predecessors; best-effort only, never fails the run.
func (s *Supervisor) reapOrphans() {
	if runtime.GOOS == "windows" {
		return
	}
	taskDirs, err := os.ReadDir(s.runRoot)
	if err != nil {
		return
	}
	for _, taskDir := range taskDirs {
		if !taskDir.IsDir() {
			continue
		}
		attempts, err := os.ReadDir(filepath.Join(s.runRoot, taskDir.Name()))
		if err != nil {
			continue
		}
		for _, attempt := range attempts {
			pidPath := filepath.Join(s.runRoot, taskDir.Name(), attempt.Name(), "child.pid")
			raw, rerr := os.ReadFile(pidPath)
			if rerr != nil {
				continue
			}
			pid, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if perr == nil && pid > 0 {
				_ = killProcessGroup(pid, syscallSIGKILL)
			}
			_ = os.Remove(pidPath)
		}
	}
}

// ExitCodeForSignal maps a terminating signal number onto the shell exit-code
// convention (128+n), so SIGTERM reports 143 exactly like the baseline.
func ExitCodeForSignal(sig int) int { return 128 + sig }

func sameLease(t *controlplane.Task, workerID, token string) bool {
	return t.LeaseOwner != nil && *t.LeaseOwner == workerID &&
		t.LeaseToken != nil && *t.LeaseToken == token
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func firstOf(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	return dirs[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func clampDuration(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func stdbuf(raw []byte) []byte {
	if raw == nil {
		return []byte{}
	}
	return raw
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{"ok":false,"status":"marshal_error"}`)
	}
	return data
}

func outcomeEnvelope(ok bool, status, stdout, stderr string) map[string]any {
	return map[string]any{
		"ok":     ok,
		"status": status,
		"stdout": dispatch.SanitizeOutput(stdout),
		"stderr": dispatch.SanitizeOutput(stderr),
	}
}

func mustResultJSON(wr workerResult, stdout, stderr string) json.RawMessage {
	envelope := map[string]any{
		"ok":     wr.OK,
		"status": wr.Status,
	}
	if wr.Reason != "" {
		envelope["reason"] = wr.Reason
	}
	if wr.Summary != "" {
		envelope["summary"] = wr.Summary
	}
	if len(wr.ContractViolation) > 0 {
		envelope["contract_violation"] = wr.ContractViolation
	}
	if stdout != "" {
		envelope["stdout"] = dispatch.SanitizeOutput(stdout)
	}
	if stderr != "" {
		envelope["stderr"] = dispatch.SanitizeOutput(stderr)
	}
	return mustJSON(envelope)
}
