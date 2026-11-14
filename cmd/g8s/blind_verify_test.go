package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/provider"
)

func TestParseBlindVerdict(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantVerdict string
		wantPassed  bool
		wantErr     bool
	}{
		{
			name:        "accepted verdict field",
			input:       `{"verdict":"ACCEPTED","summary":"clean","findings":[]}`,
			wantVerdict: "ACCEPTED",
			wantPassed:  true,
		},
		{
			name:        "rejected with counterexample",
			input:       `{"verdict":"REJECTED","findings":["race"],"counterexamples":["concurrent map write"]}`,
			wantVerdict: "REJECTED",
			wantPassed:  false,
		},
		{
			name:        "legacy passed bool in markdown fence",
			input:       "```json\n{\"passed\": true, \"findings\": [\"ok\"]}\n```",
			wantVerdict: "ACCEPTED",
			wantPassed:  true,
		},
		{
			name:        "reasoning think block prefix before JSON",
			input:       "<think>Analyzing packet with {braces} inside thinking...</think>\n{\"verdict\":\"ACCEPTED\",\"passed\":true}",
			wantVerdict: "ACCEPTED",
			wantPassed:  true,
		},
		{
			name:    "contradictory accepted verdict with passed false fails closed",
			input:   `{"verdict":"ACCEPTED","passed":false}`,
			wantErr: true,
		},
		{
			name:    "contradictory rejected verdict with passed true fails closed",
			input:   `{"verdict":"REJECTED","passed":true}`,
			wantErr: true,
		},
		{
			name:    "unrecognized verdict string fails closed",
			input:   `{"verdict":"MAYBE","passed":true}`,
			wantErr: true,
		},
		{
			name:    "duplicate passed keys fail closed",
			input:   `{"verdict":"ACCEPTED","passed":false,"passed":true}`,
			wantErr: true,
		},
		{
			name:    "duplicate verdict keys fail closed",
			input:   `{"verdict":"MAYBE","verdict":"ACCEPTED","passed":true}`,
			wantErr: true,
		},
		{
			name:    "case-variant duplicate verdict keys fail closed",
			input:   `{"Verdict":"MAYBE","verdict":"ACCEPTED","passed":true}`,
			wantErr: true,
		},
		{
			name:    "non-canonical PASS verdict fails closed",
			input:   `{"verdict":"PASS","passed":true}`,
			wantErr: true,
		},
		{
			name:    "null verdict with passed true fails closed",
			input:   `{"verdict":null,"passed":true}`,
			wantErr: true,
		},
		{
			name:    "null passed field fails closed",
			input:   `{"verdict":"ACCEPTED","passed":null}`,
			wantErr: true,
		},
		{
			name:    "whitespace-padded passed key with contradictory value fails closed",
			input:   `{"verdict":"ACCEPTED"," passed ":false}`,
			wantErr: true,
		},
		{
			name:    "whitespace-padded passed key alone fails closed without panic",
			input:   `{" passed ":true}`,
			wantErr: true,
		},
		{
			name:    "truncated outer rejection with nested passed true fails closed",
			input:   `{"verdict":"REJECTED","passed":false,"details":{"passed":true}`,
			wantErr: true,
		},
		{
			name:    "unclosed think block fails closed",
			input:   `<think>still thinking {"verdict":"ACCEPTED","passed":true}`,
			wantErr: true,
		},
		{
			name:    "invalid non-json text",
			input:   "not json at all",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBlindVerdict(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %+v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Verdict != tc.wantVerdict || got.Passed != tc.wantPassed {
				t.Fatalf("got (%s, %v), want (%s, %v)", got.Verdict, got.Passed, tc.wantVerdict, tc.wantPassed)
			}
		})
	}
}

