package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	var (
		mu      sync.Mutex
		gotBody string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.WriteHeader(http.StatusOK)
			return
		case "/api/generate":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			gotBody = string(b)
			mu.Unlock()
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
	if p.TransportKind() != TransportKindRequest {
		t.Fatalf("expected TransportKindRequest, got %q", p.TransportKind())
	}

	h, err := p.Spawn(context.Background(), Spec{Model: "llama3.2", Brief: "ping"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if h.PID() != NoPID {
		t.Fatalf("expected NoPID (%d), got %d", NoPID, h.PID())
	}

	rcpt, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	mu.Lock()
	bodySnap := gotBody
	mu.Unlock()
	if !strings.Contains(bodySnap, `"prompt":"ping"`) {
		t.Fatalf("expected prompt in generate body, got %q", bodySnap)
	}
	if rcpt.Status != "COMPLETED" || rcpt.Stdout != "hello ollama" || rcpt.TransportKind != TransportKindRequest {
		t.Fatalf("unexpected receipt: %+v", rcpt)
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

func TestOllamaProvider_AsyncCancelInFlight(t *testing.T) {
	started := make(chan struct{})
	canceledCh := make(chan struct{})
	shutdown := make(chan struct{})
	var startOnce, cancelOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/generate" {
			_, _ = io.Copy(io.Discard, r.Body)
			startOnce.Do(func() { close(started) })
			select {
			case <-r.Context().Done():
				cancelOnce.Do(func() { close(canceledCh) })
			case <-shutdown:
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	defer close(shutdown)

	t.Setenv("OLLAMA_HOST", srv.URL)
	p := NewOllamaProvider().WithHTTPClient(srv.Client())

	rh, err := p.StartRequest(context.Background(), Spec{Model: "llama3.2", Brief: "slow"})
	if err != nil {
		t.Fatalf("StartRequest: %v", err)
	}
	<-started
	if err := rh.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	rc, waitErr := rh.Wait(context.Background())
	if rc.Status != "CANCELLED" || !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("expected CANCELLED/context.Canceled, got status=%q err=%v", rc.Status, waitErr)
	}
	select {
	case <-canceledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected server request context to cancel")
	}
}
