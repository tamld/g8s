package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/config"
	"github.com/tamld/g8s/internal/sandbox"
)

func TestHTTPProvider_PolicyEnforcementReportAndPreSpawnRejection(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-policy-1",
			"model": "mock-v1",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "ok",
					},
				},
			},
		})
	}))
	defer srv.Close()

	p := NewHTTPProviderFromConfig(config.ProviderEntry{
		Name:     "mockhttp",
		BaseURL:  srv.URL,
		Protocol: "openai",
	}).WithHTTPClient(srv.Client())

	t.Run("normal_http_receipt_emits_mediated_only_policy_report", func(t *testing.T) {
		requests.Store(0)
		rh, err := p.StartRequest(context.Background(), Spec{
			Brief:           "read module",
			Model:           "mock-v1",
			Role:            "collector",
			Permission:      "read_only",
			HoldoutDenyDirs: []string{"/tmp/holdout-eval"},
		})
		if err != nil {
			t.Fatalf("StartRequest: %v", err)
		}
		rc, werr := rh.Wait(context.Background())
		if werr != nil {
			t.Fatalf("Wait: %v", werr)
		}
		if rc.PolicyEnforcement == nil {
			t.Fatal("expected Receipt.PolicyEnforcement populated on HTTP receipt")
		}
		rep := rc.PolicyEnforcement
		if rep.RejectedBeforeSpawn || !rep.Evaluated.Allowed {
			t.Fatalf("unexpected rejection on normal HTTP request: %+v", rep)
		}
		if rep.NetworkEnforcement != sandbox.NetworkMediatedOnly || rep.Effective.NetworkEnforcement != sandbox.NetworkMediatedOnly {
			t.Fatalf("NetworkEnforcement = %q / %q, want %q",
				rep.NetworkEnforcement, rep.Effective.NetworkEnforcement, sandbox.NetworkMediatedOnly)
		}
		if rep.Effective.ContainmentMode != sandbox.ModePolicyOnly {
			t.Fatalf("ContainmentMode = %q, want %q", rep.Effective.ContainmentMode, sandbox.ModePolicyOnly)
		}
		if rep.EpistemicClass != sandbox.EpistemicMeasuredMetric {
			t.Fatalf("EpistemicClass = %q, want %q", rep.EpistemicClass, sandbox.EpistemicMeasuredMetric)
		}
		if rep.Effective.HoldoutDenied {
			t.Fatalf("HTTP transport on policy_only must not claim HoldoutDenied=true")
		}
	})

	t.Run("require_kernel_or_enforced_network_rejects_before_http_dispatch", func(t *testing.T) {
		requests.Store(0)
		_, err := p.StartRequest(context.Background(), Spec{
			Brief:                 "attempt strict kernel on HTTP",
			Model:                 "mock-v1",
			Role:                  "implementer",
			Permission:            "workspace_write",
			ReceiptID:             "rcpt-http-1",
			RequireKernelEnforced: true,
		})
		if !errors.Is(err, sandbox.ErrKernelSandboxRequired) {
			t.Fatalf("expected ErrKernelSandboxRequired, got %v", err)
		}
		var rej *sandbox.PolicyRejectionError
		if !errors.As(err, &rej) || !rej.Report.RejectedBeforeSpawn {
			t.Fatalf("expected *PolicyRejectionError with RejectedBeforeSpawn=true, got %T (%v)", err, err)
		}
		if requests.Load() != 0 {
			t.Fatalf("expected 0 HTTP requests dispatched after pre-spawn rejection, got %d", requests.Load())
		}

		_, nerr := p.StartRequest(context.Background(), Spec{
			Brief:                  "attempt strict network on HTTP",
			Model:                  "mock-v1",
			Permission:             "read_only",
			RequireNetworkEnforced: true,
		})
		if !errors.Is(nerr, sandbox.ErrNetworkEnforcementRequired) {
			t.Fatalf("expected ErrNetworkEnforcementRequired, got %v", nerr)
		}
		if requests.Load() != 0 {
			t.Fatalf("expected 0 HTTP requests dispatched on network rejection, got %d", requests.Load())
		}
	})
}

func TestExecHandle_EmitsPolicyEnforcementReport(t *testing.T) {
	h := &execHandle{
		provider: "claude",
		start:    time.Now(),
		done:     make(chan struct{}),
	}
	close(h.done)
	rc, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if rc.PolicyEnforcement == nil {
		t.Fatal("expected CLI execHandle receipt to include PolicyEnforcement")
	}
	if rc.PolicyEnforcement.Requested.TransportKind != string(TransportKindProcess) {
		t.Fatalf("TransportKind = %q, want %q", rc.PolicyEnforcement.Requested.TransportKind, TransportKindProcess)
	}
	if rc.PolicyEnforcement.EpistemicClass != sandbox.EpistemicMeasuredMetric {
		t.Fatalf("EpistemicClass = %q, want %q", rc.PolicyEnforcement.EpistemicClass, sandbox.EpistemicMeasuredMetric)
	}
}
