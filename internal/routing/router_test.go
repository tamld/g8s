package routing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/harness/probe"
	"github.com/tamld/g8s/internal/routing"
)

func sampleManifest() *config.File {
	return &config.File{
		Providers: []config.ProviderEntry{
			{
				Name:  "agy",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{ID: "gemini-3.8-flash-high", ContextWindow: 1000000},
				},
			},
			{
				Name:  "claude",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{ID: "claude-haiku-4-5", ContextWindow: 200000},
				},
			},
			{
				Name:  "ollama",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{ID: "llama3.1", ContextWindow: 128000},
				},
			},
		},
	}
}

// TestRoute_DeterministicFixtures tests table-driven deterministic routing across
// the six benchmark fixture classes.
func TestRoute_DeterministicFixtures(t *testing.T) {
	manifest := sampleManifest()

	tests := []struct {
		name         string
		req          routing.RouteRequest
		wantRole     string
		wantProvider string
		wantModel    string
	}{
		{
			name: "docs-only routes to summarizer and cheapest model (claude-haiku-4-5)",
			req: routing.RouteRequest{
				Prompt:   "Review and summarize markdown documentation",
				Paths:    []string{"docs/decisions/0030-context-router.md", "README.md"},
				Manifest: manifest,
			},
			wantRole:     "summarizer",
			wantProvider: "claude",
			wantModel:    "claude-haiku-4-5",
		},
		{
			name: "trust-boundary path routes to test-runner and largest-context model",
			req: routing.RouteRequest{
				Prompt:   "Verify receipt issuance security checks",
				Paths:    []string{"internal/receipt/verifier.go"},
				Manifest: manifest,
			},
			wantRole:     "test-runner",
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
		},
		{
			name: "empty-paths defaults to manifest default (agy/gemini-3.8-flash-high/collector)",
			req: routing.RouteRequest{
				Prompt:   "General inventory collection task",
				Paths:    []string{},
				Manifest: manifest,
			},
			wantRole:     "collector",
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
		},
		{
			name: "mixed paths route deterministically within manifest",
			req: routing.RouteRequest{
				Prompt:   "Documentation and CLI update",
				Paths:    []string{"docs/cli.md", "cmd/g8s/main.go"},
				Manifest: manifest,
			},
			wantRole:     "collector",
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
		},
		{
			name: "huge timeout hint routes to high-capacity provider",
			req: routing.RouteRequest{
				Prompt:      "Execute large codebase migration",
				Paths:       []string{"pkg/migration.go"},
				TimeoutHint: "2h",
				Manifest:    manifest,
			},
			wantRole:     "collector",
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
		},
		{
			name: "unknown-path fallback routes to manifest default",
			req: routing.RouteRequest{
				Prompt:   "Process unknown file",
				Paths:    []string{"nonexistent/path/file.xyz"},
				Manifest: manifest,
			},
			wantRole:     "collector",
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := routing.Route(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Route returned unexpected error: %v", err)
			}
			if dec.Source != "deterministic" {
				t.Errorf("Source = %q, want %q", dec.Source, "deterministic")
			}
			if dec.IsFallback {
				t.Errorf("IsFallback = true, want false")
			}
			if dec.Role != tc.wantRole {
				t.Errorf("Role = %q, want %q", dec.Role, tc.wantRole)
			}
			if dec.Provider != tc.wantProvider {
				t.Errorf("Provider = %q, want %q", dec.Provider, tc.wantProvider)
			}
			if dec.Model != tc.wantModel {
				t.Errorf("Model = %q, want %q", dec.Model, tc.wantModel)
			}
			if strings.TrimSpace(dec.Reason) == "" {
				t.Errorf("Reason is empty")
			}
		})
	}
}

// TestRoute_ManifestNil_CatalogDefaults tests that nil manifest falls back to catalog defaults.
func TestRoute_ManifestNil_CatalogDefaults(t *testing.T) {
	req := routing.RouteRequest{
		Prompt:   "General inventory collection task",
		Paths:    []string{},
		Manifest: nil,
	}

	dec, err := routing.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("Route returned error on nil manifest: %v", err)
	}
	if dec.Provider != "agy" {
		t.Errorf("Provider = %q, want %q", dec.Provider, "agy")
	}
	if dec.Model != "gemini-3.8-flash-high" {
		t.Errorf("Model = %q, want %q", dec.Model, "gemini-3.8-flash-high")
	}
	if dec.Role != "collector" {
		t.Errorf("Role = %q, want %q", dec.Role, "collector")
	}
	if dec.Source != "deterministic" {
		t.Errorf("Source = %q, want %q", dec.Source, "deterministic")
	}
	if dec.IsFallback {
		t.Errorf("IsFallback = true, want false")
	}
}

