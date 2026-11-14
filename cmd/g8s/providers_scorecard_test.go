package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/harness/probe"
	"github.com/tamld/g8s/internal/provider"
)

func TestClassifyProbeFailure(t *testing.T) {
	tests := []struct {
		name         string
		success      bool
		verifyPassed bool
		errMsg       string
		expected     string
	}{
		{
			name:         "HEALTHY",
			success:      true,
			verifyPassed: true,
			errMsg:       "",
			expected:     "HEALTHY",
		},
		{
			name:         "CANARY_MISMATCH",
			success:      true,
			verifyPassed: false,
			errMsg:       "canary mismatch",
			expected:     "CANARY_MISMATCH",
		},
		{
			name:         "MODEL_EOL_410_status",
			success:      false,
			verifyPassed: false,
			errMsg:       "status 410 Gone",
			expected:     "MODEL_EOL_410",
		},
		{
			name:         "MODEL_EOL_410_bracket",
			success:      false,
			verifyPassed: false,
			errMsg:       "[410] model retired",
			expected:     "MODEL_EOL_410",
		},
		{
			name:         "MODEL_EOL_410_end_of_life",
			success:      false,
			verifyPassed: false,
			errMsg:       "end of life for this model",
			expected:     "MODEL_EOL_410",
		},
		{
			name:         "MODEL_EOL_410_no_longer_available",
			success:      false,
			verifyPassed: false,
			errMsg:       "model no longer available",
			expected:     "MODEL_EOL_410",
		},
		{
			name:         "MODEL_NOT_FOUND_404_status",
			success:      false,
			verifyPassed: false,
			errMsg:       "status 404 Not Found",
			expected:     "MODEL_NOT_FOUND_404",
		},
		{
			name:         "MODEL_NOT_FOUND_404_bracket",
			success:      false,
			verifyPassed: false,
			errMsg:       "[404] missing",
			expected:     "MODEL_NOT_FOUND_404",
		},
		{
			name:         "MODEL_NOT_FOUND_404_not_found",
			success:      false,
			verifyPassed: false,
			errMsg:       "model not found",
			expected:     "MODEL_NOT_FOUND_404",
		},
		{
			name:         "MODEL_NOT_FOUND_404_page_not_found",
			success:      false,
			verifyPassed: false,
			errMsg:       "page not found",
			expected:     "MODEL_NOT_FOUND_404",
		},
		{
			name:         "PROTOCOL_MISMATCH_400_status",
			success:      false,
			verifyPassed: false,
			errMsg:       "status 400 Bad Request",
			expected:     "PROTOCOL_MISMATCH_400",
		},
		{
			name:         "PROTOCOL_MISMATCH_400_agentservice",
			success:      false,
			verifyPassed: false,
			errMsg:       "AgentService requested an unsupported IDE tool",
			expected:     "PROTOCOL_MISMATCH_400",
		},
		{
			name:         "TIMEOUT_context_deadline",
			success:      false,
			verifyPassed: false,
			errMsg:       "context deadline exceeded",
			expected:     "TIMEOUT",
		},
		{
			name:         "TIMEOUT_client_timeout",
			success:      false,
			verifyPassed: false,
			errMsg:       "Client.Timeout exceeded",
			expected:     "TIMEOUT",
		},
		{
			name:         "UPSTREAM_ERROR_default",
			success:      false,
			verifyPassed: false,
			errMsg:       "internal server error",
			expected:     "UPSTREAM_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyProbeFailure(tt.success, tt.verifyPassed, tt.errMsg)
			if result != tt.expected {
				t.Errorf("classifyProbeFailure(%v, %v, %q) = %q, want %q", tt.success, tt.verifyPassed, tt.errMsg, result, tt.expected)
			}
		})
	}
}

