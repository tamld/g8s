// Package worker implements the DELTA-09 task supervisor: it claims leased
// tasks from the control plane, runs each attempt inside a private run
// directory and a killable POSIX process group, enforces timeouts against an
// injectable clock, and exports sealed evidence without ever persisting raw
// worker output.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/controlplane"
	"github.com/tamld/g8s/internal/dispatch"
	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/pathutil"
	"github.com/tamld/g8s/internal/reflex"
	"github.com/tamld/g8s/internal/telemetry"
)

// agyUsage carries token usage metrics reported by the agy tool wrapper.

type WorkerControlPlane interface {
	ClaimTask(ctx context.Context, workerID string, leaseDurationSeconds int) (*controlplane.Task, error)
	ClaimTaskProvider(ctx context.Context, workerID string, leaseDurationSeconds int, provider string) (*controlplane.Task, error)
	ClaimTaskFiltered(ctx context.Context, workerID string, leaseDurationSeconds int, filter controlplane.ClaimFilter) (*controlplane.Task, error)
	StartTask(taskID, workerID, leaseToken string) bool
	RenewHeartbeat(ctx context.Context, taskID, workerID string, extensionSeconds int) error
	FinishAttempt(taskID, workerID, leaseToken string, params controlplane.FinishAttemptParams) (*controlplane.Task, error)
	PauseTask(taskID, workerID, leaseToken, pauseState string, result json.RawMessage, reason string) (*controlplane.Task, error)
	GetTask(ctx context.Context, taskID string) (*controlplane.Task, error)
	AddErrorCall(ctx context.Context, taskID string, record controlplane.ErrorCallRecord) error
	ValidateResult(ctx context.Context, taskID string, validation controlplane.ResultValidation) error
	ValidateContract(ctx context.Context, taskID string, validation controlplane.ContractValidation) error
	// ConsumeWriteReceipt marks a delegated-write receipt consumed by the
	// task (#346): exactly one caller succeeds; replay by a different task
	// trips controlplane.ErrReceiptAlreadyConsumed.
	ConsumeWriteReceipt(ctx context.Context, receiptID, consumerTaskID string) error

	// Checkpoint/recovery for long-running tasks (issue #290)
	CheckpointTask(ctx context.Context, taskID, workerID, leaseToken string, checkpoint *controlplane.CheckpointData) (*controlplane.Task, error)
	ResumeFromCheckpoint(ctx context.Context, taskID, workerID, leaseToken string, newLeaseSeconds int) (*controlplane.Task, error)

	// Receipt export outcome persistence (issue #658)
	RecordReceiptExport(ctx context.Context, taskID string, status controlplane.ReceiptExportStatus) error
}

