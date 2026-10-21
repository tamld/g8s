package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadAcceptsValidMixedClasses(t *testing.T) {
	path := writeTemp(t, `{
  "providers": [
    {
      "class": "api_call",
      "name": "nine-router",
      "base_url": "https://router.example.com/v1",
      "auth_env": "NINE_ROUTER_API_KEY",
      "models": [{"id": "gemini-3.8-flash-high", "context_window": 1000000}],
      "slots": 4
    },
    {
      "class": "platform_dispatch",
      "name": "agy",
      "models": [{"id": "gemini-3.8-flash-high"}]
    }
  ]
}`)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Providers) != 2 {
		t.Fatalf("got %d providers, want 2", len(f.Providers))
	}
	api := f.Providers[0]
	if api.Class != "api_call" || api.BaseURL == "" || api.AuthEnv != "NINE_ROUTER_API_KEY" || api.Slots != 4 || api.Models[0].ID != "gemini-3.8-flash-high" {
		t.Fatalf("api_call entry mismatch: %+v", api)
	}
	disp := f.Providers[1]
	if disp.Class != "platform_dispatch" || disp.Name != "agy" || len(disp.Models) != 1 {
		t.Fatalf("platform_dispatch entry mismatch: %+v", disp)
	}
}

func TestLoadRejectsUnknownProviderClass(t *testing.T) {
	path := writeTemp(t, `{"providers":[{"class":"quantum","name":"q","models":[{"id":"m"}]}]}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want error for unknown class")
	}
	if !strings.Contains(err.Error(), "unknown provider class") {
		t.Fatalf("error must contain 'unknown provider class': %v", err)
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	path := writeTemp(t, `{"providers": [`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want parse error for malformed json")
	}
}

func TestLoadRejectsAPICallMissingBaseURL(t *testing.T) {
	path := writeTemp(t, `{"providers":[{"class":"api_call","name":"x","models":[{"id":"m"}],"slots":1}]}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want error for api_call missing base_url")
	}
}

func TestLoadRejectsAPICallWithoutSlots(t *testing.T) {
	path := writeTemp(t, `{"providers":[{"class":"api_call","name":"x","base_url":"http://p","models":[{"id":"m"}]}]}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want error for api_call without slots")
	}
}

// auth_env emptiness must NOT fail at load time: the entry loads and later
// degrades to UNAVAILABLE at probe time without any HTTP request (spec R2).
func TestLoadAllowsEmptyAuthEnvDeferredToProbe(t *testing.T) {
	path := writeTemp(t, `{"providers":[{"class":"api_call","name":"no-auth","base_url":"http://p","models":[{"id":"m"}],"slots":1}]}`)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("load must succeed with empty auth_env: %v", err)
	}
	if f.Providers[0].AuthEnv != "" {
		t.Fatalf("auth_env should stay empty: %+v", f.Providers[0])
	}
}

func TestLoadRejectsEntryWithoutModels(t *testing.T) {
	path := writeTemp(t, `{"providers":[{"class":"platform_dispatch","name":"bare"}]}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want error for entry without models")
	}
}

func TestLoadPreservesArgsTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")
	content := `{
  "providers": [
    {
      "class": "platform_dispatch",
      "name": "agy-direct",
      "models": [{"id": "gemini-3.8-flash-high"}],
      "slots": 4,
      "args": ["-p", "{prompt}", "--model", "{model}", "--print-timeout", "{timeout}"]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	file, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(file.Providers) != 1 || file.Providers[0].Name != "agy-direct" {
		t.Fatalf("entry = %+v", file.Providers)
	}
	want := []string{"-p", "{prompt}", "--model", "{model}", "--print-timeout", "{timeout}"}
	if len(file.Providers[0].Args) != len(want) {
		t.Fatalf("args = %v, want %v", file.Providers[0].Args, want)
	}
	for i := range want {
		if file.Providers[0].Args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, file.Providers[0].Args[i], want[i])
		}
	}
}

func TestInitwizFixturePassesValidator(t *testing.T) {
	f, err := Load("testdata/initwiz.json")
	if err != nil {
		t.Fatalf("Load(testdata/initwiz.json): %v", err)
	}
	if len(f.Providers) != 2 {
		t.Fatalf("got %d providers, want 2", len(f.Providers))
	}
	agy := f.Providers[0]
	if agy.Class != "platform_dispatch" || agy.Name != "agy" || len(agy.Models) != 1 || agy.Models[0].ID != "gemini-3.8-flash-high" {
		t.Errorf("unexpected agy provider: %+v", agy)
	}
	claude := f.Providers[1]
	if claude.Class != "platform_dispatch" || claude.Name != "claude" || len(claude.Models) != 2 {
		t.Errorf("unexpected claude provider: %+v", claude)
	}
}

func TestLoadRejectsUnknownEffortStyle(t *testing.T) {
	path := writeTemp(t, `{
  "providers": [{
    "class": "platform_dispatch",
    "name": "agy",
    "models": [{
      "id": "m1",
      "effort_style": "custom_unknown"
    }]
  }]
}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want error for unknown effort_style")
	}
	if !strings.Contains(err.Error(), "m1") || !strings.Contains(err.Error(), "effort_style") {
		t.Fatalf("error must name model and field: %v", err)
	}
}

