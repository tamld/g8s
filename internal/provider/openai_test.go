package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
)

func TestOpenAIProvider_AvailableAndAuth(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			gotAuthHeader = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data": [{"id": "gpt-4o"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("TEST_OPENAI_KEY", "secret-test-token-123")
	p := NewOpenAIProvider("test-ai", srv.URL, "TEST_OPENAI_KEY")

	err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("expected provider available, got: %v", err)
	}

	if gotAuthHeader != "Bearer secret-test-token-123" {
		t.Fatalf("expected Authorization header with token, got %q", gotAuthHeader)
	}
}

func TestOpenAIProvider_SpawnSuccess(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/chat/completions" {
			if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"choices": [
					{
						"message": {
							"role": "assistant",
							"content": "Synthesized code patch output"
						}
					}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	spec := Spec{
		Model:   "gpt-4o-mini",
		Brief:   "Review this module",
		Timeout: 5 * time.Second,
		Effort:  "xhigh",
	}

	handle, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	receipt, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if receipt.Status != "COMPLETED" {
		t.Fatalf("expected status COMPLETED, got %s", receipt.Status)
	}
	if receipt.Stdout != "Synthesized code patch output" {
		t.Fatalf("unexpected stdout: %s", receipt.Stdout)
	}

	// Verify protocol requirements (#636):
	// 1. stream is explicitly false
	if streamVal, ok := gotReq["stream"].(bool); !ok || streamVal != false {
		t.Errorf("expected stream: false in reqBody, got: %v", gotReq["stream"])
	}
	// 2. timeout must NOT be in JSON request body
	if _, ok := gotReq["timeout"]; ok {
		t.Errorf("timeout must NOT be in JSON request body, got: %v", gotReq["timeout"])
	}
	// 3. reasoning_effort normalized: "xhigh" -> "high"
	if effortVal, ok := gotReq["reasoning_effort"].(string); !ok || effortVal != "high" {
		t.Errorf("expected reasoning_effort: high, got: %v", gotReq["reasoning_effort"])
	}
}

func TestOpenAIProvider_ReasoningEffortNormalization(t *testing.T) {
	testCases := []struct {
		inputEffort  string
		wantEffort   string
		wantPresent  bool
		wantDegraded bool
	}{
		{"minimal", "low", true, true},
		{"low", "low", true, false},
		{"medium", "medium", true, false},
		{"high", "high", true, false},
		{"xhigh", "high", true, true},
		{"max", "high", true, true},
		{"ultra", "high", true, true},
		{"unsupported_level", "", false, true},
		{"", "", false, false},
	}

	for _, tc := range testCases {
		var gotReq map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/models" {
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Path == "/chat/completions" {
				_ = json.NewDecoder(r.Body).Decode(&gotReq)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "ok"}}]}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))

		p := NewOpenAIProvider("test-ai", srv.URL, "")
		spec := Spec{
			Model:  "test-model",
			Brief:  "prompt",
			Effort: tc.inputEffort,
		}

		h, err := p.Spawn(context.Background(), spec)
		if err != nil {
			srv.Close()
			t.Fatalf("effort %q: Spawn failed: %v", tc.inputEffort, err)
		}
		rcpt, err := h.Wait(context.Background())
		srv.Close()
		if err != nil {
			t.Fatalf("effort %q: Wait failed: %v", tc.inputEffort, err)
		}

		if !tc.wantPresent {
			if _, ok := gotReq["reasoning_effort"]; ok {
				t.Errorf("effort %q: expected reasoning_effort absent, got %v", tc.inputEffort, gotReq["reasoning_effort"])
			}
		} else {
			if got, ok := gotReq["reasoning_effort"].(string); !ok || got != tc.wantEffort {
				t.Errorf("effort %q: got reasoning_effort %q, want %q", tc.inputEffort, got, tc.wantEffort)
			}
		}
		if rcpt.EffortDegraded != tc.wantDegraded {
			t.Errorf("effort %q: EffortDegraded = %v, want %v (msg=%q)", tc.inputEffort, rcpt.EffortDegraded, tc.wantDegraded, rcpt.EffortDegradeMsg)
		}
		if tc.wantDegraded && rcpt.EffortDegradeMsg == "" {
			t.Errorf("effort %q: expected non-empty EffortDegradeMsg when EffortDegraded=true", tc.inputEffort)
		}
	}
}

