package config

import (
	"errors"
	"strings"
	"testing"
)

const testCatalogYAML = `
schema_version: agent-models.v1
catalog_version: 1
verified_at: "2026-10-04"
sources:
  - https://developers.openai.com/api/docs/models
  - https://platform.claude.com/docs/en/docs/build-with-claude/effort

providers:
  openai:
    effort_style: named
    field: reasoning_effort
    default_effort: adaptive
    mandatory: false
    notes: "openai notes"
    models:
      - id: gpt-6-astra
        supported_efforts: [low, medium, high, xhigh, max]
        default_effort: adaptive
      - id: gpt-6.1-sol
        supported_efforts: [low, medium, high, xhigh, max]
        default_effort: medium

  anthropic:
    effort_style: named
    field: output_config.effort
    default_effort: high
    mandatory: false
    models:
      - id: claude-opus-5-5
        supported_efforts: [low, medium, high, xhigh, max]
        default_effort: medium
      - id: claude-sonnet-5-5
        supported_efforts: [low, medium, high, xhigh]
        default_effort: high
      - id: claude-haiku-4-5
        supported_efforts: []

  google:
    effort_style: named
    field: thinking_level
    default_effort: dynamic
    mandatory: false
    models:
      - id: gemini-3.1-pro-preview
        supported_efforts: [minimal, low, medium, high]
        default_effort: dynamic
      - id: gemini-3.8-flash
        supported_efforts: [low, medium, high]
        default_effort: medium

  xai:
    effort_style: named
    field: reasoning_effort
    default_effort: high
    mandatory: true
    models:
      - id: grok-4.7
        supported_efforts: [low, medium, high, xhigh]
        default_effort: high

  legacy:
    effort_style: budget
    field: max_tokens
    mandatory: false
    models:
      - id: budget-model-1
        supported_efforts: [low, medium, high]
        default_effort: medium
        effort_budget_map: {low: 2048, medium: 8192, high: 32768}

  ollama:
    effort_style: toggle
    field: think
    mandatory: false
    models: []

  agy:
    effort_style: baked-name
    field: model-name-suffix
    default_effort: high
    mandatory: false
    models:
      - id: gemini-3.8-flash
        supported_efforts: [low, medium, high]
        default_effort: high
`

func TestParseCatalogValid(t *testing.T) {
	cat, err := ParseCatalog([]byte(testCatalogYAML))
	if err != nil {
		t.Fatalf("ParseCatalog failed: %v", err)
	}

	if cat.SchemaVersion != CurrentCatalogSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", cat.SchemaVersion, CurrentCatalogSchemaVersion)
	}
	if cat.CatalogVersion != 1 {
		t.Errorf("CatalogVersion = %d, want 1", cat.CatalogVersion)
	}
	if cat.VerifiedAt != "2026-10-04" {
		t.Errorf("VerifiedAt = %q, want '2026-10-04'", cat.VerifiedAt)
	}
	if len(cat.Sources) != 2 {
		t.Errorf("got %d sources, want 2", len(cat.Sources))
	}
	if len(cat.Providers) != 7 {
		t.Errorf("got %d providers, want 7", len(cat.Providers))
	}

	// Verify openai provider & models
	openai, ok := cat.FindProvider("openai")
	if !ok {
		t.Fatal("openai provider not found")
	}
	if openai.EffortStyle != EffortStyleNamed || openai.DefaultEffort != EffortAdaptive {
		t.Errorf("unexpected openai config: %+v", openai)
	}
	if len(openai.Models) != 2 {
		t.Errorf("got %d openai models, want 2", len(openai.Models))
	}

	astra, ok := openai.FindModel("gpt-6-astra")
	if !ok {
		t.Fatal("gpt-6-astra model not found")
	}
	if len(astra.SupportedEfforts) != 5 || astra.DefaultEffort != EffortAdaptive {
		t.Errorf("unexpected astra model: %+v", astra)
	}

	// Verify xai mandatory
	xai, ok := cat.FindProvider("xai")
	if !ok || !xai.Mandatory {
		t.Errorf("xai should have mandatory = true, got %+v", xai)
	}

	// Verify ollama empty models
	ollama, ok := cat.FindProvider("ollama")
	if !ok || len(ollama.Models) != 0 {
		t.Errorf("ollama should have 0 models, got %+v", ollama)
	}

	// Verify legacy budget map
	legacy, ok := cat.FindProvider("legacy")
	if !ok {
		t.Fatal("legacy provider not found")
	}
	bm, ok := legacy.FindModel("budget-model-1")
	if !ok || len(bm.EffortBudgetMap) != 3 || bm.EffortBudgetMap["medium"] != 8192 {
		t.Errorf("unexpected budget model: %+v", bm)
	}
}