// TestRoute_JevMocked_HappyPath tests when Jev provides a valid manifest-legal suggestion.
func TestRoute_JevMocked_HappyPath(t *testing.T) {
	manifest := sampleManifest()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		resp := map[string]any{
			"model": "jev-latest",
			"answers": map[string]any{
				"provider": map[string]any{"type": "choice", "choice": "claude", "confidence": 0.95},
				"model":    map[string]any{"type": "choice", "choice": "claude-haiku-4-5", "confidence": 0.95},
				"role":     map[string]any{"type": "choice", "choice": "summarizer", "confidence": 0.90},
				"reason":   map[string]any{"type": "choice", "choice": "Jev: documentation summarization on cheap tier", "confidence": 0.90},
			},
			"usage": map[string]any{"input_tokens": 120, "output_tokens": 30},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	os.Setenv("G8S_ROUTER_MODE", "jev_assisted")
	os.Setenv("TYPESAFE_API_KEY", "test-key-mock")
	os.Setenv("TYPESAFE_ENDPOINT", ts.URL)
	defer func() {
		os.Unsetenv("G8S_ROUTER_MODE")
		os.Unsetenv("TYPESAFE_API_KEY")
		os.Unsetenv("TYPESAFE_ENDPOINT")
	}()

	req := routing.RouteRequest{
		Prompt:   "Summarize release notes",
		Paths:    []string{"docs/release.md"},
		Manifest: manifest,
	}

	dec, err := routing.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}

	if dec.Source != "jev" {
		t.Errorf("Source = %q, want %q", dec.Source, "jev")
	}
	if dec.IsFallback {
		t.Errorf("IsFallback = true, want false")
	}
	if dec.Provider != "claude" {
		t.Errorf("Provider = %q, want %q", dec.Provider, "claude")
	}
	if dec.Model != "claude-haiku-4-5" {
		t.Errorf("Model = %q, want %q", dec.Model, "claude-haiku-4-5")
	}
	if dec.Role != "summarizer" {
		t.Errorf("Role = %q, want %q", dec.Role, "summarizer")
	}
	if strings.TrimSpace(dec.Reason) == "" {
		t.Errorf("Reason is empty")
	}
	if dec.Confidence < 0.8 {
		t.Errorf("Confidence = %f, want >= 0.8", dec.Confidence)
	}
}

// TestRoute_JevMocked_Hostile tests all failure modes of Jev, asserting deterministic fallback with IsFallback=true.
func TestRoute_JevMocked_Hostile(t *testing.T) {
	manifest := sampleManifest()

	tests := []struct {
		name       string
		handler    http.HandlerFunc
		timeout    time.Duration
		reasonFrag string
	}{
		{
			name: "unknown model suggested by Jev",
			handler: func(w http.ResponseWriter, r *http.Request) {
				resp := map[string]any{
					"model": "jev-latest",
					"answers": map[string]any{
						"provider": map[string]any{"type": "choice", "choice": "agy"},
						"model":    map[string]any{"type": "choice", "choice": "gpt-unknown-999"},
						"role":     map[string]any{"type": "choice", "choice": "collector"},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			},
			reasonFrag: "unknown provider/model",
		},
		{
			name: "unknown role suggested by Jev",
			handler: func(w http.ResponseWriter, r *http.Request) {
				resp := map[string]any{
					"model": "jev-latest",
					"answers": map[string]any{
						"provider": map[string]any{"type": "choice", "choice": "agy"},
						"model":    map[string]any{"type": "choice", "choice": "gemini-3.8-flash-high"},
						"role":     map[string]any{"type": "choice", "choice": "super-admin"},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			},
			reasonFrag: "unknown role",
		},
		{
			name: "garbage JSON returned by Jev",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{not-valid-json..."))
			},
			reasonFrag: "Jev assist unavailable",
		},
		{
			name: "HTTP 500 internal server error from Jev",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("internal server error"))
			},
			reasonFrag: "500",
		},
		{
			name: "timeout from Jev",
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(100 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			},
			timeout:    10 * time.Millisecond,
			reasonFrag: "context deadline exceeded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(tc.handler)
			defer ts.Close()

			os.Setenv("G8S_ROUTER_MODE", "jev_assisted")
			os.Setenv("TYPESAFE_API_KEY", "test-key-mock")
			os.Setenv("TYPESAFE_ENDPOINT", ts.URL)
			defer func() {
				os.Unsetenv("G8S_ROUTER_MODE")
				os.Unsetenv("TYPESAFE_API_KEY")
				os.Unsetenv("TYPESAFE_ENDPOINT")
			}()

			ctx := context.Background()
			if tc.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.timeout)
				defer cancel()
			}

			req := routing.RouteRequest{
				Prompt:   "Review and summarize markdown documentation",
				Paths:    []string{"docs/test.md"},
				Manifest: manifest,
			}

			dec, err := routing.Route(ctx, req)
			if err != nil {
				t.Fatalf("Route should never propagate Jev errors, got: %v", err)
			}

			if dec.Source != "deterministic" {
				t.Errorf("Source = %q, want %q", dec.Source, "deterministic")
			}
			if !dec.IsFallback {
				t.Errorf("IsFallback = false, want true")
			}
			// Deterministic fallback for docs-only should still be summarizer and claude-haiku-4-5
			if dec.Role != "summarizer" {
				t.Errorf("Role = %q, want %q", dec.Role, "summarizer")
			}
			if dec.Model != "claude-haiku-4-5" {
				t.Errorf("Model = %q, want %q", dec.Model, "claude-haiku-4-5")
			}
			if !strings.Contains(dec.Reason, tc.reasonFrag) && !strings.Contains(dec.Reason, "Jev") {
				t.Errorf("Reason %q does not note rejected suggestion / failure basis (%q)", dec.Reason, tc.reasonFrag)
			}
		})
	}
}

