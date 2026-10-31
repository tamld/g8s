package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

// OWASP Agentic AI ASI08 — Agentic AI Security: unexpected agent behavior via
// telemetry / scorecard poisoning (hostile JSONL ledger rows must not override
// legitimate rolling-window conclusions).

func writeScorecardLedger(t *testing.T, path string, lines ...string) {
	t.Helper()
	var body string
	for _, ln := range lines {
		body += ln + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write ledger %s: %v", path, err)
	}
}

func redtestScorecardManifest() *config.File {
	return &config.File{
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
}

func scoreByModel(t *testing.T, scores []ModelQualityScore) map[string]ModelQualityScore {
	t.Helper()
	out := make(map[string]ModelQualityScore, len(scores))
	for _, s := range scores {
		out[s.Model] = s
	}
	return out
}

func mustJSONLine(t *testing.T, rec ModelOutcomeRecord) string {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal outcome: %v", err)
	}
	return string(b)
}

func TestRed_ASI08_PoisonedLedgerIgnoresHostileRows(t *testing.T) {
	tmpDir := t.TempDir()
	ledgerPath := filepath.Join(tmpDir, "model_quality_events.jsonl")
	t.Setenv("G8S_MODEL_SCORECARD_PATH", ledgerPath)

	legit := []string{
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "fast-agent", Success: true,
			VerifyChecked: true, VerifyPassed: true, DurationMs: 1500,
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "fast-agent", Success: true,
			VerifyChecked: true, VerifyPassed: true, DurationMs: 1600,
		}),
		// Forged historical success with inflated metrics (must not erase trailing failure streak).
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: true,
			VerifyChecked: true, VerifyPassed: true, DurationMs: 1, TotalTokens: 999999,
			ModelResolved: "forged-fingerprint-upstream",
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 8000,
			ErrorReason: "verify test failed",
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 9000,
			ErrorReason: "verify test failed",
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 10000,
			ErrorReason: "verify test failed",
		}),
	}
	writeScorecardLedger(t, ledgerPath, legit...)

	cleanScores, err := ComputeScorecard(tmpDir, "9router", 20)
	if err != nil {
		t.Fatalf("ComputeScorecard clean baseline: %v", err)
	}
	cleanByModel := scoreByModel(t, cleanScores)
	if cleanByModel["broken-combo"].HealthStatus != ModelHealthQuarantined {
		t.Fatalf("baseline broken-combo = %s, want QUARANTINED", cleanByModel["broken-combo"].HealthStatus)
	}

	manifest := redtestScorecardManifest()
	cleanRoute := RouteDeterministic(RouteRequest{
		Prompt:   "Audit internal/harness security boundary",
		Paths:    []string{"internal/harness/runner.go"},
		Manifest: manifest,
	})

	poison := append([]string{}, legit...)
	poison = append(poison,
		"{this is not json",
		"",
		`{"provider":"9router","model_requested":"fast-agent","success":"yes","duration_ms":"not-a-number"}`,
		`{"provider":"9router","model_requested":"fast-agent","success":true,"duration_ms":-500,"total_tokens":"forge"}`,
		`{"provider":"evil-vendor","model_requested":"broken-combo","success":true,"verify_checked":true,"verify_passed":true,"duration_ms":1}`,
	)
	writeScorecardLedger(t, ledgerPath, poison...)

	poisonedScores, err := ComputeScorecard(tmpDir, "9router", 20)
	if err != nil {
		t.Fatalf("ComputeScorecard poisoned ledger: %v", err)
	}
	poisonedByModel := scoreByModel(t, poisonedScores)

	if poisonedByModel["broken-combo"].HealthStatus != ModelHealthQuarantined {
		t.Errorf("ASI08: broken-combo must stay QUARANTINED under poisoned ledger, got %s", poisonedByModel["broken-combo"].HealthStatus)
	}
	if poisonedByModel["fast-agent"].HealthStatus != cleanByModel["fast-agent"].HealthStatus {
		t.Errorf("ASI08: fast-agent health changed %s -> %s", cleanByModel["fast-agent"].HealthStatus, poisonedByModel["fast-agent"].HealthStatus)
	}
	if poisonedByModel["fast-agent"].SuccessCount != cleanByModel["fast-agent"].SuccessCount {
		t.Errorf("ASI08: fast-agent success_count changed %d -> %d", cleanByModel["fast-agent"].SuccessCount, poisonedByModel["fast-agent"].SuccessCount)
	}

	poisonedRoute := RouteDeterministic(RouteRequest{
		Prompt:   "Audit internal/harness security boundary",
		Paths:    []string{"internal/harness/runner.go"},
		Manifest: manifest,
	})
	if poisonedRoute.Model != cleanRoute.Model || poisonedRoute.Provider != cleanRoute.Provider {
		t.Errorf("ASI08: routing changed under poison: clean=%s/%s poisoned=%s/%s",
			cleanRoute.Provider, cleanRoute.Model, poisonedRoute.Provider, poisonedRoute.Model)
	}
	if poisonedRoute.Model == "broken-combo" {
		t.Error("ASI08: RouteDeterministic routed to QUARANTINED broken-combo under poisoned ledger")
	}
}