func TestExecuteBlindVerify_UntruncatedLargeDiff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	largeMarker := "END_OF_UNTRUNCATED_30KB_DIFF_SENTINEL"
	largeDiff := strings.Repeat("+added line of diff context for untruncated packet test\n", 560) + "+" + largeMarker

	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"id":    "chatcmpl-blind-1",
			"model": "nvidia/nemotron-3.5-lightning-30b-a3b",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"verdict":"ACCEPTED","passed":true,"summary":"All 30KB diff lines verified"}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	httpProv := provider.NewHTTPProviderFromConfig(config.ProviderEntry{
		Class:               "api_call",
		Name:                "9router",
		Protocol:            "9router",
		BaseURL:             srv.URL,
		AllowUnlistedModels: true,
	})

	verdict, err := executeBlindVerify(
		context.Background(),
		httpProv,
		BlindReviewPacket{
			SpecContract:         "Spec contract",
			Invariants:           "Zero-CGO",
			RawGitDiff:           largeDiff,
			MechanicalTestOutput: "PASS",
		},
		"fast-agent",
		"low",
		4096,
		15*time.Second,
		"trace-blind-1",
	)
	if err != nil {
		t.Fatalf("executeBlindVerify failed: %v", err)
	}
	if !verdict.Passed || verdict.Verdict != "ACCEPTED" {
		t.Fatalf("expected ACCEPTED, got %+v", verdict)
	}
	if !strings.Contains(receivedBody, largeMarker) {
		t.Fatalf("expected >30KB diff sentinel %q in transmitted prompt (no truncation cap), len=%d", largeMarker, len(receivedBody))
	}
	if !strings.Contains(receivedBody, "HYDRATED SKILL CONTRACTS") {
		t.Fatalf("expected hydrated g8s-blind-verifier contract in transmitted prompt")
	}
}

func TestExecuteBlindVerify_MissingSkillBankFailsClosed(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	httpProv := provider.NewHTTPProviderFromConfig(config.ProviderEntry{
		Class:               "api_call",
		Name:                "9router",
		Protocol:            "9router",
		BaseURL:             srv.URL,
		AllowUnlistedModels: true,
	})

	_, err := executeBlindVerify(
		context.Background(),
		httpProv,
		BlindReviewPacket{SpecContract: "Spec", Invariants: "Inv", RawGitDiff: "+x", MechanicalTestOutput: "PASS"},
		"fast-agent",
		"low",
		4096,
		5*time.Second,
		"trace-blind-missing-skill",
		t.TempDir(),
	)
	if err == nil || !strings.Contains(err.Error(), "hydrate g8s-blind-verifier skill") {
		t.Fatalf("expected fail-closed skill hydration error, got %v", err)
	}
	if called {
		t.Fatal("provider must not be invoked when mandatory g8s-blind-verifier skill hydration fails")
	}
}

type mockCLIVerifierHandle struct {
	receipt provider.Receipt
}

func (h *mockCLIVerifierHandle) PID() int { return 101 }
func (h *mockCLIVerifierHandle) Wait(_ context.Context) (provider.Receipt, error) {
	return h.receipt, nil
}
func (h *mockCLIVerifierHandle) Cancel(_ context.Context) error { return nil }
func (h *mockCLIVerifierHandle) StdoutStream() io.ReadCloser {
	return io.NopCloser(strings.NewReader(""))
}

type mockCLIVerifierProvider struct {
	name     string
	lastSpec provider.Spec
	hasHooks bool
}

func (p *mockCLIVerifierProvider) Name() string                              { return p.name }
func (p *mockCLIVerifierProvider) Binary() string                            { return p.name }
func (p *mockCLIVerifierProvider) Version(_ context.Context) (string, error) { return "1.5.8", nil }
func (p *mockCLIVerifierProvider) Available(_ context.Context) error         { return nil }
func (p *mockCLIVerifierProvider) Spawn(_ context.Context, spec provider.Spec) (provider.Handle, error) {
	p.lastSpec = spec
	if spec.WorktreeDir != "" {
		if _, err := os.Stat(filepath.Join(spec.WorktreeDir, ".agents", "hooks.json")); err == nil {
			p.hasHooks = true
		}
	}
	return &mockCLIVerifierHandle{
		receipt: provider.Receipt{
			Provider:      p.name,
			Status:        "COMPLETED",
			Stdout:        `{"verdict":"ACCEPTED","passed":true,"summary":"No counterexample found","findings":[],"counterexamples":[]}`,
			ExitCode:      0,
			ModelResolved: spec.Model,
		},
	}, nil
}