func TestOpenAIProvider_MissingModelFailsEarly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	spec := Spec{
		Model: "", // Red test: empty model must be rejected
		Brief: "Task",
	}

	_, err := p.Spawn(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error on empty model, got nil")
	}
}

func TestOpenAIProvider_APIErrorHandling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error": {"message": "invalid_api_key"}}`))
			return
		}
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	spec := Spec{
		Model: "gpt-4o",
		Brief: "Task",
	}

	handle, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn unexpected error: %v", err)
	}

	receipt, err := handle.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error on API failure, got nil")
	}
	if receipt.Status != "FAILED" {
		t.Fatalf("expected status FAILED, got %s", receipt.Status)
	}
}

func TestOpenAIProvider_NewHTTPProviderFromConfig(t *testing.T) {
	t.Setenv("CUSTOM_GATEWAY_URL", "http://gateway.local:20128/v1///")
	entry := config.ProviderEntry{
		Class:      "api_call",
		Name:       "my-9router",
		Protocol:   "9router",
		BaseURL:    "http://default.local/v1",
		BaseURLEnv: "CUSTOM_GATEWAY_URL",
		AuthEnv:    "MY_AUTH_ENV",
		Headers: map[string]string{
			"X-Custom-Client": "g8s-worker",
		},
	}

	p := NewHTTPProviderFromConfig(entry)
	if p.Name() != "my-9router" {
		t.Errorf("expected name my-9router, got %q", p.Name())
	}
	if p.protocol != "9router" {
		t.Errorf("expected protocol 9router, got %q", p.protocol)
	}
	if p.EffectiveBaseURL() != "http://gateway.local:20128/v1" {
		t.Errorf("expected env override base URL, got %q", p.EffectiveBaseURL())
	}
	if p.headers["X-Custom-Client"] != "g8s-worker" {
		t.Errorf("headers mismatch: %+v", p.headers)
	}
	if p.httpClient.Timeout != 120*time.Second {
		t.Errorf("expected 120s timeout, got %v", p.httpClient.Timeout)
	}
}

func TestOpenAIProvider_Available_9RouterFallback(t *testing.T) {
	// 9router where /models returns 404, but /api/health returns 200 OK {"ok":true}
	var hitHealth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/api/health" {
			hitHealth = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("9router", srv.URL+"/v1", "")
	p.protocol = "9router"

	err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("expected 9router Available fallback to succeed, got: %v", err)
	}
	if !hitHealth {
		t.Fatal("expected /api/health to be probed on 9router")
	}

	// When both fail
	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvFail.Close()

	pFail := NewOpenAIProvider("9router", srvFail.URL+"/v1", "")
	pFail.protocol = "9router"
	if err := pFail.Available(context.Background()); err == nil {
		t.Fatal("expected error when both /models and /api/health fail")
	}
}

func TestOpenAIProvider_Available_Anthropic404(t *testing.T) {
	// Standard Anthropic endpoints do not always expose GET /v1/models (returns 404/405)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("anthropic-provider", srv.URL+"/v1", "")
	p.protocol = "anthropic"

	err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("expected anthropic 404 on /models to be treated as available, got: %v", err)
	}
}

