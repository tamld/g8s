package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerLifecycle(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}

	if srv.config.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("expected ReadHeaderTimeout to be 10s, got %v", srv.config.ReadHeaderTimeout)
	}

	err := srv.Start()
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	if err := srv.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}
}

func TestServerConfigValidation(t *testing.T) {
	cfg := Config{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	if cfg.Address != ":8080" {
		t.Errorf("expected Address to be :8080, got %s", cfg.Address)
	}
	if cfg.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("expected ReadHeaderTimeout to be 10s, got %v", cfg.ReadHeaderTimeout)
	}
}

func TestCorsMiddleware(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableCORS = true
	cfg.AllowedOrigins = []string{"http://localhost:3000"}

	srv := NewServer(cfg, nil)

	handler := srv.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 No Content, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("expected Access-Control-Allow-Origin to be http://localhost:3000, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCorsMiddleware_Asterisk(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableCORS = true
	cfg.AllowedOrigins = []string{"*"}

	srv := NewServer(cfg, nil)

	handler := srv.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.com" {
		t.Errorf("expected Access-Control-Allow-Origin to be http://example.com, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCorsMiddleware_Disabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableCORS = false

	srv := NewServer(cfg, nil)

	handler := srv.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected empty Access-Control-Allow-Origin, got %s", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestHandleHealthz(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", rec.Header().Get("Content-Type"))
	}
}

func TestHandleReadyz_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/readyz", nil)
	rec := httptest.NewRecorder()

	srv.handleReadyz(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleMetrics_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()

	srv.handleMetrics(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleHealthz_MethodNotAllowed(t *testing.T) {
	cfg := DefaultConfig()
	srv := NewServer(cfg, nil)

	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.handleHealthz(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
	}
}