func TestBlindVerify_PlatformDispatchAdapter(t *testing.T) {
	cliProv := &mockCLIVerifierProvider{name: "agy"}
	verdict, err := executeBlindVerify(
		context.Background(),
		cliProv,
		BlindReviewPacket{
			SpecContract:         "DELTA-48 adapter neutrality",
			Invariants:           "Zero-CGO, fail-closed sandbox",
			RawGitDiff:           "+func DiscoverModels()",
			MechanicalTestOutput: "PASS",
		},
		"gemini-4-hydrogen-high",
		"high",
		4096,
		5*time.Second,
		"trace-cli-blind-verify",
		filepath.Join("..", "..", "skills"),
	)
	if err != nil {
		t.Fatalf("executeBlindVerify with platform_dispatch adapter failed: %v", err)
	}
	if verdict.Verdict != "ACCEPTED" || !verdict.Passed {
		t.Fatalf("expected ACCEPTED verdict, got %+v", verdict)
	}
	if !cliProv.hasHooks {
		t.Fatalf("expected isolated tool-denied workspace with .agents/hooks.json, got %q", cliProv.lastSpec.WorktreeDir)
	}
	if cliProv.lastSpec.Env["G8S_SANDBOX"] != "1" {
		t.Fatalf("expected G8S_SANDBOX=1 on CLI blind verifier, got %+v", cliProv.lastSpec.Env)
	}
	if !strings.Contains(cliProv.lastSpec.Brief, "Do NOT invoke any tools, shell commands, or file reads") {
		t.Fatalf("expected inline tool-denial guard in verifier prompt")
	}
}

func TestValidateDisjointVerifierFamily(t *testing.T) {
	if err := validateDisjointVerifierFamily("", "google"); err != nil {
		t.Fatalf("empty authorFamily must pass: %v", err)
	}
	if err := validateDisjointVerifierFamily("nvidia", "google"); err != nil {
		t.Fatalf("disjoint families nvidia vs google must pass: %v", err)
	}
	if err := validateDisjointVerifierFamily("google", "multi"); err != nil {
		t.Fatalf("multi gateway family must pass: %v", err)
	}
	if err := validateDisjointVerifierFamily("google", "GOOGLE"); err == nil || !strings.Contains(err.Error(), "E_DISJOINT_FAMILY") {
		t.Fatalf("same family must fail closed with E_DISJOINT_FAMILY, got %v", err)
	}
	if err := validateDisjointVerifierFamily("google", ""); err == nil || !strings.Contains(err.Error(), "E_DISJOINT_FAMILY") {
		t.Fatalf("unknown verifier family must fail closed when authorFamily is set, got %v", err)
	}
}

type shardedMockCLIVerifierProvider struct {
	name          string
	specs         []provider.Spec
	rejectShardAt int // 1-based shard index to reject; 0 = accept all
}

func (p *shardedMockCLIVerifierProvider) Name() string   { return p.name }
func (p *shardedMockCLIVerifierProvider) Binary() string { return p.name }
func (p *shardedMockCLIVerifierProvider) Version(_ context.Context) (string, error) {
	return "1.5.8", nil
}
func (p *shardedMockCLIVerifierProvider) Available(_ context.Context) error { return nil }
func (p *shardedMockCLIVerifierProvider) Spawn(_ context.Context, spec provider.Spec) (provider.Handle, error) {
	p.specs = append(p.specs, spec)
	idx := len(p.specs)
	stdout := `{"verdict":"ACCEPTED","passed":true,"summary":"shard clean","findings":[],"counterexamples":[]}`
	if p.rejectShardAt == idx {
		stdout = `{"verdict":"REJECTED","passed":false,"summary":"bug in shard","findings":["nil deref"],"counterexamples":["input=nil panics"]}`
	}
	return &mockCLIVerifierHandle{
		receipt: provider.Receipt{
			Provider:      p.name,
			Status:        "COMPLETED",
			Stdout:        stdout,
			ExitCode:      0,
			ModelResolved: spec.Model,
		},
	}, nil
}

