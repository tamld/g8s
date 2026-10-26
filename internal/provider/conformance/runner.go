package conformance

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/config"
)

// Options configures a full conformance run.
type Options struct {
	// Providers filters the run to these names (case-insensitive); empty = all.
	Providers          []string
	Live               bool
	AllowPaid          map[string]bool
	Model              string // model override (applies to every selected provider)
	Timeout            time.Duration
	KillAfter          time.Duration
	ContextProbeTokens int
	// ProvidersConfig is the operator providers.json (api_call entries become HTTP targets).
	ProvidersConfig *config.File
	// CLIProfiles / HTTPTargets override the defaults (tests).
	CLIProfiles []CLIProfile
	HTTPTargets []HTTPTarget
	HTTPClient  *http.Client
}

// DefaultHTTPTargets derives HTTP targets from providers.json api_call
// entries, falling back to a loopback 9router, plus a local ollama.
func DefaultHTTPTargets(cfg *config.File) []HTTPTarget {
	var out []HTTPTarget
	seen := map[string]bool{}
	if cfg != nil {
		for _, e := range cfg.Providers {
			if e.Class != "api_call" {
				continue
			}
			name := strings.ToLower(e.Name)
			policy := PolicyPaid
			model := e.DefaultModel
			if name == "9router" {
				policy = PolicyFree
				m := strings.ToLower(strings.TrimSpace(model))
				if m == "" || (!free9RouterCombos[m] && !isFreeModel(m)) {
					model = "docs-retriever"
				}
			}
			out = append(out, HTTPTarget{
				Name:     e.Name,
				BaseURL:  e.EffectiveBaseURL(),
				AuthEnv:  e.AuthEnv,
				Headers:  e.Headers,
				Model:    model,
				Policy:   policy,
				Protocol: e.EffectiveProtocol(),
			})
			seen[name] = true
		}
	}
	if !seen["9router"] {
		base := "http://127.0.0.1:20128/v1"
		if v := strings.TrimSpace(os.Getenv("NINEROUTER_URL")); v != "" {
			base = v
		}
		out = append(out, HTTPTarget{Name: "9router", BaseURL: base, AuthEnv: "OPENAI_API_KEY", Model: "docs-retriever", Policy: PolicyFree, Protocol: "9router"})
	}
	if !seen["ollama"] {
		host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
		if host == "" {
			host = "http://127.0.0.1:11434"
		}
		if !strings.HasPrefix(host, "http") {
			host = "http://" + host
		}
		out = append(out, HTTPTarget{Name: "ollama", BaseURL: strings.TrimRight(host, "/") + "/v1", Policy: PolicyFree, Protocol: "openai"})
	}
	return out
}

func selected(filter []string, name string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if strings.EqualFold(strings.TrimSpace(f), name) {
			return true
		}
	}
	return false
}

// KnownProvider reports whether name matches a probe-able provider.
func KnownProvider(opts Options, name string) bool {
	profiles := opts.CLIProfiles
	if profiles == nil {
		profiles = DefaultCLIProfiles()
	}
	for _, p := range profiles {
		if strings.EqualFold(p.Name, name) {
			return true
		}
	}
	targets := opts.HTTPTargets
	if targets == nil {
		targets = DefaultHTTPTargets(opts.ProvidersConfig)
	}
	for _, t := range targets {
		if strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

// Run executes the probe suite for every selected provider.
func Run(ctx context.Context, opts Options) Matrix {
	m := Matrix{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   nowRFC3339(),
		Generator:     "g8s providers conformance",
	}
	profiles := opts.CLIProfiles
	if profiles == nil {
		profiles = DefaultCLIProfiles()
	}
	for _, p := range profiles {
		if !selected(opts.Providers, p.Name) {
			continue
		}
		m.Providers = append(m.Providers, ProbeCLI(ctx, p, CLIOptions{
			Live:      opts.Live,
			AllowPaid: opts.AllowPaid,
			Model:     opts.Model,
			Timeout:   opts.Timeout,
			KillAfter: opts.KillAfter,
		}))
	}
	targets := opts.HTTPTargets
	if targets == nil {
		targets = DefaultHTTPTargets(opts.ProvidersConfig)
	}
	for _, t := range targets {
		if !selected(opts.Providers, t.Name) {
			continue
		}
		if opts.Model != "" {
			t.Model = opts.Model
		}
		if t.Client == nil {
			t.Client = opts.HTTPClient
		}
		m.Providers = append(m.Providers, ProbeHTTP(ctx, t, HTTPOptions{
			Live:               opts.Live,
			AllowPaid:          opts.AllowPaid,
			ContextProbeTokens: opts.ContextProbeTokens,
		}))
	}
	return m
}
