package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/tamld/g8s/internal/harness"
	"github.com/tamld/g8s/internal/routing"
)

func runRoutingProbe(ctx context.Context, req routing.RouteRequest, expectedRole string, jevSuggestHostile bool) (string, error) {
	// Call 1: Deterministic-only execution (zero-network)
	d1, err := routing.Route(ctx, req)
	if err != nil {
		return "", fmt.Errorf("deterministic Route failed: %w", err)
	}
	if d1.Source != "deterministic" {
		return "", fmt.Errorf("deterministic run got Source %q, want 'deterministic'", d1.Source)
	}
	if d1.IsFallback {
		return "", fmt.Errorf("deterministic run unexpectedly marked IsFallback")
	}
	if d1.Provider == "" || d1.Model == "" {
		return "", fmt.Errorf("deterministic run returned empty provider/model (%s/%s)", d1.Provider, d1.Model)
	}
	if _, err := harness.GetRole(d1.Role); err != nil {
		return "", fmt.Errorf("deterministic run returned invalid role %q: %w", d1.Role, err)
	}
	if expectedRole != "" && d1.Role != expectedRole {
		return "", fmt.Errorf("deterministic run role = %q, want %q", d1.Role, expectedRole)
	}

	// Call 2: Jev-assisted execution against mock httptest server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var resp map[string]any
		if jevSuggestHostile {
			// Hostile suggestion: unknown provider and model to trigger deterministic fallback
			resp = map[string]any{
				"model": "jev-latest",
				"answers": map[string]any{
					"provider": map[string]any{"type": "choice", "choice": "hostile-unregistered-provider"},
					"model":    map[string]any{"type": "choice", "choice": "hostile-unregistered-model"},
					"role":     map[string]any{"type": "choice", "choice": "collector"},
				},
			}
		} else {
			// Compliant suggestion: mirror manifest-legal placement with high confidence
			resp = map[string]any{
				"model": "jev-latest",
				"answers": map[string]any{
					"provider": map[string]any{"type": "choice", "choice": d1.Provider, "confidence": 0.95},
					"model":    map[string]any{"type": "choice", "choice": d1.Model, "confidence": 0.95},
					"role":     map[string]any{"type": "choice", "choice": d1.Role, "confidence": 0.90},
					"reason":   map[string]any{"type": "choice", "choice": "Jev benchmark placement verified", "confidence": 0.90},
				},
				"usage": map[string]any{"input_tokens": 120, "output_tokens": 25},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	jevCtx := routing.WithJevAssist(ctx, ts.URL, "probe-mock-key")
	d2, err := routing.Route(jevCtx, req)
	if err != nil {
		return "", fmt.Errorf("jev-assisted Route returned unexpected error: %w", err)
	}

	switch d2.Source {
	case "jev":
		if d2.IsFallback {
			return "", fmt.Errorf("jev-sourced decision unexpectedly marked IsFallback")
		}
		if d2.Provider == "" || d2.Model == "" {
			return "", fmt.Errorf("jev-sourced decision has empty provider/model")
		}
		if _, err := harness.GetRole(d2.Role); err != nil {
			return "", fmt.Errorf("jev-sourced decision has invalid role %q: %w", d2.Role, err)
		}
		if strings.TrimSpace(d2.Reason) == "" {
			return "", fmt.Errorf("jev-sourced decision has empty Reason")
		}
	case "deterministic":
		if !d2.IsFallback {
			return "", fmt.Errorf("deterministic fallback decision missing IsFallback=true")
		}
		if d2.Provider == "" || d2.Model == "" {
			return "", fmt.Errorf("fallback decision has empty provider/model")
		}
	default:
		return "", fmt.Errorf("unexpected Source %q in jev-assisted run", d2.Source)
	}

	return OutcomeClassCompleted, nil
}

// RoutingProbes returns the benchmark probes for context routing per ADR-0030 (SCORECARD S-4 seed).
func RoutingProbes() []*Probe {
	return []*Probe{
		{
			ID:              "rt-001",
			Category:        CategoryTaskRouting,
			Name:            "docs-only",
			Description:     "Documentation-only path sets route to summarizer role with lightweight model",
			Input:           "docs/decisions/0030-context-router.md, README.md",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
				return runRoutingProbe(ctx, routing.RouteRequest{
					Prompt: "Summarize architecture decisions",
					Paths:  []string{"docs/decisions/0030-context-router.md", "README.md"},
				}, "summarizer", false)
			},
		},
		{
			ID:              "rt-002",
			Category:        CategoryTaskRouting,
			Name:            "trust-path",
			Description:     "Trust boundary paths route to test-runner role with large-context model",
			Input:           "internal/harness/probe/types.go",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
				return runRoutingProbe(ctx, routing.RouteRequest{
					Prompt: "Update harness types and verify policy gates",
					Paths:  []string{"internal/harness/probe/types.go"},
				}, "test-runner", false)
			},
		},
		{
			ID:              "rt-003",
			Category:        CategoryTaskRouting,
			Name:            "empty-paths default",
			Description:     "Empty path inventory defaults to catalog default provider and collector role",
			Input:           "",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
				return runRoutingProbe(ctx, routing.RouteRequest{
					Prompt: "General repository inspection and discovery",
					Paths:  []string{},
				}, "collector", false)
			},
		},
		{
			ID:              "rt-004",
			Category:        CategoryTaskRouting,
			Name:            "mixed paths",
			Description:     "Mixed code and documentation paths route deterministically within manifest",
			Input:           "docs/index.md, cmd/g8s/main.go",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
				return runRoutingProbe(ctx, routing.RouteRequest{
					Prompt: "CLI entrypoint documentation and handler updates",
					Paths:  []string{"docs/index.md", "cmd/g8s/main.go"},
				}, "", false)
			},
		},
		{
			ID:              "rt-005",
			Category:        CategoryTaskRouting,
			Name:            "huge timeout hint",
			Description:     "Extended execution timeout hint allocates to high-capacity provider",
			Input:           "timeout=120m",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
				return runRoutingProbe(ctx, routing.RouteRequest{
					Prompt:      "Execute deep dependency migration across modules",
					Paths:       []string{"internal/worker/worker.go"},
					TimeoutHint: "120m",
				}, "", false)
			},
		},
		{
			ID:              "rt-006",
			Category:        CategoryTaskRouting,
			Name:            "unknown-path fallback",
			Description:     "Unmatched paths with hostile Jev suggestion safely fallback with IsFallback=true",
			Input:           "nonexistent/unmatched/file.xyz",
			ExpectedOutcome: OutcomeClassCompleted,
			Runner: func(ctx context.Context, provider WorkerProvider) (string, error) {
				return runRoutingProbe(ctx, routing.RouteRequest{
					Prompt: "Analyze unmatched filesystem paths",
					Paths:  []string{"nonexistent/unmatched/file.xyz"},
				}, "", true)
			},
		},
	}
}

// RoutingSuite returns the complete probe suite for task routing benchmarks.
func RoutingSuite() *ProbeSuite {
	return &ProbeSuite{
		Name:        "Context Router Benchmark Probe Suite",
		Description: "Evaluates deterministic and Jev-assisted task routing per ADR-0030 (SCORECARD S-4 seed)",
		Probes:      RoutingProbes(),
	}
}
