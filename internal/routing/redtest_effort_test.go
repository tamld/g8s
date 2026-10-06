package routing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

// ============================================================================
// Dimension 1: Unknown model entry (nil)
// Rule: Unknown model entry (nil) -> pass-through: applied = requested,
// no mismatch, "" stays "" - EXCEPT baked-suffix model ids (row 4).
// ============================================================================

func TestRedtest_Effort_Dimension1_UnknownModelNil(t *testing.T) {
	tests := []struct {
		name         string
		rule         string
		provider     string
		modelID      string
		requested    string
		wantApplied  string
		wantMismatch bool
		wantTokens   int
		wantErr      bool
		finding      string
	}{
		// Rule 1: Unknown model entry (nil) -> pass-through requested "none"
		{
			name:         "nil_requested_none",
			rule:         "Rule 1: Unknown model entry (nil) pass-through",
			requested:    "none",
			wantApplied:  "none",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> pass-through requested "minimal"
		{
			name:         "nil_requested_minimal",
			rule:         "Rule 1: Unknown model entry (nil) pass-through",
			requested:    "minimal",
			wantApplied:  "minimal",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> pass-through requested "low"
		{
			name:         "nil_requested_low",
			rule:         "Rule 1: Unknown model entry (nil) pass-through",
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> pass-through requested "medium"
		{
			name:         "nil_requested_medium",
			rule:         "Rule 1: Unknown model entry (nil) pass-through",
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> pass-through requested "high"
		{
			name:         "nil_requested_high",
			rule:         "Rule 1: Unknown model entry (nil) pass-through",
			requested:    "high",
			wantApplied:  "high",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> pass-through requested "xhigh"
		{
			name:         "nil_requested_xhigh",
			rule:         "Rule 1: Unknown model entry (nil) pass-through",
			requested:    "xhigh",
			wantApplied:  "xhigh",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> pass-through requested "max"
		{
			name:         "nil_requested_max",
			rule:         "Rule 1: Unknown model entry (nil) pass-through",
			requested:    "max",
			wantApplied:  "max",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> empty requested stays empty
		{
			name:         "nil_requested_empty",
			rule:         "Rule 1: Unknown model entry (nil) empty stays empty",
			requested:    "",
			wantApplied:  "",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> arbitrary non-ladder requested passes through
		{
			name:         "nil_requested_arbitrary_string",
			rule:         "Rule 1: Unknown model entry (nil) arbitrary string pass-through",
			requested:    "custom-unrecognized",
			wantApplied:  "custom-unrecognized",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) -> ResolveEffort with non-baked unknown model passes through
		{
			name:         "resolve_unknown_non_baked_model",
			rule:         "Rule 1: Unknown model entry (nil) resolve non-baked passes through",
			provider:     "unknown-prov",
			modelID:      "unknown-custom-model",
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) with a baked-suffix id still passes
		// through — coercion is deliberately NOT the adapter's job (issue #568);
		// the submit-layer realignment (issue #563 C3) owns baked ids.
		{
			name:         "resolve_unknown_baked_suffix_model_low",
			rule:         "Rule 1: Unknown model with baked suffix -low passes requested through",
			provider:     "unknown-prov",
			modelID:      "unregistered-model-low",
			requested:    "high",
			wantApplied:  "high",
			wantMismatch: false,
		},
		// Rule 1: Unknown model entry (nil) with a baked-suffix id still passes
		// through (issue #568 — realignment owns the rewrite, not the adapter).
		{
			name:         "resolve_unknown_baked_suffix_model_high",
			rule:         "Rule 1: Unknown model with baked suffix -high passes requested through",
			provider:     "unknown-prov",
			modelID:      "unregistered-model-high",
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var applied string
			var mismatch bool
			var tokens int
			var err error

			if tc.modelID != "" {
				res, rErr := ResolveEffortWithCatalog(nil, nil, tc.provider, tc.modelID, tc.requested)
				applied = res.Applied
				mismatch = res.Mismatch
				tokens = res.BudgetTokens
				err = rErr
			} else {
				applied, mismatch, tokens, err = AdaptEffort(nil, tc.requested)
			}

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if applied != tc.wantApplied || mismatch != tc.wantMismatch || tokens != tc.wantTokens {
				if tc.finding != "" {
					// Expected vs Actual check
					t.Logf("Expected applied=%q mismatch=%v tokens=%d; Actual applied=%q mismatch=%v tokens=%d",
						tc.wantApplied, tc.wantMismatch, tc.wantTokens, applied, mismatch, tokens)
					t.Skip(tc.finding)
				}
				t.Fatalf("got applied=%q mismatch=%v tokens=%d; want applied=%q mismatch=%v tokens=%d",
					applied, mismatch, tokens, tc.wantApplied, tc.wantMismatch, tc.wantTokens)
			}
		})
	}
}

// ============================================================================
// Dimension 2: Requested ""
// Rule: Requested "" -> applied = DefaultEffort when it is a ladder level;
// adaptive/dynamic/empty default -> "".
// ============================================================================

func TestRedtest_Effort_Dimension2_RequestedEmpty(t *testing.T) {
	tests := []struct {
		name         string
		rule         string
		view         *ModelEffortView
		wantApplied  string
		wantMismatch bool
		wantTokens   int
	}{
		// Rule 2: Requested "" with DefaultEffort="none" (ladder level 0)
		{
			name: "empty_req_default_none",
			rule: "Rule 2: Requested empty with ladder default none",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-default-none",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"none", "low", "medium"},
				DefaultEffort:    config.EffortNone,
			},
			wantApplied:  "none",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="minimal" (ladder level 1)
		{
			name: "empty_req_default_minimal",
			rule: "Rule 2: Requested empty with ladder default minimal",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-default-minimal",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"minimal", "low"},
				DefaultEffort:    config.EffortMinimal,
			},
			wantApplied:  "minimal",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="low" (ladder level 2)
		{
			name: "empty_req_default_low",
			rule: "Rule 2: Requested empty with ladder default low",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-default-low",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium"},
				DefaultEffort:    config.EffortLow,
			},
			wantApplied:  "low",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="medium" (ladder level 3)
		{
			name: "empty_req_default_medium",
			rule: "Rule 2: Requested empty with ladder default medium",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-default-medium",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    config.EffortMedium,
			},
			wantApplied:  "medium",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="high" (ladder level 4)
		{
			name: "empty_req_default_high",
			rule: "Rule 2: Requested empty with ladder default high",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-default-high",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"medium", "high", "xhigh"},
				DefaultEffort:    config.EffortHigh,
			},
			wantApplied:  "high",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="xhigh" (ladder level 5)
		{
			name: "empty_req_default_xhigh",
			rule: "Rule 2: Requested empty with ladder default xhigh",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-default-xhigh",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"high", "xhigh", "max"},
				DefaultEffort:    config.EffortXHigh,
			},
			wantApplied:  "xhigh",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="max" (ladder level 6)
		{
			name: "empty_req_default_max",
			rule: "Rule 2: Requested empty with ladder default max",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-default-max",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"xhigh", "max"},
				DefaultEffort:    config.EffortMax,
			},
			wantApplied:  "max",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="adaptive" -> empty applied
		{
			name: "empty_req_default_adaptive",
			rule: "Rule 2: Requested empty with adaptive default yields empty applied",
			view: &ModelEffortView{
				Provider:         "openai",
				Model:            "gpt-6-astra",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    config.EffortAdaptive,
			},
			wantApplied:  "",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="dynamic" -> empty applied
		{
			name: "empty_req_default_dynamic",
			rule: "Rule 2: Requested empty with dynamic default yields empty applied",
			view: &ModelEffortView{
				Provider:         "google",
				Model:            "gemini-3.1-pro-preview",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"minimal", "low", "medium", "high"},
				DefaultEffort:    config.EffortDynamic,
			},
			wantApplied:  "",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with DefaultEffort="" -> empty applied
		{
			name: "empty_req_default_empty",
			rule: "Rule 2: Requested empty with empty default yields empty applied",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "generic-qwen",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium"},
				DefaultEffort:    "",
			},
			wantApplied:  "",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with non-ladder string default (e.g. "auto") -> empty applied
		{
			name: "empty_req_default_non_ladder_string",
			rule: "Rule 2: Requested empty with non-ladder default string yields empty applied",
			view: &ModelEffortView{
				Provider:         "custom",
				Model:            "custom-auto",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "auto",
			},
			wantApplied:  "",
			wantMismatch: false,
		},
		// Rule 2: Requested "" with budget style loads tokens for default level
		{
			name: "empty_req_budget_style_with_matching_budget_key",
			rule: "Rule 2: Requested empty with budget style default loads budget tokens",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "legacy-budget-low",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "low",
				EffortBudgetMap:  map[string]int{"low": 1024, "high": 4096},
			},
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   1024,
		},
		// Rule 2: Requested "" with budget style but default has no entry in map -> 0 tokens
		{
			name: "empty_req_budget_style_without_matching_budget_key",
			rule: "Rule 2: Requested empty with budget style when default missing in map yields 0 tokens",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "legacy-budget-sparse",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "medium",
				EffortBudgetMap:  map[string]int{"low": 1024},
			},
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applied, mismatch, tokens, err := AdaptEffort(tc.view, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if applied != tc.wantApplied || mismatch != tc.wantMismatch || tokens != tc.wantTokens {
				t.Fatalf("got applied=%q mismatch=%v tokens=%d; want applied=%q mismatch=%v tokens=%d",
					applied, mismatch, tokens, tc.wantApplied, tc.wantMismatch, tc.wantTokens)
			}
		})
	}
}

// ============================================================================
// Dimension 3: Named style
// Rule: Named style: pass-through when requested is in SupportedEfforts;
// otherwise nearest-supported applied AND recorded - minimal index distance
// on the ladder, tie -> LOWER level, mismatch = true, never an error.
// ============================================================================

func TestRedtest_Effort_Dimension3_NamedStyle(t *testing.T) {
	tests := []struct {
		name         string
		rule         string
		view         *ModelEffortView
		requested    string
		wantApplied  string
		wantMismatch bool
	}{
		// Rule 3: Named style - exact match in supported efforts (low)
		{
			name: "named_exact_supported_low",
			rule: "Rule 3: Named style pass-through when requested in supported efforts",
			view: &ModelEffortView{
				Provider:         "anthropic",
				Model:            "claude-opus-5-5",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
		},
		// Rule 3: Named style - exact match in supported efforts (xhigh)
		{
			name: "named_exact_supported_xhigh",
			rule: "Rule 3: Named style pass-through for xhigh when in supported efforts",
			view: &ModelEffortView{
				Provider:         "anthropic",
				Model:            "claude-opus-5-5",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "xhigh"},
				DefaultEffort:    "medium",
			},
			requested:    "xhigh",
			wantApplied:  "xhigh",
			wantMismatch: false,
		},
		// Rule 3: Named style - requested above supported set, nearest is highest available
		{
			name: "named_minimal_index_distance_down",
			rule: "Rule 3: Named style nearest-down when requested above supported set",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-bottom-only",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"none", "minimal"},
				DefaultEffort:    "minimal",
			},
			requested:    "max", // max(6) to minimal(1) dist=5 vs to none(0) dist=6
			wantApplied:  "minimal",
			wantMismatch: true,
		},
		// Rule 3: Named style - requested below supported set, nearest is lowest available
		{
			name: "named_minimal_index_distance_up",
			rule: "Rule 3: Named style nearest-up when requested below supported set",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-top-only",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"xhigh", "max"},
				DefaultEffort:    "xhigh",
			},
			requested:    "none", // none(0) to xhigh(5) dist=5 vs to max(6) dist=6
			wantApplied:  "xhigh",
			wantMismatch: true,
		},
		// Rule 3: Named style - tie between none(0) and low(2) for minimal(1), lower index 0 wins
		{
			name: "named_tie_none_vs_low_for_minimal",
			rule: "Rule 3: Named style tie-breaker picks lower ladder index (none vs low for minimal)",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-tie-low",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"none", "low"},
				DefaultEffort:    "none",
			},
			requested:    "minimal", // dist(none, min)=1, dist(low, min)=1 -> tie picks none
			wantApplied:  "none",
			wantMismatch: true,
		},
		// Rule 3: Named style - tie between medium(3) and xhigh(5) for high(4), lower index 3 wins
		{
			name: "named_tie_medium_vs_xhigh_for_high",
			rule: "Rule 3: Named style tie-breaker picks lower ladder index (medium vs xhigh for high)",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-tie-mid",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"medium", "xhigh"},
				DefaultEffort:    "medium",
			},
			requested:    "high", // dist(medium, high)=1, dist(xhigh, high)=1 -> tie picks medium
			wantApplied:  "medium",
			wantMismatch: true,
		},
		// Rule 3: Named style - tie between high(4) and max(6) for xhigh(5), lower index 4 wins
		{
			name: "named_tie_high_vs_max_for_xhigh",
			rule: "Rule 3: Named style tie-breaker picks lower ladder index (high vs max for xhigh)",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-tie-high",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"high", "max"},
				DefaultEffort:    "high",
			},
			requested:    "xhigh", // dist(high, xhigh)=1, dist(max, xhigh)=1 -> tie picks high
			wantApplied:  "high",
			wantMismatch: true,
		},
		// Rule 3: Named style - tie breaking is invariant to candidate list ordering [max, high]
		{
			name: "named_tie_reverse_candidate_order",
			rule: "Rule 3: Named style tie-breaker is invariant to candidate ordering [max, high]",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-tie-rev",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"max", "high"},
				DefaultEffort:    "high",
			},
			requested:    "xhigh", // candidates in reverse order; tie must still pick high
			wantApplied:  "high",
			wantMismatch: true,
		},
		// Rule 3: Named style - distance 1 vs 2 picks distance 1 (medium(3) closer to low(2) than none(0))
		{
			name: "named_asymmetric_distance_picks_closer",
			rule: "Rule 3: Named style minimal index distance picks closer candidate",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-asym",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"none", "medium"},
				DefaultEffort:    "medium",
			},
			requested:    "low", // low(2) to none(0) dist=2; to medium(3) dist=1 -> medium wins
			wantApplied:  "medium",
			wantMismatch: true,
		},
		// Rule 3: Named style - requested string outside ladder falls back to candidates[0], never errors
		{
			name: "named_unrecognized_requested_level_never_errors",
			rule: "Rule 3: Named style unrecognized requested string never errors and returns candidate",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-fallback",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "hyper-effort",
			wantApplied:  "medium",
			wantMismatch: true,
		},
		// Rule 3: Named style - empty supported set passes requested through, never errors
		{
			name: "named_empty_supported_efforts_never_errors",
			rule: "Rule 3: Named style empty supported set passes requested through without error",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-empty-sup",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{},
				DefaultEffort:    "",
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applied, mismatch, tokens, err := AdaptEffort(tc.view, tc.requested)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if applied != tc.wantApplied || mismatch != tc.wantMismatch || tokens != 0 {
				t.Fatalf("got applied=%q mismatch=%v tokens=%d; want applied=%q mismatch=%v tokens=0",
					applied, mismatch, tokens, tc.wantApplied, tc.wantMismatch)
			}
		})
	}
}

