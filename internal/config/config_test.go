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
      "models": [{"id": "gemini-3.7-flash-high", "context_window": 1000000}],
      "slots": 4
    },
    {
      "class": "platform_dispatch",
      "name": "agy",
      "models": [{"id": "gemini-3.7-flash-high"}]
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
	if api.Class != "api_call" || api.BaseURL == "" || api.AuthEnv != "NINE_ROUTER_API_KEY" || api.Slots != 4 || api.Models[0].ID != "gemini-3.7-flash-high" {
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
      "models": [{"id": "gemini-3.7-flash-high"}],
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
