package lane

import (
	"testing"

	"github.com/tamld/g8s/internal/config"
)

func TestResolveEffortSignals_PrecedenceMatrix(t *testing.T) {
	// 1. Explicit over declared over registry over floor
	t.Run("explicit_wins_over_all", func(t *testing.T) {
		res := ResolveEffortSignals(
			"xhigh",
			BlastLow,
			20,
			"docs",
			"low",
		)
		if res.Effort != "xhigh" {
			t.Errorf("Effort = %q, want xhigh", res.Effort)
		}
		if res.Source != EffortSourceExplicit {
			t.Errorf("Source = %q, want explicit", res.Source)
		}
		if res.ExplicitSuggestion != "xhigh" {
			t.Errorf("ExplicitSuggestion = %q, want xhigh", res.ExplicitSuggestion)
		}
		if res.DeclaredSuggestion != "low" {
			t.Errorf("DeclaredSuggestion = %q, want low", res.DeclaredSuggestion)
		}
		if res.RegistrySuggestion != "low" {
			t.Errorf("RegistrySuggestion = %q, want low", res.RegistrySuggestion)
		}
		if res.FloorSuggestion != "medium" {
			t.Errorf("FloorSuggestion = %q, want medium", res.FloorSuggestion)
		}
		if res.OverrideDown {
			t.Errorf("OverrideDown = true, want false (xhigh > low)")
		}
	})

	// 2. Declared over registry over floor
	t.Run("declared_wins_over_registry_and_floor", func(t *testing.T) {
		res := ResolveEffortSignals(
			"",
			BlastHigh,
			300,
			"docs",
			"low",
		)
		if res.Effort != "high" {
			t.Errorf("Effort = %q, want high", res.Effort)
		}
		if res.Source != EffortSourceDeclared {
			t.Errorf("Source = %q, want declared", res.Source)
		}
		if res.ExplicitSuggestion != "" {
			t.Errorf("ExplicitSuggestion = %q, want empty", res.ExplicitSuggestion)
		}
		if res.DeclaredSuggestion != "high" {
			t.Errorf("DeclaredSuggestion = %q, want high", res.DeclaredSuggestion)
		}
		if res.RegistrySuggestion != "low" {
			t.Errorf("RegistrySuggestion = %q, want low", res.RegistrySuggestion)
		}
		if res.FloorSuggestion != "medium" {
			t.Errorf("FloorSuggestion = %q, want medium", res.FloorSuggestion)
		}
		if res.OverrideDown {
			t.Errorf("OverrideDown = true, want false when explicit is empty")
		}
	})

	// 3. Registry over floor
	t.Run("registry_wins_over_floor", func(t *testing.T) {
		res := ResolveEffortSignals(
			"",
			"",
			0,
			"docs",
			"low",
		)
		if res.Effort != "low" {
			t.Errorf("Effort = %q, want low", res.Effort)
		}
		if res.Source != EffortSourceRegistry {
			t.Errorf("Source = %q, want registry", res.Source)
		}
		if res.DeclaredSuggestion != "" {
			t.Errorf("DeclaredSuggestion = %q, want empty", res.DeclaredSuggestion)
		}
		if res.RegistrySuggestion != "low" {
			t.Errorf("RegistrySuggestion = %q, want low", res.RegistrySuggestion)
		}
		if res.FloorSuggestion != "medium" {
			t.Errorf("FloorSuggestion = %q, want medium", res.FloorSuggestion)
		}
	})

	// 4. Floor when nothing else matches
	t.Run("floor_wins_when_unregistered", func(t *testing.T) {
		res := ResolveEffortSignals(
			"",
			"",
			0,
			UnregisteredClassName,
			"medium",
		)
		if res.Effort != "medium" {
			t.Errorf("Effort = %q, want medium", res.Effort)
		}
		if res.Source != EffortSourceFloor {
			t.Errorf("Source = %q, want floor", res.Source)
		}
		if res.RegistrySuggestion != "" {
			t.Errorf("RegistrySuggestion = %q, want empty for unregistered", res.RegistrySuggestion)
		}
		if res.FloorSuggestion != "medium" {
			t.Errorf("FloorSuggestion = %q, want medium", res.FloorSuggestion)
		}
	})
}

