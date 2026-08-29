package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Registry maps worker names to constructors. Workers are looked up by
// name when an orchestrator needs a backend; the first one whose
// Available returns nil is selected.
//
// Adding a new backend = register a constructor at process start (or via
// config). No edits to orchestrator core.
type Registry struct {
	mu    sync.RWMutex
	names []string
	ctors map[string]func() Worker
}

// NewRegistry returns an empty registry. Use Register to add backends.
func NewRegistry() *Registry {
	return &Registry{ctors: map[string]func() Worker{}}
}

// Register adds a backend. Names must be unique; duplicates return an
// error so configuration errors can be handled cleanly.
func (r *Registry) Register(name string, ctor func() Worker) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.ctors[name]; dup {
		return fmt.Errorf("orchestrator: worker %q already registered", name)
	}
	r.ctors[name] = ctor
	r.names = append(r.names, name)
	sort.Strings(r.names)
	return nil
}

// Names returns the sorted list of registered worker names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.names))
	copy(out, r.names)
	return out
}

// Pick returns the first worker whose Available is nil, in sorted-name
// order. Returns ErrWorkerUnavailable if none resolve.
func (r *Registry) Pick(ctx context.Context) (Worker, error) {
	r.mu.RLock()
	names := append([]string(nil), r.names...)
	ctors := make(map[string]func() Worker, len(r.ctors))
	for k, v := range r.ctors {
		ctors[k] = v
	}
	r.mu.RUnlock()

	for _, name := range names {
		w := ctors[name]()
		if err := w.Available(ctx); err == nil {
			return w, nil
		}
	}
	return nil, fmt.Errorf("no worker available (registered: %v): %w", names, ErrWorkerUnavailable)
}

// Get returns the worker registered under name. Useful when the
// orchestrator pins a specific backend (e.g. for tests).
func (r *Registry) Get(name string) (Worker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ctor, ok := r.ctors[name]
	if !ok {
		return nil, false
	}
	return ctor(), true
}

// DefaultRegistry returns a registry preloaded with the workers g8s
// supports today. New backends land here as they stabilize.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	if err := r.Register("agy", func() Worker { return NewAgyWorker() }); err != nil {
		return nil
	}
	return r
}
