package routing

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/harness"
)

func redtestManifest() *config.File {
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

// -----------------------------------------------------------------------------
// Guarantee 1: Jev suggestion cannot escape the manifest.
// Every suggestion is either rejected to the deterministic fallback or resolved
// strictly within the manifest — never executed with unregistered provider/model/role.
// -----------------------------------------------------------------------------

func TestRedtest_Guarantee1_SuggestionRejectionMatrix(t *testing.T) {
	manifest := redtestManifest()
	candidates := extractCandidates(manifest)

	// Baseline deterministic fallback decision
	baseReq := RouteRequest{
		Prompt:   "General redteam verification task",
		Paths:    []string{"pkg/arbitrary.go"},
		Manifest: manifest,
	}
	deterministicLayer1 := RouteDeterministic(baseReq)

	type suggestionTestCase struct {
		name         string
		category     string
		sugProvider  string
		sugModel     string
		sugRole      string
		wantFallback bool
		wantProvider string
		wantModel    string
		wantRole     string
	}

	tests := []suggestionTestCase{
		// 1. Unregistered provider
		{
			name:         "unregistered provider openai",
			category:     "unregistered_provider",
			sugProvider:  "openai",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "unregistered provider unknown-vendor",
			category:     "unregistered_provider",
			sugProvider:  "unknown-vendor",
			sugModel:     "llama3.1",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "unregistered provider evil_dispatch",
			category:     "unregistered_provider",
			sugProvider:  "evil_dispatch",
			sugModel:     "claude-haiku-4-5",
			sugRole:      "summarizer",
			wantFallback: true,
		},

		// 2. Registered provider with UNREGISTERED model
		{
			name:         "registered agy with unregistered model gemini-ultra-fake",
			category:     "unregistered_model",
			sugProvider:  "agy",
			sugModel:     "gemini-ultra-fake",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "registered claude with unregistered model gpt-4o",
			category:     "unregistered_model",
			sugProvider:  "claude",
			sugModel:     "gpt-4o",
			sugRole:      "summarizer",
			wantFallback: true,
		},
		{
			name:         "registered ollama with unregistered model llama-unregistered-99b",
			category:     "unregistered_model",
			sugProvider:  "ollama",
			sugModel:     "llama-unregistered-99b",
			sugRole:      "collector",
			wantFallback: true,
		},

		// 3. Valid model with role 'escalated' (non-existent role) and invalid roles
		{
			name:         "valid model with non-existent role escalated",
			category:     "invalid_role",
			sugProvider:  "agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "escalated",
			wantFallback: true,
		},
		{
			name:         "valid model with role admin",
			category:     "invalid_role",
			sugProvider:  "agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "admin",
			wantFallback: true,
		},
		{
			name:         "valid model with role root",
			category:     "invalid_role",
			sugProvider:  "agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "root",
			wantFallback: true,
		},
		{
			name:         "valid model with empty role",
			category:     "invalid_role",
			sugProvider:  "agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "",
			wantFallback: true,
		},

		// 4. Empty provider with valid model
		{
			name:         "empty provider string with valid model",
			category:     "empty_fields",
			sugProvider:  "",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "whitespace-only provider with valid model",
			category:     "empty_fields",
			sugProvider:  "   ",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},

		// 5. Empty model with valid provider
		{
			name:         "empty model string with valid provider",
			category:     "empty_fields",
			sugProvider:  "agy",
			sugModel:     "",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "whitespace-only model with valid provider",
			category:     "empty_fields",
			sugProvider:  "agy",
			sugModel:     "   ",
			sugRole:      "collector",
			wantFallback: true,
		},

		// 6. Provider whose name differs only by case/whitespace ("AGY", " agy")
		{
			name:         "uppercase provider AGY (case mismatch rejected to fallback)",
			category:     "case_whitespace",
			sugProvider:  "AGY",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "titlecase provider Agy (case mismatch rejected to fallback)",
			category:     "case_whitespace",
			sugProvider:  "Agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "leading whitespace ' agy' (trimmed and resolved strictly within manifest)",
			category:     "case_whitespace",
			sugProvider:  " agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: false,
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
			wantRole:     "collector",
		},
		{
			name:         "trailing whitespace 'agy ' (trimmed and resolved strictly within manifest)",
			category:     "case_whitespace",
			sugProvider:  "agy ",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: false,
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
			wantRole:     "collector",
		},
		{
			name:         "surrounding whitespace '  agy  ' (trimmed and resolved strictly within manifest)",
			category:     "case_whitespace",
			sugProvider:  "  agy  ",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: false,
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
			wantRole:     "collector",
		},
		{
			name:         "tab whitespace 'agy\t' (trimmed and resolved strictly within manifest)",
			category:     "case_whitespace",
			sugProvider:  "agy\t",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: false,
			wantProvider: "agy",
			wantModel:    "gemini-3.8-flash-high",
			wantRole:     "collector",
		},
		{
			name:         "internal whitespace 'a gy' (rejected to fallback)",
			category:     "case_whitespace",
			sugProvider:  "a gy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "uppercase model GEMINI-3.8-FLASH-HIGH (rejected to fallback)",
			category:     "case_whitespace",
			sugProvider:  "agy",
			sugModel:     "GEMINI-3.8-FLASH-HIGH",
			sugRole:      "collector",
			wantFallback: true,
		},

		// 7. Extremely long strings (64KB provider/model/role)
		{
			name:         "64KB provider string",
			category:     "extreme_lengths",
			sugProvider:  strings.Repeat("A", 65536),
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "64KB model string",
			category:     "extreme_lengths",
			sugProvider:  "agy",
			sugModel:     strings.Repeat("M", 65536),
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "64KB role string",
			category:     "extreme_lengths",
			sugProvider:  "agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      strings.Repeat("R", 65536),
			wantFallback: true,
		},

		// 8. Unicode look-alikes
		{
			name:         "unicode look-alike Cyrillic 'а' in provider '\\u0430gy'",
			category:     "unicode_lookalike",
			sugProvider:  "\u0430gy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "unicode look-alike fullwidth '\\uff41\\uff47\\uff59'",
			category:     "unicode_lookalike",
			sugProvider:  "\uff41\uff47\uff59",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "unicode zero-width space in provider 'agy\\u200b'",
			category:     "unicode_lookalike",
			sugProvider:  "agy\u200b",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "unicode look-alike Cyrillic 'а' in model 'gemini-3.8-fl\\u0430sh-high'",
			category:     "unicode_lookalike",
			sugProvider:  "agy",
			sugModel:     "gemini-3.8-fl\u0430sh-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "unicode look-alike Cyrillic 'о' in role 'c\\u043ellector'",
			category:     "unicode_lookalike",
			sugProvider:  "agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "c\u043ellector",
			wantFallback: true,
		},
		{
			name:         "unicode non-breaking hyphen in role 'test\\u2010runner'",
			category:     "unicode_lookalike",
			sugProvider:  "agy",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "test\u2010runner",
			wantFallback: true,
		},

		// 9. Cross-provider model mismatch (registered provider with model belonging to another provider)
		{
			name:         "cross-provider mismatch: claude provider with agy model",
			category:     "cross_provider_mismatch",
			sugProvider:  "claude",
			sugModel:     "gemini-3.8-flash-high",
			sugRole:      "collector",
			wantFallback: true,
		},
		{
			name:         "cross-provider mismatch: ollama provider with claude model",
			category:     "cross_provider_mismatch",
			sugProvider:  "ollama",
			sugModel:     "claude-haiku-4-5",
			sugRole:      "summarizer",
			wantFallback: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				resp := JevResponse{
					Model: "jev-latest",
					Answers: map[string]Answer{
						"provider": {Choice: tc.sugProvider, Confidence: 0.90},
						"model":    {Choice: tc.sugModel, Confidence: 0.90},
						"role":     {Choice: tc.sugRole, Confidence: 0.90},
						"reason":   {Choice: "adversarial suggestion test"},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			ctx := WithJevAssist(context.Background(), ts.URL, "redtest-api-key")
			dec, err := Route(ctx, baseReq)
			if err != nil {
				t.Fatalf("Route() unexpected error: %v", err)
			}

			if tc.wantFallback {
				if !dec.IsFallback {
					t.Errorf("[%s] expected fallback, got IsFallback=false (dec: %+v)", tc.name, dec)
				}
				if dec.Source != "deterministic" {
					t.Errorf("[%s] expected Source=deterministic, got %q", tc.name, dec.Source)
				}
				if dec.Provider != deterministicLayer1.Provider || dec.Model != deterministicLayer1.Model || dec.Role != deterministicLayer1.Role {
					t.Errorf("[%s] fallback decision (%s/%s/%s) does not match deterministic Layer 1 (%s/%s/%s)",
						tc.name, dec.Provider, dec.Model, dec.Role,
						deterministicLayer1.Provider, deterministicLayer1.Model, deterministicLayer1.Role)
				}
			} else {
				if dec.IsFallback {
					t.Errorf("[%s] expected successful resolution within manifest, got fallback (reason: %q)", tc.name, dec.Reason)
				}
				if dec.Source != "jev" {
					t.Errorf("[%s] expected Source=jev, got %q", tc.name, dec.Source)
				}
				if dec.Provider != tc.wantProvider || dec.Model != tc.wantModel || dec.Role != tc.wantRole {
					t.Errorf("[%s] got %s/%s/%s, want %s/%s/%s", tc.name, dec.Provider, dec.Model, dec.Role, tc.wantProvider, tc.wantModel, tc.wantRole)
				}
			}

			// Invariant Check for EVERY test: result must strictly exist within the manifest candidates
			foundCandidate := false
			for _, c := range candidates {
				if c.provider == dec.Provider && c.model == dec.Model {
					foundCandidate = true
					break
				}
			}
			if !foundCandidate {
				t.Fatalf("FATAL GUARANTEE BREACH [%s]: decision (%s/%s) escapes manifest candidates!", tc.name, dec.Provider, dec.Model)
			}

			// Invariant Check: role must be a registered harness role
			if _, err := harness.GetRole(dec.Role); err != nil {
				t.Fatalf("FATAL GUARANTEE BREACH [%s]: decision role %q is not a valid harness role: %v", tc.name, dec.Role, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Guarantee 2: Deterministic layer cannot be steered by prompt content.
// The decision depends only on documented rule inputs (payload class, manifest)
// and never leaks filesystem state through the prompt.
// -----------------------------------------------------------------------------

func TestRedtest_Guarantee2_DeterministicPromptImmunity(t *testing.T) {
	manifest := redtestManifest()

	adversarialPrompts := []struct {
		name   string
		prompt string
	}{
		{"path_traversal_simple", "../../etc"},
		{"path_traversal_deep", "../../../../../../etc/passwd"},
		{"path_traversal_code_target", "docs/../../../cmd/g8s/main.go"},
		{"absolute_system_path", "/etc/shadow"},
		{"absolute_var_log", "/var/log/system.log"},
		{"nul_byte_injection", "normal_task\x00../../etc/passwd"},
		{"nul_byte_only", "\x00\x00\x00"},
		{"1mb_large_prompt", strings.Repeat("adversarial prompt content with path /etc/passwd and docs/readme.md ", 16000)},
		{"repo_file_existing", "cmd/g8s/main.go"},
		{"repo_file_nonexistent", "cmd/g8s/nonexistent_phantom_file.go"},
		{"repo_root_relative", "."},
		{"repo_root_parent", ".."},
	}

	t.Run("EmptyPaths_FilesystemStateProbeImmunity", func(t *testing.T) {
		// When Paths is empty, prompt strings pointing to existing repo files vs non-existent
		// files MUST produce identical decisions without leaking filesystem existence.
		reqExisting := RouteRequest{
			Prompt:   "Inspect cmd/g8s/main.go", // exists on disk
			Paths:    nil,
			Manifest: manifest,
		}
		reqNonexistent := RouteRequest{
			Prompt:   "Inspect cmd/g8s/nonexistent_phantom_file.go", // does not exist
			Paths:    nil,
			Manifest: manifest,
		}

		decExisting := RouteDeterministic(reqExisting)
		decNonexistent := RouteDeterministic(reqNonexistent)

		if decExisting.Provider != decNonexistent.Provider ||
			decExisting.Model != decNonexistent.Model ||
			decExisting.Role != decNonexistent.Role ||
			decExisting.Source != decNonexistent.Source ||
			decExisting.Confidence != decNonexistent.Confidence ||
			decExisting.Reason != decNonexistent.Reason {
			t.Errorf("Decision differs based on filesystem existence of prompt path!\nexisting: %+v\nnonexistent: %+v",
				decExisting, decNonexistent)
		}

		// Both must resolve to manifest default
		if decExisting.Role != "collector" || decExisting.Provider != "agy" {
			t.Errorf("Expected manifest default collector/agy, got %+v", decExisting)
		}
	})

	t.Run("EmptyPaths_HostilePromptsProduceManifestDefault", func(t *testing.T) {
		for _, tc := range adversarialPrompts {
			t.Run(tc.name, func(t *testing.T) {
				req := RouteRequest{
					Prompt:   tc.prompt,
					Paths:    nil,
					Manifest: manifest,
				}
				dec := RouteDeterministic(req)

				if dec.Source != "deterministic" {
					t.Errorf("[%s] Source = %q, want deterministic", tc.name, dec.Source)
				}
				if dec.Role != "collector" || dec.Provider != "agy" || dec.Model != "gemini-3.8-flash-high" {
					t.Errorf("[%s] steered away from manifest default: got %+v", tc.name, dec)
				}
			})
		}
	})

	t.Run("DocsPayloadClass_CannotBeSteeredByHostilePrompts", func(t *testing.T) {
		// When Paths is a docs path, no hostile prompt (even trust boundary keywords or 1MB)
		// should steer it away from the docs rule (summarizer/claude-haiku-4-5).
		for _, tc := range adversarialPrompts {
			t.Run(tc.name, func(t *testing.T) {
				req := RouteRequest{
					Prompt:   tc.prompt,
					Paths:    []string{"docs/decisions/0030-context-router.md"},
					Manifest: manifest,
				}
				dec := RouteDeterministic(req)

				if dec.Role != "summarizer" || dec.Provider != "claude" || dec.Model != "claude-haiku-4-5" {
					t.Errorf("[%s] docs payload was steered away: got %+v", tc.name, dec)
				}
			})
		}
	})

	t.Run("TrustBoundaryClass_CannotBeSteeredOrDowngraded", func(t *testing.T) {
		// When Paths is a trust-boundary path, no hostile prompt (even docs strings or 1MB)
		// should downgrade or change the trust decision (test-runner/gemini-3.8-flash-high).
		for _, tc := range adversarialPrompts {
			t.Run(tc.name, func(t *testing.T) {
				req := RouteRequest{
					Prompt:   tc.prompt,
					Paths:    []string{"internal/receipt/verifier.go"},
					Manifest: manifest,
				}
				dec := RouteDeterministic(req)

				if dec.Role != "test-runner" || dec.Provider != "agy" || dec.Model != "gemini-3.8-flash-high" {
					t.Errorf("[%s] trust boundary was steered or downgraded: got %+v", tc.name, dec)
				}
			})
		}
	})

	t.Run("CodePaths_PromptCannotInjectTrustBoundaryHeuristic", func(t *testing.T) {
		// When Paths has a code path, a prompt containing 'internal/receipt' must NOT
		// trigger the trust-boundary heuristic (which is only documented for empty Paths).
		req := RouteRequest{
			Prompt:   "Review and verify internal/receipt security checks",
			Paths:    []string{"cmd/g8s/main.go"},
			Manifest: manifest,
		}
		dec := RouteDeterministic(req)

		// cmd/g8s/main.go has paths, so prompt heuristic is not evaluated
		if dec.Role == "test-runner" {
			t.Errorf("Prompt heuristic unexpectedly escalated code file to test-runner: %+v", dec)
		}
		if dec.Role != "collector" {
			t.Errorf("Expected manifest default collector for code path, got %q", dec.Role)
		}
	})
}

// -----------------------------------------------------------------------------
// Guarantee 3: Route() never touches the network when Jev is unconfigured.
// Point the client at an unreachable endpoint (closed port) with G8S_ROUTER_MODE
// unset — assert zero errors, zero latency anomaly, Source=deterministic.
// Then set the env but remove keys — same.
// -----------------------------------------------------------------------------

func getUnreachableClosedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind temporary listener: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // Immediately close so port is unreachable
	return "http://" + addr
}

func TestRedtest_Guarantee3_RouteNeverTouchesNetworkWhenUnconfigured(t *testing.T) {
	manifest := redtestManifest()
	closedEndpoint := getUnreachableClosedPort(t)

	req := RouteRequest{
		Prompt:   "Arbitrary prompt for network isolation test",
		Paths:    []string{"pkg/worker.go"},
		Manifest: manifest,
	}

	t.Run("RouterModeUnset_ZeroNetwork_ZeroLatencyAnomaly", func(t *testing.T) {
		// Point client at unreachable endpoint, G8S_ROUTER_MODE unset
		os.Unsetenv("G8S_ROUTER_MODE")
		os.Setenv("TYPESAFE_ENDPOINT", closedEndpoint)
		os.Setenv("TYPESAFE_API_KEYS", "dummy-key-should-never-be-used")
		defer func() {
			os.Unsetenv("TYPESAFE_ENDPOINT")
			os.Unsetenv("TYPESAFE_API_KEYS")
		}()

		ctx := context.Background()

		start := time.Now()
		dec, err := Route(ctx, req)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Route() returned error when Jev unconfigured: %v", err)
		}
		if dec.Source != "deterministic" {
			t.Errorf("Source = %q, want deterministic", dec.Source)
		}
		if dec.IsFallback {
			t.Errorf("IsFallback = true, want false (deterministic mode is not a fallback)")
		}

		// Network attempt against closed port or hanging would take significant time;
		// pure in-memory deterministic routing completes in < 50ms (typically < 1ms).
		if elapsed > 100*time.Millisecond {
			t.Errorf("Latency anomaly detected: Route() took %v (> 100ms), network may have been touched!", elapsed)
		}
	})

	t.Run("RouterModeSet_KeysRemoved_ZeroNetwork_ZeroLatencyAnomaly", func(t *testing.T) {
		// G8S_ROUTER_MODE="jev_assisted", unreachable endpoint, but ALL keys removed
		os.Setenv("G8S_ROUTER_MODE", "jev_assisted")
		os.Setenv("TYPESAFE_ENDPOINT", closedEndpoint)
		os.Unsetenv("TYPESAFE_API_KEYS")
		os.Unsetenv("TYPESAFE_API_KEY")
		os.Unsetenv("TYPESAFE_API_KEY_FALLBACK")
		defer func() {
			os.Unsetenv("G8S_ROUTER_MODE")
			os.Unsetenv("TYPESAFE_ENDPOINT")
		}()

		// Run in empty temp dir to prevent loading any .env file
		emptyDir := t.TempDir()
		t.Chdir(emptyDir)

		ctx := context.Background()

		start := time.Now()
		dec, err := Route(ctx, req)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Route() returned error when keys removed: %v", err)
		}
		if dec.Source != "deterministic" {
			t.Errorf("Source = %q, want deterministic", dec.Source)
		}
		if !dec.IsFallback {
			t.Errorf("IsFallback = false, want true (no keys available triggers fallback)")
		}
		if !strings.Contains(dec.Reason, "no API keys found") {
			t.Errorf("Reason = %q, want mentioning 'no API keys found'", dec.Reason)
		}

		// Must not dial network
		if elapsed > 100*time.Millisecond {
			t.Errorf("Latency anomaly detected: Route() took %v (> 100ms), network may have been touched!", elapsed)
		}
	})
}