// TestRoute_EnvOff_NoNetwork verifies that with env off, no network request is made.
func TestRoute_EnvOff_NoNetwork(t *testing.T) {
	manifest := sampleManifest()

	// Ensure router mode is NOT jev_assisted
	os.Unsetenv("G8S_ROUTER_MODE")
	os.Unsetenv("TYPESAFE_API_KEY")
	// Set endpoint to an invalid unreachable address; if a connection is attempted it will fail
	os.Setenv("TYPESAFE_ENDPOINT", "http://127.0.0.1:0")
	defer os.Unsetenv("TYPESAFE_ENDPOINT")

	req := routing.RouteRequest{
		Prompt:   "Summarize docs",
		Paths:    []string{"docs/intro.md"},
		Manifest: manifest,
	}

	dec, err := routing.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error with env off: %v", err)
	}

	if dec.Source != "deterministic" {
		t.Errorf("Source = %q, want %q", dec.Source, "deterministic")
	}
	if dec.IsFallback {
		t.Errorf("IsFallback = true, want false")
	}
	if dec.Role != "summarizer" {
		t.Errorf("Role = %q, want %q", dec.Role, "summarizer")
	}
}

// TestRoute_FallbackParity_RT_F asserts RT-F: with no keys and no env,
// Route returns byte-identical Decisions to the deterministic layer.
func TestRoute_FallbackParity_RT_F(t *testing.T) {
	manifest := sampleManifest()

	fixtures := []routing.RouteRequest{
		{Prompt: "docs task", Paths: []string{"docs/decisions/0030.md"}, Manifest: manifest},
		{Prompt: "trust task", Paths: []string{"internal/controlplane/store.go"}, Manifest: manifest},
		{Prompt: "empty paths", Paths: []string{}, Manifest: manifest},
	}

	for i, req := range fixtures {
		os.Unsetenv("G8S_ROUTER_MODE")
		os.Unsetenv("TYPESAFE_API_KEY")

		d1, err := routing.Route(context.Background(), req)
		if err != nil {
			t.Fatalf("fixture %d failed: %v", i, err)
		}

		// Re-run with unreachable endpoint to guarantee zero network
		os.Setenv("TYPESAFE_ENDPOINT", "http://127.0.0.1:0")
		d2, err := routing.Route(context.Background(), req)
		os.Unsetenv("TYPESAFE_ENDPOINT")
		if err != nil {
			t.Fatalf("fixture %d repeat failed: %v", i, err)
		}

		if d1 != d2 {
			t.Errorf("fixture %d parity violated:\n  d1: %+v\n  d2: %+v", i, d1, d2)
		}
	}
}

// TestRoutingSuite_PassesAsUnitTests asserts that the benchmark probes pass in unit test mode
// with PRI scoring = 1.0 against StaticProvider without live network.
func TestRoutingSuite_PassesAsUnitTests(t *testing.T) {
	suite := probe.RoutingSuite()
	if len(suite.Probes) < 6 {
		t.Fatalf("RoutingSuite defines %d probes, want >= 6", len(suite.Probes))
	}

	pri, err := probe.RunSuite(context.Background(), suite, &probe.StaticProvider{Compliant: true})
	if err != nil {
		t.Fatalf("RunSuite failed: %v", err)
	}

	if pri.FailedProbes > 0 {
		for _, d := range pri.Details {
			if !d.Passed {
				t.Errorf("probe %s (%s) failed: actual=%s, expected=%s, err=%s",
					d.ProbeID, d.Name, d.ActualOutcome, d.ExpectedOutcome, d.Error)
			}
		}
	}
	if pri.Score < 1.0 {
		t.Errorf("PRI Score = %f, want 1.0", pri.Score)
	}
	if pri.CategoryScores[string(probe.CategoryTaskRouting)] < 1.0 {
		t.Errorf("CategoryScore for %s = %f, want 1.0",
			probe.CategoryTaskRouting, pri.CategoryScores[string(probe.CategoryTaskRouting)])
	}
}
