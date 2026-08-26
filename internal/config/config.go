// Package config loads the operator-declared provider registry that feeds
// the two-class resource pool (DELTA-10): api_call proxy pools defined by
// hand and platform_dispatch entries resolved from local CLI binaries.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// ModelEntry describes one callable model exposed by a provider.
type ModelEntry struct {
	ID            string `json:"id"`
	ContextWindow int    `json:"context_window,omitempty"`
}

// ProviderEntry is one operator-declared provider in the registry file.
type ProviderEntry struct {
	Class   string       `json:"class"`
	Name    string       `json:"name"`
	BaseURL string       `json:"base_url,omitempty"`
	AuthEnv string       `json:"auth_env,omitempty"`
	Models  []ModelEntry `json:"models"`
	Slots   int          `json:"slots,omitempty"`

	// Args optionally declares an operator-defined invocation template for
	// platform_dispatch binaries that do not speak the g8s dispatch
	// contract (DELTA-10 R6). Placeholders {prompt}, {model} and {timeout}
	// are substituted verbatim into the exec argv; templates never
	// originate from task payloads.
	Args []string `json:"args,omitempty"`
}

// File is the root shape of providers.json.
type File struct {
	Providers []ProviderEntry `json:"providers"`
}

const (
	classAPICall          = "api_call"
	classPlatformDispatch = "platform_dispatch"
)

// Load reads and validates a provider registry file. Validation enforces
// the class taxonomy and per-class required fields; auth_env emptiness is
// intentionally NOT rejected here — it degrades the entry to UNAVAILABLE
// at probe time without issuing any HTTP request (spec R2).
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provider config: %w", err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse provider config: %w", err)
	}
	for i, p := range f.Providers {
		switch p.Class {
		case classAPICall:
			if p.BaseURL == "" {
				return nil, fmt.Errorf("provider %d (%s): base_url is required for api_call entries", i, p.Name)
			}
			if len(p.Models) == 0 {
				return nil, fmt.Errorf("provider %d (%s): at least one model is required", i, p.Name)
			}
			if p.Slots < 1 {
				return nil, fmt.Errorf("provider %d (%s): slots must be >= 1 for api_call entries", i, p.Name)
			}
		case classPlatformDispatch:
			if p.Name == "" {
				return nil, fmt.Errorf("provider %d: name is required for platform_dispatch entries", i)
			}
			if len(p.Models) == 0 {
				return nil, fmt.Errorf("provider %d (%s): at least one model is required", i, p.Name)
			}
		default:
			return nil, fmt.Errorf("provider %d: unknown provider class %q (want api_call or platform_dispatch)", i, p.Class)
		}
	}
	return &f, nil
}
