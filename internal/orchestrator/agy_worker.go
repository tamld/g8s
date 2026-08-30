package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/dispatch"
)

// AgyWorker wraps the agy CLI behind the Worker interface. AGY is the
// default backend — the orchestrator registry resolves "agy" to this type.
//
// Spawn launches a real subprocess and returns immediately. Wait blocks
// on the subprocess and synthesizes a Receipt. This makes fan-out truly
// parallel: N Spawn calls complete in O(spawn latency), not O(worker latency).
type AgyWorker struct {
	binary string
	clock  func() time.Time
	mounts *MountRegistry
}

// NewAgyWorker resolves the agy binary once. Missing binary does not fail
// construction — Available reports it — so the registry can fall back to
// a different worker without panicking.
func NewAgyWorker() *AgyWorker {
	bin, _ := dispatch.ResolveBinary("", dispatch.ResolveOptions{})
	return &AgyWorker{
		binary: bin,
		clock:  time.Now,
		mounts: NewMountRegistry(),
	}
}

func (w *AgyWorker) Name() string { return "agy" }

func (w *AgyWorker) Available(_ context.Context) error {
	if w.binary == "" {
		return fmt.Errorf("agy binary not on PATH")
	}
	return nil
}

// WithMounts configures mounts for AgyWorker.
func (w *AgyWorker) WithMounts(mounts MountRegistry) Worker {
	cp := *w
	mountsCopy := mounts
	cp.mounts = &mountsCopy
	return &cp
}

// Mounts returns the worker's MountRegistry.
func (w *AgyWorker) Mounts() *MountRegistry {
	return w.mounts
}

// Inject delegates prompt mutation to the worker's SkillMount chain.
func (w *AgyWorker) Inject(payload string) (string, error) {
	return w.mounts.Skills().Inject(payload)
}

// PreSpawn delegates pre-spawn task interception to the worker's HookMount chain.
func (w *AgyWorker) PreSpawn(ctx context.Context, task TaskSpec) (TaskSpec, error) {
	return w.mounts.Hooks().PreSpawn(ctx, task)
}

// PostWait delegates post-wait receipt interception to the worker's HookMount chain.
func (w *AgyWorker) PostWait(ctx context.Context, task TaskSpec, receipt Receipt) error {
	return w.mounts.Hooks().PostWait(ctx, task, receipt)
}

// Load delegates variable hydration to the worker's MemoryMount.
func (w *AgyWorker) Load(ctx context.Context, sessionID string) (map[string]string, error) {
	return w.mounts.Memory().Load(ctx, sessionID)
}

// Save delegates variable persistence to the worker's MemoryMount.
func (w *AgyWorker) Save(ctx context.Context, sessionID string, vars map[string]string) error {
	return w.mounts.Memory().Save(ctx, sessionID, vars)
}

// Spawn builds the agy argv via internal/dispatch and starts the subprocess.
// exec.CommandContext ties the subprocess lifetime to ctx, so a parent ctx
// cancellation kills the worker cleanly.
func (w *AgyWorker) Spawn(ctx context.Context, t Task) (Handle, error) {
	if w.binary == "" {
		return nil, ErrWorkerUnavailable
	}

	var err error
	t.Prompt, err = w.mounts.Skills().Inject(t.Prompt)
	if err != nil {
		return nil, fmt.Errorf("agy skill inject: %w", err)
	}

	origPrompt := t.Prompt
	spec := TaskSpec{
		TaskID:     t.ID,
		SessionID:  t.ID,
		Prompt:     t.Prompt,
		WorktreeID: t.Worktree.ID,
		WorkerName: w.Name(),
		Iter:       t.Iter,
		Task:       t,
	}
	spec, err = w.mounts.Hooks().PreSpawn(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("agy hook pre-spawn: %w", err)
	}
	t = spec.Task
	if spec.Task.Prompt != origPrompt {
		t.Prompt = spec.Task.Prompt
	} else if spec.Prompt != origPrompt {
		t.Prompt = spec.Prompt
	}

	role := t.Role
	if role == "" {
		role = dispatch.DefaultRole
	}
	perm := t.Permission
	if perm == "" {
		perm = dispatch.DefaultPermission
	}
	model := t.Model
	if model == "" {
		model = dispatch.DefaultModel
	}
	timeout := t.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	argv := dispatch.BuildCommand(dispatch.CommandOptions{
		Binary:          w.binary,
		Prompt:          t.Prompt,
		Model:           model,
		Timeout:         timeout.String(),
		AddDirs:         []string{t.Worktree.Path},
		SkipPermissions: perm == "automation_read",
		NoSandbox:       perm == "workspace_write",
		Home:            "",
	})
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agy spawn: %w", err)
	}
	return &agyHandle{
		cmd:       cmd,
		stdout:    &stdout,
		stderr:    &stderr,
		task:      t,
		spec:      spec,
		mounts:    w.mounts,
		startedAt: w.clock(),
		timeout:   timeout,
		clock:     w.clock,
	}, nil
}

