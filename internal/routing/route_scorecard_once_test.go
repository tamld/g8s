package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

func TestRoute_JevAssisted_ComputesScorecardOnce(t *testing.T) {
	t.Setenv("G8S_STATE_DIR", t.TempDir())

	var scorecardCalls int
	orig := computeScorecardFn
	computeScorecardFn = func(stateDir string, providerFilter string, windowSize int) ([]ModelQualityScore, error) {
		scorecardCalls++
		return orig(stateDir, providerFilter, windowSize)
	}
	t.Cleanup(func() { computeScorecardFn = orig })

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"model": "jev-latest",
			"answers": map[string]any{
				"provider": map[string]any{"type": "choice", "choice": "claude", "confidence": 0.95},
				"model":    map[string]any{"type": "choice", "choice": "claude-haiku-4-5", "confidence": 0.95},
				"role":     map[string]any{"type": "choice", "choice": "summarizer", "confidence": 0.90},
				"reason":   map[string]any{"type": "choice", "choice": "Jev: docs", "confidence": 0.90},
			},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx := WithJevAssist(context.Background(), ts.URL, "test-key")
	manifest := DefaultManifest()

	req := RouteRequest{
		Prompt:   "Summarize release notes",
		Paths:    []string{"docs/release.md"},
		Manifest: manifest,
	}

	if _, err := Route(ctx, req); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if scorecardCalls != 1 {
		t.Fatalf("ComputeScorecard calls = %d, want 1", scorecardCalls)
	}
}

func TestRouteDeterministic_ScorecardParityLargeLedger(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("G8S_STATE_DIR", tmpDir)

	ledgerPath := filepath.Join(tmpDir, "model_quality_events.jsonl")
	if err := writeSyntheticScorecardLedger(t, ledgerPath, 20_000); err != nil {
		t.Fatalf("writeSyntheticScorecardLedger: %v", err)
	}

	manifest := &config.File{
		Providers: []config.ProviderEntry{
			{
				Name:  "9router",
				Class: "api_call",
				Models: []config.ModelEntry{
					{ID: "broken-combo", ContextWindow: 2_000_000},
					{ID: "fast-agent", ContextWindow: 200_000},
				},
			},
			{
				Name:  "agy",
				Class: "platform_dispatch",
				Models: []config.ModelEntry{
					{ID: "gemini-3.8-flash-high", ContextWindow: 1_000_000},
				},
			},
		},
	}

	cases := []struct {
		name string
		req  RouteRequest
	}{
		{
			name: "docs-only",
			req: RouteRequest{
				Prompt:   "Summarize docs",
				Paths:    []string{"docs/guide.md", "README.md"},
				Manifest: manifest,
			},
		},
		{
			name: "trust-boundary",
			req: RouteRequest{
				Prompt:   "Audit harness",
				Paths:    []string{"internal/harness/runner.go"},
				Manifest: manifest,
			},
		},
		{
			name: "default-empty-paths",
			req: RouteRequest{
				Prompt:   "General task",
				Paths:    nil,
				Manifest: manifest,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viaPublic := RouteDeterministic(tc.req)
			candidates := extractCandidates(manifest)
			viaShared := routeDeterministicWithCandidates(tc.req, candidates)
			assertDecisionsEqual(t, viaPublic, viaShared)
		})
	}

	t.Run("quarantined-candidate-excluded", func(t *testing.T) {
		quarantineManifest := &config.File{
			Providers: []config.ProviderEntry{
				{
					Name:  "9router",
					Class: "api_call",
					Models: []config.ModelEntry{
						{ID: "broken-combo", ContextWindow: 2_000_000},
						{ID: "fast-agent", ContextWindow: 200_000},
					},
				},
			},
		}
		req := RouteRequest{
			Prompt:   "Trust boundary audit",
			Paths:    []string{"internal/receipt/verifier.go"},
			Manifest: quarantineManifest,
		}
		dec := RouteDeterministic(req)
		if dec.Model == "broken-combo" {
			t.Fatalf("selected quarantined broken-combo")
		}
		if dec.Model != "fast-agent" {
			t.Fatalf("model = %q, want fast-agent (quarantine filter)", dec.Model)
		}
		candidates := extractCandidates(quarantineManifest)
		viaShared := routeDeterministicWithCandidates(req, candidates)
		assertDecisionsEqual(t, dec, viaShared)
	})
}

func writeSyntheticScorecardLedger(t *testing.T, path string, fillerRows int) error {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	healthyLine, err := json.Marshal(ModelOutcomeRecord{
		Provider:       "9router",
		ModelRequested: "fast-agent",
		Success:        true,
		VerifyChecked:  true,
		VerifyPassed:   true,
		DurationMs:     1500,
		RecordedAt:     "2026-01-01T00:00:00Z",
	})
	if err != nil {
		return err
	}
	for i := 0; i < fillerRows; i++ {
		if _, err := f.Write(append(healthyLine, '\n')); err != nil {
			return err
		}
	}

	failLine, err := json.Marshal(ModelOutcomeRecord{
		Provider:       "9router",
		ModelRequested: "broken-combo",
		Success:        false,
		VerifyChecked:  true,
		VerifyPassed:   false,
		DurationMs:     8000,
		ErrorReason:    "upstream 502",
		RecordedAt:     "2026-01-02T00:00:00Z",
	})
	if err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		if _, err := f.Write(append(failLine, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func assertDecisionsEqual(t *testing.T, a, b Decision) {
	t.Helper()
	if a.Provider != b.Provider || a.Model != b.Model || a.Role != b.Role ||
		a.Source != b.Source || a.IsFallback != b.IsFallback ||
		a.Reason != b.Reason || a.Confidence != b.Confidence ||
		a.SuggestedLane != b.SuggestedLane || a.SuggestedEffort != b.SuggestedEffort {
		t.Fatalf("decisions differ:\n  public: %+v\n  shared: %+v", a, b)
	}
}