func TestExecuteBlindVerify_LargeMultiFileDiffShardsFailClosed(t *testing.T) {
	fileChunk := func(path, sentinel string) string {
		var sb strings.Builder
		sb.WriteString("diff --git a/" + path + " b/" + path + "\n")
		sb.WriteString("--- a/" + path + "\n+++ b/" + path + "\n@@ -1,1 +1,900 @@\n")
		sb.WriteString(strings.Repeat("+line of untruncated file implementation detail for sharded blind verification\n", 850))
		sb.WriteString("+" + sentinel + "\n")
		return sb.String()
	}
	bigDiff := fileChunk("pkg/a.go", "SENTINEL_FILE_A_END") +
		fileChunk("pkg/b.go", "SENTINEL_FILE_B_END") +
		fileChunk("pkg/c.go", "SENTINEL_FILE_C_END")

	// 1. All shards ACCEPTED -> aggregate ACCEPTED, every prompt <= maxBlindPromptBytes, all sentinels present
	provAccept := &shardedMockCLIVerifierProvider{name: "agy"}
	v1, err := executeBlindVerify(
		context.Background(),
		provAccept,
		BlindReviewPacket{
			SpecContract:         "Spec",
			Invariants:           "Inv",
			RawGitDiff:           bigDiff,
			MechanicalTestOutput: "PASS",
		},
		"gemini-4-hydrogen-high",
		"high",
		4096,
		10*time.Second,
		"trace-shard-accept",
		filepath.Join("..", "..", "skills"),
	)
	if err != nil {
		t.Fatalf("sharded executeBlindVerify failed: %v", err)
	}
	if !v1.Passed || v1.Verdict != "ACCEPTED" {
		t.Fatalf("expected aggregate ACCEPTED, got %+v", v1)
	}
	if len(provAccept.specs) < 3 {
		t.Fatalf("expected >= 3 shards for ~200KB diff, got %d", len(provAccept.specs))
	}
	var combinedPrompts strings.Builder
	for i, sp := range provAccept.specs {
		if len(sp.Brief) > maxBlindPromptBytes+4096 {
			t.Fatalf("shard %d prompt exceeded safe bound: %d bytes", i+1, len(sp.Brief))
		}
		combinedPrompts.WriteString(sp.Brief)
	}
	for _, sentinel := range []string{"SENTINEL_FILE_A_END", "SENTINEL_FILE_B_END", "SENTINEL_FILE_C_END"} {
		if !strings.Contains(combinedPrompts.String(), sentinel) {
			t.Fatalf("missing untruncated sentinel %q across sharded prompts", sentinel)
		}
	}

	// 2. Shard 2 REJECTED -> aggregate REJECTED fail-closed with counterexample preserved
	provReject := &shardedMockCLIVerifierProvider{name: "agy", rejectShardAt: 2}
	v2, err := executeBlindVerify(
		context.Background(),
		provReject,
		BlindReviewPacket{
			SpecContract:         "Spec",
			Invariants:           "Inv",
			RawGitDiff:           bigDiff,
			MechanicalTestOutput: "PASS",
		},
		"gemini-4-hydrogen-high",
		"high",
		4096,
		10*time.Second,
		"trace-shard-reject",
		filepath.Join("..", "..", "skills"),
	)
	if err != nil {
		t.Fatalf("sharded executeBlindVerify reject test failed: %v", err)
	}
	if v2.Passed || v2.Verdict != "REJECTED" {
		t.Fatalf("expected fail-closed REJECTED when shard 2 rejects, got %+v", v2)
	}
	if len(v2.Counterexamples) == 0 || !strings.Contains(v2.Counterexamples[0], "input=nil panics") {
		t.Fatalf("expected shard 2 counterexample preserved, got %+v", v2.Counterexamples)
	}
}