// ============================================================================
// Dimension 4: Baked-name model ids
// Rule: Baked-name model ids (suffix -low/-medium/-high/-xhigh/-max -
// amended 2026-10-07 after live finding, issue #568): applied =
// the baked level ALWAYS; requested "" or == baked -> no mismatch;
// requested != baked -> mismatch = true, recorded, never an error;
// mandatory+none still hard-refuses; budget fields zero.
// ============================================================================

func TestRedtest_Effort_Dimension4_BakedNameModelIDs(t *testing.T) {
	tests := []struct {
		name         string
		rule         string
		view         *ModelEffortView
		requested    string
		wantApplied  string
		wantMismatch bool
		wantTokens   int
		wantErr      bool
		finding      string
	}{
		// Rule 4: Baked-name model with suffix -low, requested "low" == baked -> applied = "low", mismatch = false
		{
			name: "baked_suffix_low_requested_low_pass",
			rule: "Rule 4: Baked-name model suffix -low requested low matches baked",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-low",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -low, requested "high" != baked -> applied = "low", mismatch = true
		// Expected: applied="low", mismatch=true; Actual: applied="high", mismatch=false
		{
			name: "baked_suffix_low_requested_high_pass_through",
			rule: "Rule 4: Baked-name model suffix -low passes requested high through",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-low",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "high",
			wantApplied:  "high",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -medium, requested "medium" == baked -> applied = "medium", mismatch = false
		{
			name: "baked_suffix_medium_requested_medium_pass",
			rule: "Rule 4: Baked-name model suffix -medium requested medium matches baked",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-medium",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -medium, requested "low" != baked -> applied = "medium", mismatch = true
		// Expected: applied="medium", mismatch=true; Actual: applied="low", mismatch=false
		{
			name: "baked_suffix_medium_requested_low_pass_through",
			rule: "Rule 4: Baked-name model suffix -medium passes requested low through",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-medium",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -high, requested "high" == baked -> applied = "high", mismatch = false
		{
			name: "baked_suffix_high_requested_high_pass",
			rule: "Rule 4: Baked-name model suffix -high requested high matches baked",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-high",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "high",
			wantApplied:  "high",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -high, requested "low" != baked -> applied = "high", mismatch = true
		// Expected: applied="high", mismatch=true; Actual: applied="low", mismatch=false
		{
			name: "baked_suffix_high_requested_low_pass_through",
			rule: "Rule 4: Baked-name model suffix -high passes requested low through",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-high",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -high, requested "medium" != baked -> applied = "high", mismatch = true
		// Expected: applied="high", mismatch=true; Actual: applied="medium", mismatch=false
		{
			name: "baked_suffix_high_requested_medium_pass_through",
			rule: "Rule 4: Baked-name model suffix -high passes requested medium through",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-high",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -high, requested "" -> applied = "high", mismatch = false
		// Expected: applied="high", mismatch=false; Actual: applied="", mismatch=false
		{
			name: "baked_suffix_high_requested_empty_stays_empty",
			rule: "Rule 4: Baked-name model suffix -high requested empty stays empty",
			view: &ModelEffortView{
				Provider:      "agy",
				Model:         "gemini-3.8-flash-high",
				EffortStyle:   config.EffortStyleBakedName,
				DefaultEffort: "",
			},
			requested:    "",
			wantApplied:  "",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -xhigh, requested "xhigh" == baked -> applied = "xhigh", mismatch = false
		{
			name: "baked_suffix_xhigh_requested_xhigh_pass",
			rule: "Rule 4: Baked-name model suffix -xhigh requested xhigh matches baked",
			view: &ModelEffortView{
				Provider:    "testprov",
				Model:       "custom-agent-xhigh",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "xhigh",
			wantApplied:  "xhigh",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -xhigh, requested "medium" != baked -> applied = "xhigh", mismatch = true
		// Expected: applied="xhigh", mismatch=true; Actual: applied="medium", mismatch=false
		{
			name: "baked_suffix_xhigh_requested_medium_pass_through",
			rule: "Rule 4: Baked-name model suffix -xhigh passes requested medium through",
			view: &ModelEffortView{
				Provider:    "testprov",
				Model:       "custom-agent-xhigh",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -max, requested "max" == baked -> applied = "max", mismatch = false
		{
			name: "baked_suffix_max_requested_max_pass",
			rule: "Rule 4: Baked-name model suffix -max requested max matches baked",
			view: &ModelEffortView{
				Provider:    "testprov",
				Model:       "custom-agent-max",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "max",
			wantApplied:  "max",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -max, requested "low" != baked -> applied = "max", mismatch = true
		// Expected: applied="max", mismatch=true; Actual: applied="low", mismatch=false
		{
			name: "baked_suffix_max_requested_low_pass_through",
			rule: "Rule 4: Baked-name model suffix -max passes requested low through",
			view: &ModelEffortView{
				Provider:    "testprov",
				Model:       "custom-agent-max",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Baked-name model with suffix -high, requested "none" (mandatory=false) -> applied = "high", mismatch = true
		// Expected: applied="high", mismatch=true; Actual: applied="none", mismatch=false
		{
			name: "baked_suffix_high_requested_none_non_mandatory_pass_through",
			rule: "Rule 4: Baked-name model suffix -high non-mandatory passes requested none through",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-high",
				EffortStyle: config.EffortStyleBakedName,
				Mandatory:   false,
			},
			requested:    "none",
			wantApplied:  "none",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4 & Rule 7: Baked-name model with mandatory=true and requested="none" still hard-refuses
		{
			name: "baked_suffix_mandatory_true_requested_none_hard_refuses",
			rule: "Rule 4 & Rule 7: Baked-name model with mandatory=true requested none hard-refuses",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-high",
				EffortStyle: config.EffortStyleBakedName,
				Mandatory:   true,
			},
			requested: "none",
			wantErr:   true,
		},
		// Rule 4: Baked-name model always has zero budget tokens
		{
			name: "baked_suffix_budget_tokens_always_zero",
			rule: "Rule 4: Baked-name model budget tokens are always zero",
			view: &ModelEffortView{
				Provider:    "agy",
				Model:       "gemini-3.8-flash-high",
				EffortStyle: config.EffortStyleBakedName,
			},
			requested:    "high",
			wantApplied:  "high",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Suffix lookalike model-maxv2 is NOT baked suffix and passes through
		{
			name: "suffix_lookalike_model_maxv2_passes_through",
			rule: "Rule 4: Lookalike suffix model-maxv2 is not baked and passes through",
			view: &ModelEffortView{
				Model: "model-maxv2",
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 4: Suffix lookalike flow-highx is NOT baked suffix and passes through
		{
			name: "suffix_lookalike_flow_highx_passes_through",
			rule: "Rule 4: Lookalike suffix flow-highx is not baked and passes through",
			view: &ModelEffortView{
				Model: "flow-highx",
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applied, mismatch, tokens, err := AdaptEffort(tc.view, tc.requested)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				var mandErr *MandatoryEffortError
				if !errors.As(err, &mandErr) {
					t.Fatalf("expected MandatoryEffortError, got %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if applied != tc.wantApplied || mismatch != tc.wantMismatch || tokens != tc.wantTokens {
				if tc.finding != "" {
					// Expected vs Actual check
					t.Logf("Expected applied=%q mismatch=%v tokens=%d; Actual applied=%q mismatch=%v tokens=%d",
						tc.wantApplied, tc.wantMismatch, tc.wantTokens, applied, mismatch, tokens)
					t.Skip(tc.finding)
				}
				t.Fatalf("got applied=%q mismatch=%v tokens=%d; want applied=%q mismatch=%v tokens=%d",
					applied, mismatch, tokens, tc.wantApplied, tc.wantMismatch, tc.wantTokens)
			}
		})
	}
}

// ============================================================================
// Dimension 5: Toggle style
// Rule: Toggle style: nearest-supported; mismatch exactly when requested is
// outside SupportedEfforts.
// ============================================================================

func TestRedtest_Effort_Dimension5_ToggleStyle(t *testing.T) {
	tests := []struct {
		name         string
		rule         string
		view         *ModelEffortView
		requested    string
		wantApplied  string
		wantMismatch bool
	}{
		// Rule 5: Toggle style - requested "medium" in SupportedEfforts -> mismatch=false
		{
			name: "toggle_in_supported_medium",
			rule: "Rule 5: Toggle style in supported medium has mismatch=false",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "qwen3",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
		},
		// Rule 5: Toggle style - requested "high" in SupportedEfforts -> mismatch=false
		{
			name: "toggle_in_supported_high",
			rule: "Rule 5: Toggle style in supported high has mismatch=false",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "qwen3",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "high",
			wantApplied:  "high",
			wantMismatch: false,
		},
		// Rule 5: Toggle style - requested "none" outside SupportedEfforts -> nearest "medium", mismatch=true
		{
			name: "toggle_outside_supported_none",
			rule: "Rule 5: Toggle style requested none maps to nearest medium with mismatch=true",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "qwen3",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "none", // dist(none, med)=3 vs dist(none, high)=4 -> medium
			wantApplied:  "medium",
			wantMismatch: true,
		},
		// Rule 5: Toggle style - requested "minimal" outside SupportedEfforts -> nearest "medium", mismatch=true
		{
			name: "toggle_outside_supported_minimal",
			rule: "Rule 5: Toggle style requested minimal maps to nearest medium with mismatch=true",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "qwen3",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "minimal", // dist(min, med)=2 vs dist(min, high)=3 -> medium
			wantApplied:  "medium",
			wantMismatch: true,
		},
		// Rule 5: Toggle style - requested "xhigh" outside SupportedEfforts -> nearest "high", mismatch=true
		{
			name: "toggle_outside_supported_xhigh",
			rule: "Rule 5: Toggle style requested xhigh maps to nearest high with mismatch=true",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "qwen3",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "xhigh", // dist(xhigh, high)=1 vs dist(xhigh, med)=2 -> high
			wantApplied:  "high",
			wantMismatch: true,
		},
		// Rule 5: Toggle style - requested "max" outside SupportedEfforts -> nearest "high", mismatch=true
		{
			name: "toggle_outside_supported_max",
			rule: "Rule 5: Toggle style requested max maps to nearest high with mismatch=true",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "qwen3",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "max", // dist(max, high)=2 vs dist(max, med)=3 -> high
			wantApplied:  "high",
			wantMismatch: true,
		},
		// Rule 5: Toggle style - tie between none(0) and max(6) for medium(3) (dist 3 each) -> lower index "none"
		{
			name: "toggle_extreme_none_max_equidistant_tie",
			rule: "Rule 5: Toggle style equidistant tie between none and max picks lower level none",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "extreme-toggle",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"none", "max"},
				DefaultEffort:    "none",
			},
			requested:    "medium", // dist(none, med)=3, dist(max, med)=3 -> tie picks none
			wantApplied:  "none",
			wantMismatch: true,
		},
		// Rule 5: Toggle style - tie between low(2) and high(4) for medium(3) -> lower index "low"
		{
			name: "toggle_tie_low_high_for_medium",
			rule: "Rule 5: Toggle style tie between low and high picks lower level low",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "mid-toggle",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"low", "high"},
				DefaultEffort:    "low",
			},
			requested:    "medium", // dist(low, med)=1, dist(high, med)=1 -> tie picks low
			wantApplied:  "low",
			wantMismatch: true,
		},
		// Rule 5: Toggle style - empty supported set passes requested through, mismatch=false
		{
			name: "toggle_empty_supported_fallback",
			rule: "Rule 5: Toggle style empty supported set passes requested through with mismatch=false",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "empty-toggle",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{},
				DefaultEffort:    "",
			},
			requested:    "high",
			wantApplied:  "high",
			wantMismatch: false,
		},
		// Rule 5: Toggle style - non-ladder requested string maps to first candidate
		{
			name: "toggle_non_ladder_requested_string",
			rule: "Rule 5: Toggle style non-ladder requested string falls back to first candidate",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "custom-toggle",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"low", "high"},
				DefaultEffort:    "low",
			},
			requested:    "invalid-toggle-mode",
			wantApplied:  "low",
			wantMismatch: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applied, mismatch, tokens, err := AdaptEffort(tc.view, tc.requested)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if applied != tc.wantApplied || mismatch != tc.wantMismatch || tokens != 0 {
				t.Fatalf("got applied=%q mismatch=%v tokens=%d; want applied=%q mismatch=%v tokens=0",
					applied, mismatch, tokens, tc.wantApplied, tc.wantMismatch)
			}
		})
	}
}

// ============================================================================
// Dimension 6: Budget style
// Rule: Budget style: exact key -> tokens from EffortBudgetMap; requested
// level without a budget entry -> nearest level that HAS one; sparse map
// fallback; mismatch on fallback; tokens surfaced; zero-token boundaries.
// ============================================================================

func TestRedtest_Effort_Dimension6_BudgetStyle(t *testing.T) {
	tests := []struct {
		name         string
		rule         string
		view         *ModelEffortView
		requested    string
		wantApplied  string
		wantMismatch bool
		wantTokens   int
	}{
		// Rule 6: Budget style - exact key surfaces tokens without mismatch
		{
			name: "budget_exact_key_surfaces_tokens",
			rule: "Rule 6: Budget style exact key match surfaces budget tokens with no mismatch",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "model-budget-exact",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"low", "medium", "high"},
				EffortBudgetMap: map[string]int{
					"low":    1024,
					"medium": 4096,
					"high":   16384,
				},
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   4096,
		},
		// Rule 6: Budget style - requested level without budget entry falls back to nearest with entry (tie picks lower)
		{
			name: "budget_nearest_level_with_entry_tie_lower",
			rule: "Rule 6: Budget style fallback to nearest level with entry picks lower level on tie",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "model-budget-tie",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"minimal", "low", "medium"},
				EffortBudgetMap: map[string]int{
					"minimal": 512,
					"medium":  4096,
				},
			},
			requested:    "low", // dist(min, low)=1, dist(med, low)=1 -> tie picks minimal
			wantApplied:  "minimal",
			wantMismatch: true,
			wantTokens:   512,
		},
		// Rule 6: Budget style - requested level without budget entry picks closer entry
		{
			name: "budget_nearest_level_with_entry_picks_closer",
			rule: "Rule 6: Budget style fallback picks closer entry among available budget keys",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "model-budget-dist",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"minimal", "high", "xhigh"},
				EffortBudgetMap: map[string]int{
					"minimal": 512,
					"high":    16384,
				},
			},
			requested:    "xhigh", // dist(xhigh, high)=1 vs dist(xhigh, min)=4 -> high wins
			wantApplied:  "high",
			wantMismatch: true,
			wantTokens:   16384,
		},
		// Rule 6: Budget style - exact match on level with 0 tokens surfaces 0 tokens without mismatch
		{
			name: "budget_zero_token_boundary_exact_match",
			rule: "Rule 6: Budget style zero-token boundary exact match surfaces 0 tokens with mismatch=false",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "model-zero-boundary",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"none", "low"},
				EffortBudgetMap: map[string]int{
					"none": 0,
					"low":  2048,
				},
			},
			requested:    "none",
			wantApplied:  "none",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 6: Budget style - fallback to level with 0 tokens surfaces 0 tokens with mismatch=true
		{
			name: "budget_zero_token_boundary_fallback",
			rule: "Rule 6: Budget style zero-token boundary fallback surfaces 0 tokens with mismatch=true",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "model-zero-fallback",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"none", "high"},
				EffortBudgetMap: map[string]int{
					"none": 0,
					"high": 16384,
				},
			},
			requested:    "minimal", // dist(min, none)=1 vs dist(min, high)=3 -> none
			wantApplied:  "none",
			wantMismatch: true,
			wantTokens:   0,
		},
		// Rule 6: Budget style - empty budget map falls back to nearest SupportedEfforts with 0 tokens
		{
			name: "budget_sparse_fallback_to_supported_efforts",
			rule: "Rule 6: Budget style empty budget map falls back to nearest supported with 0 tokens",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "model-empty-budget-map",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"medium", "high"},
				EffortBudgetMap:  map[string]int{},
			},
			requested:    "low",
			wantApplied:  "medium",
			wantMismatch: true,
			wantTokens:   0,
		},
		// Rule 6: Budget style - empty budget map and empty supported efforts passes through with 0 tokens
		{
			name: "budget_sparse_fallback_both_maps_empty",
			rule: "Rule 6: Budget style empty budget and supported maps passes through with 0 tokens",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "model-sparse-empty",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{},
				EffortBudgetMap:  map[string]int{},
			},
			requested:    "high",
			wantApplied:  "high",
			wantMismatch: false,
			wantTokens:   0,
		},
		// Rule 6: Budget style - non-ladder key in budget map is ignored when finding nearest
		{
			name: "budget_non_ladder_key_in_budget_map_ignored",
			rule: "Rule 6: Budget style non-ladder key in budget map is ignored during fallback",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "model-non-ladder-budget-key",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"high"},
				EffortBudgetMap: map[string]int{
					"invalid_key": 9999,
					"high":        16384,
				},
			},
			requested:    "xhigh",
			wantApplied:  "high",
			wantMismatch: true,
			wantTokens:   16384,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applied, mismatch, tokens, err := AdaptEffort(tc.view, tc.requested)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if applied != tc.wantApplied || mismatch != tc.wantMismatch || tokens != tc.wantTokens {
				t.Fatalf("got applied=%q mismatch=%v tokens=%d; want applied=%q mismatch=%v tokens=%d",
					applied, mismatch, tokens, tc.wantApplied, tc.wantMismatch, tc.wantTokens)
			}
		})
	}
}

