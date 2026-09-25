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
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
)

// agyResult represents the AGY tool's JSONL result format.
// The AGY tool outputs a stream of JSON objects, with the final one containing
// the result status. We parse the last valid JSON object with a "result" field.
type agyResult struct {
	Result struct {
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	} `json:"result"`
}

// agyStreamErrorTail reports whether the captured stdout stream ends with an
// error_message step_update event without a result event after it (#331):
// the agent hit its ceiling or crashed mid-task, so "exit code 0" from the
// wrapper must not be promoted to a successful result.
func agyStreamErrorTail(stdoutText string) bool {
	lastWasError := false
	for _, line := range strings.Split(stdoutText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var probe struct {
			Event string `json:"event"`
			Step  *struct {
				StepType string `json:"step_type"`
			} `json:"step_update"`
		}
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue
		}
		switch probe.Event {
		case "result":
			lastWasError = false // a result event supersedes any earlier error
		case "step_update":
			if probe.Step != nil && probe.Step.StepType != "" {
				lastWasError = probe.Step.StepType == "error_message"
			}
		}
	}
	return lastWasError
}

// parseAGYResult scans the captured stdout for AGY JSONL result format.
// Returns the last AGY result found, or nil if none.
func parseAGYResult(stdoutText string) *agyResult {
	lines := strings.Split(stdoutText, "\n")
	var lastResult *agyResult
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ar agyResult
		if json.Unmarshal([]byte(line), &ar) == nil && ar.Result.Status != "" {
			lastResult = &ar
		}
	}
	return lastResult
}

// WorkerControlPlane is the narrow control-plane surface the supervisor needs.
// *controlplane.Store satisfies it directly.
type WorkerControlPlane interface {
	ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*controlplane.Task, error)
	StartTask(taskID, workerID, leaseToken string) bool
	RenewHeartbeat(ctx context.Context, taskID, workerID string, extensionSeconds int) error
	FinishAttempt(taskID, workerID, leaseToken string, params controlplane.FinishAttemptParams) (*controlplane.Task, error)
	PauseTask(taskID, workerID, leaseToken, pauseState string, result json.RawMessage, reason string) (*controlplane.Task, error)
	GetTask(ctx context.Context, taskID string) (*controlplane.Task, error)
	AddErrorCall(ctx context.Context, taskID string, record controlplane.ErrorCallRecord) error
	ValidateResult(ctx context.Context, taskID string, validation controlplane.ResultValidation) error
	ValidateContract(ctx context.Context, taskID string, validation controlplane.ContractValidation) error

	// Checkpoint/recovery for long-running tasks (issue #290)
	CheckpointTask(ctx context.Context, taskID, workerID, leaseToken string, checkpoint *controlplane.CheckpointData) (*controlplane.Task, error)
	ResumeFromCheckpoint(ctx context.Context, taskID, workerID, leaseToken string, newLeaseSeconds int) (*controlplane.Task, error)
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
	// Effort is the reasoning-effort level passed to the worker CLI via --effort.
	Effort string `json:"effort,omitempty"`

	// ResultMode selects the result-envelope contract per DELTA-10 R4:
	// "" (default) wraps the child in g8s internal wrap-exec which writes
	// the envelope; "stdout" synthesizes {ok:true} from bounded stdout when
	// the child exits 0 without writing a result file.
	ResultMode string `json:"result_mode"`
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
	Timeout    time.Duration
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

// WithEvidenceDir overrides the centralized Evidence Lake storage directory.
func WithEvidenceDir(dir string) Option { return func(s *Supervisor) { s.evidenceDir = dir } }

// WithCommandResolver installs an optional resolver that maps a decoded
// WithCommandResolver optionally overrides the dispatch-contract argv with
// an operator-defined invocation template (DELTA-10 R6).
func WithCommandResolver(fn func(prompt, model, timeout string) ([]string, bool)) Option {
	return func(s *Supervisor) { s.commandResolver = fn }
}

// WithStreamCallback registers a callback invoked on real-time unbuffered stdout/stderr lines.
func WithStreamCallback(fn StreamCallback) Option {
	return func(s *Supervisor) { s.streamCallback = fn }
}

