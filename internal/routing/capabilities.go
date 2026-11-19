package routing

import (
	"fmt"
	"sort"

	"github.com/tamld/g8s/internal/config"
)

// ErrCapabilityUnsupported signals that a provider/model cannot satisfy a requested capability.
var ErrCapabilityUnsupported = config.ErrCapabilityUnsupported

// CapabilityUnsupportedError describes the exact capability or effort mismatch before dispatch.
type CapabilityUnsupportedError struct {
	Provider   string
	Model      string
	Capability string
	Requested  string
	Reason     string
	Cause      error
}

func (e *CapabilityUnsupportedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s (%s/%s: %s)", ErrCapabilityUnsupported.Error(), e.Provider, e.Model, e.Reason)
	}
	return fmt.Sprintf("%s (%s/%s: capability %q)", ErrCapabilityUnsupported.Error(), e.Provider, e.Model, e.Capability)
}

func (e *CapabilityUnsupportedError) Unwrap() []error {
	if e.Cause != nil {
		return []error{ErrCapabilityUnsupported, e.Cause}
	}
	return []error{ErrCapabilityUnsupported}
}

// CapabilityRequest describes the capabilities required by a dispatch request.
type CapabilityRequest struct {
	Provider           string
	Model              string
	RequireToolCalling bool
	RequireStructured  bool
	RequireStreaming   bool
	RequireCancel      bool
	RequestedEffort    string
	MinContextWindow   int
	Strict             bool
	AllowEffortDegrade bool
}

// CapabilityGateResult captures the effective capabilities and any explicit effort degradation.
type CapabilityGateResult struct {
	Allowed         bool                     `json:"allowed"`
	Capabilities    config.ModelCapabilities `json:"capabilities"`
	EffortRequested string                   `json:"effort_requested,omitempty"`
	EffortApplied   string                   `json:"effort_applied,omitempty"`
	EffortDegraded  bool                     `json:"effort_degraded,omitempty"`
	DegradeReason   string                   `json:"effort_degrade_reason,omitempty"`
}

func lookupCatalogCapabilitiesAndView(cat *config.Catalog, provider, modelID string) (config.ModelCapabilities, *ModelEffortView) {
	if cat == nil {
		return config.ModelCapabilities{}, nil
	}
	if provider != "" {
		if prov, ok := cat.FindProvider(provider); ok {
			if m, found := findCatalogModel(prov, modelID); found {
				view := NormalizeCatalogModel(prov.Name, &prov, &m)
				populateBakedFamilyEfforts(view, nil, cat)
				return config.EffectiveCapabilities(prov, m), view
			}
			if prov.Capabilities != nil {
				return *prov.Capabilities, nil
			}
			return config.ModelCapabilities{}, nil
		}
		return config.ModelCapabilities{}, nil
	}

	keys := make([]string, 0, len(cat.Providers))
	for k := range cat.Providers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		prov := cat.Providers[k]
		if m, found := findCatalogModel(prov, modelID); found {
			view := NormalizeCatalogModel(prov.Name, &prov, &m)
			populateBakedFamilyEfforts(view, nil, cat)
			return config.EffectiveCapabilities(prov, m), view
		}
	}
	return config.ModelCapabilities{}, nil
}

// EvaluateCapabilityGate enforces declared capabilities and effort compatibility prior to dispatch.
func EvaluateCapabilityGate(cat *config.Catalog, req CapabilityRequest) (CapabilityGateResult, error) {
	caps, view := lookupCatalogCapabilitiesAndView(cat, req.Provider, req.Model)
	res := CapabilityGateResult{
		Allowed:         true,
		Capabilities:    caps,
		EffortRequested: req.RequestedEffort,
	}

	boolChecks := []struct {
		needed bool
		flag   *bool
		name   string
	}{
		{req.RequireToolCalling, caps.ToolCalling, "tool_calling"},
		{req.RequireStructured, caps.StructuredOutput, "structured_output"},
		{req.RequireStreaming, caps.Streaming, "streaming"},
		{req.RequireCancel, caps.CancelInflight, "cancel_inflight"},
	}
	for _, chk := range boolChecks {
		if chk.needed && chk.flag != nil && !*chk.flag {
			return CapabilityGateResult{}, &CapabilityUnsupportedError{
				Provider:   req.Provider,
				Model:      req.Model,
				Capability: chk.name,
				Reason:     fmt.Sprintf("%s is unsupported by catalog capabilities", chk.name),
			}
		}
	}

	if req.MinContextWindow > 0 && caps.ContextWindow > 0 && caps.ContextWindow < req.MinContextWindow {
		return CapabilityGateResult{}, &CapabilityUnsupportedError{
			Provider:   req.Provider,
			Model:      req.Model,
			Capability: "context_window",
			Reason:     fmt.Sprintf("context_window %d < required %d", caps.ContextWindow, req.MinContextWindow),
		}
	}

	if req.RequestedEffort == "" {
		return res, nil
	}

	if caps.ReasoningEffort != nil && !*caps.ReasoningEffort {
		if req.Strict || !req.AllowEffortDegrade {
			return CapabilityGateResult{}, &CapabilityUnsupportedError{
				Provider:   req.Provider,
				Model:      req.Model,
				Capability: "reasoning_effort",
				Requested:  req.RequestedEffort,
				Reason:     fmt.Sprintf("effort %q rejected because model does not support reasoning_effort", req.RequestedEffort),
			}
		}
		res.EffortApplied = ""
		res.EffortDegraded = true
		res.DegradeReason = "model does not support reasoning_effort; dropped per fallback policy"
		return res, nil
	}

	applied, mismatch, _, err := AdaptEffort(view, req.RequestedEffort)
	if err != nil {
		return CapabilityGateResult{}, &CapabilityUnsupportedError{
			Provider:   req.Provider,
			Model:      req.Model,
			Capability: "reasoning_effort",
			Requested:  req.RequestedEffort,
			Reason:     err.Error(),
			Cause:      err,
		}
	}
	if mismatch {
		if req.Strict || !req.AllowEffortDegrade {
			return CapabilityGateResult{}, &CapabilityUnsupportedError{
				Provider:   req.Provider,
				Model:      req.Model,
				Capability: "reasoning_effort",
				Requested:  req.RequestedEffort,
				Reason:     fmt.Sprintf("effort %q not supported; nearest is %q", req.RequestedEffort, applied),
			}
		}
		res.EffortApplied = applied
		res.EffortDegraded = true
		res.DegradeReason = fmt.Sprintf("effort %q degraded to %q per fallback policy", req.RequestedEffort, applied)
		return res, nil
	}

	res.EffortApplied = applied
	return res, nil
}
