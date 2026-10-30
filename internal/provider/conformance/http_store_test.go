package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/config"
)

func TestProbeHTTPStaticAndLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"fast-agent","context_length":100},{"id":"paid-pro","top_provider":{"context_length":200000}}]}`))
		case "/chat/completions":
			var req struct {
				Model          string           `json:"model"`
				Messages       []map[string]any `json:"messages"`
				Tools          []map[string]any `json:"tools"`
				ResponseFormat map[string]any   `json:"response_format"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Model == badModel {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if len(req.Tools) > 0 {
				_, _ = w.Write([]byte(`{"model":"fast-agent","choices":[{"message":{"tool_calls":[{"function":{"name":"g8s_probe_echo","arguments":"{\"text\":\"PONG\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
				return
			}
			if req.ResponseFormat != nil {
				_, _ = w.Write([]byte(`{"model":"fast-agent","choices":[{"message":{"content":"{\"answer\":\"PONG\"}"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
				return
			}
			_, _ = w.Write([]byte(`{"model":"fast-agent","choices":[{"message":{"content":"PONG"}}],"usage":{"prompt_tokens":120,"completion_tokens":1,"total_tokens":121}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	target := HTTPTarget{
		Name:    "9router",
		BaseURL: srv.URL,
		Model:   "fast-agent",
		Policy:  PolicyFree,
		Client:  srv.Client(),
	}

	// 1. Static mode
	staticRep := ProbeHTTP(context.Background(), target, HTTPOptions{Live: false})
	if !staticRep.Available || staticRep.Mode != "static" {
		t.Fatalf("expected available static report, got %+v", staticRep)
	}
	if p0, ok := staticRep.Probe(ProbeP0); !ok || p0.Status != StatusPass {
		t.Fatalf("expected P0 PASS, got %+v", p0)
	}
	if h1, ok := staticRep.Probe(ProbeH1); !ok || h1.Status != StatusPass {
		t.Fatalf("expected H1 PASS, got %+v", h1)
	}
	if len(staticRep.RolesEligible) == 0 {
		t.Fatal("expected RolesEligible populated")
	}

	// 2. Live mode with free combo
	liveRep := ProbeHTTP(context.Background(), target, HTTPOptions{
		Live:               true,
		ContextProbeTokens: 120,
	})
	if liveRep.Mode != "live" {
		t.Fatalf("expected live mode, got %s", liveRep.Mode)
	}
	for _, id := range []ProbeID{ProbeP0, ProbeH1, ProbeP1, ProbeP2, ProbeP3, ProbeP7, ProbeP8, ProbeH2, ProbeH3} {
		pr, ok := liveRep.Probe(id)
		if !ok || pr.Status != StatusPass {
			t.Errorf("probe %s = %+v, want PASS", id, pr)
		}
	}

	// 3. Live mode blocked by paid model policy
	paidTarget := target
	paidTarget.Model = "paid-pro"
	blockedRep := ProbeHTTP(context.Background(), paidTarget, HTTPOptions{Live: true})
	if p1, _ := blockedRep.Probe(ProbeP1); p1.Status != StatusUnverified || !strings.Contains(p1.Reason, "E_PAID_MODEL_NOT_ALLOWED") {
		t.Errorf("expected E_PAID_MODEL_NOT_ALLOWED on paid-pro, got %+v", p1)
	}

	// 4. Unreachable endpoint
	unreachable := HTTPTarget{Name: "ollama", BaseURL: "http://127.0.0.1:1", Policy: PolicyFree}
	unreachRep := ProbeHTTP(context.Background(), unreachable, HTTPOptions{Live: false})
	if p0, _ := unreachRep.Probe(ProbeP0); p0.Status != StatusUnverified {
		t.Errorf("expected P0 UNVERIFIED for unreachable endpoint, got %+v", p0)
	}
	if len(unreachRep.RolesEligible) == 0 || unreachRep.RolesEligible[0] != "compatibility-only" {
		t.Errorf("expected ollama RolesEligible=[compatibility-only], got %v", unreachRep.RolesEligible)
	}
}

func TestStoreRunnerAndDispatchGate(t *testing.T) {
	dir := t.TempDir()
	matrixPath := filepath.Join(dir, DefaultMatrixPath)
	docPath := filepath.Join(dir, DefaultDocPath)

	cfg := &config.File{
		Providers: []config.ProviderEntry{
			{Name: "9router", Class: "api_call", BaseURL: "http://127.0.0.1:20128/v1"},
		},
	}
	targets := DefaultHTTPTargets(cfg)
	if len(targets) < 2 {
		t.Fatalf("expected at least 9router and ollama targets, got %d", len(targets))
	}
	if !KnownProvider(Options{ProvidersConfig: cfg}, "9router") {
		t.Fatal("expected 9router to be known")
	}
	if KnownProvider(Options{ProvidersConfig: cfg}, "unknown-xyz") {
		t.Fatal("expected unknown-xyz to be false")
	}

	m := Run(context.Background(), Options{
		Providers:   []string{"ollama"},
		CLIProfiles: []CLIProfile{},
		HTTPTargets: []HTTPTarget{{Name: "ollama", BaseURL: "http://127.0.0.1:1", Policy: PolicyFree}},
	})
	if len(m.Providers) != 1 {
		t.Fatalf("expected 1 provider report, got %d", len(m.Providers))
	}

	// Add a failing provider to test CheckDispatch
	failRep := ProviderReport{
		Name:       "broken-cli",
		Class:      ClassCLIAgent,
		Mode:       "static",
		LivePolicy: "free",
		Probes: []ProbeResult{
			newResult(ClassCLIAgent, ProbeP0, "static", StatusFail, "version failed", nil),
		},
	}
	// Provider with P5 fail but P6 pass to test CheckDispatchFromDirWithReceipt mitigation
	mitigatedRep := ProviderReport{
		Name:       "p5-fail-cli",
		Class:      ClassCLIAgent,
		Mode:       "static",
		LivePolicy: "free",
		Probes: []ProbeResult{
			newResult(ClassCLIAgent, ProbeP5, "static", StatusFail, "worktree escape detected", nil),
			newResult(ClassCLIAgent, ProbeP6, "static", StatusPass, "edit lands", nil),
		},
	}
	m.Providers = append(m.Providers, failRep, mitigatedRep)

	merged, err := WriteMatrix(matrixPath, m)
	if err != nil {
		t.Fatalf("WriteMatrix failed: %v", err)
	}
	if err := WriteDoc(docPath, merged); err != nil {
		t.Fatalf("WriteDoc failed: %v", err)
	}
	docBytes, err := os.ReadFile(docPath)
	if err != nil || !strings.Contains(string(docBytes), "Provider Conformance Matrix") {
		t.Fatalf("unexpected doc content: err=%v", err)
	}

	// CheckDispatch from subdir
	subDir := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vFail, err := CheckDispatchFromDir(subDir, "broken-cli")
	if err != nil || !vFail.Refuse || len(vFail.FailedRequired) == 0 {
		t.Fatalf("expected broken-cli dispatch refused, got %+v (err=%v)", vFail, err)
	}
	vUnv, err := CheckDispatchFromDir(subDir, "ollama")
	if err != nil || vUnv.Refuse {
		t.Fatalf("expected UNVERIFIED ollama not to refuse dispatch, got %+v (err=%v)", vUnv, err)
	}
	vMitigatedNoReceipt, err := CheckDispatchFromDirWithReceipt(subDir, "p5-fail-cli", false)
	if err != nil || !vMitigatedNoReceipt.Refuse || len(vMitigatedNoReceipt.FailedRequired) != 1 || vMitigatedNoReceipt.FailedRequired[0] != ProbeP5 {
		t.Fatalf("expected p5-fail-cli without receipt to refuse dispatch with P5 fail, got %+v (err=%v)", vMitigatedNoReceipt, err)
	}
	vMitigatedWithReceipt, err := CheckDispatchFromDirWithReceipt(subDir, "p5-fail-cli", true)
	if err != nil || vMitigatedWithReceipt.Refuse || len(vMitigatedWithReceipt.FailedRequired) != 0 {
		t.Fatalf("expected p5-fail-cli with receipt to mitigate P5 and allow dispatch, got %+v (err=%v)", vMitigatedWithReceipt, err)
	}
}