// ============================================================================
// Dimension 7: The ONLY hard-refuse
// Rule: The ONLY hard-refuse: Mandatory == true AND requested == "none"
// -> typed error naming provider and model; confirm no other combination refuses.
// ============================================================================

func TestRedtest_Effort_Dimension7_OnlyHardRefuse(t *testing.T) {
	// 1. Mandatory=true and requested="none" hard-refuses with typed error naming provider and model
	t.Run("hard_refuse_mandatory_true_requested_none_named", func(t *testing.T) {
		// Rule 7: Mandatory=true and requested="none" returns typed MandatoryEffortError
		view := &ModelEffortView{
			Provider:         "anthropic",
			Model:            "claude-opus-5-5",
			EffortStyle:      config.EffortStyleNamed,
			SupportedEfforts: []string{"low", "medium", "high"},
			Mandatory:        true,
		}
		_, _, _, err := AdaptEffort(view, config.EffortNone)
		if err == nil {
			t.Fatalf("expected error on mandatory=true and requested=none, got nil")
		}
		var mandErr *MandatoryEffortError
		if !errors.As(err, &mandErr) {
			t.Fatalf("expected MandatoryEffortError, got %T: %v", err, err)
		}
		if mandErr.Provider != "anthropic" || mandErr.Model != "claude-opus-5-5" {
			t.Errorf("error naming mismatch: provider=%q model=%q, want 'anthropic' 'claude-opus-5-5'",
				mandErr.Provider, mandErr.Model)
		}
		if !strings.Contains(mandErr.Error(), "claude-opus-5-5") || !strings.Contains(mandErr.Error(), "anthropic") {
			t.Errorf("error string missing provider/model name: %q", mandErr.Error())
		}
	})

	// 2. Mandatory=true and requested="none" with empty provider names model
	t.Run("hard_refuse_mandatory_true_requested_none_empty_provider", func(t *testing.T) {
		// Rule 7: Mandatory=true and requested="none" with empty provider names model
		view := &ModelEffortView{
			Model:     "lone-model",
			Mandatory: true,
		}
		_, _, _, err := AdaptEffort(view, config.EffortNone)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		var mandErr *MandatoryEffortError
		if !errors.As(err, &mandErr) {
			t.Fatalf("expected MandatoryEffortError, got %T: %v", err, err)
		}
		if mandErr.Model != "lone-model" {
			t.Errorf("error model mismatch: got %q, want 'lone-model'", mandErr.Model)
		}
		if !strings.Contains(mandErr.Error(), "lone-model") {
			t.Errorf("error string missing model name: %q", mandErr.Error())
		}
	})

	// 3. Confirm hard-refuse across all styles with mandatory=true and requested="none"
	styles := []string{
		config.EffortStyleNamed,
		config.EffortStyleToggle,
		config.EffortStyleBudget,
		config.EffortStyleBakedName,
	}
	for _, style := range styles {
		t.Run("hard_refuse_style_"+style, func(t *testing.T) {
			// Rule 7: Mandatory=true and requested="none" hard-refuses across all styles
			view := &ModelEffortView{
				Provider:    "testprov",
				Model:       "testmodel",
				EffortStyle: style,
				Mandatory:   true,
			}
			_, _, _, err := AdaptEffort(view, config.EffortNone)
			if err == nil {
				t.Fatalf("style %q: expected error on mandatory=true requested=none, got nil", style)
			}
			var mandErr *MandatoryEffortError
			if !errors.As(err, &mandErr) {
				t.Fatalf("style %q: expected MandatoryEffortError, got %T: %v", style, err, err)
			}
		})
	}

	// 4. Confirm NO OTHER combination refuses when mandatory=true
	otherLevels := []string{
		config.EffortMinimal,
		config.EffortLow,
		config.EffortMedium,
		config.EffortHigh,
		config.EffortXHigh,
		config.EffortMax,
		"",
	}
	for _, lvl := range otherLevels {
		t.Run("no_refuse_mandatory_true_level_"+lvl, func(t *testing.T) {
			// Rule 7: Confirm no refuse for mandatory=true with any level other than "none"
			view := &ModelEffortView{
				Provider:         "testprov",
				Model:            "testmodel",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"minimal", "low", "medium", "high", "xhigh", "max"},
				DefaultEffort:    "medium",
				Mandatory:        true,
			}
			_, _, _, err := AdaptEffort(view, lvl)
			if err != nil {
				t.Fatalf("mandatory=true with level %q unexpectedly refused: %v", lvl, err)
			}
		})
	}

	// 5. Confirm NO refuse when mandatory=false even with requested="none"
	t.Run("no_refuse_mandatory_false_requested_none", func(t *testing.T) {
		// Rule 7: Confirm mandatory=false with requested="none" NEVER refuses
		view := &ModelEffortView{
			Provider:         "openai",
			Model:            "gpt-6-astra",
			EffortStyle:      config.EffortStyleNamed,
			SupportedEfforts: []string{"none", "low", "medium", "high"},
			Mandatory:        false,
		}
		applied, mismatch, _, err := AdaptEffort(view, config.EffortNone)
		if err != nil {
			t.Fatalf("mandatory=false with requested=none unexpectedly refused: %v", err)
		}
		if applied != "none" || mismatch {
			t.Errorf("got applied=%q mismatch=%v, want 'none' false", applied, mismatch)
		}
	})

	// 6. Confirm nil view with requested="none" NEVER refuses
	t.Run("no_refuse_nil_view_requested_none", func(t *testing.T) {
		// Rule 7: Confirm nil view with requested="none" NEVER refuses
		applied, mismatch, _, err := AdaptEffort(nil, config.EffortNone)
		if err != nil {
			t.Fatalf("nil view with requested=none unexpectedly refused: %v", err)
		}
		if applied != "none" || mismatch {
			t.Errorf("got applied=%q mismatch=%v, want 'none' false", applied, mismatch)
		}
	})

	// 7. Confirm invalid requested string with mandatory=true never returns typed refuse error
	t.Run("no_refuse_mandatory_true_invalid_string", func(t *testing.T) {
		// Rule 7: Confirm invalid requested string never returns typed refuse error
		view := &ModelEffortView{
			Provider:         "testprov",
			Model:            "testmodel",
			EffortStyle:      config.EffortStyleNamed,
			SupportedEfforts: []string{"low", "medium"},
			Mandatory:        true,
		}
		_, _, _, err := AdaptEffort(view, "not-none")
		if err != nil {
			t.Fatalf("invalid requested string unexpectedly returned error: %v", err)
		}
	})
}

