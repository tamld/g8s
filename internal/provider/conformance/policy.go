package conformance

import (
	"fmt"
	"strings"
)

// LivePolicy classifies whether live (quota-consuming) probes may run.
type LivePolicy string

const (
	// PolicyFree providers may be live-probed without an explicit paid grant
	// (subject to model gating, e.g. opencode requires a *free* model).
	PolicyFree LivePolicy = "free"
	// PolicyPaid providers require --allow-paid <name> for live probes.
	PolicyPaid LivePolicy = "paid"
	// PolicyForbidden providers are never live-probed (operator policy).
	PolicyForbidden LivePolicy = "forbidden"
)

// free9RouterCombos are 9router combos treated as cheapest-tier for live probing.
var free9RouterCombos = map[string]bool{
	"fast-agent":     true,
	"reasoning-free": true,
	"docs-retriever": true,
}

// isFreeModel mirrors routing.IsFreeTierModel without importing the router.
func isFreeModel(model string) bool {
	id := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(id, "free") || id == "docs-retriever"
}

// ParseAllowPaid parses a comma-separated --allow-paid value.
func ParseAllowPaid(v string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(v, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p != "" {
			out[p] = true
		}
	}
	return out
}

// LiveDecision is the outcome of the live-probe policy gate.
type LiveDecision struct {
	Allowed bool
	Reason  string
}

// DecideLive determines whether live probes may run for a provider/model.
// agy is unconditionally forbidden. Paid providers require an explicit
// allow-paid grant. Free providers are additionally model-gated so that a
// free CLI cannot silently bill a paid upstream model.
func DecideLive(name string, policy LivePolicy, model string, allowPaid map[string]bool) LiveDecision {
	key := strings.ToLower(strings.TrimSpace(name))
	switch policy {
	case PolicyForbidden:
		return LiveDecision{Reason: fmt.Sprintf("E_LIVE_FORBIDDEN: live probes against %s are prohibited by operator policy", name)}
	case PolicyPaid:
		if allowPaid[key] {
			return LiveDecision{Allowed: true, Reason: "paid provider explicitly allowed via --allow-paid"}
		}
		return LiveDecision{Reason: fmt.Sprintf("E_PAID_NOT_ALLOWED: %s live probes consume paid quota; pass --allow-paid %s after operator approval", name, key)}
	}
	// PolicyFree: model gate.
	if allowPaid[key] {
		return LiveDecision{Allowed: true, Reason: "provider explicitly allowed via --allow-paid"}
	}
	switch key {
	case "9router":
		if free9RouterCombos[strings.ToLower(strings.TrimSpace(model))] || isFreeModel(model) {
			return LiveDecision{Allowed: true, Reason: "9router cheapest-tier combo"}
		}
		return LiveDecision{Reason: fmt.Sprintf("E_PAID_MODEL_NOT_ALLOWED: 9router model %q is not a cheapest-tier combo (fast-agent, reasoning-free, docs-retriever, *free*); pass --allow-paid 9router", model)}
	case "opencode":
		if isFreeModel(model) {
			return LiveDecision{Allowed: true, Reason: "opencode free-tier model"}
		}
		return LiveDecision{Reason: fmt.Sprintf("E_PAID_MODEL_NOT_ALLOWED: opencode model %q is not a *free* model; pass --allow-paid opencode", model)}
	case "commandcode", "qwen":
		trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "9router/")
		if free9RouterCombos[trimmed] || isFreeModel(model) {
			return LiveDecision{Allowed: true, Reason: key + " free-tier model"}
		}
		return LiveDecision{Reason: fmt.Sprintf("E_PAID_MODEL_NOT_ALLOWED: %s model %q is not a free-tier model (*free*, docs-retriever, reasoning-free, fast-agent); pass --allow-paid %s", name, model, key)}
	}
	return LiveDecision{Allowed: true, Reason: "local/free provider"}
}
