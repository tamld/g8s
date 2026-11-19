package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// TransportKind returns TransportKindRequest for Ollama HTTP inference.
func (o *OllamaProvider) TransportKind() TransportKind { return TransportKindRequest }

// StartRequest starts an asynchronous Ollama /api/generate request returning a typed RequestHandle (#940).
func (o *OllamaProvider) StartRequest(ctx context.Context, spec Spec) (RequestHandle, error) {
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
	var (
		reqCtx context.Context
		cancel context.CancelFunc
	)
	if spec.Timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	} else {
		reqCtx, cancel = context.WithCancel(ctx)
	}

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

	h := newAsyncRequestHandle(o.Name(), cancel)
	client := o.httpClient

	go func() {
		resp, err := client.Do(req)
		if err != nil {
			rc := Receipt{Status: "FAILED", Stderr: err.Error(), ExitCode: 1, ModelResolved: model}
			if errors.Is(reqCtx.Err(), context.Canceled) {
				rc.Status = "CANCELLED"
				rc.ExitCode = -1
				h.complete(rc, context.Canceled)
				return
			}
			if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
				rc.Status = "TIMEOUT"
				rc.ExitCode = -1
				h.complete(rc, reqCtx.Err())
				return
			}
			h.complete(rc, fmt.Errorf("ollama generate request: %w", err))
			return
		}
		defer resp.Body.Close()

		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if readErr != nil {
			rc := Receipt{Status: "FAILED", Stderr: readErr.Error(), ExitCode: 1, ModelResolved: model}
			if errors.Is(reqCtx.Err(), context.Canceled) {
				rc.Status = "CANCELLED"
				rc.ExitCode = -1
				h.complete(rc, context.Canceled)
				return
			}
			h.complete(rc, fmt.Errorf("ollama generate read body: %w", readErr))
			return
		}

		if resp.StatusCode != http.StatusOK {
			h.complete(Receipt{
				Status:        "FAILED",
				Stderr:        string(respBody),
				ExitCode:      resp.StatusCode,
				ModelResolved: model,
			}, nil)
			return
		}

		var genResp struct {
			Response        string `json:"response"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
		}
		if err := json.Unmarshal(respBody, &genResp); err != nil {
			h.complete(Receipt{
				Status:        "FAILED",
				Stderr:        err.Error(),
				ExitCode:      1,
				ModelResolved: model,
			}, nil)
			return
		}

		inTok := 0
		outTok := len(genResp.Response)
		totalTok := len(genResp.Response)
		if genResp.PromptEvalCount > 0 || genResp.EvalCount > 0 {
			inTok = genResp.PromptEvalCount
			outTok = genResp.EvalCount
			totalTok = genResp.PromptEvalCount + genResp.EvalCount
		}

		h.streamBuf.WriteString(genResp.Response)
		h.complete(Receipt{
			Status:        "COMPLETED",
			Stdout:        genResp.Response,
			ExitCode:      0,
			ModelResolved: model,
			InputTokens:   inTok,
			OutputTokens:  outTok,
			TotalTokens:   totalTok,
		}, nil)
	}()

	return h, nil
}

// Spawn starts an Ollama request asynchronously and adapts RequestHandle for legacy Handle callers.
func (o *OllamaProvider) Spawn(ctx context.Context, spec Spec) (Handle, error) {
	rh, err := o.StartRequest(ctx, spec)
	if err != nil {
		return nil, err
	}
	return newRequestHandleAdapter(rh), nil
}
