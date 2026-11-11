package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