func TestLoadDefaultCatalogFile(t *testing.T) {
	cat, err := LoadDefaultCatalog()
	if err != nil {
		t.Fatalf("LoadDefaultCatalog failed: %v", err)
	}

	if cat.SchemaVersion != CurrentCatalogSchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", cat.SchemaVersion, CurrentCatalogSchemaVersion)
	}
	if len(cat.Providers) != 8 {
		t.Fatalf("got %d providers, want 8", len(cat.Providers))
	}

	totalModels := 0
	for _, prov := range cat.Providers {
		totalModels += len(prov.Models)
	}
	if totalModels != 29 {
		t.Fatalf("got %d total models across providers, want 29", totalModels)
	}

	// Spot-check agy
	agy, ok := cat.FindProvider("agy")
	if !ok {
		t.Fatal("agy provider missing in default catalog")
	}
	if agy.EffortStyle != EffortStyleBakedName {
		t.Errorf("agy effort_style = %q, want %q", agy.EffortStyle, EffortStyleBakedName)
	}
	flash, ok := agy.FindModel("gemini-3.8-flash")
	if !ok {
		t.Fatal("gemini-3.8-flash missing in agy provider")
	}
	if flash.DefaultEffort != EffortHigh {
		t.Errorf("gemini-3.8-flash default_effort = %q, want %q", flash.DefaultEffort, EffortHigh)
	}
}

func TestParseCatalogWrongSchemaVersion(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantGot string
	}{
		{
			name: "newer version",
			yaml: `
schema_version: agent-models.v2
verified_at: "2026-10-04"
providers: {}
`,
			wantGot: "agent-models.v2",
		},
		{
			name: "older version",
			yaml: `
schema_version: agent-models.v0
verified_at: "2026-10-04"
providers: {}
`,
			wantGot: "agent-models.v0",
		},
		{
			name: "empty version",
			yaml: `
schema_version: ""
verified_at: "2026-10-04"
providers: {}
`,
			wantGot: "",
		},
		{
			name: "missing version",
			yaml: `
verified_at: "2026-10-04"
providers: {}
`,
			wantGot: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error for wrong schema_version, got nil")
			}

			var sve *SchemaVersionError
			if !errors.As(err, &sve) {
				t.Fatalf("expected *SchemaVersionError, got %T: %v", err, err)
			}
			if sve.Got != tt.wantGot {
				t.Errorf("sve.Got = %q, want %q", sve.Got, tt.wantGot)
			}
			if sve.Want != CurrentCatalogSchemaVersion {
				t.Errorf("sve.Want = %q, want %q", sve.Want, CurrentCatalogSchemaVersion)
			}
		})
	}
}