func TestOpenAIProvider_DiscoverModels(t *testing.T) {
	var gotAuth, gotCustomHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			gotAuth = r.Header.Get("Authorization")
			gotCustomHeader = r.Header.Get("X-Discover-Header")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"data": [
					{"id": "reasoning-high"},
					{"id": "fast-agent"},
					{"id": "claude-3.5-sonnet"}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("TEST_DISCOVER_KEY", "disc-token-456")
	p := NewHTTPProviderFromConfig(config.ProviderEntry{
		Name:     "nine-test",
		BaseURL:  srv.URL,
		AuthEnv:  "TEST_DISCOVER_KEY",
		Protocol: "openai",
		Headers:  map[string]string{"X-Discover-Header": "found-it"},
	})

	models, err := p.DiscoverModels(context.Background())
	if err != nil {
		t.Fatalf("DiscoverModels failed: %v", err)
	}
	want := []string{"claude-3.5-sonnet", "fast-agent", "reasoning-high"}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("DiscoverModels = %v, want %v", models, want)
	}
	if gotAuth != "Bearer disc-token-456" {
		t.Errorf("expected Bearer token, got %q", gotAuth)
	}
	if gotCustomHeader != "found-it" {
		t.Errorf("expected custom header, got %q", gotCustomHeader)
	}
}

func TestOpenAIProvider_Spawn_AnthropicProtocol(t *testing.T) {
	var (
		gotPath         string
		gotAPIKey       string
		gotAuthHeader   string
		gotVersion      string
		gotCustomHeader string
		gotBody         map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/v1/messages" {
			gotPath = r.URL.Path
			gotAPIKey = r.Header.Get("x-api-key")
			gotAuthHeader = r.Header.Get("Authorization")
			gotVersion = r.Header.Get("anthropic-version")
			gotCustomHeader = r.Header.Get("X-Anth-Custom")

			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": "msg_123",
				"type": "message",
				"role": "assistant",
				"model": "claude-3-5-sonnet-20241022",
				"content": [
					{"type": "text", "text": "Anthropic generated patch"}
				],
				"usage": {
					"input_tokens": 42,
					"output_tokens": 84
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_KEY", "sk-ant-test-999")
	p := NewHTTPProviderFromConfig(config.ProviderEntry{
		Name:     "anthropic-direct",
		BaseURL:  srv.URL + "/v1",
		AuthEnv:  "ANTHROPIC_KEY",
		Protocol: "anthropic",
		Headers:  map[string]string{"X-Anth-Custom": "enabled"},
	})

	spec := Spec{
		Model:        "claude-3.5-sonnet",
		Brief:        "Write code",
		SystemPrompt: "You are an expert",
		MaxTokens:    2048,
	}

	h, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn anthropic failed: %v", err)
	}

	receipt, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if receipt.Status != "COMPLETED" {
		t.Fatalf("expected COMPLETED, got %s", receipt.Status)
	}
	if receipt.Stdout != "Anthropic generated patch" {
		t.Fatalf("stdout = %q, want 'Anthropic generated patch'", receipt.Stdout)
	}
	if receipt.ModelResolved != "claude-3-5-sonnet-20241022" {
		t.Fatalf("ModelResolved = %q, want 'claude-3-5-sonnet-20241022'", receipt.ModelResolved)
	}
	if receipt.InputTokens != 42 || receipt.OutputTokens != 84 || receipt.TotalTokens != 126 {
		t.Fatalf("token accounting mismatch: input=%d, output=%d, total=%d", receipt.InputTokens, receipt.OutputTokens, receipt.TotalTokens)
	}

	// Verify headers & body
	if gotPath != "/v1/messages" {
		t.Errorf("got path %s, want /v1/messages", gotPath)
	}
	if gotAPIKey != "sk-ant-test-999" || gotAuthHeader != "Bearer sk-ant-test-999" {
		t.Errorf("auth headers mismatch: apiKey=%q, auth=%q", gotAPIKey, gotAuthHeader)
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("version = %q, want 2023-06-01", gotVersion)
	}
	if gotCustomHeader != "enabled" {
		t.Errorf("custom header = %q, want enabled", gotCustomHeader)
	}
	if gotBody["model"] != "claude-3.5-sonnet" {
		t.Errorf("body model = %v", gotBody["model"])
	}
	if gotBody["system"] != "You are an expert" {
		t.Errorf("body system = %v", gotBody["system"])
	}
	if mt, ok := gotBody["max_tokens"].(float64); !ok || int(mt) != 2048 {
		t.Errorf("body max_tokens = %v", gotBody["max_tokens"])
	}
}

func TestOpenAIProvider_Spawn_9Router_AnthropicPrefix(t *testing.T) {
	var gotModelInBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/v1/messages" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if m, ok := body["model"].(string); ok {
				gotModelInBody = m
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"content": [{"type": "text", "text": "prefix stripped response"}],
				"model": "claude-3-5-sonnet",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewHTTPProviderFromConfig(config.ProviderEntry{
		Name:     "9router",
		BaseURL:  srv.URL + "/v1",
		Protocol: "9router",
	})

	spec := Spec{
		Model: "anthropic-messages/claude-3.5-sonnet",
		Brief: "Hello Anthropic through 9router",
	}

	h, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn 9router with anthropic prefix failed: %v", err)
	}
	receipt, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if receipt.Stdout != "prefix stripped response" {
		t.Fatalf("unexpected stdout: %s", receipt.Stdout)
	}
	if gotModelInBody != "claude-3.5-sonnet" {
		t.Fatalf("expected anthropic-messages/ to be stripped in body, got: %q", gotModelInBody)
	}
}