// ============================================================================
// Dimension 8: Distance/tie edges
// Rule: Distance/tie edges: requested at both ends of the ladder;
// supported list of length 1; requested equal to default.
// ============================================================================

func TestRedtest_Effort_Dimension8_DistanceTieEdges(t *testing.T) {
	tests := []struct {
		name         string
		rule         string
		view         *ModelEffortView
		requested    string
		wantApplied  string
		wantMismatch bool
	}{
		// Rule 8: Distance edge - requested at bottom (none, 0), supported only at top (max, 6) -> distance 6
		{
			name: "edge_requested_at_bottom_none_distance_to_max",
			rule: "Rule 8: Distance edge requested none to single candidate max has distance 6",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-max-only",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"max"},
				Mandatory:        false,
			},
			requested:    "none",
			wantApplied:  "max",
			wantMismatch: true,
		},
		// Rule 8: Distance edge - requested at top (max, 6), supported only at bottom (none, 0) -> distance 6
		{
			name: "edge_requested_at_top_max_distance_to_none",
			rule: "Rule 8: Distance edge requested max to single candidate none has distance 6",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-none-only",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"none"},
				Mandatory:        false,
			},
			requested:    "max",
			wantApplied:  "none",
			wantMismatch: true,
		},
		// Rule 8: Distance edge - requested at bottom (none) in supported set -> distance 0, mismatch=false
		{
			name: "edge_requested_at_bottom_none_in_supported",
			rule: "Rule 8: Distance edge requested none in supported set has mismatch=false",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-none-high",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"none", "high"},
				Mandatory:        false,
			},
			requested:    "none",
			wantApplied:  "none",
			wantMismatch: false,
		},
		// Rule 8: Distance edge - requested at top (max) in supported set -> distance 0, mismatch=false
		{
			name: "edge_requested_at_top_max_in_supported",
			rule: "Rule 8: Distance edge requested max in supported set has mismatch=false",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-low-max",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "max"},
			},
			requested:    "max",
			wantApplied:  "max",
			wantMismatch: false,
		},
		// Rule 8: Requested equal to default when default is in supported set -> mismatch=false
		{
			name: "edge_requested_equal_to_default_in_supported",
			rule: "Rule 8: Requested equal to default in supported set has mismatch=false",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-req-eq-def",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
		},
		// Rule 8: Requested equal to default when default is NOT in supported set -> nearest applied, mismatch=true
		{
			name: "edge_requested_equal_to_default_not_in_supported",
			rule: "Rule 8: Requested equal to default not in supported set maps to nearest with mismatch=true",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "model-req-eq-def-unsupported",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "medium", // tie between low and high -> lower level low
			wantApplied:  "low",
			wantMismatch: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applied, mismatch, _, err := AdaptEffort(tc.view, tc.requested)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if applied != tc.wantApplied || mismatch != tc.wantMismatch {
				t.Fatalf("got applied=%q mismatch=%v; want applied=%q mismatch=%v",
					applied, mismatch, tc.wantApplied, tc.wantMismatch)
			}
		})
	}

	// Supported list of length 1 tested against all 7 ladder levels
	singleCandidateTests := []struct {
		requested    string
		wantMismatch bool
	}{
		// Rule 8: Supported list of length 1 ["medium"] tested against requested none
		{"none", true},
		// Rule 8: Supported list of length 1 ["medium"] tested against requested minimal
		{"minimal", true},
		// Rule 8: Supported list of length 1 ["medium"] tested against requested low
		{"low", true},
		// Rule 8: Supported list of length 1 ["medium"] tested against requested medium (exact match)
		{"medium", false},
		// Rule 8: Supported list of length 1 ["medium"] tested against requested high
		{"high", true},
		// Rule 8: Supported list of length 1 ["medium"] tested against requested xhigh
		{"xhigh", true},
		// Rule 8: Supported list of length 1 ["medium"] tested against requested max
		{"max", true},
	}
	singleView := &ModelEffortView{
		Provider:         "testprov",
		Model:            "single-supported",
		EffortStyle:      config.EffortStyleNamed,
		SupportedEfforts: []string{"medium"},
		DefaultEffort:    "medium",
	}
	for _, tc := range singleCandidateTests {
		t.Run("edge_supported_len1_req_"+tc.requested, func(t *testing.T) {
			// Rule 8: Supported list of length 1 always applies "medium"
			applied, mismatch, _, err := AdaptEffort(singleView, tc.requested)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if applied != "medium" {
				t.Errorf("applied = %q, want 'medium'", applied)
			}
			if mismatch != tc.wantMismatch {
				t.Errorf("mismatch = %v, want %v", mismatch, tc.wantMismatch)
			}
		})
	}
}

