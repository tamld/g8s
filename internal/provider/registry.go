package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var (
	// ErrProviderNotFound is returned when looking up an unregistered provider name.
	ErrProviderNotFound = errors.New("provider not found")

	// ErrProviderUnavailable is returned when attempting to spawn against an unavailable provider.
	ErrProviderUnavailable = errors.New("provider unavailable")
)

// Provider abstracts an AI CLI worker backend.
type Provider interface {
	Name() string
	Binary() string
	Version(ctx context.Context) (string, error)
	Available(ctx context.Context) error
	Spawn(ctx context.Context, spec Spec) (Handle, error)
}

// Spec defines the configuration and requirements for a worker execution.
type Spec struct {
	Brief        string        `json:"brief"`
	Model        string        `json:"model,omitempty"`
	AddDirs      []string      `json:"add_dirs,omitempty"`
	SystemPrompt string        `json:"system_prompt,omitempty"`
	Timeout      time.Duration `json:"timeout,omitempty"`
	TraceID      string        `json:"trace_id,omitempty"`
	Role         string        `json:"role,omitempty"`
	Permission   string        `json:"permission,omitempty"`
	WorktreeDir  string        `json:"worktree_dir,omitempty"`
}

// Receipt records the outcome of a worker subprocess execution.
type Receipt struct {
	TaskID     string   `json:"task_id,omitempty"`
	Provider   string   `json:"provider"`
	Status     string   `json:"status"` // "COMPLETED", "FAILED", "TIMEOUT", "CANCELLED"
	Stdout     string   `json:"stdout,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
	ExitCode   int      `json:"exit_code"`
	DurationMs int64    `json:"duration_ms"`
	Violations []string `json:"violations,omitempty"`
}

// Handle represents a running worker subprocess.
type Handle interface {
	PID() int
	Wait(ctx context.Context) (Receipt, error)
	Cancel(ctx context.Context) error
	StdoutStream() io.ReadCloser
}

// ProviderStatus summarizes the detection status of a provider for CLI reporting.
type ProviderStatus struct {
	Name       string `json:"name"`
	Status     string `json:"status"` // "OK" or "NO"
	BinaryPath string `json:"binary_path,omitempty"`
	Version    string `json:"version,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Registry manages the set of available agent providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	order     []string
}

// NewRegistry constructs a new Registry preloaded with default providers if none are specified.
func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{
		providers: make(map[string]Provider),
	}
	if len(providers) == 0 {
		providers = []Provider{
			NewAgyProvider(),
			NewCodexProvider(),
			NewClaudeProvider(),
			NewOllamaProvider(),
		}
	}
	for _, p := range providers {
		r.Register(p)
	}
	return r
}

// Register adds or updates a provider in the registry.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(p.Name())
	if _, exists := r.providers[key]; !exists {
		r.order = append(r.order, key)
	}
	r.providers[key] = p
}

// AutoDetect probes all registered providers and returns only those that are available.
func (r *Registry) AutoDetect(ctx context.Context) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var available []Provider
	for _, name := range r.order {
		p := r.providers[name]
		if err := p.Available(ctx); err == nil {
			available = append(available, p)
		}
	}
	return available
}

// Get returns the provider matching name (case-insensitive), or ErrProviderNotFound.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := strings.ToLower(name)
	p, ok := r.providers[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}
	return p, nil
}

// List returns the detection status for all registered providers.
func (r *Registry) List(ctx ...context.Context) []ProviderStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var c context.Context
	if len(ctx) > 0 && ctx[0] != nil {
		c = ctx[0]
	} else {
		c = context.Background()
	}

	statuses := make([]ProviderStatus, 0, len(r.order))
	for _, name := range r.order {
		p := r.providers[name]
		st := ProviderStatus{
			Name: p.Name(),
		}
		if err := p.Available(c); err != nil {
			st.Status = "NO"
			st.Reason = err.Error()
		} else {
			st.Status = "OK"
			st.BinaryPath = p.Binary()
			if ver, err := p.Version(c); err == nil {
				st.Version = ver
			}
		}
		statuses = append(statuses, st)
	}
	return statuses
}

// Recommend returns the built-in catalog with current detection status.
// Compiled into the binary (catalog.go) so the CLI can list what exists
// without a network call.
func (r *Registry) Recommend(ctx context.Context) []CatalogEntry {
	cat := Catalog()
	for i := range cat {
		_, err := r.Get(cat[i].Name)
		if err != nil {
			cat[i].InstallHint = "not registered: " + cat[i].InstallHint
			continue
		}
		if probeErr := r.probeOne(ctx, cat[i].Name); probeErr != nil {
			cat[i].InstallHint = "registered but unavailable: " + probeErr.Error()
		}
	}
	return cat
}

// probeOne is a read-only availability probe scoped to a single registered
// provider.
func (r *Registry) probeOne(ctx context.Context, name string) error {
	r.mu.RLock()
	p, ok := r.providers[strings.ToLower(name)]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("provider %q not registered", name)
	}
	return p.Available(ctx)
}

// RegisterHTTP wires an OpenAI-compatible HTTP provider into the registry.
// Used by `g8s providers enable` to add 9router-like gateways without
// recompiling.
func (r *Registry) RegisterHTTP(name, baseURL, authEnv string) {
	r.Register(NewOpenAIProvider(name, baseURL, authEnv))
}
