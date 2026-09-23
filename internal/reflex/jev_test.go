package reflex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestKeyPoolLoading(t *testing.T) {
	origKeys := os.Getenv("TYPESAFE_API_KEYS")
	os.Unsetenv("TYPESAFE_API_KEYS")
	defer func() {
		if origKeys != "" {
			os.Setenv("TYPESAFE_API_KEYS", origKeys)
		}
	}()

	os.Setenv("TYPESAFE_API_KEY", "primary_key")
	os.Setenv("TYPESAFE_API_KEY_FALLBACK", "secondary_key")
	defer os.Unsetenv("TYPESAFE_API_KEY")
	defer os.Unsetenv("TYPESAFE_API_KEY_FALLBACK")

	gate := NewReflexGate(WithEnvFile(false))
	if len(gate.keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(gate.keys))
	}
	if gate.keys[0] != "primary_key" || gate.keys[1] != "secondary_key" {
		t.Fatalf("unexpected keys: %v", gate.keys)
	}
}

func TestTriageWithMockJev(t *testing.T) {
	// Mock Jev System 1 endpoint emitting pure telemetry (no action questions)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		resp := JevResponse{
			Model: "jev-1.13.0",
			Answers: map[string]Answer{
				"risk_tier": {
					Type:          "score",
					Score:         1.1,
					Confidence:    0.90,
					Probabilities: map[string]float64{"low": 0.85, "medium": 0.15},
				},
				"sandbox_breach": {
					Type:       "noul",
					Noul:       0.05,
					Confidence: 0.95,
				},
			},
			Usage: Usage{InputTokens: 250, OutputTokens: 30},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	gate := NewReflexGate(
		WithEndpoint(ts.URL),
		WithKeys([]string{"test_key"}),
		WithEnvFile(false),
	)

	req := TriageRequest{
		TaskID:        "task-101",
		FilesModified: []string{"README.md"},
		DiffSummary:   "update docs",
		AllowedPaths:  []string{"*.md"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	verdict, err := gate.TriageMutation(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if verdict.Action != ActionGrantReceipt {
		t.Fatalf("expected ActionGrantReceipt, got %s", verdict.Action)
	}
	if verdict.BreachProb != 0.05 {
		t.Fatalf("expected breach prob 0.05, got %f", verdict.BreachProb)
	}
	if verdict.RiskScore != 1.1 {
		t.Fatalf("expected risk score 1.1, got %f", verdict.RiskScore)
	}
	if verdict.Signal.Source != "jev" {
		t.Fatalf("expected source jev, got %s", verdict.Signal.Source)
	}
}

func TestTriageCircuitBreakerInstantKill(t *testing.T) {
	// Mock Jev returning high breach probability
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := JevResponse{
			Model: "jev-1.13.0",
			Answers: map[string]Answer{
				"risk_tier": {
					Type:          "score",
					Score:         4.0,
					Confidence:    0.95,
					Probabilities: map[string]float64{"critical": 0.95},
				},
				"sandbox_breach": {
					Type:       "noul",
					Noul:       0.92, // Severe violation
					Confidence: 0.98,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	gate := NewReflexGate(
		WithEndpoint(ts.URL),
		WithKeys([]string{"test_key"}),
		WithEnvFile(false),
	)

	req := TriageRequest{
		TaskID:        "malicious-subagent",
		FilesModified: []string{"/root/.ssh/id_rsa"},
		DiffSummary:   "steal credentials",
	}

	verdict, err := gate.TriageMutation(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if verdict.Action != ActionInstantKill {
		t.Fatalf("expected ActionInstantKill on breach prob 0.92, got %s", verdict.Action)
	}
}

func TestScopeViolationRejection(t *testing.T) {
	// Even if Jev reports low risk, Supervisor policy MUST reject scope violations
	gate := NewReflexGate(
		WithKeys([]string{}),
		WithEnvFile(false),
	)

	signal := ReflexSignal{
		RiskScore:  1.0,
		BreachProb: 0.05,
		Confidence: 0.95,
		Source:     "jev",
	}

	req := TriageRequest{
		TaskID:        "task-scope-test",
		FilesModified: []string{"cmd/g8s/main.go"},
		AllowedPaths:  []string{"internal/review/*"}, // main.go is NOT allowed
	}

	verdict := gate.EvaluatePolicy(signal, req)
	if verdict.Action != ActionEscalateHITL {
		t.Fatalf("expected ActionEscalateHITL on scope violation, got %s", verdict.Action)
	}
	if !strings.Contains(verdict.Reason, "scope violation") {
		t.Errorf("expected scope violation reason, got: %s", verdict.Reason)
	}
}

func TestOfflineGracefulFallback(t *testing.T) {
	gate := NewReflexGate(
		WithKeys([]string{}),
		WithEnvFile(false),
	)

	// Safe doc edit
	safeReq := TriageRequest{
		TaskID:        "doc-task",
		FilesModified: []string{"notes/design.md"},
	}
	verdict, err := gate.TriageMutation(context.Background(), safeReq)
	if err != nil {
		t.Fatalf("fallback should not return error: %v", err)
	}
	if verdict.Action != ActionGrantReceipt {
		t.Fatalf("expected ActionGrantReceipt for doc edit, got %s", verdict.Action)
	}
	if verdict.Signal.Source != "deterministic" {
		t.Errorf("expected deterministic fallback source, got %s", verdict.Signal.Source)
	}

	// Unsafe secret access
	secretReq := TriageRequest{
		TaskID:        "hack-task",
		FilesModified: []string{".env", "id_rsa"},
	}
	verdictSecret, err := gate.TriageMutation(context.Background(), secretReq)
	if err != nil {
		t.Fatalf("fallback should not return error: %v", err)
	}
	if verdictSecret.Action != ActionInstantKill {
		t.Fatalf("expected ActionInstantKill for credential files, got %s", verdictSecret.Action)
	}
}

func TestDLPNoHardcodedKeys(t *testing.T) {
	keyPattern := regexp.MustCompile(`apikey_[0-9a-f]{40}_[0-9a-f]{64}`)

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if matches := keyPattern.FindAll(data, -1); len(matches) > 0 {
			t.Errorf("DLP VIOLATION: Hardcoded API key found in %s: %s", path, matches)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
}

func TestScopeBoundarySiblingDirectoryDefense(t *testing.T) {
	// Sibling directories should NOT match prefix
	if isWithinScope([]string{"docs_secret/passwords.txt"}, []string{"docs/*"}) {
		t.Errorf("SECURITY: docs_secret should not match docs/*")
	}
	if isWithinScope([]string{"internal_exploit/hack.go"}, []string{"internal"}) {
		t.Errorf("SECURITY: internal_exploit should not match internal")
	}
	if isWithinScope([]string{"../../etc/passwd"}, []string{"*"}) {
		t.Errorf("SECURITY: path traversal should never be within scope")
	}

	// Valid paths should match
	if !isWithinScope([]string{"docs/guide.md"}, []string{"docs/*"}) {
		t.Errorf("expected docs/guide.md to match docs/*")
	}
	if !isWithinScope([]string{"internal/reflex/jev.go"}, []string{"internal"}) {
		t.Errorf("expected internal/reflex/jev.go to match internal")
	}
}

func TestWeakestLinkConfidenceAggregation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := JevResponse{
			Model: "jev-1.13.0",
			Answers: map[string]Answer{
				"risk_tier": {
					Type:       "score",
					Score:      1.0,
					Confidence: 0.20, // Low confidence on risk!
				},
				"sandbox_breach": {
					Type:       "noul",
					Noul:       0.05,
					Confidence: 0.95, // High confidence on breach
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	gate := NewReflexGate(
		WithEndpoint(ts.URL),
		WithKeys([]string{"test-key"}),
		WithEnvFile(false),
	)

	req := TriageRequest{
		TaskID:        "task-weakest-link",
		FilesModified: []string{"README.md"},
		AllowedPaths:  []string{"README.md"},
	}

	signal, err := gate.EmitSignal(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal.Confidence != 0.20 {
		t.Fatalf("expected confidence to be min(0.20, 0.95) = 0.20, got %.2f", signal.Confidence)
	}

	verdict := gate.EvaluatePolicy(signal, req)
	if verdict.Action != ActionEscalateHITL {
		t.Fatalf("expected ActionEscalateHITL due to low confidence (0.20 < 0.80), got %s", verdict.Action)
	}
}

func TestMissingAnswerKeyFailClosed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Missing "sandbox_breach" key
		resp := JevResponse{
			Model: "jev-1.13.0",
			Answers: map[string]Answer{
				"risk_tier": {
					Type:       "score",
					Score:      1.0,
					Confidence: 0.95,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	gate := NewReflexGate(
		WithEndpoint(ts.URL),
		WithKeys([]string{"test-key"}),
		WithEnvFile(false),
	)

	req := TriageRequest{
		TaskID:        "task-missing-key",
		FilesModified: []string{"cmd/g8s/main.go"},
	}

	signal, err := gate.EmitSignal(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal.Source != "deterministic" || !signal.IsFallback {
		t.Errorf("expected deterministic fallback on missing key, got source=%s isFallback=%v", signal.Source, signal.IsFallback)
	}
}
