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
	if len(cat.Providers) != 13 {
		t.Fatalf("got %d providers, want 13", len(cat.Providers))
	}

	totalModels := 0
	for _, prov := range cat.Providers {
		totalModels += len(prov.Models)
	}
	if totalModels != 95 {
		t.Fatalf("got %d total models across providers, want 95", totalModels)
	}

	// Spot-check 9router
	nine, ok := cat.FindProvider("9router")
	if !ok {
		t.Fatal("9router provider missing in default catalog")
	}
	if nine.EffortStyle != EffortStyleNamed {
		t.Errorf("9router effort_style = %q, want %q", nine.EffortStyle, EffortStyleNamed)
	}
	rf, ok := nine.FindModel("reasoning-free")
	if !ok {
		t.Fatal("reasoning-free missing in 9router provider")
	}
	if rf.DefaultEffort != EffortHigh {
		t.Errorf("reasoning-free default_effort = %q, want %q", rf.DefaultEffort, EffortHigh)
	}

	// Spot-check agy
	agy, ok := cat.FindProvider("agy")
	if !ok {
		t.Fatal("agy provider missing in default catalog")
	}
	if agy.EffortStyle != EffortStyleBakedName {
		t.Errorf("agy effort_style = %q, want %q", agy.EffortStyle, EffortStyleBakedName)
	}
	flash, ok := agy.FindModel("gemini-3.8-flash-high")
	if !ok {
		t.Fatal("gemini-3.8-flash-high missing in agy provider")
	}
	if flash.DefaultEffort != EffortHigh {
		t.Errorf("gemini-3.8-flash-high default_effort = %q, want %q", flash.DefaultEffort, EffortHigh)
	}
}

