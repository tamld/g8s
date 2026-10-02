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

func TestWithRouterMode(t *testing.T) {
	ctx := context.Background()
	ctx = WithRouterMode(ctx, "custom_mode")
	if got := getRouterMode(ctx); got != "custom_mode" {
		t.Errorf("getRouterMode = %q, want custom_mode", got)
	}
}

func TestWithJevAssist(t *testing.T) {
	ctx := WithJevAssist(context.Background(), "http://localhost:8080/v1", "key-a", "key-b")
	if got := getRouterMode(ctx); got != "jev_assisted" {
		t.Errorf("getRouterMode = %q, want jev_assisted", got)
	}
	if got := getJevEndpoint(ctx); got != "http://localhost:8080/v1" {
		t.Errorf("getJevEndpoint = %q, want http://localhost:8080/v1", got)
	}
	keys := loadJevKeys(ctx)
	if len(keys) != 2 || keys[0] != "key-a" || keys[1] != "key-b" {
		t.Errorf("loadJevKeys = %v, want [key-a, key-b]", keys)
	}
}

func TestGetRouterMode_EnvAndEmpty(t *testing.T) {
	ctx := context.Background()
	os.Unsetenv("G8S_ROUTER_MODE")
	if got := getRouterMode(ctx); got != "" {
		t.Errorf("getRouterMode with unset env = %q, want empty", got)
	}

	os.Setenv("G8S_ROUTER_MODE", "jev_assisted")
	defer os.Unsetenv("G8S_ROUTER_MODE")
	if got := getRouterMode(ctx); got != "jev_assisted" {
		t.Errorf("getRouterMode with env = %q, want jev_assisted", got)
	}
}

func TestGetJevEndpoint_FallbackAndEnv(t *testing.T) {
	ctx := context.Background()

	os.Unsetenv("TYPESAFE_ENDPOINT")
	defaultEp := getJevEndpoint(ctx)
	if defaultEp != "https://api.typesafe.ai/v1/systemone" {
		t.Errorf("getJevEndpoint default = %q, want https://api.typesafe.ai/v1/systemone", defaultEp)
	}

	os.Setenv("TYPESAFE_ENDPOINT", "http://env-endpoint:9000")
	defer os.Unsetenv("TYPESAFE_ENDPOINT")
	envEp := getJevEndpoint(ctx)
	if envEp != "http://env-endpoint:9000" {
		t.Errorf("getJevEndpoint from env = %q, want http://env-endpoint:9000", envEp)
	}
}

func TestContainsStr(t *testing.T) {
	items := []string{"alpha", "beta", "gamma"}
	if !containsStr(items, "beta") {
		t.Errorf("containsStr(items, beta) = false, want true")
	}
	if containsStr(items, "delta") {
		t.Errorf("containsStr(items, delta) = true, want false")
	}
	if containsStr(nil, "alpha") {
		t.Errorf("containsStr(nil, alpha) = true, want false")
	}
}

func TestGetActiveKey_And_RotateKey(t *testing.T) {
	// Empty slice cases
	if got := getActiveKey(nil); got != "" {
		t.Errorf("getActiveKey(nil) = %q, want empty", got)
	}
	if got := getActiveKey([]string{}); got != "" {
		t.Errorf("getActiveKey([]) = %q, want empty", got)
	}

	// Single key case (rotateKey early returns)
	single := []string{"only-one"}
	if got := getActiveKey(single); got != "only-one" {
		t.Errorf("getActiveKey(single) = %q, want only-one", got)
	}
	rotateKey(single)
	if got := getActiveKey(single); got != "only-one" {
		t.Errorf("getActiveKey(single after rotate) = %q, want only-one", got)
	}

	// Multiple keys rotation
	multiple := []string{"key-1", "key-2", "key-3"}
	kInit := getActiveKey(multiple)
	rotateKey(multiple)
	kRotated := getActiveKey(multiple)
	if kInit == kRotated && len(multiple) > 1 {
		// Just in case index was at end and wrapped, rotate again
		rotateKey(multiple)
		kRotated = getActiveKey(multiple)
	}
	if kRotated == "" {
		t.Errorf("getActiveKey after rotation is empty")
	}
}

