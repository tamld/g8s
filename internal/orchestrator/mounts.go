package orchestrator

import (
	"context"
	"fmt"
	"sync"
)

// MountRegistry manages registered SkillMount, HookMount, and MemoryMount
// instances for worker execution.
type MountRegistry struct {
	mu            *sync.RWMutex
	skills        []SkillMount
	hooks         []HookMount
	memories      []MemoryMount
	defaultMemory MemoryMount
}

// NewMountRegistry constructs an empty MountRegistry with an in-memory default store.
func NewMountRegistry() *MountRegistry {
	return &MountRegistry{
		mu:            &sync.RWMutex{},
		skills:        make([]SkillMount, 0),
		hooks:         make([]HookMount, 0),
		memories:      make([]MemoryMount, 0),
		defaultMemory: NewNoOpMemory(),
	}
}

// RegisterSkill registers a SkillMount to be executed in registration order.
func (r *MountRegistry) RegisterSkill(s SkillMount) {
	if s == nil {
		return
	}
	if r.mu != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
	}
	r.skills = append(r.skills, s)
}

// RegisterHook registers a HookMount to be executed in registration order.
func (r *MountRegistry) RegisterHook(h HookMount) {
	if h == nil {
		return
	}
	if r.mu != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
	}
	r.hooks = append(r.hooks, h)
}

// RegisterMemory registers a MemoryMount. The first registered memory mount
// serves Load and Save operations.
func (r *MountRegistry) RegisterMemory(m MemoryMount) {
	if m == nil {
		return
	}
	if r.mu != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
	}
	r.memories = append(r.memories, m)
}

// Skills returns a SkillMount chain executing all registered skills in order.
func (r *MountRegistry) Skills() SkillMount {
	if r == nil {
		return NoOpSkill{}
	}
	if r.mu != nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
	}
	if len(r.skills) == 0 {
		return NoOpSkill{}
	}
	chain := make([]SkillMount, len(r.skills))
	copy(chain, r.skills)
	return &skillChain{skills: chain}
}

// Hooks returns a HookMount chain executing all registered hooks in order.
func (r *MountRegistry) Hooks() HookMount {
	if r == nil {
		return NoOpHook{}
	}
	if r.mu != nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
	}
	if len(r.hooks) == 0 {
		return NoOpHook{}
	}
	chain := make([]HookMount, len(r.hooks))
	copy(chain, r.hooks)
	return &hookChain{hooks: chain}
}

// Memory returns the active MemoryMount (first registered, or in-memory default).
func (r *MountRegistry) Memory() MemoryMount {
	if r == nil {
		return NewNoOpMemory()
	}
	if r.mu != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
	}
	if len(r.memories) == 0 {
		if r.defaultMemory == nil {
			r.defaultMemory = NewNoOpMemory()
		}
		return r.defaultMemory
	}
	return r.memories[0]
}

type skillChain struct {
	skills []SkillMount
}

func (c *skillChain) Name() string {
	return "skill_chain"
}

func (c *skillChain) Inject(payload string) (string, error) {
	current := payload
	for _, s := range c.skills {
		if s == nil {
			continue
		}
		var err error
		current, err = s.Inject(current)
		if err != nil {
			return "", err
		}
	}
	return current, nil
}

type hookChain struct {
	hooks []HookMount
}

func (c *hookChain) PreSpawn(ctx context.Context, task TaskSpec) (TaskSpec, error) {
	current := task
	for _, h := range c.hooks {
		if h == nil {
			continue
		}
		var err error
		current, err = h.PreSpawn(ctx, current)
		if err != nil {
			return current, err
		}
	}
	return current, nil
}

func (c *hookChain) PostWait(ctx context.Context, task TaskSpec, receipt Receipt) error {
	for _, h := range c.hooks {
		if h == nil {
			continue
		}
		if err := h.PostWait(ctx, task, receipt); err != nil {
			return err
		}
	}
	return nil
}

// MountableWorker is an optional interface implemented by workers that natively
// support MountRegistry configuration.
type MountableWorker interface {
	Worker
	WithMounts(mounts MountRegistry) Worker
}

