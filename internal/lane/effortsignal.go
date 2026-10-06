package lane

import (
	"strings"

	"github.com/tamld/g8s/internal/config"
)

// Effort sources in declared precedence order.
const (
	EffortSourceExplicit = "explicit"
	EffortSourceDeclared = "declared"
	EffortSourceRegistry = "registry"
	EffortSourceFloor    = "floor"
)

// Declared blast radius levels.
const (
	BlastLow    = "low"
	BlastMedium = "medium"
	BlastHigh   = "high"
)

// IsValidBlastRadius reports whether s is a valid declared blast radius level.
func IsValidBlastRadius(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case BlastLow, BlastMedium, BlastHigh:
		return true
	default:
		return false
	}
}

// EffortSignalsPayload carries the signals object recorded in task payloads.
type EffortSignalsPayload struct {
	BlastRadius        string `json:"blast_radius"`
	LOCEstimate        int    `json:"loc_estimate"`
	DeclaredSuggestion string `json:"declared_suggestion"`
	RegistrySuggestion string `json:"registry_suggestion"`
	OverrideDown       bool   `json:"override_down"`
}

// SignalResult carries the outcome of effort signal resolution.
type SignalResult struct {
	Effort             string
	Source             string
	BlastRadius        string
	LOCEstimate        int
	ExplicitSuggestion string
	DeclaredSuggestion string
	RegistrySuggestion string
	FloorSuggestion    string
	OverrideDown       bool
}

// EffortSignals returns the serializable payload representation of the signals.
func (r SignalResult) EffortSignals() EffortSignalsPayload {
	return EffortSignalsPayload{
		BlastRadius:        r.BlastRadius,
		LOCEstimate:        r.LOCEstimate,
		DeclaredSuggestion: r.DeclaredSuggestion,
		RegistrySuggestion: r.RegistrySuggestion,
		OverrideDown:       r.OverrideDown,
	}
}

// ResolveEffortSignals resolves target effort according to the declared-signal
// precedence chain: explicit -> declared -> registry -> floor.
//
// Precedence & semantics per pass-3 design (plans/261004-effort-optimization/design-pass3-signal-model.md):
//  1. Explicit flag (--effort): always wins when passed.
//  2. Declared signals (--blast-radius, --loc-estimate): evaluated via hypothesis matrix v1.
//  3. Path-class registry (effort-classes.yml): fallback prior.
//  4. Floor: config.EffortMedium.
//
// Hypothesis matrix v1:
//   - blast=high ∧ loc>200 -> high
//   - blast=high -> at least medium (elevates registry < medium to medium; registry >= medium stands)
//   - blast=low ∧ loc<=50 -> low
//   - otherwise the registry result (or medium floor) stands
func ResolveEffortSignals(
	explicit string,
	blast string,
	loc int,
	registryClass string,
	registryEffort string,
) SignalResult {
	explicit = strings.ToLower(strings.TrimSpace(explicit))
	blast = strings.ToLower(strings.TrimSpace(blast))
	registryEffort = strings.ToLower(strings.TrimSpace(registryEffort))
	floorSuggestion := config.EffortMedium

	var regSuggestion string
	if registryClass != "" && registryClass != UnregisteredClassName && registryEffort != "" {
		regSuggestion = registryEffort
	}

	// Evaluate declared suggestion via hypothesis matrix v1
	var (
		decSuggestion    string
		decHasSuggestion bool
	)
	switch blast {
	case BlastHigh:
		if loc > 200 {
			decSuggestion = config.EffortHigh
			decHasSuggestion = true
		} else {
			decSuggestion = config.EffortMedium
			decHasSuggestion = true
		}
	case BlastLow:
		if loc <= 50 {
			decSuggestion = config.EffortLow
			decHasSuggestion = true
		}
	}

	// Determine winning non-explicit level and source (the baseline prior)
	var (
		priorEffort string
		priorSource string
	)

	if decHasSuggestion {
		if blast == BlastHigh && loc <= 200 {
			// blast=high -> at least medium
			if regSuggestion != "" && EffortLadderIndex(regSuggestion) >= EffortLadderIndex(config.EffortMedium) {
				priorEffort = regSuggestion
				priorSource = EffortSourceRegistry
			} else {
				priorEffort = config.EffortMedium
				priorSource = EffortSourceDeclared
			}
		} else {
			// blast=high ∧ loc>200 -> high; blast=low ∧ loc<=50 -> low
			priorEffort = decSuggestion
			priorSource = EffortSourceDeclared
		}
	} else {
		// Otherwise registry result (or medium floor) stands
		if regSuggestion != "" {
			priorEffort = regSuggestion
			priorSource = EffortSourceRegistry
		} else {
			priorEffort = floorSuggestion
			priorSource = EffortSourceFloor
		}
	}

	res := SignalResult{
		BlastRadius:        blast,
		LOCEstimate:        loc,
		ExplicitSuggestion: explicit,
		DeclaredSuggestion: decSuggestion,
		RegistrySuggestion: regSuggestion,
		FloorSuggestion:    floorSuggestion,
	}

	if explicit != "" {
		res.Effort = explicit
		res.Source = EffortSourceExplicit
		res.OverrideDown = IsOverrideDown(explicit, priorEffort)
	} else {
		res.Effort = priorEffort
		res.Source = priorSource
		res.OverrideDown = false
	}

	return res
}