// agyHandle wraps a live agy subprocess. Wait blocks until the process
// exits or ctx fires; Cancel kills the process group.
type agyHandle struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdout    *bytes.Buffer
	stderr    *bytes.Buffer
	task      Task
	spec      TaskSpec
	mounts    *MountRegistry
	startedAt time.Time
	timeout   time.Duration
	clock     func() time.Time
	done      bool
	cancelled bool
	waitErr   error
}

func (h *agyHandle) PID() int {
	if h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *agyHandle) Wait(ctx context.Context) (Receipt, error) {
	h.mu.Lock()
	if h.done {
		err := h.waitErr
		h.mu.Unlock()
		return h.synthesize(err), err
	}
	h.mu.Unlock()

	waitCh := make(chan error, 1)
	go func() { waitCh <- h.cmd.Wait() }()

	var runErr error
	select {
	case runErr = <-waitCh:
	case <-ctx.Done():
		_ = h.Cancel(context.Background())
		runErr = ErrWorkerTimeout
	}

	h.mu.Lock()
	h.done = true
	switch {
	case h.cancelled:
		h.waitErr = ErrWorkerCancelled
	case errors.Is(runErr, context.DeadlineExceeded):
		h.waitErr = ErrWorkerTimeout
	default:
		h.waitErr = runErr
	}
	err := h.waitErr
	h.mu.Unlock()

	receipt := h.synthesize(err)
	if hookErr := h.mounts.Hooks().PostWait(ctx, h.spec, receipt); hookErr != nil {
		if err == nil {
			err = fmt.Errorf("agy hook post-wait: %w", hookErr)
		}
	}
	return receipt, err
}

func (h *agyHandle) synthesize(runErr error) Receipt {
	r := Receipt{
		WorkerName:      "agy",
		TaskID:          h.task.ID,
		WorktreeID:      h.task.Worktree.ID,
		Branch:          h.task.Worktree.Branch,
		Stdout:          h.stdout.String(),
		Stderr:          h.stderr.String(),
		StartedAt:       h.startedAt,
		FinishedAt:      h.clock(),
		DurationSeconds: time.Since(h.startedAt).Seconds(),
	}
	switch {
	case runErr == nil:
		r.OK = true
	case errors.Is(runErr, ErrWorkerCancelled):
		r.HarnessCode = 130
	case errors.Is(runErr, ErrWorkerTimeout):
		r.HarnessCode = 124
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			r.ReturnCode = exitErr.ExitCode()
		}
	}
	return r
}

func (h *agyHandle) Cancel(_ context.Context) error {
	h.mu.Lock()
	if h.done {
		h.mu.Unlock()
		return nil
	}
	h.cancelled = true
	cmd := h.cmd
	h.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func (h *agyHandle) StdoutStream() interface {
	Read(p []byte) (int, error)
	Close() error
} {
	return nil
}