// taskRequest mirrors the worker-facing payload stored on every task.
type taskRequest struct {
	Prompt      string   `json:"prompt"`
	Provider    string   `json:"provider,omitempty"`
	Model       string   `json:"model"`
	Role        string   `json:"role"`
	Permission  string   `json:"permission"`
	Timeout     string   `json:"timeout"`
	AddDirs     []string `json:"add_dirs"`
	NoSandbox   bool     `json:"no_sandbox"`
	SkipPermiss bool     `json:"skip_permissions"`
	// Effort is the reasoning-effort level passed to the worker CLI via --effort.
	Effort string `json:"effort,omitempty"`

	EffortClass        string `json:"effort_class,omitempty"`
	EffortRequested    string `json:"effort_requested,omitempty"`
	EffortApplied      string `json:"effort_applied,omitempty"`
	EffortMismatch     bool   `json:"effort_mismatch,omitempty"`
	EffortBudgetTokens int    `json:"effort_budget_tokens,omitempty"`

	// ResultMode selects the result-envelope contract per DELTA-10 R4:
	// "" (default) wraps the child in g8s internal wrap-exec which writes
	// the envelope; "stdout" synthesizes {ok:true} from bounded stdout when
	// the child exits 0 without writing a result file.
	ResultMode string `json:"result_mode"`

	// ReceiptID carries the delegated-write receipt for workspace_write
	// tasks; consumed exactly once on a successful attempt (#346).
	ReceiptID string `json:"receipt_id,omitempty"`

	// ToolPolicy configures model tool use ("" | "default" | "none") (#616/#635/#653).
	ToolPolicy            string `json:"tool_policy,omitempty"`
	ToolPolicyEvidenceLog string `json:"-"`
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
	Env        []string
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

// WithCommandResolver installs an optional legacy resolver that maps a
// decoded task request to an operator-defined invocation template by model
// ID (DELTA-10 R6). Superseded by WithProviderCommandResolver; the two are
// mutually exclusive and the provider-aware resolver wins when both are set.
func WithCommandResolver(fn func(prompt, model, timeout string) ([]string, bool)) Option {
	return func(s *Supervisor) { s.commandResolver = fn }
}

// WithProviderCommandResolver installs a provider-aware resolver
// (DELTA-10 R6). It receives the task's provider so templates can be keyed
// by provider name; a returned error fails the attempt (no silent fallback
// to the default argv), while a nil error with empty argv falls through to
// the dispatch-contract default.
func WithProviderCommandResolver(fn func(prompt, provider, model, timeout string) ([]string, error)) Option {
	return func(s *Supervisor) { s.providerResolver = fn }
}

// WorktreeIsolator hands workspace_write attempts a private worktree
// directory so concurrent code-writing attempts in one worker process
// cannot clobber each other's uncommitted deliverables (#427). Implemented
// by the cmd layer over the orchestrator pool. Under #656, isolation
// fails closed for workspace_write: nil isolator or acquisition failure
// terminally fails the attempt.
type WorktreeIsolator interface {
	// AcquireWorktree returns the worktree directory for the task and a
	// release function. release(true) keeps the worktree for inspection
	// (deliverable survival — the point of #427); release(false) discards.
	AcquireWorktree(ctx context.Context, taskID string) (dir string, release func(keep bool), err error)
}

// RepoWorktreeIsolator extends WorktreeIsolator for cross-repo dispatches
// where task add_dirs point to a sibling git repository outside cwd.
type RepoWorktreeIsolator interface {
	WorktreeIsolator
	AcquireRepoWorktree(ctx context.Context, repoDir, taskID string) (dir string, release func(keep bool), err error)
}

// WithWorktreeIsolator wires the isolation seam. Only workspace_write
// attempts consult it; read_only attempts keep the shared checkout.
func WithWorktreeIsolator(iso WorktreeIsolator) Option {
	return func(s *Supervisor) { s.isolator = iso }
}

// WithStreamCallback registers a callback invoked on real-time unbuffered stdout/stderr lines.
func WithStreamCallback(fn StreamCallback) Option {
	return func(s *Supervisor) { s.streamCallback = fn }
}

// OutputQualityGate defines the interface for L3 post-run quality evaluations.
type OutputQualityGate interface {
	EmitOutputQualitySignal(ctx context.Context, req reflex.OutputQualityRequest) (reflex.ReflexSignal, error)
}

// WithQualityGate injects a custom quality gate for L3 post-run checks.
func WithQualityGate(g OutputQualityGate) Option {
	return func(s *Supervisor) { s.qualityGate = g }
}

// SubstituteTemplate replaces {prompt}, {model} and {timeout} placeholders
// verbatim in an operator-defined invocation template (DELTA-10 R6).
func SubstituteTemplate(tmpl []string, prompt, model, timeout string) []string {
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

// ProviderResolverOptions configures template resolution for providers and models.
type ProviderResolverOptions struct {
	ProviderTemplates     map[string][]string
	ModelTemplates        map[string][]string
	PlatformDispatchNames []string
	APICallNames          map[string]bool
}

// NewProviderCommandResolver builds a command resolver implementing DELTA-10 resolution order.
func NewProviderCommandResolver(opts ProviderResolverOptions) func(prompt, provider, model, timeout string) ([]string, error) {
	return func(prompt, provider, model, timeout string) ([]string, error) {
		if provider == "" {
			if tmpl, ok := opts.ModelTemplates[model]; ok {
				return SubstituteTemplate(tmpl, prompt, model, timeout), nil
			}
			return nil, nil // miss -> fallback to default agy argv
		}

		// provider != ""
		if opts.APICallNames != nil && opts.APICallNames[provider] {
			return nil, fmt.Errorf("provider %q is an api_call provider and is not executable on the queue path", provider)
		}

		if tmpl, ok := opts.ProviderTemplates[provider]; ok {
			return SubstituteTemplate(tmpl, prompt, model, timeout), nil
		}

		for _, name := range opts.PlatformDispatchNames {
			if name == provider {
				if defArgv, ok := resolveDefaultCLITemplate(provider, prompt, model, timeout); ok {
					return defArgv, nil
				}
				return nil, nil // miss -> fallback to default agy argv
			}
		}

		// Not found among platform_dispatch entries
		names := make([]string, len(opts.PlatformDispatchNames))
		copy(names, opts.PlatformDispatchNames)
		sort.Strings(names)
		available := strings.Join(names, ", ")
		if available == "" {
			available = "none"
		}
		return nil, fmt.Errorf("provider %q not found among platform_dispatch entries (available: %s)", provider, available)
	}
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
	// an operator-defined invocation template keyed by model ID (DELTA-10
	// R6, legacy seam).
	commandResolver func(prompt, model, timeout string) ([]string, bool)

	// providerResolver is the provider-aware resolver seam (DELTA-10 R6).
	// When set it wins over commandResolver and receives the task's
	// provider; errors fail the attempt instead of falling back silently.
	providerResolver func(prompt, provider, model, timeout string) ([]string, error)

	// isolator hands workspace_write attempts a private worktree directory
	// so concurrent code-writing attempts in one worker process cannot
	// clobber each other's uncommitted deliverables (#427). Nil = no
	// isolation (legacy shared-checkout behavior).
	isolator WorktreeIsolator

	// Closed-loop telemetry ingestion (#253): opt-in via G8S_TELEMETRY=1
	// (default ledger path) or G8S_TELEMETRY_DB=<path>. Lazily opened per
	// process; refcounted per in-flight run so concurrent runs (#394) close
	// the engine only after the last one exits, and closing resets the
	// lazy-init so later runs in the same process re-open a fresh engine
	// instead of silently dropping events.
	telMu     sync.Mutex
	telInited bool
	telEngine *telemetry.TelemetryEngine
	telRuns   int

	// L3 post-run quality gate reflex (#253/#371/#418): cached to avoid per-attempt
	// .env scanning and http.Client allocation.
	qualityGate   OutputQualityGate
	reflexOnce    sync.Once
	reflexGateIns *reflex.ReflexGate
}

// NewSupervisor builds a supervisor over the given control plane and run root.
func NewSupervisor(cp WorkerControlPlane, runRoot string, opts ...Option) *Supervisor {
	evidenceDir := os.Getenv("G8S_EVIDENCE_DIR")
	if evidenceDir == "" {
		evidenceDir = pathutil.DefaultEvidenceDir()
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
	Provider     string
	Model        string
	// Dir optionally overrides the child process working directory — the
	// concurrent loop's per-worker worktree (#394). Empty keeps the
	// request-driven default (first AddDir, else the run root).
	Dir string
}

// workerResult is the JSON contract a worker writes to result.json.
type workerResult struct {
	OK                      bool            `json:"ok"`
	Status                  string          `json:"status"`
	Reason                  string          `json:"reason,omitempty"`
	Summary                 string          `json:"summary,omitempty"`
	Response                string          `json:"response,omitempty"`
	ContractViolation       json.RawMessage `json:"contract_violation,omitempty"`
	Usage                   *agyUsage       `json:"usage,omitempty"`
	ProviderDurationSeconds float64         `json:"provider_duration_seconds,omitempty"`
}

// maxResultFileBytes caps the worker-written result file — a cheap worker-side
// DoS guard; larger files are a schema violation, not a parse target.
const maxResultFileBytes = 8 << 20

// readResultFile reads the worker's result file exactly once (single-read
// TOCTOU fix, #383 review): the same bytes feed result parsing and schema
// validation, so a compromised worker cannot swap the file between reads.
func readResultFile(path string) (raw []byte, oversize bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if fi.Size() > maxResultFileBytes {
		return nil, true
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return raw, false
}

// isWrapperEnvelopeMap reports whether the parsed result file matches the
// wrap-exec adapter's synthetic envelope shape (exactly the keys ok +
// exit_code), which the supervisor itself controls and therefore does not
// schema-validate (#383).
func isWrapperEnvelopeMap(m map[string]any) bool {
	if len(m) != 2 {
		return false
	}
	_, hasOK := m["ok"]
	_, hasExit := m["exit_code"]
	return hasOK && hasExit
}

// degenerateResponse reports whether a stream-sourced model response carries
// no deliverable content (#383): an explicit empty JSON object. An absent
// response field is NOT degenerate — legacy/synthetic streams may omit it.
func degenerateResponse(response string) bool {
	trimmed := strings.TrimSpace(response)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var obj map[string]any
	return json.Unmarshal([]byte(trimmed), &obj) == nil && len(obj) == 0
}

var fencedJSONPattern = regexp.MustCompile("(?s)(?:```|`)(?:json)?\\s*(\\{.*?\\})\\s*(?:```|`)")

// RunOnce claims one task and drives a single supervised attempt to a
// terminal or paused outcome, cleaning private artifacts either way.
func (s *Supervisor) RunOnce(ctx context.Context, opts RunOptions) (*controlplane.Task, error) {
	var task *controlplane.Task
	var err error
	if opts.Provider != "" || opts.Model != "" {
		task, err = s.cp.ClaimTaskFiltered(ctx, opts.WorkerID, opts.LeaseSeconds, controlplane.ClaimFilter{
			Provider: opts.Provider,
			Model:    opts.Model,
		})
	} else {
		task, err = s.cp.ClaimTask(ctx, opts.WorkerID, opts.LeaseSeconds)
	}
	if err != nil || task == nil {
		return nil, err
	}
	token := derefString(task.LeaseToken)
	s.telEnter()      // engine outlives every in-flight run (#394)
	defer s.telExit() // flush ingestion at run end (#253)

	var req taskRequest
	if uerr := json.Unmarshal(task.Request, &req); uerr != nil {
		return nil, fmt.Errorf("decode task request: %w", uerr)
	}
	s.ingestTrace(telemetry.TraceEventWorkerStarted, task.TaskID, nil, "")
	if req.Model == "" {
		req.Model = "gemini-3.8-flash-high"
	}
	if req.Role == "" {
		req.Role = dispatch.DefaultRole
	}
	if req.Permission == "" {
		req.Permission = dispatch.DefaultPermission
	}
	if req.Permission == "supplied_text" && req.ToolPolicy == "" {
		req.ToolPolicy = "none"
	}
	if req.ToolPolicy == "none" {
		hookDir, evidenceLogPath, cleanupHook, hErr := dispatch.SetupToolPolicyNoneWorkspace()
		if hErr != nil {
			return nil, hErr
		}
		defer cleanupHook()
		req.ToolPolicyEvidenceLog = evidenceLogPath
		req.AddDirs = append(append([]string{}, req.AddDirs...), hookDir)
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

	var resolvedArgv []string
	if s.providerResolver != nil {
		templateArgv, err := s.providerResolver(req.Prompt, req.Provider, req.Model, req.Timeout)
		if err != nil {
			outFile.Close()
			errFile.Close()
			errMsg := dispatch.SanitizeOutput(err.Error())
			if !s.cp.StartTask(task.TaskID, opts.WorkerID, token) {
				return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
			}
			_, _ = s.cp.FinishAttempt(task.TaskID, opts.WorkerID, token, controlplane.FinishAttemptParams{
				Result:    mustJSON(map[string]any{"ok": false, "status": "resolution_failed"}),
				Success:   false,
				Retryable: false,
				Err:       errMsg,
			})
			return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
		}
		if len(templateArgv) > 0 {
			resolvedArgv = templateArgv
		}
	} else if s.commandResolver != nil {
		if templateArgv, ok := s.commandResolver(req.Prompt, req.Model, req.Timeout); ok {
			resolvedArgv = templateArgv
		}
	}
	outStreamer := NewUnbufferedPipeStreamer(task.TaskID, task.Attempts, "stdout", s.streamCallback)
	errStreamer := NewUnbufferedPipeStreamer(task.TaskID, task.Attempts, "stderr", s.streamCallback)
	outWriter := outStreamer.PipeTee(outFile)
	errWriter := errStreamer.PipeTee(errFile)

	// #427: workspace_write attempts get per-attempt worktree isolation —
	// concurrent code-writing attempts sharing one checkout destroy each
	// other's uncommitted deliverables.
	// #656: workspace_write fails closed if isolation is unavailable.
	// Nil isolator, Acquire error, or empty dir with nil error produces a
	// structured isolation_unavailable failure, never invokes the child
	// command, and terminally releases/finishes the attempt without leaking
	// leases. read_only attempts are untouched.
	// #610: when req.AddDirs specifies an explicit target repository
	// (cross-repo dispatch), it takes precedence over pool worktree opts.Dir.
	explicitAddDir := strings.TrimSpace(firstOf(req.AddDirs))
	var spawnDir string
	if explicitAddDir != "" && explicitAddDir != "." && (s.runRoot == "" || filepath.Clean(explicitAddDir) != filepath.Clean(s.runRoot)) {
		spawnDir = explicitAddDir
	} else {
		spawnDir = firstNonEmpty(opts.Dir, explicitAddDir, s.runRoot)
	}
	var releaseWorktree func(keep bool)
	var isolatedWorktreeDir string
	if req.Permission == "workspace_write" {
		if s.isolator == nil {
			outWriter.Close()
			errWriter.Close()
			outFile.Close()
			errFile.Close()
			errMsg := "worktree isolation unavailable: isolator is nil"
			fmt.Fprintf(os.Stderr, "[warn] worker: %s for %s\n", errMsg, task.TaskID)
			if !s.cp.StartTask(task.TaskID, opts.WorkerID, token) {
				return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
			}
			_, _ = s.cp.FinishAttempt(task.TaskID, opts.WorkerID, token, controlplane.FinishAttemptParams{
				Result: mustJSON(map[string]any{
					"ok":     false,
					"status": "isolation_unavailable",
					"error":  errMsg,
					"reason": errMsg,
				}),
				Success:   false,
				Retryable: false,
				Err:       errMsg,
			})
			return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
		}

		var (
			wt      string
			release func(keep bool)
			err     error
		)
		if repoIso, ok := s.isolator.(RepoWorktreeIsolator); ok && spawnDir != "" {
			wt, release, err = repoIso.AcquireRepoWorktree(ctx, spawnDir, task.TaskID)
		} else {
			wt, release, err = s.isolator.AcquireWorktree(ctx, task.TaskID)
		}
		if err != nil || strings.TrimSpace(wt) == "" {
			if release != nil {
				release(false)
			}
			outWriter.Close()
			errWriter.Close()
			outFile.Close()
			errFile.Close()
			var errMsg string
			if err != nil {
				errMsg = fmt.Sprintf("worktree isolation unavailable: %v", err)
			} else {
				errMsg = "worktree isolation unavailable: isolator returned empty worktree directory"
			}
			fmt.Fprintf(os.Stderr, "[warn] worker: %s for %s\n", errMsg, task.TaskID)
			if !s.cp.StartTask(task.TaskID, opts.WorkerID, token) {
				return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
			}
			_, _ = s.cp.FinishAttempt(task.TaskID, opts.WorkerID, token, controlplane.FinishAttemptParams{
				Result: mustJSON(map[string]any{
					"ok":     false,
					"status": "isolation_unavailable",
					"error":  errMsg,
					"reason": errMsg,
				}),
				Success:   false,
				Retryable: false,
				Err:       errMsg,
			})
			return s.snapshot(ctx, task.TaskID, runDir, promptPath, stdoutPath, stderrPath)
		}

		origRepoDir := spawnDir
		spawnDir = wt
		releaseWorktree = release
		isolatedWorktreeDir = wt
		rewrittenDirs := make([]string, 0, len(req.AddDirs)+1)
		replaced := false
		for _, d := range req.AddDirs {
			trimmed := strings.TrimSpace(d)
			if !replaced && (trimmed == "" || trimmed == "." || (origRepoDir != "" && filepath.Clean(trimmed) == filepath.Clean(origRepoDir))) {
				rewrittenDirs = append(rewrittenDirs, wt)
				replaced = true
			} else {
				rewrittenDirs = append(rewrittenDirs, d)
			}
		}
		if !replaced {
			rewrittenDirs = append([]string{wt}, rewrittenDirs...)
		}
		req.AddDirs = rewrittenDirs
		fmt.Fprintf(os.Stderr, "[info] worker: attempt %s isolated in worktree %s (kept for inspection)\n", task.TaskID, wt)
	}
	keepWorktree := false
	if releaseWorktree != nil {
		defer func() { releaseWorktree(keepWorktree) }()
	}

	marker := attemptMarker(task.TaskID, task.Attempts)
	startTime := s.clock()
	childEnv := []string{
		runMarkerEnv + "=" + marker,
		"G8S_TIER=F1",
		"G8S_TASK_ID=" + task.TaskID,
		"G8S_PERMISSION=" + req.Permission,
	}
	childEnv = append(childEnv, harness.ResolveCapabilityHydration(req.Role, req.Permission).EnvVars()...)
	if req.ReceiptID != "" {
		childEnv = append(childEnv, "G8S_RECEIPT_ID="+req.ReceiptID)
	}
	if spawnDir != "" {
		childEnv = append(childEnv, "G8S_SCOPE_ROOT="+spawnDir)
	}
	childArgv := resolvedArgv
	if len(childArgv) == 0 {
		childArgv = s.buildArgv(req, promptPath, resultPath)
	}
	if req.ResultMode != "stdout" {
		if self, exeErr := os.Executable(); exeErr == nil {
			wrapped := make([]string, 0, len(childArgv)+5)
			wrapped = append(wrapped, self, "internal", "wrap-exec", "--out", resultPath, "--")
			wrapped = append(wrapped, childArgv...)
			childArgv = wrapped
		}
	}
	child, spawnErr := s.runner.Spawn(SpawnOptions{
		Argv:       childArgv,
		Dir:        spawnDir,
		Env:        childEnv,
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

	// #415 PR-1 & #439: post-run orphan sweep — on every terminal branch of this
	// attempt, verify the attempt process group is gone (PR-1) and perform a
	// layer-2 marker-based walk for any setsid grandchild escapes (#439).
	defer s.sweepAttemptGroup(child, task.TaskID, marker)

	reason := s.awaitOutcome(ctx, child, task.TaskID, opts.WorkerID, token, req, opts.LeaseSeconds)
	// Close capture handles before collect reads and removes the files; on
	// Windows an open handle makes os.Remove fail with a sharing violation.
	outWriter.Close()
	errWriter.Close()
	outFile.Close()
	errFile.Close()
	finalTask, collectErr := s.collect(ctx, child, reason, task.TaskID, opts.WorkerID, token,
		runDir, promptPath, resultPath, stdoutPath, stderrPath, req.ResultMode, req.Permission, req.ReceiptID, startTime, isolatedWorktreeDir, req)
	if collectErr == nil && finalTask != nil && isAttemptResultOK(finalTask.Result) {
		keepWorktree = true
	}
	return finalTask, collectErr
}