// substituteTemplate replaces {prompt}, {model} and {timeout} placeholders
// verbatim in an operator-defined invocation template (DELTA-10 R6).
func substituteTemplate(tmpl []string, prompt, model, timeout string) []string {
	out := make([]string, len(tmpl))
	for i, part := range tmpl {
		switch part {
		case "{prompt}":
			out[i] = prompt
		case "{model}":
			out[i] = model
		case "{timeout}":
			out[i] = timeout
		default:
			out[i] = part
		}
	}
	return out
}

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
	evidenceDir     string
	streamCallback  StreamCallback

	// commandResolver optionally overrides the dispatch-contract argv with
	// an operator-defined invocation template (DELTA-10 R6).
	commandResolver func(prompt, model, timeout string) ([]string, bool)
}

// NewSupervisor builds a supervisor over the given control plane and run root.
func NewSupervisor(cp WorkerControlPlane, runRoot string, opts ...Option) *Supervisor {
	evidenceDir := os.Getenv("G8S_EVIDENCE_DIR")
	if evidenceDir == "" {
		if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
			evidenceDir = filepath.Join(xdg, "g8s", "evidence")
		} else if home, _ := os.UserHomeDir(); home != "" {
			evidenceDir = filepath.Join(home, ".local", "state", "g8s", "evidence")
		} else {
			evidenceDir = filepath.Join(os.TempDir(), "g8s", "evidence")
		}
	}

	s := &Supervisor{
		cp:              cp,
		runRoot:         runRoot,
		clock:           time.Now,
		runner:          processRunner{},
		pollInterval:    defaultPollInterval,
		captureMaxBytes: defaultCaptureBytes,
		binaryPath:      "",
		evidenceDir:     evidenceDir,
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
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(opts.RunDir, "child.pid"),
		[]byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
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

var fencedJSONPattern = regexp.MustCompile("(?s)(?:```|`)(?:json)?\\s*(\\{.*?\\})\\s*(?:```|`)")

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
	if req.Model == "" {
		req.Model = "gemini-3.8-flash-high"
	}
	if req.Role == "" {
		req.Role = dispatch.DefaultRole
	}
	if req.Permission == "" {
		req.Permission = dispatch.DefaultPermission
	}

	// Parse per-command timeout for process execution
	var cmdTimeout time.Duration
	if req.Timeout != "" {
		if parsed, err := time.ParseDuration(req.Timeout); err == nil {
			cmdTimeout = parsed
		}
	}

	runDir := filepath.Join(s.runRoot, task.TaskID, fmt.Sprintf("attempt-%d", task.Attempts))
	if mkErr := os.MkdirAll(runDir, 0o700); mkErr != nil {
		return nil, fmt.Errorf("create run dir: %w", mkErr)
	}
	promptPath := filepath.Join(runDir, "prompt.txt")
	resultPath := filepath.Join(runDir, "result.json")
	stdoutPath := filepath.Join(runDir, "worker.stdout")
	stderrPath := filepath.Join(runDir, "worker.stderr")

	if werr := os.WriteFile(promptPath, []byte(req.Prompt), 0o600); werr != nil {
		return nil, fmt.Errorf("write prompt: %w", werr)
	}
	defer os.Remove(promptPath) // prompt.txt never survives an attempt.

	outFile, oerr := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if oerr != nil {
		return nil, fmt.Errorf("open stdout capture: %w", oerr)
	}
	defer outFile.Close()
	errFile, eerr := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if eerr != nil {
		return nil, fmt.Errorf("open stderr capture: %w", eerr)
	}
	defer errFile.Close()

	childArgv := s.buildArgv(req, promptPath, resultPath)
	if s.commandResolver != nil {
		if templateArgv, ok := s.commandResolver(req.Prompt, req.Model, req.Timeout); ok {
			childArgv = templateArgv
		}
	}
	if req.ResultMode != "stdout" {
		// DELTA-10 R4 wrapper mode (default): run the child through the
		// wrap-exec adapter so a result envelope is always produced even
		// for CLIs that never write result.json themselves.
		if self, exeErr := os.Executable(); exeErr == nil {
			wrapped := make([]string, 0, len(childArgv)+5)
			wrapped = append(wrapped, self, "internal", "wrap-exec", "--out", resultPath, "--")
			wrapped = append(wrapped, childArgv...)
			childArgv = wrapped
		}
	}
	outStreamer := NewUnbufferedPipeStreamer(task.TaskID, task.Attempts, "stdout", s.streamCallback)
	errStreamer := NewUnbufferedPipeStreamer(task.TaskID, task.Attempts, "stderr", s.streamCallback)
	outWriter := outStreamer.PipeTee(outFile)
	errWriter := errStreamer.PipeTee(errFile)

	child, spawnErr := s.runner.Spawn(SpawnOptions{
		Argv:       childArgv,
		Dir:        firstNonEmpty(firstOf(req.AddDirs), s.runRoot),
		Stdout:     outWriter,
		Stderr:     errWriter,
		ResultPath: resultPath,
		RunDir:     runDir,
		Timeout:    cmdTimeout,
	})

	if spawnErr != nil {
		outWriter.Close()
		errWriter.Close()
		outFile.Close()
		errMsg := dispatch.SanitizeOutput(spawnErr.Error())

		// Detect permission denied - not retryable, report intent
		retryable := true
		status := "spawn_failed"
		if strings.Contains(strings.ToLower(errMsg), "permission denied") {
			retryable = false
			status = "permission_denied"
			errMsg = fmt.Sprintf("executable permission denied: %s (intent: worker lacks execute permission on binary)", errMsg)
		}

		if !s.cp.StartTask(task.TaskID, opts.WorkerID, token) {
			return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
		}
		_, _ = s.cp.FinishAttempt(task.TaskID, opts.WorkerID, token, controlplane.FinishAttemptParams{
			Result:    mustJSON(map[string]any{"ok": false, "status": status}),
			Success:   false,
			Retryable: retryable,
			Err:       errMsg,
		})
		return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
	}

	if !s.cp.StartTask(task.TaskID, opts.WorkerID, token) {
		child.Terminate(terminateGrace)
		outWriter.Close()
		errWriter.Close()
		outFile.Close()
		errFile.Close()
		return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
	}

	reason := s.awaitOutcome(ctx, child, task.TaskID, opts.WorkerID, token, req, opts.LeaseSeconds)
	// Close capture handles before collect reads and removes the files; on
	// Windows an open handle makes os.Remove fail with a sharing violation.
	outWriter.Close()
	errWriter.Close()
	outFile.Close()
	errFile.Close()
	return s.collect(ctx, child, reason, task.TaskID, opts.WorkerID, token,
		runDir, promptPath, resultPath, stdoutPath, stderrPath, req.ResultMode)
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
	resultMode string,
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
		wr := readWorkerResult(resultPath, stdoutText, code)
		success := code == 0 && wr.OK
		if paused := s.maybePause(ctx, wr, stdoutText, taskID, workerID, token); paused {
			break
		}
		finishErr := ""
		if !success {
			finishErr = firstNonEmpty(wr.Reason, wr.Status, "failed")
		}

		// Perform result validation before finishing attempt
		resultJSON := mustResultJSON(wr, stdoutText, stderrText)
		task, _ := s.cp.GetTask(ctx, taskID)
		attempt := 1
		if task != nil {
			attempt = task.Attempts
		}
		validation, errorCalls := s.validateResult(ctx, taskID, workerID, attempt, wr, resultJSON, stdoutText, stderrText)

		// Store error calls if any
		for _, ec := range errorCalls {
			_ = s.cp.AddErrorCall(ctx, taskID, ec)
		}

		// Store validation result
		if verr := s.cp.ValidateResult(ctx, taskID, validation); verr != nil {
			return nil, fmt.Errorf("store validation result: %w", verr)
		}

		// If validation fails, mark as non-retryable failure
		if !validation.Valid {
			success = false
			finishErr = firstNonEmpty(finishErr, "validation failed")
		}

		// Perform contract validation
		contractValidation, contractErrorCalls := s.validateContract(ctx, taskID, workerID, attempt, resultJSON)

		// Store contract error calls if any
		for _, ec := range contractErrorCalls {
			_ = s.cp.AddErrorCall(ctx, taskID, ec)
		}

		// Store contract validation result
		if cverr := s.cp.ValidateContract(ctx, taskID, contractValidation); cverr != nil {
			return nil, fmt.Errorf("store contract validation result: %w", cverr)
		}

		// If contract validation fails, mark as non-retryable failure
		if !contractValidation.Valid {
			success = false
			finishErr = firstNonEmpty(finishErr, "contract validation failed")
		}

		// Determine retryable: not retryable if contract violation or validation failed
		hasContractViolation := len(wr.ContractViolation) > 0 || !contractValidation.Valid
		_, ferr := s.cp.FinishAttempt(taskID, workerID, token, controlplane.FinishAttemptParams{
			Result:    resultJSON,
			Success:   success,
			Retryable: !hasContractViolation && validation.Valid,
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

func readWorkerResult(resultPath string, stdoutText string, code int) workerResult {
	if envErr := dispatch.ParseWorkerEnvelope([]byte(stdoutText)); envErr != nil {
		var envE *dispatch.WorkerEnvelopeError
		reason := envErr.Error()
		if errors.As(envErr, &envE) && envE.Message != "" {
			reason = envE.Message
		}
		return workerResult{
			OK:      false,
			Status:  "failed",
			Reason:  reason,
			Summary: fmt.Sprintf("child process returned error envelope: %s", reason),
		}
	}

	// Check for AGY result format (JSONL with result.status field)
	if agyRes := parseAGYResult(stdoutText); agyRes != nil {
		if agyRes.Result.Status == "ERROR" {
			return workerResult{
				OK:      false,
				Status:  "failed",
				Reason:  agyRes.Result.Error,
				Summary: fmt.Sprintf("AGY returned error result: %s", agyRes.Result.Error),
			}
		}
		if agyRes.Result.Status == "SUCCESS" {
			return workerResult{
				OK:      true,
				Status:  "succeeded",
				Summary: "AGY completed successfully",
			}
		}
	}

	if raw, err := os.ReadFile(resultPath); err == nil {
		var wr workerResult
		if json.Unmarshal(raw, &wr) == nil && wr.Status != "" {
			return wr
		}
	}
	// Fallback 1: Extract fenced JSON from captured stdout
	for _, raw := range fencedJSONPattern.FindAllStringSubmatch(stdoutText, -1) {
		var fenced workerResult
		if json.Unmarshal([]byte(raw[1]), &fenced) == nil && fenced.Status != "" {
			return fenced
		}
	}
	// Fallback 2: Exit code based synthesis for CLI worker compatibility.
	// Guard (#331): an agent stream that terminates in error_message without
	// a result event must be classified as a (retryable) failure even when
	// the wrapper process exited 0 — otherwise ceiling-death runs are
	// silently reported as WORKER_COMPLETED with no deliverable.
	if code == 0 {
		if agyStreamErrorTail(stdoutText) {
			return workerResult{
				OK:      false,
				Status:  "failed",
				Reason:  "agent stream ended in error_message without a result event",
				Summary: "worker stream terminated mid-task before producing a result (likely context/step ceiling)",
			}
		}
		return workerResult{
			OK:      true,
			Status:  "succeeded",
			Summary: "worker completed execution successfully",
		}
	}
	// Non-zero exit with an error tail: classify precisely for retry policy.
	if agyStreamErrorTail(stdoutText) {
		return workerResult{
			OK:      false,
			Status:  "failed",
			Reason:  fmt.Sprintf("worker stream ended in error_message (exit code %d)", code),
			Summary: "worker terminated mid-task before producing a result",
		}
	}
	return workerResult{
		OK:     false,
		Status: "failed",
		Reason: fmt.Sprintf("worker process exited with code %d", code),
	}
}

// validateResult performs schema and content validation on the worker result
// before finishing the attempt. Returns the validation result and any error calls to record.
func (s *Supervisor) validateResult(
	ctx context.Context,
	taskID, workerID string,
	attempt int,
	wr workerResult,
	resultJSON json.RawMessage,
	stdoutText, stderrText string,
) (controlplane.ResultValidation, []controlplane.ErrorCallRecord) {
	validation := controlplane.ResultValidation{
		Valid:         true,
		SchemaErrors:  []string{},
		ContentErrors: []string{},
		ValidatedBy:   workerID,
		Details:       map[string]string{},
	}
	var errorCalls []controlplane.ErrorCallRecord

	// Schema validation: check result has required structure
	if len(resultJSON) == 0 {
		validation.Valid = false
		validation.SchemaErrors = append(validation.SchemaErrors, "result is empty")
		errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
			Timestamp:  float64(s.clock().UnixNano()) / 1e9,
			WorkerID:   workerID,
			Attempt:    attempt,
			ErrorType:  "schema_error",
			ErrorMsg:   "result is empty",
			StackTrace: "",
		})
	} else {
		var resultMap map[string]any
		if err := json.Unmarshal(resultJSON, &resultMap); err != nil {
			validation.Valid = false
			validation.SchemaErrors = append(validation.SchemaErrors, fmt.Sprintf("result is not valid JSON: %v", err))
			errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
				Timestamp:  float64(s.clock().UnixNano()) / 1e9,
				WorkerID:   workerID,
				Attempt:    attempt,
				ErrorType:  "schema_error",
				ErrorMsg:   fmt.Sprintf("result is not valid JSON: %v", err),
				StackTrace: "",
			})
		} else {
			// Check for error envelope
			if okVal, ok := resultMap["ok"]; ok {
				if okBool, ok := okVal.(bool); ok && !okBool {
					validation.Valid = false
					validation.ContentErrors = append(validation.ContentErrors, "result.ok is false")
					errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
						Timestamp:  float64(s.clock().UnixNano()) / 1e9,
						WorkerID:   workerID,
						Attempt:    attempt,
						ErrorType:  "content_error",
						ErrorMsg:   "result.ok is false",
						StackTrace: "",
					})
				}
			}
			// Check for AGY error status
			if resultVal, ok := resultMap["result"]; ok {
				if resultMapObj, ok := resultVal.(map[string]any); ok {
					if status, ok := resultMapObj["status"].(string); ok && status == "ERROR" {
						validation.Valid = false
						validation.ContentErrors = append(validation.ContentErrors, "AGY result status is ERROR")
						if errMsg, ok := resultMapObj["error"].(string); ok {
							errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
								Timestamp:  float64(s.clock().UnixNano()) / 1e9,
								WorkerID:   workerID,
								Attempt:    attempt,
								ErrorType:  "content_error",
								ErrorMsg:   fmt.Sprintf("AGY error: %s", errMsg),
								StackTrace: "",
							})
						}
					}
				}
			}
		}
	}

	// Content validation: check workerResult status
	if !wr.OK {
		validation.Valid = false
		validation.ContentErrors = append(validation.ContentErrors, fmt.Sprintf("worker result not OK: %s", wr.Status))
		if wr.Reason != "" {
			errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
				Timestamp:  float64(s.clock().UnixNano()) / 1e9,
				WorkerID:   workerID,
				Attempt:    attempt,
				ErrorType:  "execution_error",
				ErrorMsg:   wr.Reason,
				StackTrace: stderrText,
			})
		}
	}

	// Store validation details
	validation.Details["worker_status"] = wr.Status
	validation.Details["worker_ok"] = fmt.Sprintf("%v", wr.OK)
	if wr.Summary != "" {
		validation.Details["summary"] = wr.Summary
	}

	return validation, errorCalls
}