func TestOpenAIProvider_Spawn_OpenAI_ReasoningContentFallbackAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"model": "deepseek/deepseek-r1-distill",
				"choices": [
					{
						"message": {
							"role": "assistant",
							"content": "",
							"reasoning_content": "Deep reasoning steps executed successfully"
						}
					}
				],
				"usage": {
					"prompt_tokens": 120,
					"completion_tokens": 340,
					"total_tokens": 460
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewHTTPProviderFromConfig(config.ProviderEntry{
		Name:     "9router",
		BaseURL:  srv.URL,
		Protocol: "openai",
	})

	spec := Spec{
		Model: "deep-coding",
		Brief: "Solve this hard bug",
	}

	h, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	receipt, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if receipt.Status != "COMPLETED" {
		t.Fatalf("expected COMPLETED, got %s", receipt.Status)
	}
	if receipt.Stdout != "Deep reasoning steps executed successfully" {
		t.Fatalf("expected fallback to reasoning_content, got: %q", receipt.Stdout)
	}
	if receipt.ModelResolved != "deepseek/deepseek-r1-distill" {
		t.Fatalf("expected ModelResolved 'deepseek/deepseek-r1-distill', got: %q", receipt.ModelResolved)
	}
	if receipt.InputTokens != 120 || receipt.OutputTokens != 340 || receipt.TotalTokens != 460 {
		t.Fatalf("unexpected token counts: in=%d, out=%d, tot=%d", receipt.InputTokens, receipt.OutputTokens, receipt.TotalTokens)
	}
}

func TestPoolRegistry_SelectForModel_PrefixGlob(t *testing.T) {
	configs := []Config{
		{
			Name:           "nvidia-pool",
			Class:          "api_call",
			MaxConcurrency: 4,
			Models: []ModelDescriptor{
				{ID: "nvidia/*"},
			},
		},
		{
			Name:           "anthropic-pool",
			Class:          "api_call",
			MaxConcurrency: 4,
			Models: []ModelDescriptor{
				{ID: "anthropic/*"},
			},
		},
		{
			Name:           "exact-pool",
			Class:          "platform_dispatch",
			MaxConcurrency: 2,
			Models: []ModelDescriptor{
				{ID: "exact-model"},
			},
		},
	}

	reg := NewPoolRegistry(configs, time.Now, func(string) (string, error) { return "/bin/echo", nil })
	// Set them ready in states
	reg.mu.Lock()
	for _, name := range []string{"nvidia-pool", "anthropic-pool", "exact-pool"} {
		st := reg.states[name]
		st.last = ProviderInfo{Name: name, Status: StatusReady}
	}
	reg.mu.Unlock()

	// 1. nvidia/* prefix glob
	name, release, err := reg.SelectForModel(context.Background(), "nvidia/nemotron-4-340b")
	if err != nil || name != "nvidia-pool" {
		t.Fatalf("nvidia/* match failed: name=%s, err=%v", name, err)
	}
	release()

	// 2. anthropic/* prefix glob
	name, release, err = reg.SelectForModel(context.Background(), "anthropic/claude-3-opus")
	if err != nil || name != "anthropic-pool" {
		t.Fatalf("anthropic/* match failed: name=%s, err=%v", name, err)
	}
	release()

	// 3. exact match
	name, release, err = reg.SelectForModel(context.Background(), "exact-model")
	if err != nil || name != "exact-pool" {
		t.Fatalf("exact match failed: name=%s, err=%v", name, err)
	}
	release()

	// 4. unmatched model
	_, _, err = reg.SelectForModel(context.Background(), "unmatched-model")
	if err == nil {
		t.Fatal("expected error on unmatched model")
	}
}