// WrapWithMounts wraps any Worker with a MountRegistry. If the worker implements
// MountableWorker, its WithMounts method is called; otherwise a MountedWorker
// wrapper is returned.
func WrapWithMounts(w Worker, mounts MountRegistry) Worker {
	if mw, ok := w.(MountableWorker); ok {
		return mw.WithMounts(mounts)
	}
	mountsCopy := mounts
	return &MountedWorker{Worker: w, mounts: &mountsCopy}
}

// WithMounts attaches mounts to a Worker using MountableWorker or MountedWorker.
func WithMounts(w Worker, mounts MountRegistry) Worker {
	return WrapWithMounts(w, mounts)
}

// MountedWorker adapts any Worker to execute SkillMount, HookMount, and
// MemoryMount operations throughout its lifecycle.
type MountedWorker struct {
	Worker
	mounts *MountRegistry
}

// Mounts returns the worker's MountRegistry.
func (mw *MountedWorker) Mounts() *MountRegistry {
	return mw.mounts
}

// WithMounts returns a new MountedWorker with the provided MountRegistry.
func (mw *MountedWorker) WithMounts(mounts MountRegistry) Worker {
	mountsCopy := mounts
	return &MountedWorker{Worker: mw.Worker, mounts: &mountsCopy}
}

// Inject delegates prompt mutation to the skill mount chain.
func (mw *MountedWorker) Inject(payload string) (string, error) {
	return mw.mounts.Skills().Inject(payload)
}

// PreSpawn delegates pre-spawn task transformation to the hook mount chain.
func (mw *MountedWorker) PreSpawn(ctx context.Context, task TaskSpec) (TaskSpec, error) {
	return mw.mounts.Hooks().PreSpawn(ctx, task)
}

// PostWait delegates post-wait receipt processing to the hook mount chain.
func (mw *MountedWorker) PostWait(ctx context.Context, task TaskSpec, receipt Receipt) error {
	return mw.mounts.Hooks().PostWait(ctx, task, receipt)
}

// Load delegates variable loading to the active memory mount.
func (mw *MountedWorker) Load(ctx context.Context, sessionID string) (map[string]string, error) {
	return mw.mounts.Memory().Load(ctx, sessionID)
}

// Save delegates variable persistence to the active memory mount.
func (mw *MountedWorker) Save(ctx context.Context, sessionID string, vars map[string]string) error {
	return mw.mounts.Memory().Save(ctx, sessionID, vars)
}

// Spawn executes SkillMount and HookMount transformations before dispatching
// the task to the underlying worker.
func (mw *MountedWorker) Spawn(ctx context.Context, t Task) (Handle, error) {
	var err error
	t.Prompt, err = mw.mounts.Skills().Inject(t.Prompt)
	if err != nil {
		return nil, fmt.Errorf("skill mount inject: %w", err)
	}

	origPrompt := t.Prompt
	spec := TaskSpec{
		TaskID:     t.ID,
		SessionID:  t.ID,
		Prompt:     t.Prompt,
		WorktreeID: t.Worktree.ID,
		WorkerName: mw.Worker.Name(),
		Iter:       t.Iter,
		Task:       t,
	}
	spec, err = mw.mounts.Hooks().PreSpawn(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("hook mount pre-spawn: %w", err)
	}
	t = spec.Task
	if spec.Task.Prompt != origPrompt {
		t.Prompt = spec.Task.Prompt
	} else if spec.Prompt != origPrompt {
		t.Prompt = spec.Prompt
	}

	handle, err := mw.Worker.Spawn(ctx, t)
	if err != nil {
		return nil, err
	}

	return &mountedHandle{
		Handle: handle,
		spec:   spec,
		mounts: mw.mounts,
	}, nil
}

type mountedHandle struct {
	Handle
	spec   TaskSpec
	mounts *MountRegistry
}

func (h *mountedHandle) Wait(ctx context.Context) (Receipt, error) {
	receipt, err := h.Handle.Wait(ctx)
	if hookErr := h.mounts.Hooks().PostWait(ctx, h.spec, receipt); hookErr != nil {
		if err == nil {
			err = fmt.Errorf("hook mount post-wait: %w", hookErr)
		}
	}
	return receipt, err
}