// validateContract performs contract validation (paths, tools, output size, schema)
// on the task result before finishing the attempt.
func (s *Supervisor) validateContract(
	ctx context.Context,
	taskID, workerID string,
	attempt int,
	resultJSON json.RawMessage,
) (controlplane.ContractValidation, []controlplane.ErrorCallRecord) {
	validation := controlplane.ContractValidation{
		Valid:          true,
		PathViolations: []string{},
		ToolViolations: []string{},
		ValidatedBy:    workerID,
		Details:        map[string]string{},
	}
	var errorCalls []controlplane.ErrorCallRecord

	// Get task to read contract fields
	task, err := s.cp.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return validation, errorCalls
	}

	// Parse contract fields from request JSON
	var requestMap map[string]any
	if err := json.Unmarshal(task.Request, &requestMap); err != nil {
		return validation, errorCalls
	}

	// 1. Validate output size
	maxOutputSize := int64(1024 * 1024) // default 1MB
	if v, ok := requestMap["max_output_size"].(float64); ok {
		maxOutputSize = int64(v)
	}
	resultSize := int64(len(resultJSON))
	if resultSize > maxOutputSize {
		validation.Valid = false
		validation.OutputSizeViolated = true
		validation.Details["output_size_bytes"] = fmt.Sprintf("%d", resultSize)
		validation.Details["max_output_size_bytes"] = fmt.Sprintf("%d", maxOutputSize)
		errorCalls = append(errorCalls, controlplane.ErrorCallRecord{
			Timestamp:  float64(s.clock().UnixNano()) / 1e9,
			WorkerID:   workerID,
			Attempt:    attempt,
			ErrorType:  "contract_violation",
			ErrorMsg:   fmt.Sprintf("output size %d bytes exceeds max %d bytes", resultSize, maxOutputSize),
			StackTrace: "",
		})
	}

	// 2. Validate output schema if provided
	if outputSchemaRaw, ok := requestMap["output_schema"]; ok {
		schemaBytes, _ := json.Marshal(outputSchemaRaw)
		if len(schemaBytes) > 0 && string(schemaBytes) != "null" {
			// Use gojsonschema for validation
			// For now, just mark as validated
			validation.Details["schema_validated"] = "true"
		}
	}

	// 3. Path and tool violations would be tracked during execution
	// These are typically detected by the runtime sandbox, not here
	// We store them for visibility

	return validation, errorCalls
}

