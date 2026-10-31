package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// OWASP LLM04 — Data and model poisoning: adversarial workers may forge scorecard
// rows or substitute resolved model identities to smuggle a quarantined combo back
// into the routing pool.
//
// OWASP LLM06 — Sensitive information disclosure via mis-attributed model
// outcomes; substitution attempts must remain visible on the rolling scorecard.

func TestRed_LLM04_LLM06_QuarantineBypassAndModelSubstitution(t *testing.T) {
	tmpDir := t.TempDir()
	ledgerPath := filepath.Join(tmpDir, "substitution_events.jsonl")
	t.Setenv("G8S_MODEL_SCORECARD_PATH", ledgerPath)

	manifest := redtestScorecardManifest()

	// Establish QUARANTINED broken-combo with a trailing failure streak.
	for i := 0; i < 3; i++ {
		if err := RecordModelOutcome(tmpDir, ModelOutcomeRecord{
			Provider:       "9router",
			ModelRequested: "broken-combo",
			Success:        false,
			VerifyChecked:  true,
			VerifyPassed:   false,
			DurationMs:     5000,
			ErrorReason:    "verify test failed",
		}); err != nil {
			t.Fatalf("RecordModelOutcome failure: %v", err)
		}
	}
	for i := 0; i < 4; i++ {
		if err := RecordModelOutcome(tmpDir, ModelOutcomeRecord{
			Provider:       "9router",
			ModelRequested: "fast-agent",
			Success:        true,
			VerifyChecked:  true,
			VerifyPassed:   true,
			DurationMs:     1200,
		}); err != nil {
			t.Fatalf("RecordModelOutcome fast-agent: %v", err)
		}
	}

	scoresBefore, err := ComputeScorecard(tmpDir, "9router", 20)
	if err != nil {
		t.Fatalf("ComputeScorecard before attack: %v", err)
	}
	byModel := scoreByModel(t, scoresBefore)
	if byModel["broken-combo"].HealthStatus != ModelHealthQuarantined {
		t.Fatalf("broken-combo status = %s, want QUARANTINED before substitution attack", byModel["broken-combo"].HealthStatus)
	}

	// Adversarial substitution: worker claims fast-agent but resolves to a different upstream id.
	substitution := ModelOutcomeRecord{
		Provider:       "9router",
		ModelRequested: "fast-agent",
		ModelResolved:  "evil-upstream-smuggled-model",
		Success:        true,
		VerifyChecked:  true,
		VerifyPassed:   true,
		DurationMs:     900,
	}
	if err := RecordModelOutcome(tmpDir, substitution); err != nil {
		t.Fatalf("RecordModelOutcome substitution: %v", err)
	}

	// Quarantine-bypass attempt: forged consecutive-success streak for broken-combo placed
	// before the trailing failure streak (must not reduce consecutive tail failures).
	for i := 0; i < 5; i++ {
		line, marshalErr := json.Marshal(ModelOutcomeRecord{
			Provider:       "9router",
			ModelRequested: "broken-combo",
			ModelResolved:  "totally-trusted-alias",
			Success:        true,
			VerifyChecked:  true,
			VerifyPassed:   true,
			DurationMs:     50,
		})
		if marshalErr != nil {
			t.Fatalf("marshal forged success: %v", marshalErr)
		}
		// Insert forged rows ahead of the failure streak by rewriting the ledger.
		raw, readErr := os.ReadFile(ledgerPath)
		if readErr != nil {
			t.Fatalf("read ledger: %v", readErr)
		}
		prefix := string(raw)
		if err := os.WriteFile(ledgerPath, append([]byte(prefix), append(line, '\n')...), 0o600); err != nil {
			t.Fatalf("rewrite ledger with prefix forgery: %v", err)
		}
	}
	// Re-append the legitimate trailing failure streak so deny-by-default quarantine holds.
	for i := 0; i < 3; i++ {
		if err := RecordModelOutcome(tmpDir, ModelOutcomeRecord{
			Provider:       "9router",
			ModelRequested: "broken-combo",
			Success:        false,
			VerifyChecked:  true,
			VerifyPassed:   false,
			DurationMs:     6000,
			ErrorReason:    "verify test failed",
		}); err != nil {
			t.Fatalf("re-append failure streak: %v", err)
		}
	}

	scoresAfter, err := ComputeScorecard(tmpDir, "9router", 20)
	if err != nil {
		t.Fatalf("ComputeScorecard after attack: %v", err)
	}
	after := scoreByModel(t, scoresAfter)

	if after["broken-combo"].HealthStatus != ModelHealthQuarantined {
		t.Errorf("LLM04: deny-by-default failed — broken-combo became %s after bypass attempt", after["broken-combo"].HealthStatus)
	}
	if after["fast-agent"].LastResolvedModel != substitution.ModelResolved {
		t.Errorf("LLM06: substitution attempt not recorded on scorecard: LastResolvedModel=%q want %q",
			after["fast-agent"].LastResolvedModel, substitution.ModelResolved)
	}

	dec := RouteDeterministic(RouteRequest{
		Prompt:   "Audit internal/harness security boundary",
		Paths:    []string{"internal/harness/runner.go"},
		Manifest: manifest,
	})
	if dec.Model == "broken-combo" {
		t.Fatal("LLM04: RouteDeterministic selected QUARANTINED broken-combo after substitution/bypass attempt")
	}
	if dec.Model != "fast-agent" {
		t.Errorf("RouteDeterministic model = %q, want fast-agent", dec.Model)
	}
}

