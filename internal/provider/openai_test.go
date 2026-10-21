package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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
		inputEffort string
		wantEffort  string
		wantPresent bool
	}{
		{"minimal", "low", true},
		{"low", "low", true},
		{"medium", "medium", true},
		{"high", "high", true},
		{"xhigh", "high", true},
		{"max", "high", true},
		{"ultra", "high", true},
		{"unsupported_level", "", false},
		{"", "", false},
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
		_, _ = h.Wait(context.Background())
		srv.Close()

		if !tc.wantPresent {
			if _, ok := gotReq["reasoning_effort"]; ok {
				t.Errorf("effort %q: expected reasoning_effort absent, got %v", tc.inputEffort, gotReq["reasoning_effort"])
			}
		} else {
			if got, ok := gotReq["reasoning_effort"].(string); !ok || got != tc.wantEffort {
				t.Errorf("effort %q: got reasoning_effort %q, want %q", tc.inputEffort, got, tc.wantEffort)
			}
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
	if int(gotBody["max_tokens"].(float64)) != 2048 {
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
			Name:           "cc-pool",
			Class:          "api_call",
			MaxConcurrency: 4,
			Models: []ModelDescriptor{
				{ID: "cc/*"},
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
	for _, name := range []string{"nvidia-pool", "cc-pool", "exact-pool"} {
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

	// 2. cc/* prefix glob
	name, release, err = reg.SelectForModel(context.Background(), "cc/claude-3-opus")
	if err != nil || name != "cc-pool" {
		t.Fatalf("cc/* match failed: name=%s, err=%v", name, err)
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
