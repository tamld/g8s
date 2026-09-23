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
	// Mock Jev System 1 endpoint
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		resp := JevResponse{
			Model: "jev-1.13.0",
			Answers: map[string]Answer{
				"action": {
					Type:          "choice",
					Choice:        "grant_receipt",
					Confidence:    0.88,
					Probabilities: map[string]float64{"grant_receipt": 0.88, "escalate_human": 0.12},
				},
				"risk_tier": {
					Type:          "score",
					Score:         1.1,
					Confidence:    0.90,
					Probabilities: map[string]float64{"low": 0.85, "medium": 0.15},
				},
				"sandbox_breach": {
					Type: "noul",
					Noul: 0.05,
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
}

func TestTriageCircuitBreakerInstantKill(t *testing.T) {
	// Mock Jev returning high breach probability
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := JevResponse{
			Model: "jev-1.13.0",
			Answers: map[string]Answer{
				"action": {
					Type:          "choice",
					Choice:        "escalate_human",
					Confidence:    0.95,
					Probabilities: map[string]float64{"escalate_human": 0.95},
				},
				"risk_tier": {
					Type:          "score",
					Score:         4.0,
					Confidence:    0.95,
					Probabilities: map[string]float64{"critical": 0.95},
				},
				"sandbox_breach": {
					Type: "noul",
					Noul: 0.92, // Severe violation
				},
			},
		}
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

func TestOfflineGracefulFallback(t *testing.T) {
	// Gate without keys should fall back deterministically without error
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