func TestParseCatalogLevelOutsideLadder(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantModel string
		wantField string
		wantMsg   string
	}{
		{
			name: "supported_efforts contains unknown level turbo",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: named
    models:
      - id: bad-model-1
        supported_efforts: [low, turbo]
`,
			wantModel: "bad-model-1",
			wantField: "supported_efforts",
			wantMsg:   "outside ladder",
		},
		{
			name: "supported_efforts contains extreme",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: named
    models:
      - id: bad-model-2
        supported_efforts: [extreme]
`,
			wantModel: "bad-model-2",
			wantField: "supported_efforts",
			wantMsg:   "outside ladder",
		},
		{
			name: "effort_budget_map contains level outside ladder",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: budget
    models:
      - id: bad-budget-model
        supported_efforts: [low, high]
        effort_budget_map: {quantum: 1000}
`,
			wantModel: "bad-budget-model",
			wantField: "effort_budget_map",
			wantMsg:   "outside ladder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			errStr := err.Error()
			if !strings.Contains(errStr, tt.wantModel) {
				t.Errorf("error %q should name model %q", errStr, tt.wantModel)
			}
			if !strings.Contains(errStr, tt.wantField) {
				t.Errorf("error %q should name field %q", errStr, tt.wantField)
			}
			if !strings.Contains(errStr, tt.wantMsg) {
				t.Errorf("error %q should contain message %q", errStr, tt.wantMsg)
			}
		})
	}
}

func TestParseCatalogDefaultNotInSubset(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantModel string
		wantField string
	}{
		{
			name: "default_effort high not in supported [low, medium]",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: named
    models:
      - id: default-mismatch-1
        supported_efforts: [low, medium]
        default_effort: high
`,
			wantModel: "default-mismatch-1",
			wantField: "default_effort",
		},
		{
			name: "default_effort minimal on model with empty supported_efforts",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: named
    models:
      - id: default-mismatch-2
        supported_efforts: []
        default_effort: minimal
`,
			wantModel: "default-mismatch-2",
			wantField: "default_effort",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			errStr := err.Error()
			if !strings.Contains(errStr, tt.wantModel) {
				t.Errorf("error %q should name model %q", errStr, tt.wantModel)
			}
			if !strings.Contains(errStr, tt.wantField) {
				t.Errorf("error %q should name field %q", errStr, tt.wantField)
			}
		})
	}
}

func TestParseCatalogBudgetMapOnNonBudgetStyle(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantModel string
	}{
		{
			name: "named style with budget map",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: named
    models:
      - id: named-with-budget
        supported_efforts: [low, high]
        effort_budget_map: {low: 1000}
`,
			wantModel: "named-with-budget",
		},
		{
			name: "toggle style with budget map",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: toggle
    models:
      - id: toggle-with-budget
        supported_efforts: [medium, high]
        effort_budget_map: {medium: 2000}
`,
			wantModel: "toggle-with-budget",
		},
		{
			name: "baked-name style with budget map",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: baked-name
    models:
      - id: baked-with-budget
        supported_efforts: [low, high]
        effort_budget_map: {low: 1000}
`,
			wantModel: "baked-with-budget",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			errStr := err.Error()
			if !strings.Contains(errStr, tt.wantModel) {
				t.Errorf("error %q should name model %q", errStr, tt.wantModel)
			}
			if !strings.Contains(errStr, "effort_budget_map") {
				t.Errorf("error %q should name field effort_budget_map", errStr)
			}
		})
	}
}

func TestCatalogMergeIntoFillsEmptiesWithoutOverwritingUserValues(t *testing.T) {
	cat, err := ParseCatalog([]byte(testCatalogYAML))
	if err != nil {
		t.Fatalf("parse test catalog: %v", err)
	}

	manifest := &File{
		Providers: []ProviderEntry{
			{
				Class: "platform_dispatch",
				Name:  "agy",
				Models: []ModelEntry{
					{
						ID: "gemini-3.8-flash",
						// all effort fields left empty
					},
				},
			},
			{
				Class: "api_call",
				Name:  "openai",
				Models: []ModelEntry{
					{
						ID:               "gpt-6.1-sol",
						DefaultEffort:    EffortLow,                       // user-set default effort
						SupportedEfforts: []string{EffortLow, EffortHigh}, // user-set supported efforts
						// EffortStyle left empty
					},
				},
			},
		},
	}

	report := cat.MergeInto(manifest)

	if report.FilledModels != 2 {
		t.Errorf("FilledModels = %d, want 2", report.FilledModels)
	}

	// Model 1: agy/gemini-3.8-flash should be completely filled
	agyModel := manifest.Providers[0].Models[0]
	if agyModel.EffortStyle != EffortStyleBakedName {
		t.Errorf("agyModel.EffortStyle = %q, want %q", agyModel.EffortStyle, EffortStyleBakedName)
	}
	if agyModel.DefaultEffort != EffortHigh {
		t.Errorf("agyModel.DefaultEffort = %q, want %q", agyModel.DefaultEffort, EffortHigh)
	}
	wantEfforts := []string{EffortLow, EffortMedium, EffortHigh}
	if len(agyModel.SupportedEfforts) != len(wantEfforts) {
		t.Fatalf("agyModel.SupportedEfforts len = %d, want %d", len(agyModel.SupportedEfforts), len(wantEfforts))
	}
	for i := range wantEfforts {
		if agyModel.SupportedEfforts[i] != wantEfforts[i] {
			t.Errorf("agyModel.SupportedEfforts[%d] = %q, want %q", i, agyModel.SupportedEfforts[i], wantEfforts[i])
		}
	}

	// Model 2: openai/gpt-6.1-sol should keep user-set values
	openaiModel := manifest.Providers[1].Models[0]
	if openaiModel.EffortStyle != EffortStyleNamed {
		t.Errorf("openaiModel.EffortStyle = %q, want %q", openaiModel.EffortStyle, EffortStyleNamed)
	}
	if openaiModel.DefaultEffort != EffortLow {
		t.Errorf("openaiModel.DefaultEffort = %q, want %q (user-set value must not be overwritten)", openaiModel.DefaultEffort, EffortLow)
	}
	if len(openaiModel.SupportedEfforts) != 2 || openaiModel.SupportedEfforts[0] != EffortLow || openaiModel.SupportedEfforts[1] != EffortHigh {
		t.Errorf("openaiModel.SupportedEfforts overwritten: %v", openaiModel.SupportedEfforts)
	}
}

