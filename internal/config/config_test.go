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