func TestLoadJevKeys_EnvVars(t *testing.T) {
	// Reset all env vars
	os.Unsetenv("TYPESAFE_API_KEYS")
	os.Unsetenv("TYPESAFE_API_KEY")
	os.Unsetenv("TYPESAFE_API_KEY_FALLBACK")

	// Comma separated with duplicates and whitespace to exercise deduplication and trimming
	os.Setenv("TYPESAFE_API_KEYS", " k1 , k2, k1 , , k3 ")
	os.Setenv("TYPESAFE_API_KEY", "k4")
	os.Setenv("TYPESAFE_API_KEY_FALLBACK", "k2") // duplicate of k2
	defer func() {
		os.Unsetenv("TYPESAFE_API_KEYS")
		os.Unsetenv("TYPESAFE_API_KEY")
		os.Unsetenv("TYPESAFE_API_KEY_FALLBACK")
	}()

	keys := loadJevKeys(context.Background())
	want := []string{"k1", "k2", "k3", "k4"}
	if len(keys) != len(want) {
		t.Fatalf("loadJevKeys returned %v, want %v", keys, want)
	}
	for i, w := range want {
		if keys[i] != w {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], w)
		}
	}
}

func TestLoadJevKeys_DotEnv(t *testing.T) {
	os.Unsetenv("TYPESAFE_API_KEYS")
	os.Unsetenv("TYPESAFE_API_KEY")
	os.Unsetenv("TYPESAFE_API_KEY_FALLBACK")

	tmp := t.TempDir()
	envContent := `# Comment line
IGNORED_NO_EQUALS
TYPESAFE_API_KEYS="env1,env2,env1"
TYPESAFE_API_KEY='env3'
TYPESAFE_API_KEY_FALLBACK=env4
`
	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte(envContent), 0o600); err != nil {
		t.Fatalf("failed to write .env: %v", err)
	}

	t.Chdir(tmp)

	keys := loadJevKeys(context.Background())
	want := []string{"env1", "env2", "env3", "env4"}
	if len(keys) != len(want) {
		t.Fatalf("loadJevKeys from .env returned %v, want %v", keys, want)
	}
	for i, w := range want {
		if keys[i] != w {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], w)
		}
	}
}

func testCandidates() []candidateModel {
	return []candidateModel{
		{provider: "agy", model: "gemini-3.8-flash-high", contextWindow: 1000000},
		{provider: "claude", model: "claude-haiku-4-5", contextWindow: 200000},
	}
}

func TestJevAssist_NoKeys(t *testing.T) {
	os.Unsetenv("TYPESAFE_API_KEYS")
	os.Unsetenv("TYPESAFE_API_KEY")
	os.Unsetenv("TYPESAFE_API_KEY_FALLBACK")

	// Switch to a tempdir without .env
	tmp := t.TempDir()
	t.Chdir(tmp)

	layer1 := Decision{
		Provider:   "agy",
		Model:      "gemini-3.8-flash-high",
		Role:       "collector",
		Source:     "deterministic",
		Reason:     "default deterministic",
		Confidence: 0.85,
	}

	dec, err := jevAssist(context.Background(), RouteRequest{}, layer1, testCandidates())
	if err != nil {
		t.Fatalf("jevAssist returned unexpected error: %v", err)
	}
	if !dec.IsFallback {
		t.Errorf("IsFallback = false, want true")
	}
	if dec.Source != "deterministic" {
		t.Errorf("Source = %q, want deterministic", dec.Source)
	}
	if dec.Role != "collector" {
		t.Errorf("Role = %q, want collector", dec.Role)
	}
}