func TestEvalCombosDiscoverCombos(t *testing.T) {
	// Mock HTTP provider that returns discovered models
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"reasoning-free"},{"id":"reasoning-high"},{"id":"deep-coding"},{"id":"docs-retriever"},{"id":"fast-agent"},{"id":"cu/claude-4"},{"id":"nvidia/nvidia/nemotron-3-ultra"}]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/chat/completions") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","model":"test","choices":[{"message":{"content":"READY_OK"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	entry := provider.NewHTTPProviderFromConfig(config.ProviderEntry{
		Class:               "api_call",
		Name:                "9router",
		Protocol:            "9router",
		BaseURL:             server.URL + "/v1",
		BaseURLEnv:          "NINEROUTER_URL",
		AuthEnv:             "OPENAI_API_KEY",
		AllowUnlistedModels: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test DiscoverModels
	models, err := entry.DiscoverModels(ctx)
	if err != nil {
		t.Fatalf("DiscoverModels failed: %v", err)
	}

	// Filter top-level combos (no slash)
	var combos []string
	for _, m := range models {
		if !strings.Contains(m, "/") {
			combos = append(combos, m)
		}
	}

	expectedCombos := []string{"deep-coding", "docs-retriever", "fast-agent", "reasoning-free", "reasoning-high"}
	if len(combos) != len(expectedCombos) {
		t.Errorf("expected %d combos, got %d: %v", len(expectedCombos), len(combos), combos)
	}
	for i, c := range combos {
		if c != expectedCombos[i] {
			t.Errorf("combo[%d] = %q, want %q", i, c, expectedCombos[i])
		}
	}

	// Test probe with MaxTokens >= 512
	spec := provider.Spec{
		Brief:     "Reply with the exact single token: READY_OK",
		Model:     "fast-agent",
		MaxTokens: 512,
		Timeout:   10 * time.Second,
	}
	handle, err := entry.Spawn(ctx, spec)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	res, err := handle.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if spec.MaxTokens != 512 {
		t.Errorf("expected MaxTokens 512 in spec, got %d", spec.MaxTokens)
	}
	if !strings.Contains(res.Stdout, "READY_OK") {
		t.Errorf("expected READY_OK in response, got %q", res.Stdout)
	}
}

func TestResolveCallableProviderAndCatalogMetadata(t *testing.T) {
	ctx := context.Background()
	t.Setenv("G8S_PROVIDERS", filepath.Join(t.TempDir(), "absent-providers.json"))

	// 1. Default 9router resolution
	prov, err := resolveCallableProvider(ctx, "9router")
	if err != nil {
		t.Fatalf("resolveCallableProvider(9router) failed: %v", err)
	}
	if prov == nil || prov.Name() != "9router" {
		t.Fatalf("expected 9router provider, got %+v", prov)
	}

	// 2. Declarative catalog metadata lookup (Gemini 4 active vs Gemini 3.8 deprecated)
	cat, err := config.LoadDefaultCatalog()
	if err != nil {
		t.Fatalf("LoadDefaultCatalog: %v", err)
	}
	fam, tier, st := lookupDeclarativeModelMeta(nil, cat, "agy", "gemini-4-hydrogen-high")
	if fam != "google" || tier != "economy" || st != "active" {
		t.Errorf("gemini-4-hydrogen-high meta = (%q, %q, %q), want (google, economy, active)", fam, tier, st)
	}
	_, _, depStatus := lookupDeclarativeModelMeta(nil, cat, "agy", "gemini-3.8-flash-high")
	if depStatus != "deprecated" {
		t.Errorf("gemini-3.8-flash-high status = %q, want deprecated", depStatus)
	}

	// 3. Unknown provider fails closed
	if _, err := resolveCallableProvider(ctx, "unknown-provider-xyz"); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestEvalRun_MultiAdapterResolutionAndContractPrompt(t *testing.T) {
	var capturedPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" || r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"reasoning-high"}]}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if msgs, ok := body["messages"].([]any); ok && len(msgs) > 0 {
			if m0, ok := msgs[len(msgs)-1].(map[string]any); ok {
				capturedPrompt, _ = m0["content"].(string)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"eval-1","model":"reasoning-high","choices":[{"message":{"content":"BLOCKED: request refused under zero-trust WriteReceipt contract."}}]}`))
	}))
	defer srv.Close()

	manifestPath := filepath.Join(t.TempDir(), "providers.json")
	manifestJSON := `{"providers":[{"class":"api_call","name":"9router","protocol":"9router","base_url":"` + srv.URL + `","auth_env":"OPENAI_API_KEY","default_model":"reasoning-high","slots":4,"allow_unlisted_models":true,"models":[{"id":"reasoning-high","context_window":200000}]}]}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	t.Setenv("G8S_PROVIDERS", manifestPath)
	t.Setenv("OPENAI_API_KEY", "test-key")

	ctx := context.Background()
	resolved, err := resolveCallableProvider(ctx, "9router")
	if err != nil {
		t.Fatalf("resolveCallableProvider(9router): %v", err)
	}
	adapter := probe.NewProviderWorkerAdapter(resolved, "reasoning-high", false, 5*time.Second)
	suite := probe.FilterSuite(probe.DefaultSuite(), []string{"pi-001", "re-001"}, "")
	pri, err := probe.RunSuite(ctx, suite, adapter)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if pri.Score != 1.0 || pri.PassedProbes != 2 {
		t.Fatalf("expected 100%% PRI score across pi-001 and re-001, got score=%v passed=%d details=%+v", pri.Score, pri.PassedProbes, pri.Details)
	}
	if !strings.Contains(capturedPrompt, "[G8S F1 ZERO-TRUST SECURITY HARNESS CONTRACT]") {
		t.Fatalf("expected wrapped harness contract prompt, got %q", capturedPrompt)
	}
}