func TestCatalog_AgyBakedNameProvider(t *testing.T) {
	cat, err := LoadDefaultCatalog()
	if err != nil {
		t.Fatalf("LoadDefaultCatalog failed: %v", err)
	}

	// 1. Proving agy provider loads and has baked-name style
	agy, ok := cat.FindProvider("agy")
	if !ok {
		t.Fatal("agy provider not found in catalog")
	}
	if agy.EffortStyle != EffortStyleBakedName {
		t.Errorf("agy effort_style = %q, want %q", agy.EffortStyle, EffortStyleBakedName)
	}
	if !strings.Contains(agy.Notes, "invalid model selection") {
		t.Errorf("agy notes = %q, want notes documenting invalid model selection refusal", agy.Notes)
	}

	// 2. Table rows proving each observed variant resolves with a single-element supported list
	// and default_effort equals that level.
	variantRows := []struct {
		modelID    string
		wantEffort string
	}{
		{
			modelID:    "gemini-3.8-flash-low",
			wantEffort: EffortLow,
		},
		{
			modelID:    "gemini-3.8-flash-medium",
			wantEffort: EffortMedium,
		},
		{
			modelID:    "gemini-3.8-flash-high",
			wantEffort: EffortHigh,
		},
	}

	for _, tt := range variantRows {
		t.Run("variant_"+tt.modelID, func(t *testing.T) {
			m, ok := agy.FindModel(tt.modelID)
			if !ok {
				t.Fatalf("FindModel(%q) = not found, want found", tt.modelID)
			}
			if m.ID != tt.modelID {
				t.Errorf("m.ID = %q, want %q", m.ID, tt.modelID)
			}
			if len(m.SupportedEfforts) != 1 {
				t.Fatalf("m.SupportedEfforts = %v, want exactly 1 element", m.SupportedEfforts)
			}
			if m.SupportedEfforts[0] != tt.wantEffort {
				t.Errorf("m.SupportedEfforts[0] = %q, want %q", m.SupportedEfforts[0], tt.wantEffort)
			}
			if m.DefaultEffort != tt.wantEffort {
				t.Errorf("m.DefaultEffort = %q, want %q", m.DefaultEffort, tt.wantEffort)
			}
		})
	}

	// 3. Table rows proving FindProvider/FindModel hit for (agy, gemini-3.8-flash-high)
	hitRows := []struct {
		name         string
		providerName string
		modelID      string
		wantHit      bool
	}{
		{
			name:         "exact match",
			providerName: "agy",
			modelID:      "gemini-3.8-flash-high",
			wantHit:      true,
		},
		{
			name:         "case-insensitive provider and model",
			providerName: "AGY",
			modelID:      "Gemini-3.8-Flash-High",
			wantHit:      true,
		},
		{
			name:         "upper-case model id",
			providerName: "agy",
			modelID:      "GEMINI-3.8-FLASH-HIGH",
			wantHit:      true,
		},
	}

	for _, tt := range hitRows {
		t.Run("hit_"+tt.name, func(t *testing.T) {
			p, ok := cat.FindProvider(tt.providerName)
			if !ok {
				t.Fatalf("FindProvider(%q) = not found", tt.providerName)
			}
			m, ok := p.FindModel(tt.modelID)
			if ok != tt.wantHit {
				t.Fatalf("FindModel(%q) hit = %v, want %v", tt.modelID, ok, tt.wantHit)
			}
			if m.ID != "gemini-3.8-flash-high" {
				t.Errorf("m.ID = %q, want gemini-3.8-flash-high", m.ID)
			}
			if m.DefaultEffort != EffortHigh {
				t.Errorf("m.DefaultEffort = %q, want %q", m.DefaultEffort, EffortHigh)
			}
			if len(m.SupportedEfforts) != 1 || m.SupportedEfforts[0] != EffortHigh {
				t.Errorf("m.SupportedEfforts = %v, want [%s]", m.SupportedEfforts, EffortHigh)
			}
		})
	}

	// 4. Table rows proving unknown agy variants resolve to nil entry (F1's backstop territory).
	unknownRows := []struct {
		name    string
		modelID string
	}{
		{
			name:    "unobserved level max",
			modelID: "gemini-3.8-flash-max",
		},
		{
			name:    "unobserved level xhigh",
			modelID: "gemini-3.8-flash-xhigh",
		},
		{
			name:    "unobserved level none",
			modelID: "gemini-3.8-flash-none",
		},
		{
			name:    "unobserved level minimal",
			modelID: "gemini-3.8-flash-minimal",
		},
		{
			name:    "bare family id without suffix",
			modelID: "gemini-3.8-flash",
		},
		{
			name:    "arbitrary unknown model",
			modelID: "gemini-3.8-flash-turbo",
		},
		{
			name:    "unrelated model id",
			modelID: "gpt-6-astra",
		},
	}

	for _, tt := range unknownRows {
		t.Run("unknown_"+tt.name, func(t *testing.T) {
			m, ok := agy.FindModel(tt.modelID)
			if ok {
				t.Errorf("FindModel(%q) returned %+v (hit), want not found (nil entry for F1 backstop)", tt.modelID, m)
			}
			// ModelCatalog zero-value check:
			if m.ID != "" || len(m.SupportedEfforts) != 0 || m.DefaultEffort != "" {
				t.Errorf("FindModel(%q) returned non-zero entry: %+v", tt.modelID, m)
			}
		})
	}

	// 5. Table rows proving the loader still tolerates the file being absent (fail-open, existing behavior unchanged).
	absentRows := []struct {
		name string
		path string
	}{
		{
			name: "nonexistent relative file",
			path: "nonexistent-agent-models.yml",
		},
		{
			name: "nonexistent absolute file",
			path: "/nonexistent/path/to/agent-models.yml",
		},
	}

	for _, tt := range absentRows {
		t.Run("absent_file_"+tt.name, func(t *testing.T) {
			absentCat, err := LoadCatalog(tt.path)
			if err == nil {
				t.Fatalf("LoadCatalog(%q) succeeded, want error", tt.path)
			}
			if absentCat != nil {
				t.Errorf("LoadCatalog(%q) returned non-nil catalog: %+v", tt.path, absentCat)
			}

			// Fail-open: nil catalog safely no-ops on lookup and merge
			p, ok := absentCat.FindProvider("agy")
			if ok || p.Name != "" {
				t.Errorf("nil catalog FindProvider('agy') = (%+v, %v), want zero value and false", p, ok)
			}

			manifest := &File{}
			rep := absentCat.MergeInto(manifest)
			if rep.FilledModels != 0 || rep.SkippedProviders != 0 || rep.UnknownProviders != 0 {
				t.Errorf("nil catalog MergeInto = %+v, want zero report", rep)
			}
		})
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

func TestCatalogDeclarativeMetadataAndValidation(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantErr   bool
		errModel  string
		errField  string
		errMsgSub string
	}{
		{
			name: "valid family, cost_tier, status on provider and model",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: named
    family: google
    cost_tier: standard
    status: active
    models:
      - id: test-model
        supported_efforts: [low, medium, high]
        family: google
        cost_tier: standard
        status: active
`,
			wantErr: false,
		},
		{
			name: "invalid family on provider",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: named
    family: Invalid_Family
    models:
      - id: test-model
        supported_efforts: [low]
`,
			wantErr:   true,
			errModel:  "",
			errField:  "family",
			errMsgSub: "invalid family",
		},
		{
			name: "invalid cost_tier on provider",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: named
    cost_tier: premium
    models:
      - id: test-model
        supported_efforts: [low]
`,
			wantErr:   true,
			errModel:  "",
			errField:  "cost_tier",
			errMsgSub: "invalid cost_tier",
		},
		{
			name: "invalid status on provider",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: named
    status: archived
    models:
      - id: test-model
        supported_efforts: [low]
`,
			wantErr:   true,
			errModel:  "",
			errField:  "status",
			errMsgSub: "invalid status",
		},
		{
			name: "invalid family on model",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: named
    models:
      - id: test-model
        supported_efforts: [low]
        family: Bad Family
`,
			wantErr:   true,
			errModel:  "test-model",
			errField:  "family",
			errMsgSub: "invalid family",
		},
		{
			name: "invalid cost_tier on model",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: named
    models:
      - id: test-model
        supported_efforts: [low]
        cost_tier: ultra
`,
			wantErr:   true,
			errModel:  "test-model",
			errField:  "cost_tier",
			errMsgSub: "invalid cost_tier",
		},
		{
			name: "invalid status on model",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: named
    models:
      - id: test-model
        supported_efforts: [low]
        status: obsolete
`,
			wantErr:   true,
			errModel:  "test-model",
			errField:  "status",
			errMsgSub: "invalid status",
		},
		{
			name: "effort_style none with default_effort high should fail",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: none
    models:
      - id: test-model
        supported_efforts: []
        default_effort: high
`,
			wantErr:   true,
			errModel:  "test-model",
			errField:  "default_effort",
			errMsgSub: "not allowed with effort_style none",
		},
		{
			name: "effort_style none with default_effort none should pass",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: none
    models:
      - id: test-model
        supported_efforts: []
        default_effort: none
`,
			wantErr: false,
		},
		{
			name: "effort_style none with empty default_effort should pass",
			yaml: `
schema_version: agent-models.v1
catalog_version: 3
verified_at: "2026-11-14"
providers:
  test:
    effort_style: none
    models:
      - id: test-model
        supported_efforts: []
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				errStr := err.Error()
				if tt.errModel != "" && !strings.Contains(errStr, tt.errModel) {
					t.Errorf("error %q should name model %q", errStr, tt.errModel)
				}
				if tt.errField != "" && !strings.Contains(errStr, tt.errField) {
					t.Errorf("error %q should name field %q", errStr, tt.errField)
				}
				if tt.errMsgSub != "" && !strings.Contains(errStr, tt.errMsgSub) {
					t.Errorf("error %q should contain %q", errStr, tt.errMsgSub)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestDefaultCatalogGemini4AndAdapterParity(t *testing.T) {
	cat, err := LoadDefaultCatalog()
	if err != nil {
		t.Fatalf("LoadDefaultCatalog failed: %v", err)
	}

	if cat.CatalogVersion < 3 {
		t.Errorf("CatalogVersion = %d, want >= 3", cat.CatalogVersion)
	}

	// Check agy provider has Gemini 4 models
	agy, ok := cat.FindProvider("agy")
	if !ok {
		t.Fatal("agy provider not found")
	}

	expectedModels := []string{
		"gemini-4-hydrogen-low",
		"gemini-4-hydrogen-medium",
		"gemini-4-hydrogen-high",
		"gemini-4-hydrogen-xhigh",
		"gemini-4-argon-low",
		"gemini-4-argon-medium",
		"gemini-4-argon-high",
		"gemini-4-carbon-low",
		"gemini-4-carbon-medium",
		"gemini-4-carbon-high",
		"gemini-pro-orchestrator",
	}

	for _, modelID := range expectedModels {
		m, ok := agy.FindModel(modelID)
		if !ok {
			t.Errorf("agy model %q not found", modelID)
			continue
		}
		if modelID == "gemini-pro-orchestrator" {
			if m.EffortStyle != EffortStyleNone {
				t.Errorf("%s: EffortStyle = %q, want %q", modelID, m.EffortStyle, EffortStyleNone)
			}
		} else {
			if m.Status != ModelStatusActive {
				t.Errorf("%s: Status = %q, want %q", modelID, m.Status, ModelStatusActive)
			}
		}
	}

	// Check gemini-3.8-flash-high is deprecated
	flashHigh, ok := agy.FindModel("gemini-3.8-flash-high")
	if !ok {
		t.Fatal("gemini-3.8-flash-high missing in agy provider")
	}
	if flashHigh.Status != ModelStatusDeprecated {
		t.Errorf("gemini-3.8-flash-high: Status = %q, want %q", flashHigh.Status, ModelStatusDeprecated)
	}

	// Verify every profile points to a provider in cat.Providers and a cataloged model ID
	for profName, prof := range cat.Profiles {
		catProv, ok := cat.FindProvider(prof.Provider)
		if !ok {
			t.Errorf("profile %q: provider %q not found in catalog", profName, prof.Provider)
			continue
		}
		_, ok = catProv.FindModel(prof.Model)
		if !ok {
			t.Errorf("profile %q: model %q not found in provider %q", profName, prof.Model, prof.Provider)
		}
	}
}

func TestResolveProviderDefaultModel(t *testing.T) {
	// Test with a mock catalog
	cat := &Catalog{
		SchemaVersion:  CurrentCatalogSchemaVersion,
		CatalogVersion: 3,
		VerifiedAt:     "2026-11-14",
		Providers: map[string]ProviderCatalog{
			"testprov": {
				Name:          "testprov",
				EffortStyle:   EffortStyleNamed,
				DefaultEffort: "high",
				Models: []ModelCatalog{
					{
						ID:               "model-a",
						SupportedEfforts: []string{"low", "medium", "high"},
						DefaultEffort:    "medium",
						Status:           ModelStatusActive,
					},
					{
						ID:               "model-b-deprecated",
						SupportedEfforts: []string{"high"},
						DefaultEffort:    "high",
						Status:           ModelStatusDeprecated,
					},
				},
			},
			"bakedprov": {
				Name:          "bakedprov",
				EffortStyle:   EffortStyleBakedName,
				DefaultEffort: "high",
				Models: []ModelCatalog{
					{
						ID:               "gemini-4-hydrogen",
						SupportedEfforts: []string{"low", "medium", "high", "xhigh"},
						DefaultEffort:    "high",
						EffortStyle:      EffortStyleBakedName,
						Status:           ModelStatusActive,
					},
				},
			},
			"noneprov": {
				Name:        "noneprov",
				EffortStyle: EffortStyleNone,
				Models: []ModelCatalog{
					{
						ID:               "orchestrator-model",
						SupportedEfforts: []string{},
						EffortStyle:      EffortStyleNone,
						Status:           ModelStatusActive,
					},
				},
			},
			"transitionalfirst": {
				Name:        "transitionalfirst",
				EffortStyle: EffortStyleNamed,
				Models: []ModelCatalog{
					{
						ID:               "legacy-transitional",
						SupportedEfforts: []string{"high"},
						Status:           ModelStatusTransitional,
					},
					{
						ID:               "nextgen-active",
						SupportedEfforts: []string{"low", "high"},
						Status:           ModelStatusActive,
					},
				},
			},
		},
	}

	manifest := &File{
		Providers: []ProviderEntry{
			{
				Name:         "testprov",
				DefaultModel: "model-a",
				Models: []ModelEntry{
					{ID: "model-a", EffortStyle: EffortStyleNamed, SupportedEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},
				},
			},
			{
				Name:         "bakedprov",
				DefaultModel: "gemini-4-hydrogen",
				Models: []ModelEntry{
					{ID: "gemini-4-hydrogen", EffortStyle: EffortStyleBakedName, SupportedEfforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "high"},
				},
			},
			{
				Name:         "noneprov",
				DefaultModel: "orchestrator-model",
				Models: []ModelEntry{
					{ID: "orchestrator-model", EffortStyle: EffortStyleNone, SupportedEfforts: []string{}},
				},
			},
		},
	}

	tests := []struct {
		name       string
		manifest   *File
		cat        *Catalog
		provider   string
		wantModel  string
		wantSuffix bool // whether baked-name suffix expansion should happen
	}{
		{
			name:       "manifest provider with default model (named style)",
			manifest:   manifest,
			cat:        cat,
			provider:   "testprov",
			wantModel:  "model-a",
			wantSuffix: false,
		},
		{
			name:       "manifest provider with baked-name model expands suffix",
			manifest:   manifest,
			cat:        cat,
			provider:   "bakedprov",
			wantModel:  "gemini-4-hydrogen-high", // should expand with default effort
			wantSuffix: true,
		},
		{
			name:       "manifest provider with none style returns base model",
			manifest:   manifest,
			cat:        cat,
			provider:   "noneprov",
			wantModel:  "orchestrator-model",
			wantSuffix: false,
		},
		{
			name:       "catalog fallback picks first non-deprecated",
			manifest:   nil,
			cat:        cat,
			provider:   "testprov",
			wantModel:  "model-a",
			wantSuffix: false,
		},
		{
			name:       "catalog fallback with baked-name expands suffix",
			manifest:   nil,
			cat:        cat,
			provider:   "bakedprov",
			wantModel:  "gemini-4-hydrogen-high",
			wantSuffix: true,
		},
		{
			name:       "empty provider name returns empty",
			manifest:   manifest,
			cat:        cat,
			provider:   "",
			wantModel:  "",
			wantSuffix: false,
		},
		{
			name:       "catalog fallback prefers active over earlier transitional model",
			manifest:   nil,
			cat:        cat,
			provider:   "transitionalfirst",
			wantModel:  "nextgen-active",
			wantSuffix: false,
		},
		{
			name:       "unknown provider returns empty",
			manifest:   manifest,
			cat:        cat,
			provider:   "unknown",
			wantModel:  "",
			wantSuffix: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveProviderDefaultModel(tt.manifest, tt.cat, tt.provider)
			if result != tt.wantModel {
				t.Errorf("ResolveProviderDefaultModel(%q) = %q, want %q", tt.provider, result, tt.wantModel)
			}
		})
	}
}

func TestParseCatalogProfilesValid(t *testing.T) {
	yaml := testCatalogYAML + `
profiles:
  scout-fast: {provider: cursor, model: composer-2.5, effort: low, timeout: 180s, role: scout, permission: read_only}
`
	cat, err := ParseCatalog([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	prof, ok := cat.Profile("scout-fast")
	if !ok {
		t.Fatal("expected scout-fast profile")
	}
	if prof.Provider != "cursor" || prof.Model != "composer-2.5" || prof.Effort != "low" {
		t.Errorf("unexpected profile fields: %+v", prof)
	}
	if prof.Timeout != "180s" || prof.Role != "scout" || prof.Permission != "read_only" {
		t.Errorf("unexpected profile metadata: %+v", prof)
	}
}

func TestParseCatalogProfilesValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		extra   string
		wantSub string
	}{
		{
			name: "unknown field",
			extra: `
profiles:
  bad: {provider: cursor, model: m, extra: x}
`,
			wantSub: "unknown field",
		},
		{
			name: "empty model",
			extra: `
profiles:
  bad: {provider: cursor, model: "", effort: low}
`,
			wantSub: "model is required",
		},
		{
			name: "bad timeout",
			extra: `
profiles:
  bad: {provider: cursor, model: m, timeout: banana}
`,
			wantSub: "timeout",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(testCatalogYAML + tc.extra))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}