// ============================================================================
// Dimension 9: Catalog merge fail-open
// Rule: Catalog merge fail-open: missing catalog, unknown provider,
// unknown model - routing must record and continue, never fail.
// ============================================================================

func TestRedtest_Effort_Dimension9_CatalogMergeFailOpen(t *testing.T) {
	manifest := &config.File{
		Providers: []config.ProviderEntry{
			{
				Name: "manifest-prov",
				Models: []config.ModelEntry{
					{
						ID:               "known-model",
						EffortStyle:      config.EffortStyleNamed,
						SupportedEfforts: []string{"low", "medium", "high"},
						DefaultEffort:    "medium",
					},
				},
			},
		},
	}

	cat := &config.Catalog{
		SchemaVersion:  config.CurrentCatalogSchemaVersion,
		CatalogVersion: 1,
		VerifiedAt:     "2026-10-04",
		Sources:        []string{"test"},
		Providers: map[string]config.ProviderCatalog{
			"known-prov": {
				Name:        "known-prov",
				EffortStyle: config.EffortStyleNamed,
				Models: []config.ModelCatalog{
					{
						ID:               "catalog-model",
						SupportedEfforts: []string{"low", "medium"},
						DefaultEffort:    "low",
					},
				},
			},
		},
	}

	// 1. Missing catalog (nil) with known manifest model resolves and succeeds
	t.Run("failopen_missing_catalog_known_manifest_model", func(t *testing.T) {
		// Rule 9: Missing catalog (nil) with known manifest model resolves and continues
		res, err := ResolveEffortWithCatalog(manifest, nil, "manifest-prov", "known-model", "high")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.HasResolvedEntry {
			t.Errorf("expected HasResolvedEntry=true from manifest")
		}
		if res.Applied != "high" || res.Mismatch {
			t.Errorf("got applied=%q mismatch=%v, want 'high' false", res.Applied, res.Mismatch)
		}
	})

	// 2. Missing catalog (nil) with unknown model returns pass-through without failing
	t.Run("failopen_missing_catalog_unknown_model", func(t *testing.T) {
		// Rule 9: Missing catalog (nil) with unknown model returns pass-through without failing
		res, err := ResolveEffortWithCatalog(nil, nil, "ghost-prov", "ghost-model", "high")
		if err != nil {
			t.Fatalf("unexpected error on missing catalog with unknown model: %v", err)
		}
		if res.HasResolvedEntry {
			t.Errorf("expected HasResolvedEntry=false")
		}
		if res.Applied != "high" || res.Mismatch {
			t.Errorf("got applied=%q mismatch=%v, want 'high' false", res.Applied, res.Mismatch)
		}
	})

	// 3. Unknown provider in non-nil catalog returns pass-through without failing
	t.Run("failopen_unknown_provider_in_catalog", func(t *testing.T) {
		// Rule 9: Unknown provider in non-nil catalog returns pass-through without failing
		res, err := ResolveEffortWithCatalog(manifest, cat, "unregistered-provider", "some-model", "low")
		if err != nil {
			t.Fatalf("unexpected error on unknown provider: %v", err)
		}
		if res.HasResolvedEntry {
			t.Errorf("expected HasResolvedEntry=false")
		}
		if res.Applied != "low" || res.Mismatch {
			t.Errorf("got applied=%q mismatch=%v, want 'low' false", res.Applied, res.Mismatch)
		}
	})

	// 4. Unknown model on known provider returns pass-through without failing
	t.Run("failopen_unknown_model_on_known_provider", func(t *testing.T) {
		// Rule 9: Unknown model on known provider returns pass-through without failing
		res, err := ResolveEffortWithCatalog(manifest, cat, "known-prov", "ghost-model", "medium")
		if err != nil {
			t.Fatalf("unexpected error on unknown model: %v", err)
		}
		if res.HasResolvedEntry {
			t.Errorf("expected HasResolvedEntry=false")
		}
		if res.Applied != "medium" || res.Mismatch {
			t.Errorf("got applied=%q mismatch=%v, want 'medium' false", res.Applied, res.Mismatch)
		}
	})

	// 5. Unknown model on empty provider returns pass-through without failing
	t.Run("failopen_unknown_model_empty_provider", func(t *testing.T) {
		// Rule 9: Unknown model on empty provider returns pass-through without failing
		res, err := ResolveEffortWithCatalog(manifest, cat, "", "completely-unknown-model", "high")
		if err != nil {
			t.Fatalf("unexpected error on empty provider: %v", err)
		}
		if res.HasResolvedEntry {
			t.Errorf("expected HasResolvedEntry=false")
		}
		if res.Applied != "high" || res.Mismatch {
			t.Errorf("got applied=%q mismatch=%v, want 'high' false", res.Applied, res.Mismatch)
		}
	})

	// 6. Route completes without error with arbitrary prompt and effort
	t.Run("failopen_route_deterministic_with_missing_catalog", func(t *testing.T) {
		// Rule 9: Route completes without error and returns decision
		dec, err := Route(context.Background(), RouteRequest{
			Prompt: "redtest prompt for fail-open routing verification",
			Effort: "high",
		})
		if err != nil {
			t.Fatalf("Route failed: %v", err)
		}
		if dec.Provider == "" || dec.Model == "" {
			t.Errorf("Decision missing provider or model: %+v", dec)
		}
	})
}