func TestCatalogMergeIntoUnknownProvidersSkippedAndCounted(t *testing.T) {
	cat, err := ParseCatalog([]byte(testCatalogYAML))
	if err != nil {
		t.Fatalf("parse test catalog: %v", err)
	}

	// Manifest only declares 'agy' and an unknown provider 'custom-llm'
	manifest := &File{
		Providers: []ProviderEntry{
			{
				Class: "platform_dispatch",
				Name:  "agy",
				Models: []ModelEntry{
					{ID: "gemini-3.8-flash"},
				},
			},
			{
				Class: "api_call",
				Name:  "custom-llm",
				Models: []ModelEntry{
					{ID: "custom-v1"},
				},
			},
		},
	}

	report := cat.MergeInto(manifest)

	// Catalog has 7 providers: openai, anthropic, google, xai, legacy, ollama, agy.
	// Manifest only matches 'agy'.
	// The other 6 catalog providers are unknown to manifest -> skipped and counted.
	if report.SkippedProviders != 6 {
		t.Errorf("SkippedProviders = %d, want 6", report.SkippedProviders)
	}
	if report.UnknownProviders != 6 {
		t.Errorf("UnknownProviders = %d, want 6", report.UnknownProviders)
	}
	if report.FilledModels != 1 {
		t.Errorf("FilledModels = %d, want 1", report.FilledModels)
	}

	wantSkipped := []string{"anthropic", "google", "legacy", "ollama", "openai", "xai"}
	if len(report.SkippedProviderNames) != len(wantSkipped) {
		t.Fatalf("SkippedProviderNames = %v, want %v", report.SkippedProviderNames, wantSkipped)
	}
	for i := range wantSkipped {
		if report.SkippedProviderNames[i] != wantSkipped[i] {
			t.Errorf("SkippedProviderNames[%d] = %q, want %q", i, report.SkippedProviderNames[i], wantSkipped[i])
		}
	}

	// Custom provider in manifest remains untouched
	customModel := manifest.Providers[1].Models[0]
	if customModel.EffortStyle != "" || customModel.DefaultEffort != "" {
		t.Errorf("customModel modified: %+v", customModel)
	}
}

func TestParseCatalogMalformedYAML(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "unclosed bracket in supported_efforts",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: named
    models:
      - id: m1
        supported_efforts: [low, medium
`,
		},
		{
			name: "unclosed brace in effort_budget_map",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: budget
    models:
      - id: m1
        supported_efforts: [low]
        effort_budget_map: {low: 1000
`,
		},
		{
			name: "invalid integer in effort_budget_map",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: budget
    models:
      - id: m1
        supported_efforts: [low]
        effort_budget_map: {low: notanint}
`,
		},
		{
			name: "invalid boolean in mandatory",
			yaml: `
schema_version: agent-models.v1
verified_at: "2026-10-04"
providers:
  test:
    effort_style: named
    mandatory: definitely
    models:
      - id: m1
`,
		},
		{
			name: "missing colon at top level",
			yaml: `
schema_version agent-models.v1
`,
		},
		{
			name: "random binary bytes",
			yaml: string([]byte{0x00, 0xFF, 0xFE, 0x12, 0x34}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parser panicked on malformed YAML: %v", r)
				}
			}()

			_, err := ParseCatalog([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error on malformed YAML, got nil")
			}
		})
	}
}

func TestCatalogMergeNilSafety(t *testing.T) {
	var cat *Catalog
	manifest := &File{}
	rep := cat.MergeInto(manifest)
	if rep.FilledModels != 0 || rep.SkippedProviders != 0 {
		t.Errorf("expected zero report on nil catalog: %+v", rep)
	}

	cat = &Catalog{}
	rep2 := cat.MergeInto(nil)
	if rep2.FilledModels != 0 || rep2.SkippedProviders != 0 {
		t.Errorf("expected zero report on nil manifest: %+v", rep2)
	}
}