func TestResolveEffortSignals_MatrixRows(t *testing.T) {
	tests := []struct {
		name            string
		blast           string
		loc             int
		regClass        string
		regEffort       string
		wantEffort      string
		wantSource      string
		wantDeclaredSug string
		wantRegistrySug string
	}{
		// blast=high ∧ loc>200 -> high (boundary: loc=201 vs 200)
		{
			name:            "blast_high_loc_201_is_high",
			blast:           BlastHigh,
			loc:             201,
			regClass:        "docs",
			regEffort:       "low",
			wantEffort:      config.EffortHigh,
			wantSource:      EffortSourceDeclared,
			wantDeclaredSug: config.EffortHigh,
			wantRegistrySug: "low",
		},
		{
			name:            "blast_high_loc_200_at_least_medium_elevates_low",
			blast:           BlastHigh,
			loc:             200,
			regClass:        "docs",
			regEffort:       "low",
			wantEffort:      config.EffortMedium,
			wantSource:      EffortSourceDeclared,
			wantDeclaredSug: config.EffortMedium,
			wantRegistrySug: "low",
		},
		// blast=high alone (loc=0) -> at least medium
		{
			name:            "blast_high_loc_0_elevates_low_to_medium",
			blast:           BlastHigh,
			loc:             0,
			regClass:        "docs",
			regEffort:       "low",
			wantEffort:      config.EffortMedium,
			wantSource:      EffortSourceDeclared,
			wantDeclaredSug: config.EffortMedium,
			wantRegistrySug: "low",
		},
		{
			name:            "blast_high_registry_high_stands",
			blast:           BlastHigh,
			loc:             100,
			regClass:        "core",
			regEffort:       "high",
			wantEffort:      config.EffortHigh,
			wantSource:      EffortSourceRegistry,
			wantDeclaredSug: config.EffortMedium,
			wantRegistrySug: "high",
		},
		{
			name:            "blast_high_registry_unregistered_medium_stands_as_declared",
			blast:           BlastHigh,
			loc:             0,
			regClass:        UnregisteredClassName,
			regEffort:       "medium",
			wantEffort:      config.EffortMedium,
			wantSource:      EffortSourceDeclared,
			wantDeclaredSug: config.EffortMedium,
			wantRegistrySug: "",
		},
		// blast=low ∧ loc<=50 -> low (boundary: loc=50 vs 51)
		{
			name:            "blast_low_loc_50_drops_high_to_low",
			blast:           BlastLow,
			loc:             50,
			regClass:        "core",
			regEffort:       "high",
			wantEffort:      config.EffortLow,
			wantSource:      EffortSourceDeclared,
			wantDeclaredSug: config.EffortLow,
			wantRegistrySug: "high",
		},
		{
			// blast=low -> low at ANY loc (matrix v1.1: the loc<=50 bound
			// left the low/51-200 cell empty — declared suggestions went
			// empty and the source mislabeled as registry, caught live by
			// patrol round 1 on a 95-file dispatch). Declared low also
			// overrides a registry high: the author's blast assessment is
			// the point of the signal, recorded via override_down.
			name:            "blast_low_loc_51_declares_low_over_registry_high",
			blast:           BlastLow,
			loc:             51,
			regClass:        "core",
			regEffort:       "high",
			wantEffort:      config.EffortLow,
			wantSource:      EffortSourceDeclared,
			wantDeclaredSug: config.EffortLow,
			wantRegistrySug: "high",
		},
		{
			name:            "blast_low_loc_0_is_low",
			blast:           BlastLow,
			loc:             0,
			regClass:        "core",
			regEffort:       "high",
			wantEffort:      config.EffortLow,
			wantSource:      EffortSourceDeclared,
			wantDeclaredSug: config.EffortLow,
			wantRegistrySug: "high",
		},
		// blast=medium -> registry/medium stands
		{
			name:            "blast_medium_registry_low_stands",
			blast:           BlastMedium,
			loc:             100,
			regClass:        "docs",
			regEffort:       "low",
			wantEffort:      "low",
			wantSource:      EffortSourceRegistry,
			wantDeclaredSug: "",
			wantRegistrySug: "low",
		},
		{
			name:            "blast_medium_unregistered_floor_stands",
			blast:           BlastMedium,
			loc:             100,
			regClass:        UnregisteredClassName,
			regEffort:       "medium",
			wantEffort:      "medium",
			wantSource:      EffortSourceFloor,
			wantDeclaredSug: "",
			wantRegistrySug: "",
		},
		// loc alone without blast -> registry/medium stands
		{
			name:            "loc_alone_300_registry_stands",
			blast:           "",
			loc:             300,
			regClass:        "docs",
			regEffort:       "low",
			wantEffort:      "low",
			wantSource:      EffortSourceRegistry,
			wantDeclaredSug: "",
			wantRegistrySug: "low",
		},
		{
			name:            "loc_alone_300_unregistered_floor_stands",
			blast:           "",
			loc:             300,
			regClass:        UnregisteredClassName,
			regEffort:       "medium",
			wantEffort:      "medium",
			wantSource:      EffortSourceFloor,
			wantDeclaredSug: "",
			wantRegistrySug: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ResolveEffortSignals("", tt.blast, tt.loc, tt.regClass, tt.regEffort)
			if res.Effort != tt.wantEffort {
				t.Errorf("Effort = %q, want %q", res.Effort, tt.wantEffort)
			}
			if res.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", res.Source, tt.wantSource)
			}
			if res.DeclaredSuggestion != tt.wantDeclaredSug {
				t.Errorf("DeclaredSuggestion = %q, want %q", res.DeclaredSuggestion, tt.wantDeclaredSug)
			}
			if res.RegistrySuggestion != tt.wantRegistrySug {
				t.Errorf("RegistrySuggestion = %q, want %q", res.RegistrySuggestion, tt.wantRegistrySug)
			}
		})
	}
}

