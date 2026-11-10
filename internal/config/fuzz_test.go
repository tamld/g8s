package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func FuzzConfigLoad(f *testing.F) {
	// Seed 1: minimal valid file (platform_dispatch)
	f.Add([]byte(`{"providers":[{"class":"platform_dispatch","name":"agy","models":[{"id":"gemini-3.8-flash-high"}]}]}`))

	// Seed 2: two-class file (api_call + platform_dispatch)
	f.Add([]byte(`{
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
}`))

	// Seed 3: malformed JSON
	f.Add([]byte(`{"providers": [`))

	// Seed 4: wrong class value
	f.Add([]byte(`{"providers":[{"class":"quantum","name":"q","models":[{"id":"m"}]}]}`))

	// Seed 5: missing required base_url for api_call
	f.Add([]byte(`{"providers":[{"class":"api_call","name":"x","models":[{"id":"m"}],"slots":1}]}`))

	// Seed 6: missing required slots for api_call
	f.Add([]byte(`{"providers":[{"class":"api_call","name":"x","base_url":"http://p","models":[{"id":"m"}]}]}`))

	// Seed 7: missing required name for platform_dispatch
	f.Add([]byte(`{"providers":[{"class":"platform_dispatch","models":[{"id":"m"}]}]}`))

	// Seed 8: missing required models
	f.Add([]byte(`{"providers":[{"class":"platform_dispatch","name":"bare"}]}`))

	// Seed 9: empty bytes
	f.Add([]byte{})

	// Seed 10: api_call with base_url_env and no base_url
	f.Add([]byte(`{"providers":[{"class":"api_call","name":"x","base_url_env":"NINEROUTER_URL","models":[{"id":"m"}],"slots":1}]}`))

	// Seed 11: allow_unlisted_models with empty models
	f.Add([]byte(`{"providers":[{"class":"api_call","name":"x","base_url":"http://p","allow_unlisted_models":true,"models":[],"slots":1}]}`))

	tempDir := f.TempDir()

	f.Fuzz(func(t *testing.T, data []byte) {
		tmpFile, err := os.CreateTemp(tempDir, "fuzz-cfg-*.json")
		if err != nil {
			t.Fatalf("create temp config file: %v", err)
		}
		path := tmpFile.Name()
		defer os.Remove(path)

		if _, err := tmpFile.Write(data); err != nil {
			_ = tmpFile.Close()
			t.Fatalf("write temp config file: %v", err)
		}
		if err := tmpFile.Close(); err != nil {
			t.Fatalf("close temp config file: %v", err)
		}

		cfg, err := Load(path)
		if err == nil {
			if cfg == nil {
				t.Fatal("Load succeeded but returned nil config")
			}
			for i, p := range cfg.Providers {
				switch p.Class {
				case classAPICall:
					if strings.TrimSpace(p.Name) == "" {
						t.Fatalf("provider %d: api_call accepted with empty name", i)
					}
					if strings.TrimSpace(p.BaseURL) == "" && strings.TrimSpace(p.BaseURLEnv) == "" {
						t.Fatalf("provider %d (%s): api_call accepted without base_url or base_url_env", i, p.Name)
					}
					if len(p.Models) == 0 && !p.AllowUnlistedModels {
						t.Fatalf("provider %d (%s): api_call accepted with empty models", i, p.Name)
					}
					if p.Slots < 1 {
						t.Fatalf("provider %d (%s): api_call accepted with slots < 1 (%d)", i, p.Name, p.Slots)
					}
				case classPlatformDispatch:
					if strings.TrimSpace(p.Name) == "" {
						t.Fatalf("provider %d: platform_dispatch accepted with empty name", i)
					}
					if len(p.Models) == 0 && !p.AllowUnlistedModels {
						t.Fatalf("provider %d (%s): platform_dispatch accepted with empty models", i, p.Name)
					}
				default:
					t.Fatalf("provider %d: accepted unknown class %q", i, p.Class)
				}
				for j, m := range p.Models {
					if strings.TrimSpace(m.ID) == "" {
						t.Fatalf("provider %d (%s): model %d accepted with empty id", i, p.Name, j)
					}
				}
			}
		}

		// Spot-check invariant: a class: api_call payload without base_url must error
		var raw File
		if jsonErr := json.Unmarshal(data, &raw); jsonErr == nil {
			for _, p := range raw.Providers {
				if p.Class == classAPICall && strings.TrimSpace(p.BaseURL) == "" && strings.TrimSpace(p.BaseURLEnv) == "" {
					if err == nil {
						t.Fatalf("invariant violated: api_call without base_url or base_url_env was accepted: %+v", p)
					}
				}
			}
		}
	})
}
