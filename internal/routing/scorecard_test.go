package routing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

func TestComputeScorecardAndDynamicQuarantine(t *testing.T) {
	tmpDir := t.TempDir()
	ledgerPath := filepath.Join(tmpDir, "model_quality_events.jsonl")
	t.Setenv("G8S_MODEL_SCORECARD_PATH", ledgerPath)

	// 1. Record healthy outcomes for 9router/fast-agent
	for i := 0; i < 5; i++ {
		if err := RecordModelOutcome(tmpDir, ModelOutcomeRecord{
			Provider:       "9router",
			ModelRequested: "fast-agent",
			ModelResolved:  "glm-4.7",
			Success:        true,
			VerifyChecked:  true,
			VerifyPassed:   true,
			DurationMs:     1800,
			TotalTokens:    450,
		}); err != nil {
			t.Fatalf("RecordModelOutcome fast-agent: %v", err)
		}
	}

	// 2. Record 3 consecutive failures for 9router/broken-combo -> must trigger QUARANTINED
	for i := 0; i < 3; i++ {
		if err := RecordModelOutcome(tmpDir, ModelOutcomeRecord{
			Provider:       "9router",
			ModelRequested: "broken-combo",
			Success:        false,
			VerifyChecked:  true,
			VerifyPassed:   false,
			DurationMs:     12000,
			ErrorReason:    "upstream 502 bad gateway",
		}); err != nil {
			t.Fatalf("RecordModelOutcome broken-combo: %v", err)
		}
	}

	// 3. Record mixed outcomes for 9router/degraded-combo
	_ = RecordModelOutcome(tmpDir, ModelOutcomeRecord{
		Provider:       "9router",
		ModelRequested: "degraded-combo",
		Success:        true,
		VerifyChecked:  true,
		VerifyPassed:   true,
		DurationMs:     4000,
	})
	_ = RecordModelOutcome(tmpDir, ModelOutcomeRecord{
		Provider:       "9router",
		ModelRequested: "degraded-combo",
		Success:        true,
		VerifyChecked:  true,
		VerifyPassed:   false,
		Stagnated:      true,
		DurationMs:     9000,
		ErrorReason:    "verify test failed",
	})

	scores, err := ComputeScorecard(tmpDir, "9router", 20)
	if err != nil {
		t.Fatalf("ComputeScorecard: %v", err)
	}
	if len(scores) != 3 {
		t.Fatalf("got %d scores, want 3", len(scores))
	}

	byModel := make(map[string]ModelQualityScore)
	for _, s := range scores {
		byModel[s.Model] = s
	}

	if byModel["fast-agent"].HealthStatus != ModelHealthHealthy {
		t.Errorf("fast-agent status = %s, want HEALTHY (score=%.1f)", byModel["fast-agent"].HealthStatus, byModel["fast-agent"].CompositeScore)
	}
	if byModel["fast-agent"].Recommendation != "PREFER" {
		t.Errorf("fast-agent recommendation = %s, want PREFER", byModel["fast-agent"].Recommendation)
	}

	if byModel["broken-combo"].HealthStatus != ModelHealthQuarantined {
		t.Errorf("broken-combo status = %s, want QUARANTINED", byModel["broken-combo"].HealthStatus)
	}
	if byModel["broken-combo"].ConsecutiveFailures != 3 {
		t.Errorf("broken-combo consecutive_failures = %d, want 3", byModel["broken-combo"].ConsecutiveFailures)
	}
	if byModel["broken-combo"].Recommendation != "DEMOTE_OR_FALLBACK" {
		t.Errorf("broken-combo recommendation = %s, want DEMOTE_OR_FALLBACK", byModel["broken-combo"].Recommendation)
	}

	if byModel["degraded-combo"].HealthStatus != ModelHealthDegraded {
		t.Errorf("degraded-combo status = %s, want DEGRADED (score=%.1f)", byModel["degraded-combo"].HealthStatus, byModel["degraded-combo"].CompositeScore)
	}

	// 4. Verify RouteDeterministic automatically skips QUARANTINED models when a healthy candidate exists
	manifest := &config.File{
		Providers: []config.ProviderEntry{
			{
				Name:  "9router",
				Class: "api_call",
				Models: []config.ModelEntry{
					{ID: "broken-combo", ContextWindow: 2000000}, // Larger context window, but QUARANTINED!
					{ID: "fast-agent", ContextWindow: 200000},    // Healthy candidate
				},
			},
		},
	}

	dec := RouteDeterministic(RouteRequest{
		Prompt:   "Audit internal/harness security boundary",
		Paths:    []string{"internal/harness/runner.go"},
		Manifest: manifest,
	})
	if dec.Model == "broken-combo" {
		t.Errorf("RouteDeterministic selected quarantined model 'broken-combo'; expected 'fast-agent'")
	}
	if dec.Model != "fast-agent" {
		t.Errorf("RouteDeterministic selected %q, want 'fast-agent'", dec.Model)
	}

	// Ensure ledger file exists with 0600 permissions
	info, statErr := os.Stat(ledgerPath)
	if statErr != nil {
		t.Fatalf("stat ledger: %v", statErr)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("ledger perm = %o, want 0600", info.Mode().Perm())
	}
}