func TestLoadRejectsLevelOutsideLadder(t *testing.T) {
	path := writeTemp(t, `{
  "providers": [{
    "class": "platform_dispatch",
    "name": "agy",
    "models": [{
      "id": "m2",
      "supported_efforts": ["low", "turbo"]
    }]
  }]
}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want error for level outside ladder")
	}
	if !strings.Contains(err.Error(), "m2") || !strings.Contains(err.Error(), "supported_efforts") {
		t.Fatalf("error must name model and field: %v", err)
	}
}

func TestLoadRejectsDefaultEffortNotInSupportedSet(t *testing.T) {
	path := writeTemp(t, `{
  "providers": [{
    "class": "platform_dispatch",
    "name": "agy",
    "models": [{
      "id": "m3",
      "supported_efforts": ["low", "medium"],
      "default_effort": "high"
    }]
  }]
}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want error for default not in supported set")
	}
	if !strings.Contains(err.Error(), "m3") || !strings.Contains(err.Error(), "default_effort") {
		t.Fatalf("error must name model and field: %v", err)
	}
}

func TestLoadRejectsBudgetMapOnNonBudgetStyle(t *testing.T) {
	path := writeTemp(t, `{
  "providers": [{
    "class": "platform_dispatch",
    "name": "agy",
    "models": [{
      "id": "m4",
      "effort_style": "named",
      "supported_efforts": ["low", "high"],
      "effort_budget_map": {"low": 1000}
    }]
  }]
}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("want error for budget map on non-budget style")
	}
	if !strings.Contains(err.Error(), "m4") || !strings.Contains(err.Error(), "effort_budget_map") {
		t.Fatalf("error must name model and field: %v", err)
	}
}

func TestLoadAcceptsValidEffortMetadata(t *testing.T) {
	path := writeTemp(t, `{
  "providers": [{
    "class": "platform_dispatch",
    "name": "agy",
    "models": [
      {
        "id": "gemini-3.8-flash",
        "effort_style": "baked-name",
        "supported_efforts": ["low", "medium", "high"],
        "default_effort": "medium",
        "mandatory": false
      },
      {
        "id": "custom-budget",
        "effort_style": "budget",
        "supported_efforts": ["low", "high"],
        "default_effort": "low",
        "effort_budget_map": {"low": 2048, "high": 8192}
      },
      {
        "id": "adaptive-model",
        "effort_style": "named",
        "supported_efforts": ["low", "high"],
        "default_effort": "adaptive"
      }
    ]
  }]
}`)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed on valid effort metadata: %v", err)
	}
	models := f.Providers[0].Models
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}
	if models[0].EffortStyle != EffortStyleBakedName || models[0].DefaultEffort != EffortMedium {
		t.Errorf("model 0 mismatch: %+v", models[0])
	}
	if models[1].EffortStyle != EffortStyleBudget || models[1].EffortBudgetMap["low"] != 2048 {
		t.Errorf("model 1 mismatch: %+v", models[1])
	}
	if models[2].DefaultEffort != EffortAdaptive {
		t.Errorf("model 2 mismatch: %+v", models[2])
	}
}

func TestProviderEntry_EffectiveBaseURL(t *testing.T) {
	p1 := ProviderEntry{BaseURL: "http://example.com/v1/"}
	if got := p1.EffectiveBaseURL(); got != "http://example.com/v1" {
		t.Fatalf("want http://example.com/v1, got %q", got)
	}

	p2 := ProviderEntry{
		BaseURL:    "http://fallback.com/v1",
		BaseURLEnv: "TEST_G8S_CUSTOM_BASE_URL",
	}
	if got := p2.EffectiveBaseURL(); got != "http://fallback.com/v1" {
		t.Fatalf("want fallback url when env unset, got %q", got)
	}

	t.Setenv("TEST_G8S_CUSTOM_BASE_URL", "http://override.com:20128/v1///")
	if got := p2.EffectiveBaseURL(); got != "http://override.com:20128/v1" {
		t.Fatalf("want override url trimmed, got %q", got)
	}
}

func TestProviderEntry_EffectiveProtocol(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "openai"},
		{"   ", "openai"},
		{"openai", "openai"},
		{"OPENAI", "openai"},
		{"Anthropic", "anthropic"},
		{"9router", "9router"},
		{" 9ROUTER ", "9router"},
	}
	for _, tc := range tests {
		p := ProviderEntry{Protocol: tc.input}
		if got := p.EffectiveProtocol(); got != tc.want {
			t.Errorf("EffectiveProtocol(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestProviderEntry_ServesModel(t *testing.T) {
	p := ProviderEntry{
		Models: []ModelEntry{
			{ID: "gpt-4o"},
			{ID: "nvidia/*"},
			{ID: "openrouter/anthropic/*"},
			{ID: "cc*"},
		},
	}

	// Exact match (case insensitive)
	if !p.ServesModel("gpt-4o") {
		t.Error("expected gpt-4o to match")
	}
	if !p.ServesModel("GPT-4O") {
		t.Error("expected GPT-4O case insensitive match")
	}

	// Prefix glob matching
	if !p.ServesModel("nvidia/nemotron-4-340b") {
		t.Error("expected nvidia/* to match nvidia/nemotron-4-340b")
	}
	if !p.ServesModel("NVIDIA/Llama-3.1") {
		t.Error("expected nvidia/* case insensitive match")
	}
	if !p.ServesModel("openrouter/anthropic/claude-3.5-sonnet") {
		t.Error("expected openrouter/anthropic/* match")
	}
	if !p.ServesModel("cc/gpt-4") {
		t.Error("expected cc* match")
	}

	// Non-matches
	if p.ServesModel("claude-3-opus") {
		t.Error("expected claude-3-opus to not match")
	}
	if p.ServesModel("meta/llama-3") {
		t.Error("expected meta/llama-3 to not match")
	}

	// AllowUnlistedModels
	pUnlisted := ProviderEntry{
		AllowUnlistedModels: true,
	}
	if !pUnlisted.ServesModel("any-arbitrary-model") {
		t.Error("expected AllowUnlistedModels to serve any model")
	}
}

func TestLoad_ProtocolValidation(t *testing.T) {
	for _, proto := range []string{"openai", "anthropic", "9router", "OPENAI", " 9router "} {
		path := writeTemp(t, `{
			"providers": [{
				"class": "api_call",
				"name": "p1",
				"protocol": "`+proto+`",
				"base_url": "http://example.com/v1",
				"models": [{"id": "m1"}],
				"slots": 1
			}]
		}`)
		f, err := Load(path)
		if err != nil {
			t.Fatalf("expected protocol %q to be accepted: %v", proto, err)
		}
		if len(f.Providers) != 1 {
			t.Fatalf("expected 1 provider")
		}
	}

	invalidPath := writeTemp(t, `{
		"providers": [{
			"class": "api_call",
			"name": "p-bad",
			"protocol": "cohere",
			"base_url": "http://example.com/v1",
			"models": [{"id": "m1"}],
			"slots": 1
		}]
	}`)
	_, err := Load(invalidPath)
	if err == nil {
		t.Fatal("expected error for invalid protocol")
	}
	if !strings.Contains(err.Error(), "unknown protocol") {
		t.Fatalf("error should mention unknown protocol: %v", err)
	}
}

func TestLoad_BaseURLEnvSatisfiesBaseURL(t *testing.T) {
	path := writeTemp(t, `{
		"providers": [{
			"class": "api_call",
			"name": "nine-env",
			"base_url_env": "NINEROUTER_URL",
			"models": [{"id": "m1"}],
			"slots": 2
		}]
	}`)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load with base_url_env should succeed without base_url: %v", err)
	}
	p := f.Providers[0]
	if p.BaseURL != "" {
		t.Errorf("base_url should be empty: %q", p.BaseURL)
	}
	if p.BaseURLEnv != "NINEROUTER_URL" {
		t.Errorf("base_url_env mismatch: %q", p.BaseURLEnv)
	}
}

func TestLoad_ExtendedFields(t *testing.T) {
	path := writeTemp(t, `{
		"providers": [{
			"class": "api_call",
			"name": "nine-all",
			"protocol": "9router",
			"base_url": "http://127.0.0.1:20128/v1",
			"base_url_env": "NINEROUTER_URL",
			"headers": {"X-Custom-Header": "custom-val"},
			"allow_unlisted_models": true,
			"disable_queue_exec": true,
			"slots": 4,
			"models": [{"id": "reasoning-free"}]
		}]
	}`)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	p := f.Providers[0]
	if p.Protocol != "9router" {
		t.Errorf("protocol = %q", p.Protocol)
	}
	if p.Headers["X-Custom-Header"] != "custom-val" {
		t.Errorf("headers mismatch: %+v", p.Headers)
	}
	if !p.AllowUnlistedModels {
		t.Errorf("allow_unlisted_models should be true")
	}
	if !p.DisableQueueExec {
		t.Errorf("disable_queue_exec should be true")
	}
}

func TestFindProvider(t *testing.T) {
	if _, ok := FindProvider(nil, "any"); ok {
		t.Error("expected false for nil File")
	}

	f := &File{
		Providers: []ProviderEntry{
			{Name: "9router", Class: "api_call"},
			{Name: "agy", Class: "platform_dispatch"},
		},
	}

	p, ok := FindProvider(f, "9router")
	if !ok || p == nil || p.Name != "9router" {
		t.Fatalf("FindProvider(9router) failed: ok=%v, p=%+v", ok, p)
	}

	p, ok = FindProvider(f, "AGY")
	if !ok || p == nil || p.Name != "agy" {
		t.Fatalf("FindProvider(AGY) case-insensitive failed: ok=%v, p=%+v", ok, p)
	}

	if _, ok := FindProvider(f, "non-existent"); ok {
		t.Error("expected false for non-existent provider")
	}
}

func TestResolveProviderForModel(t *testing.T) {
	if _, err := ResolveProviderForModel(nil, "p", "m"); err == nil {
		t.Error("expected error for nil File")
	}

	f := &File{
		Providers: []ProviderEntry{
			{
				Name:  "nine-router",
				Class: "api_call",
				Models: []ModelEntry{
					{ID: "reasoning-free"},
					{ID: "nvidia/*"},
				},
			},
			{
				Name:  "agy",
				Class: "platform_dispatch",
				Models: []ModelEntry{
					{ID: "gemini-3.8-flash-high"},
				},
			},
			{
				Name:                "wildcard-prov",
				Class:               "api_call",
				AllowUnlistedModels: true,
			},
		},
	}

	// 1. By explicit provider name and model ID
	p, err := ResolveProviderForModel(f, "nine-router", "reasoning-free")
	if err != nil || p == nil || p.Name != "nine-router" {
		t.Fatalf("Resolve explicit match failed: %v, p=%+v", err, p)
	}

	// 2. By explicit provider name with glob model match
	p, err = ResolveProviderForModel(f, "nine-router", "nvidia/nemotron")
	if err != nil || p == nil || p.Name != "nine-router" {
		t.Fatalf("Resolve explicit glob match failed: %v, p=%+v", err, p)
	}

	// 3. By explicit provider name only (empty model)
	p, err = ResolveProviderForModel(f, "agy", "")
	if err != nil || p == nil || p.Name != "agy" {
		t.Fatalf("Resolve explicit name only failed: %v, p=%+v", err, p)
	}

	// 4. By model ID only
	p, err = ResolveProviderForModel(f, "", "gemini-3.8-flash-high")
	if err != nil || p == nil || p.Name != "agy" {
		t.Fatalf("Resolve model ID match failed: %v, p=%+v", err, p)
	}

	// 5. By model ID only matching glob
	p, err = ResolveProviderForModel(f, "", "nvidia/anything")
	if err != nil || p == nil || p.Name != "nine-router" {
		t.Fatalf("Resolve model ID glob match failed: %v, p=%+v", err, p)
	}

	// 6. Explicit provider not found
	_, err = ResolveProviderForModel(f, "ghost", "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error: %v", err)
	}

	// 7. Explicit provider doesn't serve model
	_, err = ResolveProviderForModel(f, "agy", "gpt-4o")
	if err == nil || !strings.Contains(err.Error(), "does not serve") {
		t.Fatalf("expected does not serve error: %v", err)
	}

	// 8. Model ID matches unlisted models provider
	p, err = ResolveProviderForModel(f, "wildcard-prov", "arbitrary-new-model")
	if err != nil || p.Name != "wildcard-prov" {
		t.Fatalf("expected wildcard-prov to serve arbitrary model: %v", err)
	}

	// 9. No provider serves model
	fNoWildcard := &File{Providers: f.Providers[:2]}
	_, err = ResolveProviderForModel(fNoWildcard, "", "unserved-model")
	if err == nil || !strings.Contains(err.Error(), "no provider found") {
		t.Fatalf("expected no provider found error: %v", err)
	}

	// 10. Both empty
	_, err = ResolveProviderForModel(f, "", "")
	if err == nil {
		t.Fatal("expected error when both provider name and model ID are empty")
	}
}
