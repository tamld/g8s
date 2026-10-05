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

	// Effort fields (issue #550 / DELTA-11)
	EffortStyle      string         `json:"effort_style,omitempty"`
	SupportedEfforts []string       `json:"supported_efforts,omitempty"`
	DefaultEffort    string         `json:"default_effort,omitempty"`
	Mandatory        bool           `json:"mandatory,omitempty"`
	EffortBudgetMap  map[string]int `json:"effort_budget_map,omitempty"`
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

// Canonical effort styles (issue #550 / DELTA-11).
const (
	EffortStyleNamed     = "named"
	EffortStyleBudget    = "budget"
	EffortStyleBakedName = "baked-name"
	EffortStyleToggle    = "toggle"
)

// Canonical effort ladder levels (7-level enum: none<minimal<low<medium<high<xhigh<max).
const (
	EffortNone    = "none"
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
	EffortXHigh   = "xhigh"
	EffortMax     = "max"
)

// Special provider-level or adaptive default effort postures.
const (
	EffortAdaptive = "adaptive"
	EffortDynamic  = "dynamic"
)

// EffortLadder is the ordered 7-level canonical scale.
var EffortLadder = []string{
	EffortNone,
	EffortMinimal,
	EffortLow,
	EffortMedium,
	EffortHigh,
	EffortXHigh,
	EffortMax,
}

// IsValidEffortLevel reports whether s is one of the 7 levels in the canonical ladder.
func IsValidEffortLevel(s string) bool {
	switch s {
	case EffortNone, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	default:
		return false
	}
}

// IsValidEffortStyle reports whether s is one of the recognized effort styles.
func IsValidEffortStyle(s string) bool {
	switch s {
	case EffortStyleNamed, EffortStyleBudget, EffortStyleBakedName, EffortStyleToggle:
		return true
	default:
		return false
	}
}

// ValidateModelEffort validates the effort dimension metadata on a ModelEntry.
// Validation enforces fail-closed semantics:
//   - unknown effort style
//   - supported effort level outside the ladder
//   - default effort not in supported set (unless "adaptive" or "dynamic")
//   - effort budget map present for non-budget style
//   - budget map level outside the ladder or tokens <= 0
func ValidateModelEffort(m *ModelEntry) error {
	name := m.ID
	if name == "" {
		name = "<unnamed>"
	}

	if m.EffortStyle != "" && !IsValidEffortStyle(m.EffortStyle) {
		return fmt.Errorf("model %q: field %q: unknown style %q", name, "effort_style", m.EffortStyle)
	}

	for _, lvl := range m.SupportedEfforts {
		if !IsValidEffortLevel(lvl) {
			return fmt.Errorf("model %q: field %q: level %q outside ladder", name, "supported_efforts", lvl)
		}
	}

	if m.DefaultEffort != "" {
		if m.DefaultEffort != EffortAdaptive && m.DefaultEffort != EffortDynamic {
			found := false
			for _, lvl := range m.SupportedEfforts {
				if lvl == m.DefaultEffort {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("model %q: field %q: default %q not in supported set", name, "default_effort", m.DefaultEffort)
			}
		}
	}

	if len(m.EffortBudgetMap) > 0 {
		if m.EffortStyle != EffortStyleBudget {
			return fmt.Errorf("model %q: field %q: budget map not allowed for non-budget style %q", name, "effort_budget_map", m.EffortStyle)
		}
		for lvl, tokens := range m.EffortBudgetMap {
			if !IsValidEffortLevel(lvl) {
				return fmt.Errorf("model %q: field %q: level %q outside ladder", name, "effort_budget_map", lvl)
			}
			if tokens <= 0 {
				return fmt.Errorf("model %q: field %q: token budget for level %q must be > 0", name, "effort_budget_map", lvl)
			}
		}
	}

	return nil
}

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

		for j := range p.Models {
			if err := ValidateModelEffort(&p.Models[j]); err != nil {
				return nil, fmt.Errorf("provider %d (%s): %w", i, p.Name, err)
			}
		}
	}
	return &f, nil
}