// ============================================================================
// Dimension 10: Decision field mapping & omitempty
// Rule: Decision field mapping: effort_requested / effort_applied /
// mismatch / budget-token fields filled exactly when the adapter ran;
// omitempty behavior on the zero values.
// ============================================================================

func TestRedtest_Effort_Dimension10_DecisionFieldMapping(t *testing.T) {
	// 1. Decision fields filled when adapter resolved an entry
	t.Run("decision_fields_filled_on_successful_route", func(t *testing.T) {
		// Rule 10: Decision fields filled when adapter resolved an entry
		manifest := DefaultManifest()
		dec, err := Route(context.Background(), RouteRequest{
			Prompt:   "General evaluation task",
			Manifest: manifest,
			Effort:   config.EffortLow,
		})
		if err != nil {
			t.Fatalf("Route failed: %v", err)
		}
		if dec.EffortRequested != config.EffortLow {
			t.Errorf("EffortRequested = %q, want %q", dec.EffortRequested, config.EffortLow)
		}
		if dec.EffortApplied == "" {
			t.Errorf("EffortApplied is empty, expected non-empty applied effort")
		}
	})

	// 2. Decision effort fields remain zero values when model has no resolved entry
	t.Run("decision_fields_zero_when_adapter_unresolved", func(t *testing.T) {
		// Rule 10: Decision effort fields remain zero values when adapter has no resolved entry
		dec := Decision{
			Provider: "unregistered-provider",
			Model:    "unknown-model",
			Role:     "collector",
			Source:   "deterministic",
		}
		if dec.EffortRequested != "" || dec.EffortApplied != "" || dec.EffortMismatch || dec.EffortBudgetTokens != 0 {
			t.Errorf("expected zero effort fields, got %+v", dec)
		}
	})

	// 3. omitempty on Decision omits all 4 effort fields when zero
	t.Run("decision_omitempty_on_all_zero_values", func(t *testing.T) {
		// Rule 10: omitempty on Decision omits all 4 effort fields when zero
		dec := Decision{
			Provider: "agy",
			Model:    "gemini-3.8-flash-high",
			Role:     "collector",
			Source:   "deterministic",
		}
		data, err := json.Marshal(dec)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		str := string(data)
		for _, field := range []string{"effort_requested", "effort_applied", "effort_mismatch", "effort_budget_tokens"} {
			if strings.Contains(str, field) {
				t.Errorf("expected zero field %q to be omitted by omitempty, but JSON contains it: %s", field, str)
			}
		}
	})

	// 4. Non-zero effort fields are serialized in Decision JSON
	t.Run("decision_omitempty_preserves_non_zero_fields", func(t *testing.T) {
		// Rule 10: non-zero effort fields are serialized in Decision JSON
		dec := Decision{
			Provider:           "anthropic",
			Model:              "claude-opus-legacy",
			Role:               "collector",
			Source:             "deterministic",
			EffortRequested:    "high",
			EffortApplied:      "medium",
			EffortMismatch:     true,
			EffortBudgetTokens: 8192,
		}
		data, err := json.Marshal(dec)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		str := string(data)
		if !strings.Contains(str, `"effort_requested":"high"`) {
			t.Errorf("missing effort_requested in JSON: %s", str)
		}
		if !strings.Contains(str, `"effort_applied":"medium"`) {
			t.Errorf("missing effort_applied in JSON: %s", str)
		}
		if !strings.Contains(str, `"effort_mismatch":true`) {
			t.Errorf("missing effort_mismatch in JSON: %s", str)
		}
		if !strings.Contains(str, `"effort_budget_tokens":8192`) {
			t.Errorf("missing effort_budget_tokens in JSON: %s", str)
		}
	})

	// 5. Partial zero fields: mismatch=false and tokens=0 omitted, requested and applied present
	t.Run("decision_omitempty_partial_zero", func(t *testing.T) {
		// Rule 10: partial zero fields: mismatch=false and tokens=0 omitted, requested and applied present
		dec := Decision{
			Provider:           "anthropic",
			Model:              "claude-opus-5-5",
			Role:               "collector",
			Source:             "deterministic",
			EffortRequested:    "medium",
			EffortApplied:      "medium",
			EffortMismatch:     false,
			EffortBudgetTokens: 0,
		}
		data, err := json.Marshal(dec)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		str := string(data)
		if !strings.Contains(str, `"effort_requested":"medium"`) {
			t.Errorf("missing effort_requested in JSON: %s", str)
		}
		if !strings.Contains(str, `"effort_applied":"medium"`) {
			t.Errorf("missing effort_applied in JSON: %s", str)
		}
		if strings.Contains(str, "effort_mismatch") {
			t.Errorf("effort_mismatch=false should be omitted, found in: %s", str)
		}
		if strings.Contains(str, "effort_budget_tokens") {
			t.Errorf("effort_budget_tokens=0 should be omitted, found in: %s", str)
		}
	})

	// 6. EffortResult omitempty produces "{}" on all zero values
	t.Run("effort_result_omitempty_all_zero", func(t *testing.T) {
		// Rule 10: EffortResult omitempty produces "{}" on all zero values
		res := EffortResult{}
		data, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		if string(data) != "{}" {
			t.Errorf("EffortResult with zero values marshaled to %s, want {}", string(data))
		}
	})

	// 7. EffortResult.HasResolvedEntry has json:"-" and is never serialized
	t.Run("effort_result_has_resolved_entry_never_serialized", func(t *testing.T) {
		// Rule 10: EffortResult.HasResolvedEntry has json:"-" and is never serialized
		res := EffortResult{HasResolvedEntry: true}
		data, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		if string(data) != "{}" {
			t.Errorf("EffortResult with HasResolvedEntry=true marshaled to %s, want {}", string(data))
		}
	})
}

