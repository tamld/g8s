package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// OllamaProvider manages Ollama local model inference.
type OllamaProvider struct {
	mu         sync.RWMutex
	host       string
	version    string
	httpClient *http.Client
}

// NewOllamaProvider creates a new OllamaProvider instance.
func NewOllamaProvider() *OllamaProvider {
	return &OllamaProvider{
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
}

// WithHTTPClient overrides the HTTP client for testing.
func (o *OllamaProvider) WithHTTPClient(client *http.Client) *OllamaProvider {
	o.httpClient = client
	return o
}

// Name returns the canonical provider name.
func (o *OllamaProvider) Name() string {
	return "ollama"
}

// Binary returns the default executable name.
func (o *OllamaProvider) Binary() string {
	return "ollama"
}

func (o *OllamaProvider) getHost() string {
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		return strings.TrimRight(host, "/")
	}
	return "http://127.0.0.1:11434"
}

// Available probes the Ollama daemon HTTP health endpoint.
func (o *OllamaProvider) Available(ctx context.Context) error {
	host := o.getHost()
	url := host + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("no model server at %s", host)
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("no model server at %s", host)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("no model server at %s (status %d)", host, resp.StatusCode)
	}

	o.mu.Lock()
	o.host = host
	o.mu.Unlock()

	return nil
}

// Version returns the Ollama server version.
func (o *OllamaProvider) Version(ctx context.Context) (string, error) {
	o.mu.RLock()
	v := o.version
	o.mu.RUnlock()
	if v != "" {
		return v, nil
	}

	host := o.getHost()
	url := host + "/api/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("no model server at %s", host)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var verResp struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&verResp); err == nil && verResp.Version != "" {
			o.mu.Lock()
			o.version = verResp.Version
			o.mu.Unlock()
			return verResp.Version, nil
		}
	}

	return "v0.1.0", nil
}

// Spawn is a stub for future Ollama invocation.
func (o *OllamaProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := o.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	return nil, fmt.Errorf("ollama provider spawn not implemented")
}
