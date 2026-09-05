package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIProvider_AvailableAndAuth(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			gotAuthHeader = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data": [{"id": "gpt-4o"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	t.Setenv("TEST_OPENAI_KEY", "secret-test-token-123")
	p := NewOpenAIProvider("test-ai", srv.URL, "TEST_OPENAI_KEY")

	err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("expected provider available, got: %v", err)
	}

	if gotAuthHeader != "Bearer secret-test-token-123" {
		t.Fatalf("expected Authorization header with token, got %q", gotAuthHeader)
	}
}

func TestOpenAIProvider_SpawnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/chat/completions" {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"choices": [
					{
						"message": {
							"role": "assistant",
							"content": "Synthesized code patch output"
						}
					}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	spec := Spec{
		Model:  "gpt-4o-mini",
		Brief:  "Review this module",
		Timeout: 5 * time.Second,
	}

	handle, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	receipt, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if receipt.Status != "COMPLETED" {
		t.Fatalf("expected status COMPLETED, got %s", receipt.Status)
	}
	if receipt.Stdout != "Synthesized code patch output" {
		t.Fatalf("unexpected stdout: %s", receipt.Stdout)
	}
}

func TestOpenAIProvider_MissingModelFailsEarly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	spec := Spec{
		Model: "", // Red test: empty model must be rejected
		Brief: "Task",
	}

	_, err := p.Spawn(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error on empty model, got nil")
	}
}

func TestOpenAIProvider_APIErrorHandling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error": {"message": "invalid_api_key"}}`))
			return
		}
	}))
	defer srv.Close()

	p := NewOpenAIProvider("test-ai", srv.URL, "")
	spec := Spec{
		Model: "gpt-4o",
		Brief: "Task",
	}

	handle, err := p.Spawn(context.Background(), spec)
	if err != nil {
		t.Fatalf("Spawn unexpected error: %v", err)
	}

	receipt, err := handle.Wait(context.Background())
	if err == nil {
		t.Fatal("expected error on API failure, got nil")
	}
	if receipt.Status != "FAILED" {
		t.Fatalf("expected status FAILED, got %s", receipt.Status)
	}
}
