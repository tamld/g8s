package routing

import (
	"errors"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

func boolPtr(v bool) *bool { return &v }

func testCapabilityCatalog() *config.Catalog {
	return &config.Catalog{
		SchemaVersion:  config.CurrentCatalogSchemaVersion,
		CatalogVersion: 4,
		VerifiedAt:     "2026-11-19",
		Providers: map[string]config.ProviderCatalog{
			"openai": {
				Name:          "openai",
				EffortStyle:   config.EffortStyleNamed,
				DefaultEffort: config.EffortMedium,
				Capabilities: &config.ModelCapabilities{
					ToolCalling:      boolPtr(true),
					StructuredOutput: boolPtr(true),
					Streaming:        boolPtr(true),
					ReasoningEffort:  boolPtr(true),
					CancelInflight:   boolPtr(true),
					ContextWindow:    200000,
				},
				Models: []config.ModelCatalog{
					{
						ID:               "gpt-6-astra",
						SupportedEfforts: []string{"low", "medium", "high"},
						DefaultEffort:    config.EffortMedium,
						Capabilities: &config.ModelCapabilities{
							ToolCalling:   boolPtr(false),
							ContextWindow: 200000,
						},
					},
					{
						ID:               "gpt-5.5",
						SupportedEfforts: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
						DefaultEffort:    config.EffortMedium,
					},
				},
			},
			"anthropic": {
				Name:          "anthropic",
				EffortStyle:   config.EffortStyleNamed,
				DefaultEffort: config.EffortHigh,
				Capabilities: &config.ModelCapabilities{
					ToolCalling:      boolPtr(true),
					StructuredOutput: boolPtr(true),
					Streaming:        boolPtr(true),
					ReasoningEffort:  boolPtr(true),
					CancelInflight:   boolPtr(true),
					ContextWindow:    200000,
				},
				Models: []config.ModelCatalog{
					{
						ID:               "claude-haiku-4-5",
						SupportedEfforts: []string{},
						Capabilities: &config.ModelCapabilities{
							ReasoningEffort: boolPtr(false),
							ContextWindow:   200000,
						},
					},
				},
			},
			"xai": {
				Name:        "xai",
				EffortStyle: config.EffortStyleNamed,
				Mandatory:   true,
				Models: []config.ModelCatalog{
					{
						ID:               "grok-4.7",
						SupportedEfforts: []string{"low", "medium", "high", "xhigh"},
					},
				},
			},
		},
	}
}

func TestEvaluateCapabilityGate_StrictRejections(t *testing.T) {
	cat := testCapabilityCatalog()

	// 1. tool_calling unsupported on gpt-6-astra
	_, err := EvaluateCapabilityGate(cat, CapabilityRequest{
		Provider:           "openai",
		Model:              "gpt-6-astra",
		RequireToolCalling: true,
		Strict:             true,
	})
	if !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for tool_calling on gpt-6-astra, got %v", err)
	}

	// 2. reasoning_effort=false on claude-haiku-4-5
	_, err = EvaluateCapabilityGate(cat, CapabilityRequest{
		Provider:        "anthropic",
		Model:           "claude-haiku-4-5",
		RequestedEffort: "high",
		Strict:          true,
	})
	if !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for effort on claude-haiku-4-5, got %v", err)
	}

	// 3. unsupported effort level ("xhigh") on gpt-6-astra ([low, medium, high]) fails in Strict mode
	_, err = EvaluateCapabilityGate(cat, CapabilityRequest{
		Provider:        "openai",
		Model:           "gpt-6-astra",
		RequestedEffort: "xhigh",
		Strict:          true,
	})
	if !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for xhigh on gpt-6-astra in strict mode, got %v", err)
	}

	// 4. MinContextWindow exceeds 200000
	_, err = EvaluateCapabilityGate(cat, CapabilityRequest{
		Provider:         "openai",
		Model:            "gpt-5.5",
		MinContextWindow: 500000,
		Strict:           true,
	})
	if !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for excessive context_window, got %v", err)
	}

	// 5. MandatoryEffortError wraps both ErrCapabilityUnsupported and *MandatoryEffortError
	_, err = EvaluateCapabilityGate(cat, CapabilityRequest{
		Provider:        "xai",
		Model:           "grok-4.7",
		RequestedEffort: config.EffortNone,
		Strict:          true,
	})
	if !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for MandatoryEffortError, got %v", err)
	}
	var mandatoryErr *MandatoryEffortError
	if !errors.As(err, &mandatoryErr) {
		t.Fatalf("expected *MandatoryEffortError unwrap, got %T: %v", err, err)
	}
}

func TestEvaluateCapabilityGate_ExplicitDegradationTelemetry(t *testing.T) {
	cat := testCapabilityCatalog()

	// Fallback policy permits effort degrade: xhigh -> high with explicit telemetry
	res, err := EvaluateCapabilityGate(cat, CapabilityRequest{
		Provider:           "openai",
		Model:              "gpt-6-astra",
		RequestedEffort:    "xhigh",
		AllowEffortDegrade: true,
	})
	if err != nil {
		t.Fatalf("unexpected error under AllowEffortDegrade: %v", err)
	}
	if !res.Allowed || res.EffortApplied != "high" || !res.EffortDegraded || !strings.Contains(res.DegradeReason, "degraded") {
		t.Fatalf("unexpected degraded result: %+v", res)
	}

	// Haiku drops effort when AllowEffortDegrade is true and Strict is false
	resHaiku, err := EvaluateCapabilityGate(cat, CapabilityRequest{
		Provider:           "anthropic",
		Model:              "claude-haiku-4-5",
		RequestedEffort:    "medium",
		AllowEffortDegrade: true,
	})
	if err != nil {
		t.Fatalf("unexpected error for haiku fallback: %v", err)
	}
	if !resHaiku.EffortDegraded || resHaiku.EffortApplied != "" {
		t.Fatalf("expected dropped effort telemetry, got %+v", resHaiku)
	}

	// Exact match -> EffortDegraded == false
	resExact, err := EvaluateCapabilityGate(cat, CapabilityRequest{
		Provider:        "openai",
		Model:           "gpt-5.5",
		RequestedEffort: "xhigh",
		Strict:          true,
	})
	if err != nil {
		t.Fatalf("exact match failed: %v", err)
	}
	if resExact.EffortDegraded || resExact.EffortApplied != "xhigh" {
		t.Fatalf("expected exact match without degrade, got %+v", resExact)
	}
}
