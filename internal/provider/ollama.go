package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		httpClient: &http.Client{Timeout: 2 * time.Minute},
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
	o.mu.RLock()
	host := o.host
	o.mu.RUnlock()
	if host != "" {
		return host
	}
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		return strings.TrimRight(host, "/")
	}
	return "http://127.0.0.1:11434"
}

// Available probes the Ollama daemon HTTP health endpoint.
func (o *OllamaProvider) Available(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	host := o.getHost()
	url := host + "/api/tags"
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
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

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	host := o.getHost()
	url := host + "/api/version"
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
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

type ollamaHandle struct {
	provider string
	model    string
	stdout   string
	stderr   string
	status   string
	exitCode int
	start    time.Time
	cancel   context.CancelFunc
	done     chan struct{}
}

func (h *ollamaHandle) PID() int { return 0 }

func (h *ollamaHandle) Wait(_ context.Context) (Receipt, error) {
	<-h.done
	var err error
	if h.status != "COMPLETED" {
		if h.stderr != "" {
			err = fmt.Errorf("%s", h.stderr)
		} else {
			err = fmt.Errorf("ollama generate failed")
		}
	}
	return Receipt{
		Provider:      h.provider,
		Status:        h.status,
		Stdout:        h.stdout,
		Stderr:        h.stderr,
		ExitCode:      h.exitCode,
		DurationMs:    time.Since(h.start).Milliseconds(),
		ModelResolved: h.model,
		OutputTokens:  len(h.stdout),
		TotalTokens:   len(h.stdout),
	}, err
}

func (h *ollamaHandle) Cancel(_ context.Context) error {
	if h.cancel != nil {
		h.cancel()
	}
	return nil
}

func (h *ollamaHandle) StdoutStream() io.ReadCloser {
	return io.NopCloser(bytes.NewReader([]byte(h.stdout)))
}

// Spawn runs a single non-streaming Ollama /api/generate request.
func (o *OllamaProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	if err := o.Available(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	model := spec.Model
	if model == "" {
		model = "llama3.2"
	}
	prompt := spec.Brief
	if prompt == "" {
		prompt = spec.SystemPrompt
	}

	host := o.getHost()
	reqCtx, cancel := context.WithCancel(ctx)
	start := time.Now()

	body, err := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	})
	if err != nil {
		cancel()
		return nil, err
	}

	url := host + "/api/generate"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		cancel()
		if reqCtx.Err() != nil {
			return nil, reqCtx.Err()
		}
		return nil, fmt.Errorf("ollama generate request: %w", err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		cancel()
		if reqCtx.Err() != nil {
			return nil, reqCtx.Err()
		}
		return nil, fmt.Errorf("ollama generate read body: %w", readErr)
	}
	cancel()

	h := &ollamaHandle{
		provider: o.Name(),
		model:    model,
		start:    start,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	if resp.StatusCode != http.StatusOK {
		h.status = "FAILED"
		h.exitCode = resp.StatusCode
		h.stderr = string(respBody)
		close(h.done)
		return h, nil
	}

	var genResp struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(respBody, &genResp); err != nil {
		h.status = "FAILED"
		h.exitCode = 1
		h.stderr = err.Error()
		close(h.done)
		return h, nil
	}

	h.stdout = genResp.Response
	h.status = "COMPLETED"
	h.exitCode = 0
	close(h.done)
	return h, nil
}
