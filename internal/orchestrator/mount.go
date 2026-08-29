package orchestrator

import (
	"context"
	"sync"
)

// SkillMount mutates a worker prompt payload before dispatch.
type SkillMount interface {
	Name() string
	Inject(payload string) (string, error)
}

// HookMount intercepts worker lifecycle events before spawn and after wait.
type HookMount interface {
	PreSpawn(ctx context.Context, task TaskSpec) (TaskSpec, error)
	PostWait(ctx context.Context, task TaskSpec, receipt Receipt) error
}

// MemoryMount provides key-value state persistence across worker runs.
type MemoryMount interface {
	Load(ctx context.Context, sessionID string) (map[string]string, error)
	Save(ctx context.Context, sessionID string, vars map[string]string) error
}

// NoOpSkill is a passthrough SkillMount that leaves the payload unchanged.
type NoOpSkill struct{}

// Name returns the identifier for NoOpSkill.
func (NoOpSkill) Name() string { return "noop" }

// Inject returns the original payload without modification.
func (NoOpSkill) Inject(payload string) (string, error) {
	return payload, nil
}

// NoOpHook is a passthrough HookMount that performs no lifecycle mutations.
type NoOpHook struct{}

// PreSpawn passes the TaskSpec through unmodified.
func (NoOpHook) PreSpawn(_ context.Context, task TaskSpec) (TaskSpec, error) {
	return task, nil
}

// PostWait performs no action and returns nil.
func (NoOpHook) PostWait(_ context.Context, _ TaskSpec, _ Receipt) error {
	return nil
}

// NoOpMemory is an in-memory thread-safe MemoryMount store.
type NoOpMemory struct {
	mu    sync.RWMutex
	store map[string]map[string]string
}

// NewNoOpMemory constructs a fresh in-memory MemoryMount.
func NewNoOpMemory() *NoOpMemory {
	return &NoOpMemory{
		store: make(map[string]map[string]string),
	}
}

// Load retrieves a copy of stored variables for the given session ID.
func (m *NoOpMemory) Load(_ context.Context, sessionID string) (map[string]string, error) {
	if m == nil {
		return map[string]string{}, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.store == nil {
		return map[string]string{}, nil
	}
	src, ok := m.store[sessionID]
	if !ok {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}

// Save stores a copy of variables for the given session ID.
func (m *NoOpMemory) Save(_ context.Context, sessionID string, vars map[string]string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		m.store = make(map[string]map[string]string)
	}
	copied := make(map[string]string, len(vars))
	for k, v := range vars {
		copied[k] = v
	}
	m.store[sessionID] = copied
	return nil
}