func TestPoolRegistry_probeHTTP_V1Fallback(t *testing.T) {
	// Case 1: /v1 returns 404, /v1/models returns 200
	srvModels := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srvModels.Close()

	reg := NewPoolRegistry(nil, nil, nil)
	status := reg.probeHTTP(context.Background(), srvModels.URL+"/v1")
	if status != StatusReady {
		t.Errorf("expected StatusReady for /v1/models fallback, got %s", status)
	}

	// Case 2: /v1 returns 404, /v1/models returns 404, /api/health returns 200
	srvHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srvHealth.Close()

	status = reg.probeHTTP(context.Background(), srvHealth.URL+"/v1")
	if status != StatusReady {
		t.Errorf("expected StatusReady for /api/health fallback, got %s", status)
	}

	// Case 3: all return 500
	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvFail.Close()

	status = reg.probeHTTP(context.Background(), srvFail.URL+"/v1")
	if status != StatusDegraded {
		t.Errorf("expected StatusDegraded when all return 500, got %s", status)
	}
}

func TestOpenAIProvider_ProtocolMismatch400Hint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" || r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"upstream requested an unsupported IDE tool: diff_view"}`))
	}))
	defer srv.Close()

	p := NewOpenAIProvider("9router", srv.URL, "")
	spec := Spec{
		Model: "custom/ide-only-model",
		Brief: "Test Protocol Mismatch 400 Hint",
	}

	handle, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn unexpected error: %v", err)
	}

	receipt, err := handle.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error on HTTP 400, got nil")
	}
	if receipt.Status != "FAILED" {
		t.Fatalf("expected status FAILED, got %s", receipt.Status)
	}
	if !strings.Contains(err.Error(), "protocol/tool mismatch on the chat-completions surface") {
		t.Fatalf("expected remediation hint in error, got: %s", err.Error())
	}
}