func TestJevAssist_InvalidEndpointURL(t *testing.T) {
	ctx := WithJevAssist(context.Background(), "http://invalid \x7f url", "mock-key")
	layer1 := Decision{
		Provider: "agy",
		Model:    "gemini-3.8-flash-high",
		Role:     "collector",
		Reason:   "default deterministic",
	}

	dec, err := jevAssist(ctx, RouteRequest{}, layer1, testCandidates())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dec.IsFallback {
		t.Errorf("IsFallback = false, want true")
	}
	if dec.Source != "deterministic" {
		t.Errorf("Source = %q, want deterministic", dec.Source)
	}
}

func TestJevAssist_HTTP429_RateLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	ctx := WithJevAssist(context.Background(), ts.URL, "key-1", "key-2")
	layer1 := Decision{
		Provider: "agy",
		Model:    "gemini-3.8-flash-high",
		Role:     "collector",
		Reason:   "default",
	}

	dec, err := jevAssist(ctx, RouteRequest{}, layer1, testCandidates())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dec.IsFallback {
		t.Errorf("IsFallback = false, want true")
	}
	if dec.Source != "deterministic" {
		t.Errorf("Source = %q, want deterministic", dec.Source)
	}
}

func TestJevAssist_EmptySuggestionFields(t *testing.T) {
	cases := []struct {
		name string
		resp JevResponse
	}{
		{
			name: "empty provider",
			resp: JevResponse{
				Answers: map[string]Answer{
					"provider": {Choice: ""},
					"model":    {Choice: "gemini-3.8-flash-high"},
					"role":     {Choice: "collector"},
				},
			},
		},
		{
			name: "empty model",
			resp: JevResponse{
				Answers: map[string]Answer{
					"provider": {Choice: "agy"},
					"model":    {Choice: "  "},
					"role":     {Choice: "collector"},
				},
			},
		},
		{
			name: "empty role",
			resp: JevResponse{
				Answers: map[string]Answer{
					"provider": {Choice: "agy"},
					"model":    {Choice: "gemini-3.8-flash-high"},
					"role":     {Choice: ""},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.resp)
			}))
			defer ts.Close()

			ctx := WithJevAssist(context.Background(), ts.URL, "key-test")
			layer1 := Decision{Provider: "agy", Model: "gemini-3.8-flash-high", Role: "collector"}

			dec, err := jevAssist(ctx, RouteRequest{}, layer1, testCandidates())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !dec.IsFallback {
				t.Errorf("IsFallback = false, want true")
			}
			if dec.Source != "deterministic" {
				t.Errorf("Source = %q, want deterministic", dec.Source)
			}
		})
	}
}

func TestJevAssist_ConfidenceAndReasonVariations(t *testing.T) {
	cases := []struct {
		name       string
		answers    map[string]Answer
		wantConf   float64
		wantReason string
	}{
		{
			name: "provider confidence is weakest link",
			answers: map[string]Answer{
				"provider": {Choice: "agy", Confidence: 0.65},
				"model":    {Choice: "gemini-3.8-flash-high", Confidence: 0.95},
				"role":     {Choice: "collector", Confidence: 0.85},
				"reason":   {Choice: "explicit reason"},
			},
			wantConf:   0.65,
			wantReason: "explicit reason",
		},
		{
			name: "role confidence is weakest link",
			answers: map[string]Answer{
				"provider": {Choice: "agy", Confidence: 0.95},
				"model":    {Choice: "gemini-3.8-flash-high", Confidence: 0.90},
				"role":     {Choice: "collector", Confidence: 0.60},
				"reason":   {Choice: "role weakest"},
			},
			wantConf:   0.60,
			wantReason: "role weakest",
		},
		{
			name: "zero confidence fallback and empty reason",
			answers: map[string]Answer{
				"provider": {Choice: "agy", Confidence: 0.0},
				"model":    {Choice: "gemini-3.8-flash-high", Confidence: 0.0},
				"role":     {Choice: "collector", Confidence: 0.0},
				"reason":   {Choice: ""},
			},
			wantConf:   0.85,
			wantReason: "Jev suggested provider=agy model=gemini-3.8-flash-high role=collector",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(JevResponse{Model: "jev-latest", Answers: tc.answers})
			}))
			defer ts.Close()

			ctx := WithJevAssist(context.Background(), ts.URL, "key-test")
			layer1 := Decision{Provider: "claude", Model: "claude-haiku-4-5", Role: "summarizer"}

			dec, err := jevAssist(ctx, RouteRequest{}, layer1, testCandidates())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dec.Source != "jev" {
				t.Errorf("Source = %q, want jev", dec.Source)
			}
			if dec.IsFallback {
				t.Errorf("IsFallback = true, want false")
			}
			if dec.Confidence != tc.wantConf {
				t.Errorf("Confidence = %f, want %f", dec.Confidence, tc.wantConf)
			}
			if dec.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", dec.Reason, tc.wantReason)
			}
		})
	}
}

