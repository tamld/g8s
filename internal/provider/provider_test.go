package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func stubLookPath(paths map[string]string) func(string) (string, error) {
	return func(binary string) (string, error) {
		if p, ok := paths[binary]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
}

func newSmallRegistry(t *testing.T, maxConcurrency int) *Registry {
	t.Helper()
	cfg := Config{
		Name:           "agy",
		Binary:         "agy",
		OverrideEnvVar: "AGY_BIN",
		MaxConcurrency: maxConcurrency,
		Models: []ModelDescriptor{
			{ID: "gemini-3.7-flash-high", Name: "Gemini 3.7 Flash (High)", SupportedRoles: []string{"collector"}, ContextWindow: 1000000, MaxOutputTokens: 65536},
		},
	}
	return NewRegistry([]Config{cfg}, nil, func(string) (string, error) { return "/usr/local/bin/agy", nil })
}

func TestMissingBinariesReportUnavailable(t *testing.T) {
	r := NewRegistry(DefaultConfigs(), nil, stubLookPath(nil))
	infos, err := r.DiscoverAll(context.Background())
	if err != nil {
		t.Fatalf("DiscoverAll: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(infos))
	}
	for _, info := range infos {
		if info.Status != StatusUnavailable {
			t.Errorf("%s status = %q, want UNAVAILABLE", info.Name, info.Status)
		}
		if info.BinaryPath != "" {
			t.Errorf("%s binary path = %q, want empty", info.Name, info.BinaryPath)
		}
	}
}

func TestResolvedBinaryReportsReady(t *testing.T) {
	r := NewRegistry(DefaultConfigs(), nil, stubLookPath(map[string]string{"agy": "/usr/local/bin/agy"}))
	infos, _ := r.DiscoverAll(context.Background())
	for _, info := range infos {
		if info.Name != "agy" {
			continue
		}
		if info.Status != StatusReady {
			t.Errorf("agy status = %q, want READY", info.Status)
		}
		if info.BinaryPath != "/usr/local/bin/agy" {
			t.Errorf("binary path = %q", info.BinaryPath)
		}
	}
}

func TestOverrideEnvVarBeatsLookPath(t *testing.T) {
	t.Setenv("AGY_BIN", "/opt/override/agy")
	r := NewRegistry(DefaultConfigs(), nil, stubLookPath(map[string]string{"agy": "/usr/bin/agy"}))
	info, err := r.GetProvider("agy")
	if err == nil && info.BinaryPath == "" {
		t.Log("provider not discovered yet; discovering")
	}
	infos, _ := r.DiscoverAll(context.Background())
	for _, i := range infos {
		if i.Name == "agy" && i.BinaryPath != "/opt/override/agy" {
			t.Errorf("binary path = %q, want override", i.BinaryPath)
		}
	}
}

func TestGetProviderUnknownErrors(t *testing.T) {
	r := NewRegistry(DefaultConfigs(), nil, nil)
	if _, err := r.GetProvider("nonexistent"); err == nil || err.Error() != "unknown provider: nonexistent" {
		t.Fatalf("err = %v, want unknown provider message", err)
	}
}

func TestAcquireSlotRespectsMaxConcurrency(t *testing.T) {
	r := newSmallRegistry(t, 2)
	ctx := context.Background()
	release1, err := r.AcquireSlot(ctx, "agy")
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	defer release1()
	release2, err := r.AcquireSlot(ctx, "agy")
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	defer release2()

	blockedCtx, cancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := r.AcquireSlot(blockedCtx, "agy"); !errors.Is(err, context.Canceled) {
		t.Fatalf("third acquire err = %v, want context.Canceled after cancel", err)
	}
}

func TestReleaseFreesCapacity(t *testing.T) {
	r := newSmallRegistry(t, 1)
	ctx := context.Background()
	release, err := r.AcquireSlot(ctx, "agy")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()
	done := make(chan struct{})
	go func() {
		r2, err := r.AcquireSlot(ctx, "agy")
		if err != nil {
			t.Errorf("re-acquire: %v", err)
			close(done)
			return
		}
		r2()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("slot was not released")
	}
}

func TestOllamaProbeStates(t *testing.T) {
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()

	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badServer.Close()

	closedAddr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedAddr.URL
	closedAddr.Close()

	cases := []struct {
		name string
		host string
		want ProviderStatus
	}{
		{"ready on 200", okServer.URL, StatusReady},
		{"degraded on non-200", badServer.URL, StatusDegraded},
		{"unreachable is unavailable", closedURL, StatusUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OLLAMA_HOST", tc.host)
			cfgs := DefaultConfigs()
			var ollamaCfg *Config
			for i := range cfgs {
				if cfgs[i].Name == "ollama" {
					cfgs[i].HealthProbeURL = "/api/tags"
					ollamaCfg = &cfgs[i]
				}
			}
			if ollamaCfg == nil {
				t.Fatal("no ollama config in defaults")
			}
			r := NewRegistry(cfgs, nil, stubLookPath(nil))
			infos, err := r.DiscoverAll(context.Background())
			if err != nil {
				t.Fatalf("DiscoverAll: %v", err)
			}
			for _, info := range infos {
				if info.Name == "ollama" && info.Status != tc.want {
					t.Errorf("ollama status = %q, want %q", info.Status, tc.want)
				}
			}
		})
	}
}

func TestModelDescriptorJSONRoundTripVerbatimTags(t *testing.T) {
	m := ModelDescriptor{
		ID:              "gemini-3.7-flash-high",
		Name:            "Gemini 3.7 Flash (High)",
		SupportedRoles:  []string{"collector", "verifier"},
		ContextWindow:   1000000,
		MaxOutputTokens: 65536,
		IsLocal:         false,
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wantKeys := []string{`"id":`, `"supported_roles":`, `"max_output_tokens":`, `"is_local":`}
	for _, key := range wantKeys {
		if !contains(string(raw), key) {
			t.Errorf("json missing verbatim tag %s in %s", key, raw)
		}
	}
	var back ModelDescriptor
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.ID != m.ID || back.ContextWindow != m.ContextWindow || len(back.SupportedRoles) != 2 {
		t.Errorf("roundtrip mismatch: %+v", back)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestConcurrentSlotAccountingNeverExceedsMax(t *testing.T) {
	const maxSlots = 3
	r := newSmallRegistry(t, maxSlots)
	ctx := context.Background()

	var wg sync.WaitGroup
	var mu sync.Mutex
	peak := 0
	current := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := r.AcquireSlot(ctx, "agy")
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			mu.Lock()
			current++
			if current > peak {
				peak = current
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			current--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()
	if peak > maxSlots {
		t.Errorf("peak concurrency %d exceeded max %d", peak, maxSlots)
	}
}

func TestHealthCheckTimestampUsesInjectedClock(t *testing.T) {
	fixed := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	clockCalls := 0
	clock := func() time.Time {
		clockCalls++
		return fixed
	}
	r := NewRegistry(DefaultConfigs(), clock, stubLookPath(nil))
	infos, _ := r.DiscoverAll(context.Background())
	for _, info := range infos {
		if info.LastHealthCheckAt != fixed.Unix() {
			t.Errorf("%s LastHealthCheckAt = %d, want %d", info.Name, info.LastHealthCheckAt, fixed.Unix())
		}
	}
	if clockCalls == 0 {
		t.Error("injected clock never consulted")
	}
}

func TestDiscoveryOrderIsSortedAndStable(t *testing.T) {
	r := NewRegistry(DefaultConfigs(), nil, stubLookPath(nil))
	first, _ := r.DiscoverAll(context.Background())
	second, _ := r.DiscoverAll(context.Background())
	if len(first) != len(second) {
		t.Fatalf("snapshot sizes differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Name >= second[i].Name && i > 0 && first[i-1].Name >= first[i].Name {
			t.Errorf("order not sorted at index %d: %q then %q", i, first[i-1].Name, first[i].Name)
		}
	}
	want := []string{"agy", "claude", "ollama"}
	for i, name := range want {
		if first[i].Name != name {
			t.Errorf("index %d = %q, want %q", i, first[i].Name, name)
		}
	}
}
