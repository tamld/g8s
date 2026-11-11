package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRegistry_DeviceLeasePool(t *testing.T) {
	reg := NewRegistry()
	pool := reg.DeviceLeasePool()
	if pool == nil {
		t.Fatal("expected non-nil DeviceLeasePool")
	}
	if reg.DeviceLeasePool() != pool {
		t.Fatal("expected stable DeviceLeasePool instance")
	}
}

func TestOllamaProvider_Spawn(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.WriteHeader(http.StatusOK)
			return
		case "/api/generate":
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"response": "hello ollama"})
			return
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("OLLAMA_HOST", srv.URL)
	p := NewOllamaProvider().WithHTTPClient(srv.Client())

	h, err := p.Spawn(context.Background(), Spec{Model: "llama3.2", Brief: "ping"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !strings.Contains(gotBody, `"prompt":"ping"`) {
		t.Fatalf("expected prompt in generate body, got %q", gotBody)
	}

	rcpt, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if rcpt.Status != "COMPLETED" || rcpt.Stdout != "hello ollama" {
		t.Fatalf("unexpected receipt: %+v", rcpt)
	}
	if h.PID() != 0 {
		t.Fatalf("expected PID 0, got %d", h.PID())
	}
	out, _ := io.ReadAll(h.StdoutStream())
	if string(out) != "hello ollama" {
		t.Fatalf("stdout stream: %q", out)
	}
}

func TestOllamaProvider_Spawn_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/generate" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	p := NewOllamaProvider().WithHTTPClient(srv.Client())

	h, err := p.Spawn(context.Background(), Spec{Brief: "fail"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	rcpt, waitErr := h.Wait(context.Background())
	if waitErr == nil {
		t.Fatal("expected Wait error on HTTP 500")
	}
	if rcpt.Status != "FAILED" {
		t.Fatalf("expected FAILED status, got %+v", rcpt)
	}
}

func TestOllamaProvider_Spawn_ContextCancel(t *testing.T) {
	var hits atomic.Int32
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/generate" {
			hits.Add(1)
			<-block
		}
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	p := NewOllamaProvider().WithHTTPClient(srv.Client())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.Spawn(ctx, Spec{Brief: "slow"})
	}()
	cancel()
	<-done
	close(block)
}