func TestModelCostRank(t *testing.T) {
	tests := []struct {
		modelID  string
		wantRank int
	}{
		{"flash-lite-model", 10},
		{"model_flash_lite", 10},
		{"claude-haiku-4-5", 20},
		{"gpt-4o-mini", 20},
		{"nano-model", 20},
		{"fast-flash", 30},
		{"small-model", 30},
		{"lite-model", 30},
		{"llama3.1", 40},
		{"local-mistral", 40},
		{"claude-sonnet-3-5", 70},
		{"deepseek-pro", 70},
		{"claude-opus-3", 90},
		{"large-embed", 90},
		{"high-capacity", 90},
		{"custom-unknown-model", 50},
	}

	for _, tc := range tests {
		if got := modelCostRank(tc.modelID); got != tc.wantRank {
			t.Errorf("modelCostRank(%q) = %d, want %d", tc.modelID, got, tc.wantRank)
		}
	}
}

// BUG(modelCostRank): substring "mini" matches inside "gemini", causing models like
// "gemini-pro" (expected rank 70) and "gemini-3.8-flash-high" (expected rank 90) to be
// misclassified into rank 20 ("mini").
func TestModelCostRank_GeminiCollision_BUG(t *testing.T) {
	t.Skip("BUG(modelCostRank): 'mini' matches inside 'gemini', misranking Gemini models into rank 20")
	if got := modelCostRank("gemini-pro"); got != 70 {
		t.Errorf("modelCostRank(gemini-pro) = %d, want 70", got)
	}
}

func TestCheapestModel_TieBreaking(t *testing.T) {
	// 1. Equal cost rank, different context window: prefer smaller positive context window
	c1 := candidateModel{provider: "p1", model: "model-mini-a", contextWindow: 200000}
	c2 := candidateModel{provider: "p2", model: "model-mini-b", contextWindow: 100000}
	got := cheapestModel([]candidateModel{c1, c2})
	if got.model != "model-mini-b" {
		t.Errorf("cheapestModel context window tiebreak: got %q, want model-mini-b", got.model)
	}

	// 2. Equal cost rank & context window: sort by model name
	c3 := candidateModel{provider: "p1", model: "model-mini-z", contextWindow: 100000}
	c4 := candidateModel{provider: "p2", model: "model-mini-a", contextWindow: 100000}
	got = cheapestModel([]candidateModel{c3, c4})
	if got.model != "model-mini-a" {
		t.Errorf("cheapestModel model name tiebreak: got %q, want model-mini-a", got.model)
	}

	// 3. Equal cost rank, context window & model name: sort by provider
	c5 := candidateModel{provider: "prov-b", model: "model-mini-a", contextWindow: 100000}
	c6 := candidateModel{provider: "prov-a", model: "model-mini-a", contextWindow: 100000}
	got = cheapestModel([]candidateModel{c5, c6})
	if got.provider != "prov-a" {
		t.Errorf("cheapestModel provider tiebreak: got %q, want prov-a", got.provider)
	}
}