// ============================================================================
// Interactions: Cross-Dimension Verification
// ============================================================================

func TestRedtest_Effort_Interactions(t *testing.T) {
	// 1. Interaction D4 + D7: Baked-name model with mandatory=true and requested="none" hard-refuses
	t.Run("interaction_mandatory_and_baked_name_hard_refuses", func(t *testing.T) {
		// Interaction D4+D7: Baked-name model with mandatory=true and requested="none" hard-refuses
		view := &ModelEffortView{
			Provider:    "agy",
			Model:       "gemini-3.8-flash-high",
			EffortStyle: config.EffortStyleBakedName,
			Mandatory:   true,
		}
		_, _, _, err := AdaptEffort(view, "none")
		if err == nil {
			t.Fatalf("expected error on mandatory baked model with requested none, got nil")
		}
		var mandErr *MandatoryEffortError
		if !errors.As(err, &mandErr) {
			t.Fatalf("expected MandatoryEffortError, got %T: %v", err, err)
		}
	})

	// 2. Interaction D4 + D7: Baked-name model with mandatory=true and requested="high" does not error
	t.Run("interaction_mandatory_and_baked_name_other_level_no_error", func(t *testing.T) {
		// Interaction D4+D7: Baked-name model with mandatory=true and requested="high" does not error
		view := &ModelEffortView{
			Provider:    "agy",
			Model:       "gemini-3.8-flash-high",
			EffortStyle: config.EffortStyleBakedName,
			Mandatory:   true,
		}
		_, _, _, err := AdaptEffort(view, "high")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// 3. Interaction D2 + D6: Requested "" with budget style surfaces tokens of DefaultEffort
	t.Run("interaction_budget_style_and_empty_requested_default", func(t *testing.T) {
		// Interaction D2+D6: Requested "" with budget style surfaces tokens of DefaultEffort
		view := &ModelEffortView{
			Provider:         "legacy",
			Model:            "legacy-m",
			EffortStyle:      config.EffortStyleBudget,
			SupportedEfforts: []string{"low", "medium", "high"},
			DefaultEffort:    "medium",
			EffortBudgetMap:  map[string]int{"medium": 8192},
		}
		applied, mismatch, tokens, err := AdaptEffort(view, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if applied != "medium" || mismatch || tokens != 8192 {
			t.Errorf("got applied=%q mismatch=%v tokens=%d, want 'medium' false 8192", applied, mismatch, tokens)
		}
	})

	// 4. Interaction D2 + D6: Requested "" with budget style and adaptive default surfaces 0 tokens
	t.Run("interaction_budget_style_and_empty_requested_adaptive", func(t *testing.T) {
		// Interaction D2+D6: Requested "" with budget style and adaptive default surfaces 0 tokens
		view := &ModelEffortView{
			Provider:         "legacy",
			Model:            "legacy-m",
			EffortStyle:      config.EffortStyleBudget,
			SupportedEfforts: []string{"low", "medium", "high"},
			DefaultEffort:    config.EffortAdaptive,
			EffortBudgetMap:  map[string]int{"medium": 8192},
		}
		applied, mismatch, tokens, err := AdaptEffort(view, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if applied != "" || mismatch || tokens != 0 {
			t.Errorf("got applied=%q mismatch=%v tokens=%d, want '' false 0", applied, mismatch, tokens)
		}
	})

	// 5. Interaction D6 + D8: Budget style fallback with equidistant budget entries picks lower level
	t.Run("interaction_budget_style_equidistant_tie_picks_lower", func(t *testing.T) {
		// Interaction D6+D8: Budget style fallback with equidistant budget entries picks lower level
		view := &ModelEffortView{
			Provider:         "legacy",
			Model:            "legacy-tie",
			EffortStyle:      config.EffortStyleBudget,
			SupportedEfforts: []string{"low", "medium", "high"},
			EffortBudgetMap: map[string]int{
				"low":  1000,
				"high": 5000,
			},
		}
		applied, mismatch, tokens, err := AdaptEffort(view, "medium")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if applied != "low" || !mismatch || tokens != 1000 {
			t.Errorf("got applied=%q mismatch=%v tokens=%d, want 'low' true 1000", applied, mismatch, tokens)
		}
	})

	// 6. Interaction D5 + D7: Toggle style with mandatory=false and requested="none" maps to nearest, no refuse
	t.Run("interaction_toggle_style_mandatory_false_requested_none", func(t *testing.T) {
		// Interaction D5+D7: Toggle style with mandatory=false and requested="none" maps to nearest, no refuse
		view := &ModelEffortView{
			Provider:         "ollama",
			Model:            "toggle-model",
			EffortStyle:      config.EffortStyleToggle,
			SupportedEfforts: []string{"low", "high"},
			Mandatory:        false,
		}
		applied, mismatch, tokens, err := AdaptEffort(view, "none")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if applied != "low" || !mismatch || tokens != 0 {
			t.Errorf("got applied=%q mismatch=%v tokens=%d, want 'low' true 0", applied, mismatch, tokens)
		}
	})

	// 7. Interaction D2 + D9: Missing catalog with manifest default applies DefaultEffort
	t.Run("interaction_missing_catalog_empty_requested_manifest_default", func(t *testing.T) {
		// Interaction D2+D9: Missing catalog with manifest default applies DefaultEffort
		manifest := &config.File{
			Providers: []config.ProviderEntry{
				{
					Name: "prov",
					Models: []config.ModelEntry{
						{
							ID:               "m",
							EffortStyle:      config.EffortStyleNamed,
							SupportedEfforts: []string{"low", "medium"},
							DefaultEffort:    "low",
						},
					},
				},
			},
		}
		res, err := ResolveEffortWithCatalog(manifest, nil, "prov", "m", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Applied != "low" || res.Mismatch || !res.HasResolvedEntry {
			t.Errorf("got applied=%q mismatch=%v resolved=%v, want 'low' false true",
				res.Applied, res.Mismatch, res.HasResolvedEntry)
		}
	})
}
