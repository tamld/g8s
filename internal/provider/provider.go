// Package provider implements native discovery and concurrency governance
// for external AI worker binaries, per DELTA-05
// (spec/openspec/05-provider-and-resource-pool-spec.md).
//
// Discovery is deliberately plugin-free and 100% native Go: binaries are
// resolved through exec.LookPath (or operator override environment
// variables), Ollama instances are probed over plain HTTP, and concurrency
// is governed by in-process semaphores. No CGO, no subprocess supervision,
// no configuration files are required for the registry to function.
//
// Governance layers (DELTA-05):
//
//	L1 operator: declares binaries via environment overrides and quota ceilings.
//	L2 g8s: auto-probes availability, tracks in-flight slots, reports status.
//	L3 supervisor: matches queued tasks to discovered models (future phase).
package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/tamld/g8s/internal/config"
)

// HealthStatus reports the outcome of the most recent health check.
type HealthStatus string

const (
	// StatusReady means the binary or endpoint answered its health probe.
	StatusReady HealthStatus = "READY"

	// StatusDegraded means the provider exists but its health probe failed
	// softly (for example a non-200 response from an Ollama daemon).
	StatusDegraded HealthStatus = "DEGRADED"

	// StatusUnavailable means the provider could not be located at all.
	StatusUnavailable HealthStatus = "UNAVAILABLE"
)