func TestOpenAIProvider_AsyncCancelInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	canceledCh := make(chan struct{})
	shutdown := make(chan struct{})
	var startOnce, cancelOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/chat/completions" {
			_, _ = io.Copy(io.Discard, r.Body)
			startOnce.Do(func() { close(started) })
			select {
			case <-r.Context().Done():
				cancelOnce.Do(func() { close(canceledCh) })
			case <-shutdown:
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	defer close(shutdown)

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	if p.TransportKind() != TransportKindRequest {
		t.Fatalf("expected TransportKindRequest, got %q", p.TransportKind())
	}

	spawnStart := time.Now()
	rh, err := p.StartRequest(context.Background(), Spec{Model: "gpt-5.5", Brief: "slow"})
	if err != nil {
		t.Fatalf("StartRequest: %v", err)
	}
	if time.Since(spawnStart) > 500*time.Millisecond {
		t.Fatalf("StartRequest blocked synchronously (%v)", time.Since(spawnStart))
	}
	if rh.RequestID() == "" || rh.TransportKind() != TransportKindRequest {
		t.Fatalf("invalid RequestHandle metadata: id=%q kind=%q", rh.RequestID(), rh.TransportKind())
	}

	<-started
	if err := rh.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitDone := make(chan Receipt, 1)
	waitErrCh := make(chan error, 1)
	go func() {
		rc, wErr := rh.Wait(context.Background())
		waitDone <- rc
		waitErrCh <- wErr
	}()

	select {
	case rc := <-waitDone:
		wErr := <-waitErrCh
		if rc.Status != "CANCELLED" || !errors.Is(wErr, context.Canceled) {
			t.Fatalf("expected CANCELLED/context.Canceled, got status=%q err=%v", rc.Status, wErr)
		}
		if rc.TransportKind != TransportKindRequest || rc.RequestID != rh.RequestID() {
			t.Fatalf("receipt missing request metadata: %+v", rc)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not abort promptly after Cancel()")
	}
	select {
	case <-canceledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected HTTP request context on server to be cancelled")
	}

	// Verify legacy Spawn wrapper returns NoPID (-1), never 0
	h, err := p.Spawn(context.Background(), Spec{Model: "gpt-5.5", Brief: "slow2"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if h.PID() != NoPID {
		t.Fatalf("expected NoPID (%d), got %d", NoPID, h.PID())
	}
	_ = h.Cancel(context.Background())
	_, _ = h.Wait(context.Background())
}

func TestOpenAIProvider_SSEStreamingAndStdoutStream(t *testing.T) {
	var gotStream bool
	var gotIncludeUsage bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/chat/completions" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotStream, _ = body["stream"].(bool)
			if opts, ok := body["stream_options"].(map[string]any); ok {
				gotIncludeUsage, _ = opts["include_usage"].(bool)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			_, _ = fmt.Fprintln(w, `data: {"id":"1","model":"gpt-5.5","choices":[{"delta":{"content":"Hello "}}]}`)
			if fl != nil {
				fl.Flush()
			}
			_, _ = fmt.Fprintln(w, `data: {"id":"1","model":"gpt-5.5","choices":[{"delta":{"content":"Stream"}}],"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}}`)
			_, _ = fmt.Fprintln(w, `data: [DONE]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	h, err := p.Spawn(context.Background(), Spec{Model: "gpt-5.5", Brief: "stream it", Stream: true})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rc, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !gotStream || !gotIncludeUsage || !rc.Streamed {
		t.Fatalf("expected streaming flags (gotStream=%v, includeUsage=%v, rc.Streamed=%v)", gotStream, gotIncludeUsage, rc.Streamed)
	}
	if rc.Stdout != "Hello Stream" || rc.TotalTokens != 15 || rc.ModelResolved != "gpt-5.5" {
		t.Fatalf("unexpected SSE receipt: %+v", rc)
	}
	streamBytes, _ := io.ReadAll(h.StdoutStream())
	if string(streamBytes) != "Hello Stream" {
		t.Fatalf("StdoutStream() = %q, want %q", string(streamBytes), "Hello Stream")
	}
}

func TestOpenAIProvider_ToolsAndResponseFormatForwarding(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/chat/completions" {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	spec := Spec{
		Model: "gpt-5.5",
		Brief: "use tools",
		Tools: []map[string]any{
			{"type": "function", "function": map[string]any{"name": "read_file"}},
		},
		ResponseFormat: map[string]any{"type": "json_object"},
	}
	h, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := h.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if tools, ok := gotBody["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool in request body, got %#v", gotBody["tools"])
	}
	if rf, ok := gotBody["response_format"].(map[string]any); !ok || rf["type"] != "json_object" {
		t.Fatalf("expected response_format json_object, got %#v", gotBody["response_format"])
	}
}

func TestOpenAIProvider_AnthropicEffortAndStreaming(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/v1/messages" {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintln(w, `data: {"type":"message_start","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":10,"output_tokens":0}}}`)
			_, _ = fmt.Fprintln(w, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Anth "}}`)
			_, _ = fmt.Fprintln(w, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"SSE"}}`)
			_, _ = fmt.Fprintln(w, `data: {"type":"message_delta","usage":{"output_tokens":7}}`)
			_, _ = fmt.Fprintln(w, `data: {"type":"message_stop"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewHTTPProviderFromConfig(config.ProviderEntry{
		Name:     "anthropic",
		BaseURL:  srv.URL + "/v1",
		Protocol: "anthropic",
	})
	h, err := p.Spawn(context.Background(), Spec{
		Model:  "claude-sonnet-5-5",
		Brief:  "stream",
		Effort: "high",
		Stream: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rc, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if oc, ok := gotBody["output_config"].(map[string]any); !ok || oc["effort"] != "high" {
		t.Fatalf("expected output_config.effort=high in anthropic request, got %#v", gotBody["output_config"])
	}
	if rc.Stdout != "Anth SSE" || !rc.Streamed || rc.InputTokens != 10 || rc.OutputTokens != 7 || rc.TotalTokens != 17 {
		t.Fatalf("unexpected anthropic SSE receipt: %+v", rc)
	}
	if rc.EffortDegraded || rc.EffortApplied != "high" {
		t.Fatalf("unexpected effort telemetry: %+v", rc)
	}
}

func TestOpenAIProvider_StrictCapabilitiesRejectsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" || r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Fatalf("unexpected dispatch to %s despite failing capability gate", r.URL.Path)
	}))
	defer srv.Close()

	boolFalse := false
	boolTrue := true
	cat := &config.Catalog{
		SchemaVersion:  config.CurrentCatalogSchemaVersion,
		CatalogVersion: 4,
		VerifiedAt:     "2026-11-19",
		Providers: map[string]config.ProviderCatalog{
			"openai": {
				Name:         "openai",
				EffortStyle:  config.EffortStyleNamed,
				Capabilities: &config.ModelCapabilities{ToolCalling: &boolTrue, ReasoningEffort: &boolTrue},
				Models: []config.ModelCatalog{
					{
						ID:           "gpt-6-astra",
						Capabilities: &config.ModelCapabilities{ToolCalling: &boolFalse},
					},
				},
			},
			"anthropic": {
				Name:         "anthropic",
				EffortStyle:  config.EffortStyleNamed,
				Capabilities: &config.ModelCapabilities{ReasoningEffort: &boolTrue},
				Models: []config.ModelCatalog{
					{
						ID:           "claude-haiku-4-5",
						Capabilities: &config.ModelCapabilities{ReasoningEffort: &boolFalse},
					},
				},
			},
		},
	}

	oai := NewOpenAIProvider("openai", srv.URL, "").WithCatalog(cat)
	// 1. Strict unsupported OpenAI effort
	if _, err := oai.StartRequest(context.Background(), Spec{Model: "gpt-6-astra", Brief: "x", Effort: "xhigh", StrictCapabilities: true}); !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for strict xhigh on openai, got %v", err)
	}
	if _, err := oai.StartRequest(context.Background(), Spec{Model: "gpt-6-astra", Brief: "x", Effort: "unsupported_level", StrictCapabilities: true}); !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for strict unknown effort, got %v", err)
	}
	// 2. Tools unsupported by catalog on gpt-6-astra
	if _, err := oai.StartRequest(context.Background(), Spec{
		Model: "gpt-6-astra",
		Brief: "x",
		Tools: []map[string]any{{"type": "function"}},
	}); !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for tools on gpt-6-astra, got %v", err)
	}

	// 3. Anthropic strict unknown effort & catalog reasoning_effort=false
	anth := NewHTTPProviderFromConfig(config.ProviderEntry{Name: "anthropic", BaseURL: srv.URL + "/v1", Protocol: "anthropic"}).WithCatalog(cat)
	if _, err := anth.StartRequest(context.Background(), Spec{Model: "claude-sonnet-5-5", Brief: "x", Effort: "turbo", StrictCapabilities: true}); !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for strict anthropic effort, got %v", err)
	}
	if _, err := anth.StartRequest(context.Background(), Spec{Model: "claude-haiku-4-5", Brief: "x", Effort: "high", StrictCapabilities: true}); !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported for haiku effort, got %v", err)
	}
}
