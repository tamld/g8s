package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

func TestAdaptEffort_Table(t *testing.T) {
	tests := []struct {
		name         string
		view         *ModelEffortView
		requested    string
		wantApplied  string
		wantMismatch bool
		wantTokens   int
		wantErr      bool
	}{
		// 1. named pass-through
		{
			name: "named pass-through when requested in supported_efforts",
			view: &ModelEffortView{
				Provider:         "anthropic",
				Model:            "claude-opus-5-5",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
				DefaultEffort:    "medium",
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		// 2. named nearest-down
		{
			name: "named nearest-down when requested higher than supported set",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-low-only",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"none", "low"},
				DefaultEffort:    "low",
			},
			requested:    "medium", // distance to low (2) is 1; distance to none (0) is 3
			wantApplied:  "low",
			wantMismatch: true,
			wantTokens:   0,
			wantErr:      false,
		},
		// 3. named nearest-up
		{
			name: "named nearest-up when requested lower than supported set",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-high-only",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"high", "xhigh", "max"},
				DefaultEffort:    "high",
			},
			requested:    "medium", // distance to high (4) is 1
			wantApplied:  "high",
			wantMismatch: true,
			wantTokens:   0,
			wantErr:      false,
		},
		// 4. named tie-picks-lower
		{
			name: "named tie-picks-lower when distances are equal",
			view: &ModelEffortView{
				Provider:         "testprov",
				Model:            "test-tie",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "high"}, // distance from medium (3) to low (2) is 1, to high (4) is 1
				DefaultEffort:    "low",
			},
			requested:    "medium",
			wantApplied:  "low", // lower index on EffortLadder wins tie
			wantMismatch: true,
			wantTokens:   0,
			wantErr:      false,
		},
		// 5. toggle nearest
		{
			name: "toggle nearest when requested outside supported",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "qwen3",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "low", // nearest is medium
			wantApplied:  "medium",
			wantMismatch: true,
			wantTokens:   0,
			wantErr:      false,
		},
		{
			name: "toggle pass-through when requested in supported",
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
			wantTokens:   0,
			wantErr:      false,
		},
		// 6. baked pass-through
		{
			name: "baked-name always passes through requested level with no mismatch",
			view: &ModelEffortView{
				Provider:         "agy",
				Model:            "gemini-3.8-flash-{effort}",
				EffortStyle:      config.EffortStyleBakedName,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "high",
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		{
			name: "baked-name passes through even levels outside supported with no mismatch",
			view: &ModelEffortView{
				Provider:         "agy",
				Model:            "gemini-3.8-flash-{effort}",
				EffortStyle:      config.EffortStyleBakedName,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "high",
			},
			requested:    "xhigh",
			wantApplied:  "xhigh",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		// 7. budget exact
		{
			name: "budget exact match returns budget tokens and no mismatch",
			view: &ModelEffortView{
				Provider:         "legacy-budget-prov",
				Model:            "claude-opus-legacy",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "medium",
				EffortBudgetMap: map[string]int{
					"low":    2048,
					"medium": 8192,
					"high":   32768,
				},
			},
			requested:    "medium",
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   8192,
			wantErr:      false,
		},
		// 8. budget nearest-with-entry
		{
			name: "budget nearest-with-entry falls back to nearest level in budget map (tie picks lower)",
			view: &ModelEffortView{
				Provider:         "legacy-budget-prov",
				Model:            "claude-opus-sparse-budget",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "medium",
				EffortBudgetMap: map[string]int{
					"low":  2048,
					"high": 32768,
				}, // missing medium
			},
			requested:    "medium", // distance to low (2) is 1, to high (4) is 1 -> tie picks low
			wantApplied:  "low",
			wantMismatch: true,
			wantTokens:   2048,
			wantErr:      false,
		},
		// 9. budget sparse fallback
		{
			name: "budget sparse fallback when budget map empty falls back to nearest-supported with 0 tokens",
			view: &ModelEffortView{
				Provider:         "legacy-budget-prov",
				Model:            "empty-budget-map",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"high"},
				DefaultEffort:    "high",
				EffortBudgetMap:  map[string]int{},
			},
			requested:    "medium",
			wantApplied:  "high",
			wantMismatch: true,
			wantTokens:   0,
			wantErr:      false,
		},
		// 10. requested "" -> default / adaptive / dynamic / "" cases
		{
			name: "requested empty string uses ladder level default_effort",
			view: &ModelEffortView{
				Provider:         "anthropic",
				Model:            "claude-opus-5-5",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "medium",
			},
			requested:    "",
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		{
			name: "requested empty string with budget style loads budget tokens of default",
			view: &ModelEffortView{
				Provider:         "legacy",
				Model:            "legacy-m",
				EffortStyle:      config.EffortStyleBudget,
				SupportedEfforts: []string{"low", "medium", "high"},
				DefaultEffort:    "medium",
				EffortBudgetMap:  map[string]int{"medium": 8192},
			},
			requested:    "",
			wantApplied:  "medium",
			wantMismatch: false,
			wantTokens:   8192,
			wantErr:      false,
		},
		{
			name: "requested empty string with adaptive default yields empty applied",
			view: &ModelEffortView{
				Provider:         "openai",
				Model:            "gpt-6-astra",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high", "xhigh", "max"},
				DefaultEffort:    "adaptive",
			},
			requested:    "",
			wantApplied:  "",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		{
			name: "requested empty string with dynamic default yields empty applied",
			view: &ModelEffortView{
				Provider:         "google",
				Model:            "gemini-3.1-pro-preview",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"minimal", "low", "medium", "high"},
				DefaultEffort:    "dynamic",
			},
			requested:    "",
			wantApplied:  "",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		{
			name: "requested empty string with empty default yields empty applied",
			view: &ModelEffortView{
				Provider:         "ollama",
				Model:            "generic",
				EffortStyle:      config.EffortStyleToggle,
				SupportedEfforts: []string{"medium"},
				DefaultEffort:    "",
			},
			requested:    "",
			wantApplied:  "",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		// 11. nil entry pass-through
		{
			name:         "nil entry pass-through with non-empty requested",
			view:         nil,
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		{
			name:         "nil entry pass-through with empty requested",
			view:         nil,
			requested:    "",
			wantApplied:  "",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
		},
		// 12. mandatory+none refuse
		{
			name: "mandatory=true and requested=none returns typed error",
			view: &ModelEffortView{
				Provider:         "xai",
				Model:            "grok-4.7",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high", "xhigh"},
				DefaultEffort:    "high",
				Mandatory:        true,
			},
			requested: "none",
			wantErr:   true,
		},
		{
			name: "mandatory=true and requested=low succeeds",
			view: &ModelEffortView{
				Provider:         "xai",
				Model:            "grok-4.7",
				EffortStyle:      config.EffortStyleNamed,
				SupportedEfforts: []string{"low", "medium", "high", "xhigh"},
				DefaultEffort:    "high",
				Mandatory:        true,
			},
			requested:    "low",
			wantApplied:  "low",
			wantMismatch: false,
			wantTokens:   0,
			wantErr:      false,
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
				if mandErr.Provider != tc.view.Provider || mandErr.Model != tc.view.Model {
					t.Errorf("error naming mismatch: provider=%q model=%q, want %q %q",
						mandErr.Provider, mandErr.Model, tc.view.Provider, tc.view.Model)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if applied != tc.wantApplied {
				t.Errorf("applied = %q, want %q", applied, tc.wantApplied)
			}
			if mismatch != tc.wantMismatch {
				t.Errorf("mismatch = %v, want %v", mismatch, tc.wantMismatch)
			}
			if tokens != tc.wantTokens {
				t.Errorf("budgetTokens = %d, want %d", tokens, tc.wantTokens)
			}
		})
	}
}

func TestResolveEffort_DirectCatalogLookup(t *testing.T) {
	cat := &config.Catalog{
		SchemaVersion:  config.CurrentCatalogSchemaVersion,
		CatalogVersion: 1,
		VerifiedAt:     "2026-10-04",
		Sources:        []string{"test"},
		Providers: map[string]config.ProviderCatalog{
			"xai": {
				Name:          "xai",
				EffortStyle:   config.EffortStyleNamed,
				DefaultEffort: "high",
				Mandatory:     true,
				Models: []config.ModelCatalog{
					{
						ID:               "grok-4.7",
						SupportedEfforts: []string{"low", "medium", "high", "xhigh"},
						DefaultEffort:    "high",
						Mandatory:        true,
					},
				},
			},
			"agy": {
				Name:          "agy",
				EffortStyle:   config.EffortStyleBakedName,
				DefaultEffort: "high",
				Models: []config.ModelCatalog{
					{
						ID:               "gemini-3.8-flash",
						SupportedEfforts: []string{"low", "medium", "high"},
						DefaultEffort:    "high",
					},
				},
			},
		},
	}

	// 1. Direct lookup with suffix match on baked-name model
	res, err := ResolveEffortWithCatalog(nil, cat, "agy", "gemini-3.8-flash-high", "low")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasResolvedEntry {
		t.Errorf("expected HasResolvedEntry to be true")
	}
	if res.Applied != "low" {
		t.Errorf("applied = %q, want low", res.Applied)
	}
	if res.Mismatch {
		t.Errorf("expected mismatch=false for baked-name")
	}

	// 2. Direct lookup of mandatory reasoning model requested none -> hard refuse
	_, err = ResolveEffortWithCatalog(nil, cat, "xai", "grok-4.7", "none")
	if err == nil {
		t.Fatalf("expected error on mandatory none, got nil")
	}
	var mandErr *MandatoryEffortError
	if !errors.As(err, &mandErr) {
		t.Fatalf("expected MandatoryEffortError, got %T: %v", err, err)
	}

	// 3. Unknown model pass-through
	resUnknown, err := ResolveEffortWithCatalog(nil, cat, "unknown-prov", "unknown-model", "high")
	if err != nil {
		t.Fatalf("unexpected error on unknown model: %v", err)
	}
	if resUnknown.HasResolvedEntry {
		t.Errorf("expected HasResolvedEntry=false for unknown model")
	}
	if resUnknown.Applied != "high" {
		t.Errorf("applied = %q, want high", resUnknown.Applied)
	}
}

func TestRoute_DecisionEffortFields(t *testing.T) {
	manifest := DefaultManifest()

	// 1. Requested "low" on auto route
	dec, err := Route(context.Background(), RouteRequest{
		Prompt:   "General inventory collection task",
		Paths:    []string{},
		Manifest: manifest,
		Effort:   config.EffortLow,
	})
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}
	if dec.EffortRequested != config.EffortLow {
		t.Errorf("EffortRequested = %q, want %q", dec.EffortRequested, config.EffortLow)
	}
	if dec.EffortApplied != config.EffortLow {
		t.Errorf("EffortApplied = %q, want %q", dec.EffortApplied, config.EffortLow)
	}

	// 2. Requested "" on auto route should apply model's default effort
	decDefault, err := Route(context.Background(), RouteRequest{
		Prompt:   "General inventory collection task",
		Paths:    []string{},
		Manifest: manifest,
		Effort:   "",
	})
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}
	// For agy model gemini-3.8-flash-high, catalog default_effort is "high"
	if decDefault.EffortRequested != "" {
		t.Errorf("EffortRequested = %q, want empty", decDefault.EffortRequested)
	}
	if decDefault.EffortApplied != config.EffortHigh {
		t.Errorf("EffortApplied = %q, want %q", decDefault.EffortApplied, config.EffortHigh)
	}
}
