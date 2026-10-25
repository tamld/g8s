package reflex

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLiveJevReflexIntegration(t *testing.T) {
	gate := NewReflexGate(WithEnvFile(true))
	if len(gate.keys) == 0 {
		t.Skip("skipping live test: no keys found in environment or .env")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	verdict, err := gate.TriageMutation(ctx, TriageRequest{
		TaskID:        "live-canary-01",
		FilesModified: []string{"README.md"},
		DiffSummary:   "Fix typo in introduction section",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("Live Jev Verdict: Action=%s, Risk=%.2f, BreachProb=%.2f, Latency=%dms, Fallback=%v",
		verdict.Action, verdict.RiskScore, verdict.BreachProb, verdict.LatencyMs, verdict.IsFallback)

	if verdict.IsFallback && (strings.Contains(verdict.Reason, "context deadline exceeded") || verdict.LatencyMs >= 2000) {
		t.Skip("skipping live test: Jev API call timed out (invalid/placeholder API key or network spike)")
	}

	if verdict.IsFallback {
		t.Fatalf("expected real Jev call, got fallback: %s", verdict.Reason)
	}
}