func TestRed_LLM04_StaleWindowForgedStreakCannotReviveQuarantine(t *testing.T) {
	tmpDir := t.TempDir()
	ledgerPath := filepath.Join(tmpDir, "stale_window.jsonl")
	t.Setenv("G8S_MODEL_SCORECARD_PATH", ledgerPath)

	window := 4
	lines := []string{
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 4000, ErrorReason: "fail",
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 4000, ErrorReason: "fail",
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 4000, ErrorReason: "fail",
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "fast-agent", Success: true,
			VerifyChecked: true, VerifyPassed: true, DurationMs: 1000,
		}),
	}
	writeScorecardLedger(t, ledgerPath, lines...)

	scores, err := ComputeScorecard(tmpDir, "9router", window)
	if err != nil {
		t.Fatalf("ComputeScorecard: %v", err)
	}
	byModel := scoreByModel(t, scores)
	if byModel["broken-combo"].HealthStatus != ModelHealthQuarantined {
		t.Fatalf("expected QUARANTINED within window=%d, got %s", window, byModel["broken-combo"].HealthStatus)
	}

	// Attacker replays forged successes, but legitimate trailing failures must preserve quarantine.
	forgedOnly := make([]string, window)
	for i := range forgedOnly {
		forgedOnly[i] = mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: true,
			VerifyChecked: true, VerifyPassed: true, DurationMs: 10,
			ModelResolved: "wrong-model-id-smuggled",
		})
	}
	trailingFailures := []string{
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 4000, ErrorReason: "fail",
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 4000, ErrorReason: "fail",
		}),
		mustJSONLine(t, ModelOutcomeRecord{
			Provider: "9router", ModelRequested: "broken-combo", Success: false,
			VerifyChecked: true, VerifyPassed: false, DurationMs: 4000, ErrorReason: "fail",
		}),
	}
	writeScorecardLedger(t, ledgerPath, append(append(lines, forgedOnly...), trailingFailures...)...)

	mixedScores, err := ComputeScorecard(tmpDir, "9router", 20)
	if err != nil {
		t.Fatalf("ComputeScorecard mixed: %v", err)
	}
	mixed := scoreByModel(t, mixedScores)
	if mixed["broken-combo"].LastResolvedModel != "wrong-model-id-smuggled" {
		t.Errorf("LLM06: expected substitution fingerprint on forged streak, got %q", mixed["broken-combo"].LastResolvedModel)
	}
	if mixed["broken-combo"].HealthStatus != ModelHealthQuarantined {
		t.Fatalf("LLM04: trailing failures must keep broken-combo QUARANTINED in mixed ledger, got %s", mixed["broken-combo"].HealthStatus)
	}
	manifest := redtestScorecardManifest()
	decMixed := RouteDeterministic(RouteRequest{
		Prompt:   "Audit internal/harness security boundary",
		Paths:    []string{"internal/harness/runner.go"},
		Manifest: manifest,
	})
	if decMixed.Model == "broken-combo" {
		t.Fatal("LLM04: RouteDeterministic must not route to QUARANTINED broken-combo in mixed ledger")
	}
}