func TestResolveEffortSignals_OverrideDown(t *testing.T) {
	// 1. explicit=low, declared=high, registry=low -> override_down is TRUE against winning declared high
	t.Run("explicit_low_against_declared_high_overrides_down", func(t *testing.T) {
		res := ResolveEffortSignals("low", BlastHigh, 300, "docs", "low")
		if res.Effort != "low" {
			t.Errorf("Effort = %q, want low", res.Effort)
		}
		if res.Source != EffortSourceExplicit {
			t.Errorf("Source = %q, want explicit", res.Source)
		}
		if !res.OverrideDown {
			t.Errorf("OverrideDown = false, want true (low < declared high)")
		}
	})

	// 2. explicit=medium, declared=low, registry=high -> override_down is FALSE against winning declared low
	t.Run("explicit_medium_against_declared_low_not_override_down", func(t *testing.T) {
		res := ResolveEffortSignals("medium", BlastLow, 20, "core", "high")
		if res.Effort != "medium" {
			t.Errorf("Effort = %q, want medium", res.Effort)
		}
		if res.Source != EffortSourceExplicit {
			t.Errorf("Source = %q, want explicit", res.Source)
		}
		if res.OverrideDown {
			t.Errorf("OverrideDown = true, want false (medium > declared low)")
		}
	})

	// 3. explicit=low, registry=high, no declared signals -> override_down is TRUE against registry high
	t.Run("explicit_low_against_registry_high_overrides_down", func(t *testing.T) {
		res := ResolveEffortSignals("low", "", 0, "core", "high")
		if res.Effort != "low" {
			t.Errorf("Effort = %q, want low", res.Effort)
		}
		if res.Source != EffortSourceExplicit {
			t.Errorf("Source = %q, want explicit", res.Source)
		}
		if !res.OverrideDown {
			t.Errorf("OverrideDown = false, want true (low < registry high)")
		}
	})

	// 4. no explicit flag -> override_down is FALSE
	t.Run("no_explicit_override_down_is_false", func(t *testing.T) {
		res := ResolveEffortSignals("", BlastLow, 20, "core", "high")
		if res.OverrideDown {
			t.Errorf("OverrideDown = true, want false when explicit is empty")
		}
	})
}

func TestIsValidBlastRadius(t *testing.T) {
	valids := []string{"low", "medium", "high", "LOW", "High", " medium "}
	for _, v := range valids {
		if !IsValidBlastRadius(v) {
			t.Errorf("IsValidBlastRadius(%q) = false, want true", v)
		}
	}
	invalids := []string{"", "invalid", "superhigh", "extreme", "none", "minimal"}
	for _, inv := range invalids {
		if IsValidBlastRadius(inv) {
			t.Errorf("IsValidBlastRadius(%q) = true, want false", inv)
		}
	}
}