// ModelDescriptor describes a single model exposed by a provider.
type ModelDescriptor struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	SupportedRoles  []string `json:"supported_roles"`
	ContextWindow   int      `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	IsLocal         bool     `json:"is_local"`
}

// ProviderInfo is the externally visible snapshot of one provider.
type ProviderInfo struct {
	Name              string            `json:"name"`
	BinaryPath        string            `json:"binary_path,omitempty"`
	Status            HealthStatus      `json:"status"`
	AvailableModels   []ModelDescriptor `json:"available_models"`
	MaxConcurrency    int               `json:"max_concurrency"`
	CurrentInFlight   int               `json:"current_in_flight"`
	LastHealthCheckAt int64             `json:"last_health_check_at"`

	// Class tags the provider resource class per DELTA-10:
	// api_call (remote HTTP pool) or platform_dispatch (local CLI binary).
	Class string `json:"class"`

	// Reason carries the fail-closed explanation when Status is UNAVAILABLE.
	Reason string `json:"reason,omitempty"`
}

// ProviderRegistry is the DELTA-05 contract consumed by the future
// supervisor layer.
type ProviderRegistry interface {
	DiscoverAll(ctx context.Context) ([]ProviderInfo, error)
	GetProvider(name string) (ProviderInfo, error)
	AcquireSlot(ctx context.Context, providerName string) (func(), error)
}

// Config declares one provider for the registry.
type Config struct {
	// Name is the canonical registry key (agy, claude, ollama).
	Name string

	// Binary is the executable name passed to LookPath.
	Binary string

	// OverrideEnvVar names the operator override environment variable
	// (AGY_BIN, CLAUDE_BIN, GEMINI_BIN, ...). Empty disables overrides.
	OverrideEnvVar string

	// Models lists the descriptors this provider exposes.
	Models []ModelDescriptor

	// MaxConcurrency caps simultaneous task slots.
	MaxConcurrency int

	// IsLocal marks locally hosted runtimes (Ollama).
	IsLocal bool

	// HealthProbeURL, when set, turns discovery into an HTTP smoke check.
	HealthProbeURL string

	// Class tags the provider resource class (DELTA-10): api_call entries
	// are remote HTTP pools loaded from providers.json; platform_dispatch
	// entries resolve local CLI binaries through the discovery chain.
	Class string

	// AuthEnv names the environment variable carrying the API credential
	// for api_call providers. Empty means no credential is required.
	AuthEnv string

	// ArgsTemplate optionally carries an operator-defined invocation
	// template (DELTA-10 R6) for platform_dispatch binaries that do not
	// speak the g8s dispatch contract. Placeholders {prompt}, {model} and
	// {timeout} are substituted verbatim by the supervisor.
	ArgsTemplate []string
}

const ollamaDefaultHost = "http://127.0.0.1:11434"

// DefaultConfigs returns the sanctioned baseline provider fleet.
func DefaultConfigs() []Config {
	return []Config{
		{
			Name:           "agy",
			Binary:         "agy",
			OverrideEnvVar: "AGY_BIN",
			Class:          "platform_dispatch",
			MaxConcurrency: 10,
			Models: []ModelDescriptor{
				{
					ID:              "gemini-3.8-flash-high",
					Name:            "Gemini 3.8 Flash (High)",
					SupportedRoles:  []string{"collector", "scout", "mcp-mapper", "summarizer", "test-runner", "verifier"},
					ContextWindow:   1000000,
					MaxOutputTokens: 65536,
				},
			},
		},
		{
			Name:           "claude",
			Binary:         "claude",
			OverrideEnvVar: "CLAUDE_BIN",
			Class:          "platform_dispatch",
			MaxConcurrency: 5,
			Models: []ModelDescriptor{
				{
					ID:              "claude-haiku-4-5",
					Name:            "Claude Haiku 4.5",
					SupportedRoles:  []string{"collector", "summarizer"},
					ContextWindow:   200000,
					MaxOutputTokens: 32000,
				},
			},
		},
		{
			Name:           "ollama",
			Binary:         "ollama",
			OverrideEnvVar: "OLLAMA_HOST",
			Class:          "platform_dispatch",
			HealthProbeURL: "/api/tags",
			IsLocal:        true,
			Models: []ModelDescriptor{
				{
					ID:              "llama3.1",
					Name:            "Llama 3.1 (local)",
					SupportedRoles:  []string{"collector"},
					ContextWindow:   128000,
					MaxOutputTokens: 8192,
					IsLocal:         true,
				},
			},
		},
	}
}

// state carries mutable per-provider bookkeeping.
type state struct {
	cfg      Config
	sem      chan struct{}
	inFlight int
	last     ProviderInfo
}

// PoolRegistry is the concrete ProviderRegistry for concurrency pooling.
type PoolRegistry struct {
	mu         sync.Mutex
	clock      func() time.Time
	lookPath   func(string) (string, error)
	httpClient *http.Client
	states     map[string]*state
}

// NewPoolRegistry builds a pool registry over the supplied configs. Nil clock or
// lookPath fall back to time.Now and exec.LookPath respectively.
func NewPoolRegistry(configs []Config, clock func() time.Time, lookPath func(string) (string, error)) *PoolRegistry {
	if clock == nil {
		clock = time.Now
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	r := &PoolRegistry{
		clock:      clock,
		lookPath:   lookPath,
		httpClient: &http.Client{Timeout: time.Second},
		states:     make(map[string]*state, len(configs)),
	}
	for _, cfg := range configs {
		slots := cfg.MaxConcurrency
		if slots <= 0 {
			slots = 1
		}
		cfg.MaxConcurrency = slots
		r.states[cfg.Name] = &state{
			cfg: cfg,
			sem: make(chan struct{}, slots),
		}
	}
	return r
}

// DiscoverAll probes every configured provider and returns a stable,
// name-sorted snapshot.
func (r *PoolRegistry) DiscoverAll(ctx context.Context) ([]ProviderInfo, error) {
	names := make([]string, 0, len(r.states))
	for name := range r.states {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]ProviderInfo, 0, len(names))
	for _, name := range names {
		out = append(out, r.probe(ctx, name))
	}
	return out, nil
}

// probe resolves one provider and refreshes its stored snapshot.
func (r *PoolRegistry) probe(ctx context.Context, name string) ProviderInfo {
	info := ProviderInfo{
		Name:              name,
		Status:            StatusUnavailable,
		LastHealthCheckAt: r.clock().Unix(),
	}

	r.mu.Lock()
	st := r.states[name]
	cfg := st.cfg
	inFlight := st.inFlight
	r.mu.Unlock()

	info.MaxConcurrency = cfg.MaxConcurrency
	info.AvailableModels = append([]ModelDescriptor(nil), cfg.Models...)
	info.CurrentInFlight = inFlight

	info.Class = cfg.Class

	// DELTA-10 R2: api_call pools fail closed BEFORE any network traffic
	// when their declared credential environment variable is unset.
	if cfg.Class == "api_call" {
		if cfg.AuthEnv != "" && os.Getenv(cfg.AuthEnv) == "" {
			info.Reason = fmt.Sprintf("auth_env %s is not set", cfg.AuthEnv)
			r.mu.Lock()
			st.last = info
			r.mu.Unlock()
			return info
		}
		info.Status = r.probeHTTP(ctx, cfg.HealthProbeURL)
		r.mu.Lock()
		st.last = info
		r.mu.Unlock()
		return info
	}

	binPath := ""
	if cfg.OverrideEnvVar != "" {
		if override := os.Getenv(cfg.OverrideEnvVar); override != "" && name != "ollama" {
			binPath = override
		}
	}
	if binPath == "" && !(cfg.IsLocal && name == "ollama") {
		if resolved, err := r.lookPath(cfg.Binary); err == nil {
			binPath = resolved
		}
	}
	info.BinaryPath = binPath

	switch {
	case cfg.HealthProbeURL != "":
		host := os.Getenv(cfg.OverrideEnvVar)
		if host == "" {
			host = ollamaDefaultHost
		}
		info.Status = r.probeHTTP(ctx, host+cfg.HealthProbeURL)
	case binPath != "":
		info.Status = StatusReady
	default:
		info.Status = StatusUnavailable
	}

	r.mu.Lock()
	st.last = info
	r.mu.Unlock()
	return info
}

// LoadProvidersJSON merges provider entries from a providers.json file
// (DELTA-10 R2/R3): api_call entries become remote HTTP pools and
// platform_dispatch entries resolve local CLI binaries through the
// standard discovery chain. Duplicate names are rejected.
func (r *PoolRegistry) LoadProvidersJSON(path string) error {
	file, err := config.Load(path)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, entry := range file.Providers {
		if _, exists := r.states[entry.Name]; exists {
			return fmt.Errorf("duplicate provider name %q", entry.Name)
		}
		models := make([]ModelDescriptor, 0, len(entry.Models))
		for _, m := range entry.Models {
			models = append(models, ModelDescriptor{
				ID:            m.ID,
				Name:          m.ID,
				ContextWindow: m.ContextWindow,
			})
		}
		cfg := Config{
			Name:           entry.Name,
			Class:          entry.Class,
			MaxConcurrency: entry.Slots,
			Models:         models,
		}
		switch entry.Class {
		case "api_call":
			cfg.HealthProbeURL = entry.BaseURL
			cfg.AuthEnv = entry.AuthEnv
		default: // platform_dispatch
			cfg.Binary = entry.Name
			cfg.ArgsTemplate = entry.Args
		}
		r.states[entry.Name] = &state{
			cfg: cfg,
			sem: make(chan struct{}, cfg.MaxConcurrency),
		}
	}
	return nil
}

// SelectForModel acquires a slot on the READY provider serving modelID,
// preferring api_call pools over platform_dispatch entries per DELTA-10 R5.
func (r *PoolRegistry) SelectForModel(ctx context.Context, modelID string) (string, func(), error) {
	r.mu.Lock()
	candidates := make([]string, 0, 2)
	for name := range r.states {
		st := r.states[name]
		serves := false
		for _, m := range st.cfg.Models {
			if m.ID == modelID {
				serves = true
				break
			}
		}
		if serves && st.last.Status == StatusReady {
			candidates = append(candidates, name)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		ci, cj := r.states[candidates[i]].cfg.Class, r.states[candidates[j]].cfg.Class
		if ci != cj {
			return ci == "api_call"
		}
		return candidates[i] < candidates[j]
	})
	r.mu.Unlock()

	for _, name := range candidates {
		if release, err := r.AcquireSlot(ctx, name); err == nil {
			return name, release, nil
		}
	}
	return "", nil, fmt.Errorf("no ready provider serves model %q", modelID)
}

// probeHTTP performs the synthetic smoke health check for local daemons.
func (r *PoolRegistry) probeHTTP(ctx context.Context, url string) HealthStatus {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return StatusUnavailable
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return StatusUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return StatusDegraded
	}
	return StatusReady
}

// GetProvider returns the most recently observed snapshot for name.
func (r *PoolRegistry) GetProvider(name string) (ProviderInfo, error) {
	r.mu.Lock()
	st, ok := r.states[name]
	if !ok {
		r.mu.Unlock()
		return ProviderInfo{}, fmt.Errorf("unknown provider: %s", name)
	}
	neverProbed := st.last.LastHealthCheckAt == 0
	r.mu.Unlock()

	// DELTA-10: probe lazily on first access so entries loaded from
	// providers.json report meaningful status without requiring an
	// explicit DiscoverAll sweep first.
	if neverProbed {
		return r.probe(context.Background(), name), nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return st.last, nil
}

// AcquireSlot reserves one concurrency slot, blocking until a slot frees up
// or ctx is cancelled. The returned release function must be invoked exactly
// once when the caller finishes using the slot.
func (r *PoolRegistry) AcquireSlot(ctx context.Context, providerName string) (func(), error) {
	r.mu.Lock()
	st, ok := r.states[providerName]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case st.sem <- struct{}{}:
	}

	r.mu.Lock()
	st.inFlight++
	if st.last.Name != "" {
		st.last.CurrentInFlight = st.inFlight
	}
	r.mu.Unlock()

	release := func() {
		r.mu.Lock()
		st.inFlight--
		if st.last.Name != "" {
			st.last.CurrentInFlight = st.inFlight
		}
		r.mu.Unlock()
		<-st.sem
	}
	return release, nil
}

// ensure the concrete type satisfies the spec interface at compile time.
var _ ProviderRegistry = (*PoolRegistry)(nil)
