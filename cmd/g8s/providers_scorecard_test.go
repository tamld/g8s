package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
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