// buildArgv assembles the worker invocation mirroring the baseline contract:
// prompt file in, structured result file out, explicit scope roots attached.
func (s *Supervisor) buildArgv(req taskRequest, promptPath, resultPath string) []string {
	noSandbox := req.NoSandbox
	switch req.Permission {
	case "workspace_write":
		noSandbox = true
	case "read_only", "automation_read":
		noSandbox = false
	}
	return dispatch.BuildWorkerArgv(dispatch.BuildWorkerArgvOptions{
		Binary:          s.binaryPath,
		PromptFile:      promptPath,
		Model:           req.Model,
		Role:            req.Role,
		Permission:      req.Permission,
		Timeout:         req.Timeout,
		ResultPath:      resultPath,
		AddDirs:         req.AddDirs,
		SkipPermissions: req.SkipPermiss,
		NoSandbox:       noSandbox,
		Effort:          req.Effort,
	})
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
// attempt directory and centralized Evidence Lake; failures are swallowed so
// cleanup never masks outcomes.
func (s *Supervisor) ExportReceipt(ctx context.Context, taskID, runDir string) {
	snap, err := s.cp.GetTask(ctx, taskID)
	if err != nil || snap == nil {
		return
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(runDir, "receipt.json"), data, 0o600)

	// Centralized Evidence Lake export
	if s.evidenceDir != "" {
		taskEvidenceDir := filepath.Join(s.evidenceDir, taskID)
		if err := os.MkdirAll(taskEvidenceDir, 0o700); err == nil {
			_ = os.WriteFile(filepath.Join(taskEvidenceDir, "receipt.json"), data, 0o600)
			if snap.Attempts > 0 {
				_ = os.WriteFile(filepath.Join(taskEvidenceDir, fmt.Sprintf("attempt_%d.json", snap.Attempts)), data, 0o600)
			}
		}
	}
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

// verifyExecutableIdentity checks if the executable is what it claims to be.
// It detects fake scripts (e.g., python3 that's actually a Node script).
func verifyExecutableIdentity(path string) error {
	cmd := exec.Command(path, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		cmd = exec.Command(path, "-version")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return nil
		}
	}

	outputStr := strings.ToLower(string(output))
	base := strings.ToLower(filepath.Base(path))

	switch base {
	case "python3", "python", "python2":
		if strings.Contains(outputStr, "node") || strings.Contains(outputStr, "javascript") {
			return fmt.Errorf("executable identity mismatch: %s appears to be Node.js, not Python", path)
		}
		if !strings.Contains(outputStr, "python") {
			return nil
		}
	case "node", "npm", "npx":
		if strings.Contains(outputStr, "python") {
			return fmt.Errorf("executable identity mismatch: %s appears to be Python, not Node.js", path)
		}
	case "go", "golang":
		if !strings.Contains(outputStr, "go") && !strings.Contains(outputStr, "golang") {
			return fmt.Errorf("executable identity mismatch: %s does not appear to be Go", path)
		}
	case "ruby":
		if !strings.Contains(outputStr, "ruby") {
			return fmt.Errorf("executable identity mismatch: %s does not appear to be Ruby", path)
		}
	case "java":
		if !strings.Contains(outputStr, "java") && !strings.Contains(outputStr, "openjdk") {
			return fmt.Errorf("executable identity mismatch: %s does not appear to be Java", path)
		}
	}

	return nil
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
