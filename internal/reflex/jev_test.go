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

func TestScopeBoundaryAbsoluteAndTraversingPaths(t *testing.T) {
	wildcard := []string{"**"}

	maliciousPaths := []string{
		"/etc/passwd",
		"/var/log/syslog",
		`\Windows\System32\cmd.exe`,
		`C:/Windows/System32/drivers/etc/hosts`,
		`D:\secrets\keys.txt`,
		`c:/foo/bar`,
		`//10.0.0.1/share/payload`,
		`\\server\share\file.txt`,
		`../outside.txt`,
		`src/../../escape.txt`,
		"",
		".",
	}

	for _, badPath := range maliciousPaths {
		if isWithinScope([]string{badPath}, wildcard) {
			t.Errorf("SECURITY: absolute or traversing path %q was erroneously allowed within scope", badPath)
		}
	}
}

func TestGlobstarRecursiveMatching(t *testing.T) {
	// 1. Recursive wildcard "**" matches everything inside repo
	if !isWithinScope([]string{"a.go", "src/pkg/deep/nested.go"}, []string{"**"}) {
		t.Errorf("expected ** to match all repo paths")
	}

	// 2. "src/**" matches files under src at arbitrary depth
	srcAllowed := []string{"src/**"}
	if !isWithinScope([]string{"src/main.go", "src/a/b/c/nested.go"}, srcAllowed) {
		t.Errorf("expected src/** to match deep nested files under src")
	}
	if isWithinScope([]string{"other/main.go"}, srcAllowed) {
		t.Errorf("src/** should not match other/main.go")
	}
	if isWithinScope([]string{"src_sibling/main.go"}, srcAllowed) {
		t.Errorf("src/** should not match src_sibling/main.go")
	}

	// 3. "**/*.go" matches .go files at any depth
	goAllowed := []string{"**/*.go"}
	if !isWithinScope([]string{"main.go", "pkg/util.go", "pkg/deep/sub/file.go"}, goAllowed) {
		t.Errorf("expected **/*.go to match Go files at root and subdirs")
	}
	if isWithinScope([]string{"main.js"}, goAllowed) {
		t.Errorf("**/*.go should not match main.js")
	}
	if isWithinScope([]string{"pkg/deep/sub/file.rs"}, goAllowed) {
		t.Errorf("**/*.go should not match file.rs")
	}

	// 4. "docs/**/guide.md" matches guide.md at varying intermediate depths
	docAllowed := []string{"docs/**/guide.md"}
	if !isWithinScope([]string{"docs/guide.md"}, docAllowed) {
		t.Errorf("expected docs/**/guide.md to match docs/guide.md (zero intermediate dirs)")
	}
	if !isWithinScope([]string{"docs/intro/guide.md"}, docAllowed) {
		t.Errorf("expected docs/**/guide.md to match docs/intro/guide.md (one intermediate dir)")
	}
	if !isWithinScope([]string{"docs/intro/advanced/guide.md"}, docAllowed) {
		t.Errorf("expected docs/**/guide.md to match docs/intro/advanced/guide.md (multiple intermediate dirs)")
	}
	if isWithinScope([]string{"docs/guide.txt"}, docAllowed) {
		t.Errorf("docs/**/guide.md should not match docs/guide.txt")
	}
	if isWithinScope([]string{"other/docs/guide.md"}, docAllowed) {
		t.Errorf("docs/**/guide.md should not match other/docs/guide.md")
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

func TestReflexGateOptions(t *testing.T) {
	gate := NewReflexGate(WithTimeout(100 * time.Millisecond))
	if gate.client.Timeout != 100*time.Millisecond {
		t.Fatalf("expected timeout 100ms, got %v", gate.client.Timeout)
	}
}

func TestKeyRotation(t *testing.T) {
	gate := NewReflexGate(WithKeys([]string{"k1", "k2", "k3"}))
	if gate.currentKey() != "k1" {
		t.Fatalf("expected k1, got %s", gate.currentKey())
	}
	if k := gate.rotateKey(); k != "k2" {
		t.Fatalf("expected k2, got %s", k)
	}
	if k := gate.rotateKey(); k != "k3" {
		t.Fatalf("expected k3, got %s", k)
	}
	if k := gate.rotateKey(); k != "k1" {
		t.Fatalf("expected k1 after wrap, got %s", k)
	}

	emptyGate := NewReflexGate(WithKeys([]string{}))
	if emptyGate.currentKey() != "" {
		t.Fatalf("expected empty key")
	}
	if emptyGate.rotateKey() != "" {
		t.Fatalf("expected empty key")
	}
}

func TestLoadKeysFromEnvFile(t *testing.T) {
	origKeys := os.Getenv("TYPESAFE_API_KEYS")
	os.Unsetenv("TYPESAFE_API_KEYS")
	origKey := os.Getenv("TYPESAFE_API_KEY")
	os.Unsetenv("TYPESAFE_API_KEY")
	origFallback := os.Getenv("TYPESAFE_API_KEY_FALLBACK")
	os.Unsetenv("TYPESAFE_API_KEY_FALLBACK")
	defer func() {
		if origKeys != "" {
			os.Setenv("TYPESAFE_API_KEYS", origKeys)
		}
		if origKey != "" {
			os.Setenv("TYPESAFE_API_KEY", origKey)
		}
		if origFallback != "" {
			os.Setenv("TYPESAFE_API_KEY_FALLBACK", origFallback)
		}
	}()

	tmp := t.TempDir()
	origWd, _ := os.Getwd()
	_ = os.Chdir(tmp)
	defer func() { _ = os.Chdir(origWd) }()

	content := "# comment line\nINVALID_LINE\nTYPESAFE_API_KEYS=\"env_key1, env_key2\"\nTYPESAFE_API_KEY=env_single\nTYPESAFE_API_KEY_FALLBACK='env_fallback'\n"
	_ = os.WriteFile(".env", []byte(content), 0o600)

	gate := NewReflexGate(WithEnvFile(true))
	if len(gate.keys) < 4 {
		t.Fatalf("expected at least 4 keys loaded from .env, got %d: %v", len(gate.keys), gate.keys)
	}
}

func TestEmitSignalHTTPFailures(t *testing.T) {
	reqCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		if reqCount == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	gate := NewReflexGate(
		WithEndpoint(ts.URL),
		WithKeys([]string{"k1", "k2"}),
		WithEnvFile(false),
	)

	req := TriageRequest{TaskID: "t-fail", FilesModified: []string{"test.go"}}

	// First request triggers 429 and key rotation
	sig1, err := gate.EmitSignal(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sig1.IsFallback || !strings.Contains(sig1.Reason, "429") {
		t.Fatalf("expected 429 fallback, got %v", sig1)
	}

	// Second request triggers 500
	sig2, err := gate.EmitSignal(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sig2.IsFallback || !strings.Contains(sig2.Reason, "500") {
		t.Fatalf("expected 500 fallback, got %v", sig2)
	}
}

// #329 Red Test: a live Jev response that omits confidence must no longer
// deadlock the grant fast-path. With the calibration fix, a docs-only
// mutation scored low-risk by the sensor gets its confidence derived from
// the deterministic classifier and EvaluatePolicy grants the receipt.
func TestLiveJevMissingConfidenceCalibration(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "jev-latest",
			"answers": {
				"risk_tier": {"type": "score", "score": 0.5, "confidence": 0},
				"sandbox_breach": {"type": "noul", "noul": 0.02, "confidence": 0}
			},
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`))
	}))
	defer ts.Close()

	gate := NewReflexGate(WithEndpoint(ts.URL), WithKeys([]string{"test_key"}), WithEnvFile(false))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	signal, err := gate.EmitSignal(ctx, TriageRequest{
		TaskID:        "calibration-test",
		FilesModified: []string{"README.md"},
		DiffSummary:   "typo fix in docs",
	})
	if err != nil {
		t.Fatalf("EmitSignal: %v", err)
	}
	if signal.Source != "jev" {
		t.Fatalf("expected live jev source, got %q (%s)", signal.Source, signal.Reason)
	}
	if signal.Confidence <= 0 {
		t.Fatalf("confidence must be derived from the deterministic classifier when Jev omits it")
	}

	verdict := gate.EvaluatePolicy(signal, TriageRequest{
		TaskID:        "calibration-test",
		FilesModified: []string{"README.md"},
		DiffSummary:   "typo fix in docs",
	})
	if verdict.Action != ActionGrantReceipt {
		t.Fatalf("docs-only low-risk mutation should reach the grant fast-path, got %s (%s)", verdict.Action, verdict.Reason)
	}
}