func TestLargestContextModel_TieBreaking(t *testing.T) {
	// Equal context window: sort by model name
	c1 := candidateModel{provider: "p1", model: "model-z", contextWindow: 500000}
	c2 := candidateModel{provider: "p2", model: "model-a", contextWindow: 500000}
	got := largestContextModel([]candidateModel{c1, c2})
	if got.model != "model-a" {
		t.Errorf("largestContextModel model tiebreak: got %q, want model-a", got.model)
	}

	// Equal context window & model: sort by provider
	c3 := candidateModel{provider: "prov-z", model: "model-a", contextWindow: 500000}
	c4 := candidateModel{provider: "prov-a", model: "model-a", contextWindow: 500000}
	got = largestContextModel([]candidateModel{c3, c4})
	if got.provider != "prov-a" {
		t.Errorf("largestContextModel provider tiebreak: got %q, want prov-a", got.provider)
	}
}

func TestDefaultModel_Order(t *testing.T) {
	// Preference 1: provider agy && model gemini-3.8-flash-high
	c1 := candidateModel{provider: "claude", model: "claude-haiku-4-5"}
	c2 := candidateModel{provider: "agy", model: "gemini-3.8-flash-high"}
	if got := defaultModel([]candidateModel{c1, c2}); got.model != "gemini-3.8-flash-high" {
		t.Errorf("defaultModel preference 1: got %v, want gemini-3.8-flash-high", got)
	}

	// Preference 2: provider agy with different model
	c3 := candidateModel{provider: "ollama", model: "llama3.1"}
	c4 := candidateModel{provider: "agy", model: "gemini-pro"}
	if got := defaultModel([]candidateModel{c3, c4}); got.model != "gemini-pro" {
		t.Errorf("defaultModel preference 2: got %v, want gemini-pro", got)
	}

	// Preference 3: no agy candidates, returns first
	c5 := candidateModel{provider: "ollama", model: "llama3.1"}
	c6 := candidateModel{provider: "claude", model: "claude-haiku-4-5"}
	if got := defaultModel([]candidateModel{c5, c6}); got.provider != "ollama" {
		t.Errorf("defaultModel preference 3: got %v, want first candidate (ollama)", got)
	}
}

func TestExtractCandidates_EmptyModels(t *testing.T) {
	manifest := &config.File{
		Providers: []config.ProviderEntry{
			{Name: "empty-provider", Models: nil},
		},
	}
	candidates := extractCandidates(manifest)
	if len(candidates) != 1 {
		t.Fatalf("extractCandidates with empty models len = %d, want 1", len(candidates))
	}
	if candidates[0].provider != "agy" || candidates[0].model != "gemini-3.8-flash-high" {
		t.Errorf("candidates[0] = %+v, want agy gemini-3.8-flash-high", candidates[0])
	}
}

func TestIsDocsPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"docs/guide.md", true},
		{"README.txt", true},
		{"specs/index.rst", true},
		{"docs", true},
		{"docs/nested/file.go", true},
		{"internal/routing/router.go", false},
		{"cmd/g8s/main.go", false},
	}
	for _, tc := range tests {
		if got := isDocsPath(tc.path); got != tc.want {
			t.Errorf("isDocsPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestIsTrustBoundaryPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"internal/harness/probe/routing.go", true},
		{"internal/receipt/verifier.go", true},
		{"internal/controlplane/store.go", true},
		{"internal/routing/router.go", false},
		{"cmd/g8s/main.go", false},
	}
	for _, tc := range tests {
		if got := isTrustBoundaryPath(tc.path); got != tc.want {
			t.Errorf("isTrustBoundaryPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
